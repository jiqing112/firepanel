package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"firepanel/internal/config"
	"firepanel/internal/fw"
	"firepanel/internal/store"
)

// BasicAuthState 整站 Basic Auth 凭据（nil 或空凭据 = 关闭）。
// 哈希在写入时预计算，中间件每次请求做常量时间比较。
type BasicAuthState struct {
	User string
	Pass string
	uSum [32]byte
	pSum [32]byte
}

func NewBasicAuthState(user, pass string) *BasicAuthState {
	s := &BasicAuthState{User: user, Pass: pass}
	s.uSum = sha256.Sum256([]byte(user))
	s.pSum = sha256.Sum256([]byte(pass))
	return s
}

// Match 校验请求凭据（常量时间）。
func (s *BasicAuthState) Match(user, pass string) bool {
	u := sha256.Sum256([]byte(user))
	p := sha256.Sum256([]byte(pass))
	return u == s.uSum && p == s.pSum
}

// Enabled 凭据是否完整（用户名与密码均非空）。
func (s *BasicAuthState) Enabled() bool {
	return s != nil && s.User != "" && s.Pass != ""
}

// 凭据持久化为 basicauth.yaml（0600），方便手动查看与修改；不写入数据库。
// 位置优先数据目录（Docker 下随卷持久化；与 db/certs 同处），
// 兼容旧版的「二进制同目录」位置：读取时两个位置都认，写入固定到数据目录。
const basicAuthFileName = "basicauth.yaml"

type basicauthFile struct {
	Enabled  bool   `yaml:"enabled"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// basicAuthCandidates 凭据文件候选路径（按优先级）。
func basicAuthCandidates(cfg *config.Config) []string {
	var out []string
	if cfg != nil && cfg.Server.DataDir != "" {
		out = append(out, filepath.Join(cfg.Server.DataDir, basicAuthFileName))
	}
	if exe, err := os.Executable(); err == nil {
		legacy := filepath.Join(filepath.Dir(exe), basicAuthFileName)
		out = append(out, legacy)
	}
	return out
}

// basicAuthWritePath 写入位置：数据目录；未配置时退回程序目录。
func basicAuthWritePath(cfg *config.Config) (string, error) {
	if cfg != nil && cfg.Server.DataDir != "" {
		return filepath.Join(cfg.Server.DataDir, basicAuthFileName), nil
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), basicAuthFileName), nil
	}
	return "", fmt.Errorf("无法确定 basicauth.yaml 的写入位置")
}

// readBasicAuthFile 读取凭据文件（数据目录优先，其次旧位置）。
func readBasicAuthFile(cfg *config.Config) (basicauthFile, string, bool) {
	for _, path := range basicAuthCandidates(cfg) {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var f basicauthFile
		if yaml.Unmarshal(b, &f) != nil {
			continue
		}
		return f, path, true
	}
	return basicauthFile{}, "", false
}

// LoadBasicAuth 装载 Basic Auth 状态：
// basicauth.yaml > 旧版 settings 迁移 > 主配置文件/启动参数。
func LoadBasicAuth(st *store.Store, cfg *config.Config) *BasicAuthState {
	if f, _, ok := readBasicAuthFile(cfg); ok {
		if f.Enabled {
			return NewBasicAuthState(f.Username, f.Password)
		}
		return nil // 文件存在即权威：显式关闭
	}
	// 旧版迁移：settings 里存过的凭据搬进文件后删除
	if st != nil {
		if raw, err := st.GetSetting("basicauth"); err == nil && raw != "" {
			var s struct {
				Enabled  bool   `json:"enabled"`
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if json.Unmarshal([]byte(raw), &s) == nil {
				_ = saveBasicAuthFile(cfg, s.Enabled, s.Username, s.Password)
				_ = st.DeleteSetting("basicauth")
				if s.Enabled {
					return NewBasicAuthState(s.Username, s.Password)
				}
				return nil
			}
		}
	}
	if cfg.Server.BasicAuthUser != "" && cfg.Server.BasicAuthPass != "" {
		return NewBasicAuthState(cfg.Server.BasicAuthUser, cfg.Server.BasicAuthPass)
	}
	return nil
}

// SaveBasicAuth 凭据写入 basicauth.yaml（原子写 + 0600）。
func (a *App) SaveBasicAuth(enabled bool, user, pass string) error {
	if err := saveBasicAuthFile(a.Cfg, enabled, user, pass); err != nil {
		return err
	}
	if enabled {
		a.SetBasicAuth(NewBasicAuthState(user, pass))
	} else {
		a.SetBasicAuth(nil)
	}
	return nil
}

func saveBasicAuthFile(cfg *config.Config, enabled bool, user, pass string) error {
	path, err := basicAuthWritePath(cfg)
	if err != nil {
		return err
	}
	b, err := yaml.Marshal(basicauthFile{Enabled: enabled, Username: user, Password: pass})
	if err != nil {
		return err
	}
	b = append([]byte("# FirePanel 整站 Basic Auth（设置页修改后自动写入；手动编辑后约 2 秒热生效）\n"), b...)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建 %s 失败: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("写入 %s 失败（检查目录权限）: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return nil
}

// BasicAuthSource 当前凭据来源："file"（basicauth.yaml 存在）| "config" | ""。
func (a *App) BasicAuthSource() string {
	for _, path := range basicAuthCandidates(a.Cfg) {
		if _, err := os.Stat(path); err == nil {
			return "file"
		}
	}
	if a.Cfg.Server.BasicAuthUser != "" {
		return "config"
	}
	return ""
}

// LoadBasicAuthFile 读取文件内凭据（无论 enabled），供设置页回显用户名与"已有密码"状态。
func (a *App) LoadBasicAuthFile() (username, password string, ok bool) {
	f, _, found := readBasicAuthFile(a.Cfg)
	if !found || f.Password == "" {
		return "", "", false
	}
	return f.Username, f.Password, true
}

// WatchBasicAuthFile 监听 basicauth.yaml 变化并热装载（mtime 轮询，2s 粒度）。
// 手动编辑文件后无需重启面板；文件缺失/非法视为关闭。装载结果广播事件。
func (a *App) WatchBasicAuthFile(ctx context.Context) {
	lastMod := time.Time{}
	lastPath := ""
	lastEnabled := a.GetBasicAuth().Enabled()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			path := ""
			var mod time.Time
			for _, p := range basicAuthCandidates(a.Cfg) {
				if fi, err := os.Stat(p); err == nil {
					path, mod = p, fi.ModTime()
					break
				}
			}
			if path == "" {
				if lastEnabled {
					// 文件被删除：关闭防护
					lastMod, lastPath, lastEnabled = time.Time{}, "", false
					a.SetBasicAuth(nil)
					a.broadcast("events", fw.Event{
						Type: "basicauth_changed", Timestamp: time.Now(),
						Message: "basicauth.yaml 已删除，整站 Basic Auth 自动关闭",
					})
				}
				continue
			}
			if path == lastPath && mod.Equal(lastMod) {
				continue
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				continue
			}
			var f basicauthFile
			if yaml.Unmarshal(b, &f) != nil {
				continue // 半写状态（编辑器保存中间帧），下轮再试
			}
			lastPath, lastMod = path, mod
			enabled := f.Enabled && f.Username != "" && f.Password != ""
			changed := enabled != lastEnabled
			if enabled {
				a.SetBasicAuth(NewBasicAuthState(f.Username, f.Password))
			} else {
				a.SetBasicAuth(nil)
			}
			lastEnabled = enabled
			if changed {
				if enabled {
					a.broadcast("events", fw.Event{
						Type: "basicauth_changed", Timestamp: time.Now(),
						Message: fmt.Sprintf("basicauth.yaml 已变更：整站 Basic Auth 开启（用户 %s）", f.Username),
					})
				} else {
					a.broadcast("events", fw.Event{
						Type: "basicauth_changed", Timestamp: time.Now(),
						Message: "basicauth.yaml 已变更：整站 Basic Auth 关闭",
					})
				}
			}
		}
	}
}
