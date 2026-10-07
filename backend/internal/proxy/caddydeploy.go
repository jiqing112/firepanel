package proxy

import (
	"context"
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

// CaddyfilePath 完全接管模式写入的配置文件。
const CaddyfilePath = CaddyfileDir + "/Caddyfile"

// DeployCaddyfile 完全接管：生成物写入 /etc/caddy/Caddyfile 并 reload。
// 流程：确保目录 → caddy validate（语法防呆，失败不下发）→ 原子写入 → systemctl reload。
// 无 systemd（容器）时回退 Admin API（adapter=caddyfile，运行时生效不落盘）。
func (m *Manager) DeployCaddyfile(ctx context.Context, runner Runner, caddyfile string) error {
	hasSystemd := false
	if _, err := runner.Run(ctx, "systemctl", "--version"); err == nil {
		hasSystemd = true
	}
	if !hasSystemd {
		return m.adminLoadCaddyfile(ctx, caddyfile)
	}

	if _, err := runner.Run(ctx, "mkdir", "-p", CaddyfileDir); err != nil {
		return fmt.Errorf("创建 %s 失败: %v", CaddyfileDir, err)
	}
	// 站点日志目录（包安装通常已建；缺省补建，避免 reload 后 403/写失败）
	_, _ = runner.Run(ctx, "mkdir", "-p", "/var/log/caddy")

	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("firepanel-caddy-%d", time.Now().UnixNano()))
	if err := os.WriteFile(tmp, []byte(caddyfile), 0o644); err != nil {
		return fmt.Errorf("写临时配置失败: %v", err)
	}
	defer os.Remove(tmp)

	if out, err := runner.Run(ctx, "caddy", "validate", "--config", tmp, "--adapter", "caddyfile"); err != nil {
		return fmt.Errorf("Caddyfile 校验失败（未下发，面板域名配置未受影响）: %s", strings.TrimSpace(out))
	}

	if out, err := runner.Run(ctx, "cp", tmp, CaddyfilePath); err != nil {
		return fmt.Errorf("写入 %s 失败: %v %s", CaddyfilePath, err, strings.TrimSpace(out))
	}
	if out, err := runner.Run(ctx, "systemctl", "reload", "caddy"); err != nil {
		// reload 失败尝试 restart（服务可能未运行）
		if out2, err2 := runner.Run(ctx, "systemctl", "restart", "caddy"); err2 != nil {
			return fmt.Errorf("Caddy reload/restart 失败: %v %s", err2, strings.TrimSpace(out2))
		}
		_ = out
	}
	return nil
}

// DeployRawCaddyfile 整文件手改保存：内容经 validate 后直接落盘生效。
// 不重新生成（保留用户对生成块的全部修改）；与面板生成的关系由 UI 说明标注。
func (m *Manager) DeployRawCaddyfile(ctx context.Context, content string) error {
	if m.runner != nil {
		return m.DeployCaddyfile(ctx, m.runner, content)
	}
	return m.adminLoadCaddyfile(ctx, content)
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
