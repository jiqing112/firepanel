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
}

type ServerConfig struct {
	Listen     string `yaml:"listen"`            // 如 ":8080"
	DataDir    string `yaml:"data_dir"`          // SQLite 与运行数据目录
	ProxyHTTP  string `yaml:"proxy_http"`        // 反代 HTTP 监听，默认 ":80"
	ProxyHTTPS string `yaml:"proxy_https"`       // 反代 HTTPS 监听，默认 ":443"
	// 面板自身 HTTPS：填公网 IP 或域名（自动签发 LE 证书，IP 走 6.7 天短期证书自动续期），
	// 或配合 panel_tls_cert/panel_tls_key 使用自签/手动证书。空=关闭。
	PanelHTTPS    string `yaml:"panel_https"`
	PanelHTTPSPort string `yaml:"panel_https_port"` // 面板 HTTPS 监听，默认 ":8443"
	PanelTLSCert  string `yaml:"panel_tls_cert"`    // 手动模式：证书文件路径
	PanelTLSKey   string `yaml:"panel_tls_key"`     // 手动模式：私钥文件路径
	// 面板整站 Basic Auth（应用层登录之外的额外防护层），两者都非空时启用
	BasicAuthUser string `yaml:"basic_auth_user"`
	BasicAuthPass string `yaml:"basic_auth_pass"`
}

type AuthConfig struct {
	// JWTSecret 为空时自动生成并持久化到数据目录
	JWTSecret     string        `yaml:"jwt_secret"`
	TokenTTL      time.Duration `yaml:"token_ttl"`
	LoginRateMax  int           `yaml:"login_rate_max"` // 每分钟每 IP
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
			Listen:         ":8080",
			DataDir:        "/var/lib/firepanel",
			ProxyHTTP:      ":80",
			ProxyHTTPS:     ":443",
			PanelHTTPSPort: ":8443",
		},
		Auth: AuthConfig{
			TokenTTL:     12 * time.Hour,
			LoginRateMax: 10,
		},
		Firewall: FirewallConfig{
			Backend:  "auto",
			SSHPorts: []int{22},
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
		configPath  string
		listen      string
		dataDir     string
		backend     string
		proxyHTTP   string
		proxyHTTPS  string
		panelHTTPS  string
		panelPort   string
		basicAuth   string
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
	if len(cfg.Firewall.SSHPorts) == 0 {
		cfg.Firewall.SSHPorts = []int{22}
	}
	if cfg.Sync.DangerDelay <= 0 {
		cfg.Sync.DangerDelay = 60 * time.Second
	}
	if cfg.Sync.ConfirmWindow <= 0 {
		cfg.Sync.ConfirmWindow = 90 * time.Second
	}
	if cfg.Server.PanelHTTPSPort == "" {
		cfg.Server.PanelHTTPSPort = ":8443"
	}
	return cfg, nil
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
