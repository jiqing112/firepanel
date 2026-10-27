package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	httpNewRequestWithContext = http.NewRequestWithContext
	httpDefaultClient         = http.DefaultClient
)

// CaddyfileDir 系统包安装的 Caddy 默认配置目录（Debian/官方包/Alpine 一致）。
const CaddyfileDir = "/etc/caddy"

// CaddyfilePath 面板写入/读取的配置文件。
const CaddyfilePath = CaddyfileDir + "/Caddyfile"

// 备份保留份数（超出按时间从旧到新清理）。
const caddyBackupKeep = 10

// CaddyfileState 服务器上实际 Caddyfile 的读取结果。
type CaddyfileState struct {
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Content string `json:"content"`
	Managed bool   `json:"managed"` // 含面板托管段
	Legacy  bool   `json:"legacy"`  // 旧版整文件生成物
	Hash    string `json:"hash"`    // 内容 SHA-256（并发修改检测用）
	ReadErr string `json:"read_error,omitempty"`
}

// CaddyfileHash 内容指纹。
func CaddyfileHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// MergeResult 一次下发的合并结果。
type MergeResult struct {
	Mode    MergeMode `json:"mode"`
	Backup  string    `json:"backup"`  // 覆盖前的备份路径（首次创建时为空）
	Content string    `json:"-"`       // 实际写入内容
	Created bool      `json:"created"` // 目标原本不存在
}

// ReadCaddyfile 读取服务器上真实的 Caddyfile（而不是面板「以为」的内容）。
// 读不到时区分「不存在」与「读取失败」：后者会阻止后续写入，避免盲覆盖。
func (m *Manager) ReadCaddyfile(ctx context.Context) CaddyfileState {
	st := CaddyfileState{Path: CaddyfilePath}
	content, err := m.readFile(ctx, CaddyfilePath)
	if err != nil {
		if isNotExistErr(err) {
			return st
		}
		st.ReadErr = err.Error()
		return st
	}
	st.Exists = true
	st.Content = content
	st.Managed = HasManagedBlock(content)
	st.Legacy = IsLegacyGenerated(content)
	st.Hash = CaddyfileHash(content)
	return st
}

// readFile 经 runner（root 权限）或本进程读取文件。
func (m *Manager) readFile(ctx context.Context, path string) (string, error) {
	if m.runner != nil {
		out, err := m.runner.Run(ctx, "cat", path)
		if err != nil {
			return "", err
		}
		return out, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func isNotExistErr(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	msg := err.Error()
	for _, k := range []string{"No such file", "no such file", "cannot open", "does not exist", "not found"} {
		if strings.Contains(msg, k) {
			return true
		}
	}
	return false
}

// EffectiveCustom 选择可信的自定义片段：面板设置为空时回退到磁盘上的手写片段
// （用户直接改文件、未走面板保存的情况），旧版生成物以磁盘为准。
func EffectiveCustom(setting string, st CaddyfileState) string {
	if !st.Exists {
		return setting
	}
	tail := SplitCustom(st.Content)
	if strings.TrimSpace(setting) == "" {
		return tail
	}
	if st.Legacy && tail != "" {
		return tail
	}
	return setting
}

// DeployGenerated 重新生成托管段并合并进现有文件（不整盘覆盖）。
// 流程：读取现有文件 → 判定合并方式 → 生成托管段 → 合并 → validate → 备份 → 写入 → reload。
// 读取失败或备份失败都会中止，磁盘上的原文件保持不变。
func (m *Manager) DeployGenerated(ctx context.Context, cfgs []DomainConfig, g CaddyGlobalSettings, custom string) (MergeResult, error) {
	st := m.ReadCaddyfile(ctx)
	if st.ReadErr != "" {
		return MergeResult{}, fmt.Errorf("读取 %s 失败，已中止下发以免覆盖现有配置: %s", CaddyfilePath, st.ReadErr)
	}
	// 磁盘上已有手写自定义片段而面板 settings 为空时，以磁盘为准（不丢配置）
	eff := EffectiveCustom(custom, st)
	mode := DetectMergeMode(st.Content)
	// 追加到他人文件时不能带全局块（Caddy 要求全局块位于文件首个块）
	block := BuildManagedBlock(cfgs, g, eff, mode != MergeAppend)
	merged := ApplyMerge(st.Content, mode, block)

	backup, err := m.deployContent(ctx, merged)
	res := MergeResult{Mode: mode, Backup: backup, Content: merged, Created: !st.Exists}
	if err != nil {
		if mode == MergeAppend {
			// 追加到他人文件时最常见的失败是站点地址/全局块重复，给出可操作的提示
			return res, fmt.Errorf("%w（目标文件已有自己的配置，面板按「追加」处理；若站点地址或全局块重复，请先在 Caddyfile 中调整后重试）", err)
		}
		return res, err
	}
	return res, nil
}

// deployContent 校验并落盘（runner 可用时走系统命令，否则回退 Admin API）。
func (m *Manager) deployContent(ctx context.Context, content string) (string, error) {
	if m.runner != nil {
		return m.DeployCaddyfileWithBackup(ctx, m.runner, content)
	}
	return "", m.adminLoadCaddyfile(ctx, content)
}

// DeployCaddyfile 完全接管：写入 /etc/caddy/Caddyfile 并 reload（覆盖前自动备份）。
func (m *Manager) DeployCaddyfile(ctx context.Context, runner Runner, caddyfile string) error {
	_, err := m.DeployCaddyfileWithBackup(ctx, runner, caddyfile)
	return err
}

// DeployCaddyfileWithBackup 校验 → 备份现有文件 → 原子写入 → reload，返回备份路径。
// 备份失败即中止（宁可不下发，也不留下不可恢复的覆盖）。
func (m *Manager) DeployCaddyfileWithBackup(ctx context.Context, runner Runner, caddyfile string) (string, error) {
	hasSystemd := false
	if _, err := runner.Run(ctx, "systemctl", "--version"); err == nil {
		hasSystemd = true
	}
	if !hasSystemd {
		return "", m.adminLoadCaddyfile(ctx, caddyfile)
	}

	if _, err := runner.Run(ctx, "mkdir", "-p", CaddyfileDir); err != nil {
		return "", fmt.Errorf("创建 %s 失败: %v", CaddyfileDir, err)
	}
	// 站点日志目录（包安装通常已建；缺省补建，避免 reload 后 403/写失败）
	_, _ = runner.Run(ctx, "mkdir", "-p", "/var/log/caddy")

	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("firepanel-caddy-%d", time.Now().UnixNano()))
	if err := os.WriteFile(tmp, []byte(caddyfile), 0o644); err != nil {
		return "", fmt.Errorf("写临时配置失败: %v", err)
	}
	defer os.Remove(tmp)

	if out, err := runner.Run(ctx, "caddy", "validate", "--config", tmp, "--adapter", "caddyfile"); err != nil {
		return "", fmt.Errorf("Caddyfile 校验失败（未下发，服务器上的配置未受影响）: %s", strings.TrimSpace(out))
	}

	backup, err := m.backupCaddyfile(ctx, runner)
	if err != nil {
		return "", fmt.Errorf("备份现有 Caddyfile 失败，已中止写入（避免配置不可恢复）: %v", err)
	}

	if out, err := runner.Run(ctx, "cp", tmp, CaddyfilePath); err != nil {
		return backup, fmt.Errorf("写入 %s 失败: %v %s", CaddyfilePath, err, strings.TrimSpace(out))
	}
	if out, err := runner.Run(ctx, "systemctl", "reload", "caddy"); err != nil {
		// reload 失败尝试 restart（服务可能未运行）
		if out2, err2 := runner.Run(ctx, "systemctl", "restart", "caddy"); err2 != nil {
			return backup, fmt.Errorf("Caddy reload/restart 失败: %v %s", err2, strings.TrimSpace(out2))
		}
		_ = out
	}
	return backup, nil
}

// backupCaddyfile 覆盖前备份：首次接管另存 .fp-orig 永久保留原始文件，
// 每次写入另存 .bak-<时间戳>（最多保留 caddyBackupKeep 份）。
func (m *Manager) backupCaddyfile(ctx context.Context, runner Runner) (string, error) {
	if _, err := runner.Run(ctx, "test", "-f", CaddyfilePath); err != nil {
		return "", nil // 目标不存在，无需备份
	}
	orig := CaddyfilePath + ".fp-orig"
	if _, err := runner.Run(ctx, "test", "-f", orig); err != nil {
		if out, err := runner.Run(ctx, "cp", "-p", CaddyfilePath, orig); err != nil {
			return "", fmt.Errorf("保留原始副本失败: %v %s", err, strings.TrimSpace(out))
		}
	}
	bak := CaddyfilePath + ".bak-" + time.Now().Format("20060102-150405")
	if out, err := runner.Run(ctx, "cp", "-p", CaddyfilePath, bak); err != nil {
		return "", fmt.Errorf("创建备份 %s 失败: %v %s", bak, err, strings.TrimSpace(out))
	}
	// 清理过旧备份（仅匹配本面板的 .bak- 命名，不动其他文件）
	_, _ = runner.Run(ctx, "sh", "-c",
		fmt.Sprintf("ls -1t %s.bak-* 2>/dev/null | tail -n +%d | xargs -r rm -f", CaddyfilePath, caddyBackupKeep+1))
	return bak, nil
}

// DeployRawCaddyfile 整文件手改保存：内容经 validate 后落盘生效（覆盖前备份）。
// 不重新生成（保留用户对文件的所有修改）。
func (m *Manager) DeployRawCaddyfile(ctx context.Context, content string) (string, error) {
	return m.deployContent(ctx, content)
}

// ErrCaddyfileChanged 乐观并发冲突：读取后被其他操作改过。
var ErrCaddyfileChanged = fmt.Errorf("服务器上的 Caddyfile 已被修改")

// DeployRawCaddyfileIfUnchanged 乐观并发保存：baseHash 非空且与服务器当前文件
// 不一致时拒绝写入（返回 ErrCaddyfileChanged），避免覆盖别处刚做的修改。
func (m *Manager) DeployRawCaddyfileIfUnchanged(ctx context.Context, content, baseHash string) (MergeResult, error) {
	res := MergeResult{Mode: MergeAppend, Content: content}
	st := m.ReadCaddyfile(ctx)
	if st.ReadErr != "" {
		return res, fmt.Errorf("读取 %s 失败，已中止保存: %s", CaddyfilePath, st.ReadErr)
	}
	if baseHash != "" && st.Exists && st.Hash != baseHash {
		return res, ErrCaddyfileChanged
	}
	if st.Exists {
		switch {
		case HasManagedBlock(st.Content):
			res.Mode = MergeUpdate
		case IsLegacyGenerated(st.Content):
			res.Mode = MergeMigrate
		default:
			res.Mode = MergeAppend
		}
	} else {
		res.Mode = MergeCreate
		res.Created = true
	}
	backup, err := m.deployContent(ctx, content)
	res.Backup = backup
	return res, err
}

// adminLoadCaddyfile 回退路径：经 Admin API 下发 Caddyfile（adapter=caddyfile）。
func (m *Manager) adminLoadCaddyfile(ctx context.Context, caddyfile string) error {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	body := strings.NewReader(caddyfile)
	req, err := httpNewRequestWithContext(cctx, "POST", strings.TrimRight(m.caddyAdmin, "/")+"/load?adapter=caddyfile", body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := httpDefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("连接 Caddy Admin API %s: %w", m.caddyAdmin, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("Caddy /load 返回 %d", resp.StatusCode)
	}
	return nil
}
