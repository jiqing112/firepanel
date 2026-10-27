// Package config 提供配置文件(YAML)与命令行参数双重支持。
package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是面板运行配置；文件 + CLI 双通道，CLI 优先。
type ProxyConfig struct {
	// 反向代理实现：builtin（面板内置引擎，可监听 80/443 或任意端口）| caddy（托管给本机 Caddy，经 Admin API 下发配置）
	Backend    string `yaml:"backend"`
	CaddyAdmin string `yaml:"caddy_admin"` // Caddy Admin API 地址，默认 http://127.0.0.1:2019
}

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Auth     AuthConfig     `yaml:"auth"`
	Firewall FirewallConfig `yaml:"firewall"`
	Sync     SyncConfig     `yaml:"sync"`
	Logging  LoggingConfig  `yaml:"logging"`
	Proxy    ProxyConfig    `yaml:"proxy"`

	// PrintVersion 仅来自命令行 -version，不参与配置文件。
	PrintVersion bool `yaml:"-"`
	// Warnings 配置加载期间的告警（如旧键冲突），由 main 写入日志。
	Warnings []string `yaml:"-"`
}

type ServerConfig struct {
	Listen     string `yaml:"listen"`      // 如 ":18088"
	DataDir    string `yaml:"data_dir"`    // SQLite 与运行数据目录
	ProxyHTTP  string `yaml:"proxy_http"`  // 反代 HTTP 监听，默认 ":80"
	ProxyHTTPS string `yaml:"proxy_https"` // 反代 HTTPS 监听，默认 ":443"
	// 面板自身 HTTPS：填公网 IP 或域名（自动签发 LE 证书，IP 走 6.7 天短期证书自动续期），
	// 或配合 panel_tls_cert/panel_tls_key 使用自签/手动证书。空=关闭。
	PanelHTTPS     string `yaml:"panel_https"`
	PanelHTTPSPort string `yaml:"panel_https_port"` // 面板 HTTPS 监听，默认 ":8443"
	PanelTLSCert   string `yaml:"panel_tls_cert"`   // 手动模式：证书文件路径
	PanelTLSKey    string `yaml:"panel_tls_key"`    // 手动模式：私钥文件路径
	// 面板整站 Basic Auth（应用层登录之外的额外防护层），两者都非空时启用
	BasicAuthUser string `yaml:"basic_auth_user"`
	BasicAuthPass string `yaml:"basic_auth_pass"`
	// TrustedProxies 仅信任这些来源的 X-Forwarded-For。
	// 默认只信任本机（面板通常与反代/隧道同机）；直接暴露时保持默认。
	TrustedProxies []string `yaml:"trusted_proxies"`
	// SSHPortsLegacy 兼容旧版示例配置（误写在 server 段下）。
	// 新配置请使用 firewall.ssh_ports；两者同时存在时以 firewall.ssh_ports 为准。
	SSHPortsLegacy []int `yaml:"ssh_ports"`
}

type AuthConfig struct {
	// JWTSecret 为空时自动生成并持久化到数据目录
	JWTSecret    string        `yaml:"jwt_secret"`
	TokenTTL     time.Duration `yaml:"token_ttl"`
	LoginRateMax int           `yaml:"login_rate_max"` // 每分钟每 IP
}

type FirewallConfig struct {
	// Backend: auto | iptables | nftables
	Backend  string `yaml:"backend"`
	SSHPorts []int  `yaml:"ssh_ports"` // 防锁死保护端口
}

type SyncConfig struct {
	DriftInterval time.Duration `yaml:"drift_interval"` // 漂移检测周期
	StatsInterval time.Duration `yaml:"stats_interval"` // 采样周期
	DangerDelay   time.Duration `yaml:"danger_delay"`   // 危险规则延时生效窗口
	ConfirmWindow time.Duration `yaml:"confirm_window"` // 应用后确认窗口，超时回滚
}

type LoggingConfig struct {
	Level string `yaml:"level"` // debug|info|warn|error
}

func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Listen:         ":18088",
			DataDir:        "/var/lib/firepanel",
			ProxyHTTP:      ":80",
			ProxyHTTPS:     ":443",
			PanelHTTPSPort: ":8443",
			TrustedProxies: []string{"127.0.0.1", "::1"},
		},
		Auth: AuthConfig{
			TokenTTL:     12 * time.Hour,
			LoginRateMax: 10,
		},
		Firewall: FirewallConfig{
			Backend: "auto",
		},
		Sync: SyncConfig{
			DriftInterval: 30 * time.Second,
			StatsInterval: 5 * time.Second,
			DangerDelay:   60 * time.Second,
			ConfirmWindow: 90 * time.Second,
		},
		Logging: LoggingConfig{Level: "info"},
		Proxy: ProxyConfig{
			Backend:    "builtin",
			CaddyAdmin: "http://127.0.0.1:2019",
		},
	}
}

// Load 依次读取默认值 → 配置文件 → CLI 参数。
func Load() (*Config, error) {
	cfg := defaults()

	var (
		configPath string
		listen     string
		dataDir    string
		backend    string
		proxyHTTP  string
		proxyHTTPS string
		panelHTTPS string
		panelPort  string
		basicAuth  string
	)
	flag.StringVar(&configPath, "config", "", "配置文件路径 (YAML)")
	flag.StringVar(&listen, "listen", "", "HTTP 监听地址，覆盖配置文件")
	flag.StringVar(&dataDir, "data-dir", "", "数据目录，覆盖配置文件")
	flag.StringVar(&backend, "backend", "", "防火墙后端 auto|iptables|nftables，覆盖配置文件")
	flag.StringVar(&proxyHTTP, "proxy-http", "", "反代 HTTP 监听地址（默认 :80），覆盖配置文件")
	flag.StringVar(&proxyHTTPS, "proxy-https", "", "反代 HTTPS 监听地址（默认 :443），覆盖配置文件")
	flag.StringVar(&panelHTTPS, "panel-https", "", "面板 HTTPS 的 IP/域名（自动签发 LE 证书），空=关闭")
	flag.StringVar(&panelPort, "panel-https-port", "", "面板 HTTPS 监听地址（默认 :8443）")
	flag.StringVar(&basicAuth, "basic-auth", "", "面板整站 Basic Auth，格式 user:password")
	printVersion := flag.Bool("version", false, "打印版本后退出")
	flag.Parse()

	// 未显式指定配置文件时，自动探测程序目录 / 工作目录下的 config.yaml
	if configPath == "" {
		for _, candidate := range autoConfigPaths() {
			if _, err := os.Stat(candidate); err == nil {
				configPath = candidate
				break
			}
		}
	}

	if configPath != "" {
		b, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("读取配置文件: %w", err)
		}
		if err := yaml.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("解析配置文件 %s: %w", configPath, err)
		}
	}

	// 环境变量兜底（容器场景）
	if v := os.Getenv("FIREPANEL_LISTEN"); v != "" && listen == "" {
		listen = v
	}
	if v := os.Getenv("FIREPANEL_DATA_DIR"); v != "" && dataDir == "" {
		dataDir = v
	}

	if listen != "" {
		cfg.Server.Listen = listen
	}
	if dataDir != "" {
		cfg.Server.DataDir = dataDir
	}
	if backend != "" {
		cfg.Firewall.Backend = backend
	}
	if proxyHTTP != "" {
		cfg.Server.ProxyHTTP = proxyHTTP
	}
	if proxyHTTPS != "" {
		cfg.Server.ProxyHTTPS = proxyHTTPS
	}
	if panelHTTPS != "" {
		cfg.Server.PanelHTTPS = panelHTTPS
	}
	if panelPort != "" {
		cfg.Server.PanelHTTPSPort = panelPort
	}
	if basicAuth != "" {
		if user, pass, ok := strings.Cut(basicAuth, ":"); ok {
			cfg.Server.BasicAuthUser = user
			cfg.Server.BasicAuthPass = pass
		}
	}

	if cfg.Server.DataDir == "" {
		cfg.Server.DataDir = "./data"
	}
	resolveSSHPorts(cfg)
	if cfg.Sync.DangerDelay <= 0 {
		cfg.Sync.DangerDelay = 60 * time.Second
	}
	if cfg.Sync.ConfirmWindow <= 0 {
		cfg.Sync.ConfirmWindow = 90 * time.Second
	}
	if cfg.Server.PanelHTTPSPort == "" {
		cfg.Server.PanelHTTPSPort = ":8443"
	}
	if len(cfg.Server.TrustedProxies) == 0 {
		cfg.Server.TrustedProxies = []string{"127.0.0.1", "::1"}
	}
	cfg.PrintVersion = *printVersion
	return cfg, nil
}

// resolveSSHPorts 归并 SSH 保护端口配置。
// firewall.ssh_ports 优先；旧版误写在 server 段下的 ssh_ports 只在新区为空时
// 兜底并告警。反过来（旧键无条件覆盖）会让按 README 配了 firewall.ssh_ports
// 的用户实际仍只保护 22，防锁死保护失效。
func resolveSSHPorts(cfg *Config) {
	if len(cfg.Server.SSHPortsLegacy) > 0 {
		if len(cfg.Firewall.SSHPorts) > 0 {
			cfg.Warnings = append(cfg.Warnings,
				"配置同时存在 firewall.ssh_ports 与旧版 server.ssh_ports，已忽略后者；请删除 server.ssh_ports")
		} else {
			cfg.Firewall.SSHPorts = cfg.Server.SSHPortsLegacy
			cfg.Warnings = append(cfg.Warnings,
				"检测到旧版 server.ssh_ports，已作为 firewall.ssh_ports 使用；建议迁移到 firewall 段")
		}
		cfg.Server.SSHPortsLegacy = nil
	}
	if len(cfg.Firewall.SSHPorts) == 0 {
		cfg.Firewall.SSHPorts = []int{22}
	}
}

// autoConfigPaths 自动探测的配置文件候选路径（程序目录优先）。
func autoConfigPaths() []string {
	var out []string
	if exe, err := os.Executable(); err == nil {
		out = append(out, filepath.Join(filepath.Dir(exe), "config.yaml"))
	}
	out = append(out, "config.yaml")
	return out
}

// DBPath 返回 SQLite 数据库文件路径。
func (c *Config) DBPath() string {
	return filepath.Join(c.Server.DataDir, "firepanel.db")
}
