// Package proxy：自动证书层。
// certmagic（Let's Encrypt）按挑战方式分组管理域名/IP 证书：HTTP-01 /
// TLS-ALPN-01 / DNS-01，IP 证书走 shortlived profile（约 6.7 天，自动续期）。
// Web 服务引擎（internal/webservice）在热重载时经 CertProvider 接口调用本包；
// 面板自身的 HTTPS 也复用这里的证书存储。
package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
)

// 证书配置按挑战分组：auto（HTTP+ALPN）/ http / alpn / dns / ip（shortlived）
const (
	groupAuto = "auto" // HTTP-01 + TLS-ALPN-01（默认；certmagic 对被占用端口自动委托）
	groupHTTP = "http"
	groupALPN = "alpn"
	groupDNS  = "dns"
	groupIP   = "ip" // shortlived profile（LE IP 证书）
)

// Options Manager 构造参数。
type Options struct {
	CertDir string // certmagic 证书存储目录（面板自身证书也用它）
}

// Manager 证书管理器。实现 webservice.CertProvider。
type Manager struct {
	log     *slog.Logger
	certDir string
	OnEvent func(eventType, message, detail string)

	// mu 保护运行态。读多写少（每个 TLS 握手都要查证书），用 RWMutex
	mu        sync.RWMutex
	cache     *certmagic.Cache
	magic     map[string]*certmagic.Config // group -> config
	nameGroup map[string]string            // 已管理证书名 -> group（续期路由用）
	managed   map[string]string            // 已管理域名 -> group

	dnsCfg DNSProviderConfig // DNS-01 凭据（可能运行中更新，需重建 dns 组）
}

func NewManager(log *slog.Logger, opts Options) *Manager {
	return &Manager{
		log:       log,
		certDir:   opts.CertDir,
		magic:     map[string]*certmagic.Config{},
		nameGroup: map[string]string{},
		managed:   map[string]string{},
	}
}

func (m *Manager) emit(t, msg, detail string) {
	if m.OnEvent != nil {
		m.OnEvent(t, msg, detail)
	}
}

/* ------------------------- CertProvider 接口 ------------------------- */

// Manage 确保域名的自动证书已签发可用。challenge 为 ""（auto）/ http / alpn / dns。
// IP 名称固定走 shortlived profile；私有/回环 IP 无法获得公网证书，返回明确错误。
func (m *Manager) Manage(ctx context.Context, domain, challenge string) error {
	group, err := m.challengeGroup(domain, challenge)
	if err != nil {
		return err
	}
	if group == groupIP && isNonPublicIP(domain) {
		return fmt.Errorf("内网/私有 IP 无法申请公网证书，请改用「手动证书 → 自动生成自签证书」或无 TLS 模式")
	}
	m.mu.Lock()
	cfg := m.groupConfigLocked(group)
	m.mu.Unlock()
	if err := cfg.ManageSync(ctx, []string{domain}); err != nil {
		return err
	}
	m.mu.Lock()
	m.managed[domain] = group
	m.nameGroup[domain] = group
	m.mu.Unlock()
	return nil
}

// GetCertificate 按 SNI 取证书（TLS 握手回调）。
// 空 SNI（客户端按 IP 直连，RFC 6066 不发送 SNI）时回落到已管理的 IP 证书。
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.ToLower(hello.ServerName)
	m.mu.RLock()
	defer m.mu.RUnlock()
	tryCfg := func(group string, h *tls.ClientHelloInfo) (*tls.Certificate, bool) {
		cfg := m.magic[group]
		if cfg == nil {
			return nil, false
		}
		if c, err := cfg.GetCertificate(h); err == nil {
			return c, true
		}
		return nil, false
	}
	if g, ok := m.nameGroup[name]; ok {
		if c, ok := tryCfg(g, hello); ok {
			return c, nil
		}
	}
	// 空 SNI / 未匹配：尝试所有已管理的 IP 名称（IP 直连场景）
	if name == "" || isIPName(name) {
		for n, g := range m.nameGroup {
			if !isIPName(n) {
				continue
			}
			if c, ok := tryCfg(g, &tls.ClientHelloInfo{ServerName: n}); ok {
				return c, nil
			}
		}
	}
	// 兜底：面板自身证书 / 未记录分组的历史证书
	for _, g := range []string{groupIP, groupAuto, groupHTTP, groupALPN, groupDNS} {
		if c, ok := tryCfg(g, hello); ok {
			return c, nil
		}
	}
	return nil, fmt.Errorf("no certificate for %s", name)
}

// Expiry 查询自动证书的到期时间。
func (m *Manager) Expiry(domainName string) (time.Time, bool) {
	return m.CertExpiry(domainName)
}

// HTTPChallengeHandler 让 HTTP-01 挑战在任意 Web 服务监听端口上可达。
// 各 ACME issuer 依次尝试处理；非挑战请求直接透传。
func (m *Manager) HTTPChallengeHandler(next http.Handler) http.Handler {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, g := range []string{groupAuto, groupHTTP, groupIP} {
		if cfg := m.magic[g]; cfg != nil {
			if iss := acmeIssuerOf(cfg); iss != nil {
				next = iss.HTTPChallengeHandler(next)
			}
		}
	}
	return next
}

/* ------------------------- 面板自身 TLS ------------------------- */

// PanelTLSConfig 为面板自身 Web 端口返回 TLS 配置：
// host 为 IP 时签发 Let's Encrypt shortlived 证书（约 6.7 天，自动续期），
// 域名走默认 profile。证书与 Web 服务共享存储，同 host 不重复签发。
func (m *Manager) PanelTLSConfig(ctx context.Context, host string) (*tls.Config, error) {
	m.mu.Lock()
	m.ensureCertMagicLocked()
	group := groupAuto
	if isIPName(host) {
		group = groupIP
	}
	cfg := m.groupConfigLocked(group)
	m.mu.Unlock()

	if err := cfg.ManageSync(ctx, []string{host}); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.nameGroup[strings.ToLower(host)] = group
	m.mu.Unlock()
	return &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			// 客户端按 IP 直连时不发送 SNI（RFC 6066），回落到面板 host 证书
			if hello.ServerName == "" {
				hello.ServerName = host
			}
			return cfg.GetCertificate(hello)
		},
		NextProtos: []string{"h2", "http/1.1", "acme-tls/1"},
		MinVersion: tls.VersionTLS12,
	}, nil
}

/* ------------------------- 内部 ------------------------- */

// challengeGroup 决定域名的证书配置分组。
// IP 名称固定走 shortlived；challenge 显式指定优先；auto=双挑战由 certmagic
// 自行委托（80/443 被占用时 certmagic 会把挑战交给持有者代答——本面板自身
// 监听或寄生前置均覆盖）。
func (m *Manager) challengeGroup(domainName, challenge string) (string, error) {
	if isIPName(domainName) {
		return groupIP, nil
	}
	switch challenge {
	case "http":
		return groupHTTP, nil
	case "alpn":
		return groupALPN, nil
	case "dns":
		if m.dnsCfg.Provider == "" {
			return "", fmt.Errorf("挑战方式 DNS-01 需要先在「DNS-01 凭据」中配置服务商 API 密钥")
		}
		return groupDNS, nil
	case "", "auto":
		return groupAuto, nil
	default:
		return "", fmt.Errorf("未知挑战方式: %s", challenge)
	}
}

// ensureCertMagicLocked 初始化 certmagic 缓存与 auto/ip 两份基础配置（需持 m.mu）。
// 其余分组（http/alpn/dns）在首次用到时惰性创建。
func (m *Manager) ensureCertMagicLocked() {
	if m.cache != nil {
		return
	}
	// 续期时按证书名路由到对应挑战分组的配置：IP → shortlived，
	// 域名 → 分组记录（缺省 auto）。
	m.cache = certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(cert certmagic.Certificate) (*certmagic.Config, error) {
			for _, name := range cert.Names {
				key := strings.ToLower(name)
				if g, ok := m.nameGroup[key]; ok {
					return m.groupConfigLocked(g), nil
				}
				if isIPName(name) {
					return m.groupConfigLocked(groupIP), nil
				}
			}
			return m.groupConfigLocked(groupAuto), nil
		},
	})
	m.groupConfigLocked(groupAuto)
	m.groupConfigLocked(groupIP)
}

// groupConfigLocked 取指定分组的 certmagic 配置（惰性创建，需持 m.mu）。
// issuer 必须绑定到 certmagic.New 返回的最终配置指针（内部会挂 certCache）。
func (m *Manager) groupConfigLocked(group string) *certmagic.Config {
	if cfg := m.magic[group]; cfg != nil {
		return cfg
	}
	storage := &certmagic.FileStorage{Path: m.certDir}
	cfg := certmagic.New(m.cache, certmagic.Config{Storage: storage})
	var issuer *certmagic.ACMEIssuer
	switch group {
	case groupIP:
		issuer = certmagic.NewACMEIssuer(cfg, certmagic.ACMEIssuer{
			Profile: "shortlived", // LE IP 证书仅该 profile（约 160 小时），依赖 certmagic 自动续期
		})
	case groupHTTP:
		issuer = certmagic.NewACMEIssuer(cfg, certmagic.ACMEIssuer{
			DisableTLSALPNChallenge: true, // 80 专用挑战：443 被无关程序占用时避免无谓失败
		})
	case groupALPN:
		issuer = certmagic.NewACMEIssuer(cfg, certmagic.ACMEIssuer{
			DisableHTTPChallenge: true, // 443 专用挑战：80 被占用时走 TLS-ALPN
		})
	case groupDNS:
		issuer = certmagic.NewACMEIssuer(cfg, certmagic.ACMEIssuer{
			DisableHTTPChallenge:    true,
			DisableTLSALPNChallenge: true,
			DNS01Solver: &certmagic.DNS01Solver{
				DNSManager: certmagic.DNSManager{
					DNSProvider:        buildDNSProvider(m.dnsCfg),
					PropagationTimeout: 2 * time.Minute,
				},
			},
		})
	default: // auto：两个挑战都启用
		issuer = certmagic.NewACMEIssuer(cfg, certmagic.ACMEIssuer{})
	}
	cfg.Issuers = []certmagic.Issuer{issuer}
	cfg.OnEvent = m.certEventHandler
	m.magic[group] = cfg
	return cfg
}

// certEventHandler certmagic 事件转发：签发成功刷新事件流；失败类事件进告警通道。
func (m *Manager) certEventHandler(ctx context.Context, event string, data map[string]any) error {
	detail := ""
	if b, err := json.Marshal(data); err == nil {
		detail = string(b)
	}
	switch event {
	case "cert_obtained":
		m.emit("proxy_cert_ok", "证书签发成功", detail)
	case "cert_ocsp_revoked":
		m.emit("proxy_cert_error", "证书被吊销（OCSP）", detail)
	default:
		if strings.Contains(event, "fail") || strings.Contains(event, "error") {
			m.emit("proxy_cert_error", "证书事件: "+event, detail)
		}
	}
	return nil
}

// SetDNSProvider 运行中更新 DNS-01 凭据；dns 分组重建，相关域名重新挂管。
func (m *Manager) SetDNSProvider(cfg DNSProviderConfig) error {
	if cfg.Provider != "" {
		if err := cfg.Validate(); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := m.dnsCfg.Provider != cfg.Provider || m.dnsCfg.APIToken != cfg.APIToken
	m.dnsCfg = cfg
	if changed && m.magic[groupDNS] != nil {
		delete(m.magic, groupDNS)
		for name, g := range m.managed {
			if g == groupDNS {
				delete(m.managed, name)
			}
		}
	}
	return nil
}

// acmeIssuerOf 取配置中的 ACME issuer（挂 HTTP 挑战 handler 用）。
func acmeIssuerOf(cfg *certmagic.Config) *certmagic.ACMEIssuer {
	for _, iss := range cfg.Issuers {
		if a, ok := iss.(*certmagic.ACMEIssuer); ok {
			return a
		}
	}
	return nil
}

// isIPName 判断证书名称是否为 IP 地址。
func isIPName(name string) bool {
	return net.ParseIP(strings.TrimSuffix(strings.ToLower(name), ".")) != nil
}

// isNonPublicIP 判断是否为私有/回环/链路本地地址（无法申请公网证书）。
func isNonPublicIP(name string) bool {
	ip := net.ParseIP(strings.TrimSuffix(strings.ToLower(name), "."))
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// CertExpiry 查询证书到期时间（certmagic 缓存与存储）。
func (m *Manager) CertExpiry(domainName string) (time.Time, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	m.ensureCertMagicLocked()
	name := strings.ToLower(domainName)
	hello := &tls.ClientHelloInfo{ServerName: name}
	for _, g := range []string{groupAuto, groupIP, groupHTTP, groupALPN, groupDNS} {
		cfg := m.magic[g]
		if cfg == nil {
			continue
		}
		if c, err := cfg.GetCertificate(hello); err == nil && len(c.Certificate) > 0 {
			return leafNotAfter(c)
		}
	}
	return time.Time{}, false
}

func leafNotAfter(c *tls.Certificate) (time.Time, bool) {
	if c.Leaf != nil {
		return c.Leaf.NotAfter, true
	}
	if len(c.Certificate) == 0 {
		return time.Time{}, false
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return time.Time{}, false
	}
	return leaf.NotAfter, true
}
