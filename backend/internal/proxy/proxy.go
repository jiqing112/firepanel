// Package proxy：L7 反向代理管理器。
// 按域名 + 路径前缀路由到上游，支持 WebSocket 透传、手动证书与
// certmagic（Let's Encrypt）自动证书（挑战方式按域名可选：HTTP-01 /
// TLS-ALPN-01 / DNS-01）、上游健康检查、配置热重载。
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
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
)

// RouteConfig 反代路由配置（与 store 解耦的输入模型）。
type RouteConfig struct {
	ID          int64  `json:"id"`
	PathMatch   string `json:"path_match"`
	UpstreamURL string `json:"upstream_url"`
	WsEnabled   bool   `json:"ws_enabled"`
	HealthPath  string `json:"health_path"`
	Enabled     bool   `json:"enabled"`
}

// DomainConfig 域名配置。
type DomainConfig struct {
	ID         int64         `json:"id"`
	Domain     string        `json:"domain"`
	Enabled    bool          `json:"enabled"`
	TLSMode    string        `json:"tls_mode"`  // auto|manual|none
	Challenge  string        `json:"challenge"` // ""=auto | http | alpn | dns（仅 auto 生效）
	CaddyOpts  string        `json:"caddy_opts"` // Caddy 站点选项 JSON：encode/security_headers/log_access
	CertPEM    string        `json:"-"`
	KeyPEM     string        `json:"-"`
	CertStatus string        `json:"cert_status"`
	Routes     []RouteConfig `json:"routes"`
}

// HealthResult 单条路由健康探测结果回调。
type HealthResult struct {
	RouteID int64
	Domain  string
	Status  string // ok|down
	Detail  string
}

// EventCallback 状态事件回调（WS 广播用）。
type EventCallback func(eventType, message, detail string)

// Options Manager 构造参数。
type Options struct {
	Mode       string // builtin（默认，面板自研引擎）| caddy（托管给本机 Caddy）
	HTTPAddr   string // builtin：反代 HTTP 监听；caddy：Caddy 的 HTTP 监听（构建配置用）
	HTTPSAddr  string // builtin：反代 HTTPS 监听；caddy：Caddy 的 HTTPS 监听
	CertDir    string // builtin：certmagic 证书存储目录（面板自身证书也用它）
	CaddyAdmin string // caddy 模式：Admin API 地址
}

// Manager 反代运行时。
type Manager struct {
	log        *slog.Logger
	mode       string // builtin | caddy
	httpAddr   string
	httpsAddr  string
	certDir    string
	caddyAdmin string
	OnEvent    EventCallback
	// OnCertStatus 证书申请结果回写（app 层更新数据库 cert_status/cert_error）
	OnCertStatus func(domainID int64, status, errMsg string)

	mu      sync.Mutex
	domains map[string]*domain // key: 小写域名

	// 证书配置按挑战分组：auto（HTTP+ALPN）/ http / alpn / dns / ip（shortlived）
	cache     *certmagic.Cache
	magic     map[string]*certmagic.Config // group -> config
	nameGroup map[string]string            // 已管理证书名 -> group（续期路由用）
	managed   map[string]string            // 已管理域名 -> group

	dnsCfg DNSProviderConfig // DNS-01 凭据（可能运行中更新，需重建 dns 组）
	runner Runner            // 系统命令执行器（Caddyfile 接管部署用；nil=走 Admin API）
	caddyGlobal CaddyGlobalSettings // Caddyfile 全局选项
	caddyCustom  string              // 用户自定义片段（原样追加，重新生成不丢失）

	httpSrv  *http.Server
	httpsSrv *http.Server
	started  bool
}

// SetRunner 注入命令执行器（启用 Caddyfile 完全接管部署）。
func (m *Manager) SetRunner(r Runner) { m.runner = r }

// SetCaddyCustom 更新用户自定义片段并热重载配置。
func (m *Manager) SetCaddyCustom(custom string) {
	m.mu.Lock()
	m.caddyCustom = custom
	m.mu.Unlock()
}

// CaddyCustom 当前自定义片段。
func (m *Manager) CaddyCustom() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.caddyCustom
}

// SetCaddyGlobal 更新 Caddyfile 全局选项（email/debug）。
func (m *Manager) SetCaddyGlobal(g CaddyGlobalSettings) { m.caddyGlobal = g }

// CaddyGlobal 当前全局选项。
func (m *Manager) CaddyGlobal() CaddyGlobalSettings { return m.caddyGlobal }

// 证书配置分组名。
const (
	groupAuto = "auto" // HTTP-01 + TLS-ALPN-01（默认；certmagic 对被占用端口自动委托）
	groupHTTP = "http"
	groupALPN = "alpn"
	groupDNS  = "dns"
	groupIP   = "ip" // shortlived profile（LE IP 证书）
)

const (
	ModeBuiltin = "builtin"
	ModeCaddy   = "caddy"
)

func NewManager(log *slog.Logger, opts Options) *Manager {
	mode := opts.Mode
	if mode != ModeCaddy {
		mode = ModeBuiltin
	}
	return &Manager{
		log:        log,
		mode:       mode,
		httpAddr:   opts.HTTPAddr,
		httpsAddr:  opts.HTTPSAddr,
		certDir:    opts.CertDir,
		caddyAdmin: opts.CaddyAdmin,
		domains:    map[string]*domain{},
		magic:      map[string]*certmagic.Config{},
		nameGroup:  map[string]string{},
		managed:    map[string]string{},
	}
}

// Mode 当前反代模式。
func (m *Manager) Mode() string { return m.mode }

// Reconfigure 运行中切换引擎与监听端口（UI「运行环境」用）。
// builtin 监听中的端口立即释放，由随后的 Reload 在新地址上重建；
// caddy 模式下监听归 Caddy 管，仅切换管理目标。证书缓存保留（面板 HTTPS 不受影响）。
func (m *Manager) Reconfigure(mode, httpAddr, httpsAddr string) error {
	if mode != ModeBuiltin && mode != ModeCaddy {
		return fmt.Errorf("未知引擎: %s", mode)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mode == ModeBuiltin && m.started {
		if m.httpSrv != nil {
			_ = m.httpSrv.Close()
		}
		if m.httpsSrv != nil {
			_ = m.httpsSrv.Close()
		}
		m.started = false
	}
	m.mode = mode
	if httpAddr != "" {
		m.httpAddr = httpAddr
	}
	if httpsAddr != "" {
		m.httpsAddr = httpsAddr
	}
	return nil
}

type domain struct {
	cfg    DomainConfig
	cert   *tls.Certificate // manual 模式
	routes []*routeEntry    // 按路径长度降序
}

type routeEntry struct {
	cfg RouteConfig
	rp  *httputil.ReverseProxy
}

func (m *Manager) emit(t, msg, detail string) {
	if m.OnEvent != nil {
		m.OnEvent(t, msg, detail)
	}
}

/* ------------------------- 配置热重载 ------------------------- */

// Reload 用全量配置替换运行态（diff 域名，必要时管理/解除 ACME）。
// 注意：证书签发（ManageSync）与 Caddy 下发是秒级网络操作，必须在锁外执行，
// 否则会阻塞反代全部请求与状态查询。
func (m *Manager) Reload(ctx context.Context, cfgs []DomainConfig) error {
	type pendingCert struct {
		domainID int64
		domain   string
		group    string
	}
	var toManage []pendingCert

	m.mu.Lock()
	next := map[string]*domain{}
	for _, c := range cfgs {
		if !c.Enabled {
			continue
		}
		key := strings.ToLower(c.Domain)
		d := &domain{cfg: c}
		if c.TLSMode == "manual" && c.CertPEM != "" && c.KeyPEM != "" {
			cert, err := tls.X509KeyPair([]byte(c.CertPEM), []byte(c.KeyPEM))
			if err != nil {
				m.emit("proxy_cert_error", "手动证书解析失败: "+c.Domain, err.Error())
			} else {
				d.cert = &cert
			}
		}
		for _, rc := range c.Routes {
			if !rc.Enabled {
				continue
			}
			re, err := m.buildRoute(rc)
			if err != nil {
				m.emit("proxy_route_error", "路由构建失败: "+c.Domain+" "+rc.PathMatch, err.Error())
				continue
			}
			d.routes = append(d.routes, re)
		}
		next[key] = d
	}

	// certmagic 增量管理清单（仅 builtin 模式的 auto 模式；移除的域名保留证书
	// 文件但不再续期）。IP 名称走 shortlived profile；域名按 challenge 分组。
	if m.mode == ModeBuiltin {
		m.ensureCertMagicLocked()
		for _, d := range next {
			if d.cfg.TLSMode != "auto" {
				continue
			}
			group, err := m.challengeGroupLocked(d.cfg.Domain, d.cfg.Challenge)
			if err != nil {
				m.emit("proxy_cert_error", "无法为 "+d.cfg.Domain+" 确定签发方式", err.Error())
				m.onCertStatus(d.cfg.ID, "error", err.Error())
				continue
			}
			// 私有/回环 IP 无法获得公网证书，提前拦截并给出明确引导
			if group == groupIP && isNonPublicIP(d.cfg.Domain) {
				msg := "内网/私有 IP 无法申请公网证书，请改用「手动证书 → 自动生成自签证书」或无 TLS 模式"
				m.emit("proxy_cert_error", "无法为 "+d.cfg.Domain+" 自动签发证书", msg)
				m.onCertStatus(d.cfg.ID, "error", msg)
				continue
			}
			if m.managed[d.cfg.Domain] == group {
				continue
			}
			toManage = append(toManage, pendingCert{domainID: d.cfg.ID, domain: d.cfg.Domain, group: group})
		}
	}

	// manual 证书在 caddy 模式下不下发（需用户在 Caddy 侧配置），提示差异
	if m.mode == ModeCaddy {
		for _, d := range next {
			if d.cfg.TLSMode == "manual" {
				m.emit("proxy_cert_error", "caddy 模式不支持面板下发手动证书: "+d.cfg.Domain,
					"请在 Caddy 侧加载证书，或改用 auto/none 模式")
			}
		}
	}

	m.domains = next

	if m.mode == ModeBuiltin && !m.started && len(next) > 0 {
		if err := m.startLocked(); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	m.mu.Unlock()

	// ---- 锁外：秒级网络操作 ----
	var firstErr error
	switch m.mode {
	case ModeBuiltin:
		for _, pc := range toManage {
			m.mu.Lock()
			cfg := m.magic[pc.group]
			already := m.managed[pc.domain] == pc.group
			m.mu.Unlock()
			if already || cfg == nil {
				continue
			}
			if err := cfg.ManageSync(ctx, []string{pc.domain}); err != nil {
				m.emit("proxy_cert_error", "自动证书申请失败: "+pc.domain, err.Error())
				m.onCertStatus(pc.domainID, "error", err.Error())
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			m.mu.Lock()
			m.managed[pc.domain] = pc.group
			m.nameGroup[pc.domain] = pc.group
			m.mu.Unlock()
			m.onCertStatus(pc.domainID, "ok", "")
		}
	case ModeCaddy:
		// 完全接管：生成完整 Caddyfile → validate → 写 /etc/caddy/Caddyfile → reload。
		// 全局设置由 app 层经 SetCaddyGlobal 注入；runner 经 SetRunner 注入。
		if m.runner != nil {
			m.mu.Lock()
			cf := BuildCaddyfile(cfgs, m.caddyGlobal, m.caddyCustom)
			m.mu.Unlock()
			ctxDeploy, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			if err := m.DeployCaddyfile(ctxDeploy, m.runner, cf); err != nil {
				m.emit("proxy_caddy_error", "Caddyfile 下发失败", err.Error())
				return err
			}
			m.emit("proxy_caddy_ok", "Caddyfile 已写入并重载", fmt.Sprintf("%d 个域名", len(next)))
		} else {
			// 无 runner（测试/嵌入）：走 Admin API JSON
			body := BuildCaddyConfig(cfgs, m.httpAddr, m.httpsAddr)
			ctxLoad, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := CaddyLoad(ctxLoad, m.caddyAdmin, body, 8*time.Second); err != nil {
				m.emit("proxy_caddy_error", "Caddy 配置下发失败", err.Error())
				return err
			}
			m.emit("proxy_caddy_ok", "Caddy 配置已下发", fmt.Sprintf("%d 个域名", len(next)))
		}
	}
	return firstErr
}

// challengeGroupLocked 决定域名的证书配置分组（需持 m.mu）。
// IP 名称固定走 shortlived；challenge 显式指定优先；auto=双挑战由 certmagic
// 自行委托（80/443 被占用时 certmagic 会把挑战交给持有者代答——本面板自身
// 监听或寄生前置均覆盖）。
func (m *Manager) challengeGroupLocked(domainName, challenge string) (string, error) {
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

// onCertStatus 回调（app 层回写数据库）。
func (m *Manager) onCertStatus(domainID int64, status, errMsg string) {
	if m.OnCertStatus != nil {
		m.OnCertStatus(domainID, status, errMsg)
	}
}

func (m *Manager) buildRoute(rc RouteConfig) (*routeEntry, error) {
	u, err := url.Parse(rc.UpstreamURL)
	if err != nil {
		return nil, fmt.Errorf("上游地址无效: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("上游协议仅支持 http/https")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("上游地址缺少主机")
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(u)
			pr.SetXForwarded()
			pr.Out.Host = pr.In.Host // 保留原始 Host（上游 vhost 场景）
		},
		// Go 标准库 ReverseProxy 原生支持 WebSocket Upgrade 透传
		FlushInterval: 100 * time.Millisecond,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("FirePanel: upstream unavailable"))
		},
	}
	return &routeEntry{cfg: rc, rp: rp}, nil
}

/* ------------------------- HTTP 服务 ------------------------- */

func (m *Manager) startLocked() error {
	m.ensureCertMagicLocked()

	handler := m.acmeChallengeHandler(http.HandlerFunc(m.ServeHTTP))

	m.httpSrv = &http.Server{
		Addr:              m.httpAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	m.httpsSrv = &http.Server{
		Addr:              m.httpsAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			GetCertificate: m.getCertificate,
			NextProtos:     []string{"h2", "http/1.1", "acme-tls/1"},
		},
	}

	lnHTTP, err := net.Listen("tcp", m.httpAddr)
	if err != nil {
		return fmt.Errorf("HTTP 监听 %s: %w", m.httpAddr, err)
	}
	lnHTTPS, err := net.Listen("tcp", m.httpsAddr)
	if err != nil {
		lnHTTP.Close()
		return fmt.Errorf("HTTPS 监听 %s: %w", m.httpsAddr, err)
	}
	go func() {
		if err := m.httpSrv.Serve(lnHTTP); err != nil && err != http.ErrServerClosed {
			m.log.Error("反代 HTTP 退出", "err", err)
		}
	}()
	go func() {
		if err := m.httpsSrv.ServeTLS(lnHTTPS, "", ""); err != nil && err != http.ErrServerClosed {
			m.log.Error("反代 HTTPS 退出", "err", err)
		}
	}()
	m.started = true
	m.log.Info("反向代理已启动", "http", m.httpAddr, "https", m.httpsAddr)
	return nil
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

// acmeChallengeHandler 让 HTTP-01 挑战在任何面板监听端口上可达。
// 各 ACME issuer 依次尝试处理；非挑战请求直接透传。
func (m *Manager) acmeChallengeHandler(next http.Handler) http.Handler {
	for _, g := range []string{groupAuto, groupHTTP, groupIP} {
		if cfg := m.magic[g]; cfg != nil {
			if iss := acmeIssuerOf(cfg); iss != nil {
				next = iss.HTTPChallengeHandler(next)
			}
		}
	}
	return next
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

// PanelTLSConfig 为面板自身 Web 端口返回 TLS 配置：
// host 为 IP 时签发 Let's Encrypt shortlived 证书（约 6.7 天，自动续期），
// 域名走默认 profile。证书与反代共享存储，同 host 不重复签发。
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

// getCertificate 手动证书优先 → 按分组路由 → 依次兜底各配置。
// 空 SNI（客户端按 IP 直连，RFC 6066 不发送 SNI）时回落到已管理的 IP 证书。
func (m *Manager) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.ToLower(hello.ServerName)
	m.mu.Lock()
	defer m.mu.Unlock()
	if d := m.domains[name]; d != nil && d.cert != nil {
		return d.cert, nil
	}
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

// CertExpiry 查询证书到期时间（手动证书 / certmagic 缓存与存储）。
func (m *Manager) CertExpiry(domainName string) (time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	name := strings.ToLower(domainName)
	if d := m.domains[name]; d != nil && d.cert != nil {
		return leafNotAfter(d.cert)
	}
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

// ServeHTTP 域名 + 最长路径前缀路由。
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(strings.SplitN(r.Host, ":", 2)[0])
	m.mu.Lock()
	d := m.domains[host]
	var route *routeEntry
	if d != nil {
		path := r.URL.Path
		for _, re := range d.routes {
			pm := normalizePathMatch(re.cfg.PathMatch)
			if pm == "/" || path == pm || strings.HasPrefix(path, pm+"/") {
				route = re
				break
			}
		}
	}
	m.mu.Unlock()

	if d == nil || route == nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("FirePanel: no route for " + host + r.URL.Path))
		return
	}
	route.rp.ServeHTTP(w, r)
}

func normalizePathMatch(p string) string {
	if p == "" || p == "/" {
		return "/"
	}
	p = strings.TrimRight(p, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

/* ------------------------- 健康检查 ------------------------- */

// HealthTargets 返回当前需要探测的路由清单。
func (m *Manager) HealthTargets() []HealthTarget {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []HealthTarget
	for _, d := range m.domains {
		for _, re := range d.routes {
			out = append(out, HealthTarget{
				RouteID:    re.cfg.ID,
				Domain:     d.cfg.Domain,
				Upstream:   re.cfg.UpstreamURL,
				HealthPath: re.cfg.HealthPath,
			})
		}
	}
	return out
}

// HealthTarget 健康探测目标。
type HealthTarget struct {
	RouteID    int64  `json:"route_id"`
	Domain     string `json:"domain"`
	Upstream   string `json:"upstream"`
	HealthPath string `json:"health_path"`
}

// Probe 探测一个目标：有 health_path 用 HTTP GET，否则 TCP 拨号。
func (t HealthTarget) Probe(timeout time.Duration) HealthResult {
	client := &http.Client{Timeout: timeout}
	u := strings.TrimRight(t.Upstream, "/")
	if t.HealthPath != "" {
		u += t.HealthPath
	} else {
		u += "/"
	}
	resp, err := client.Get(u)
	if err != nil {
		return HealthResult{RouteID: t.RouteID, Domain: t.Domain, Status: "down", Detail: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return HealthResult{RouteID: t.RouteID, Domain: t.Domain, Status: "down", Detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	return HealthResult{RouteID: t.RouteID, Domain: t.Domain, Status: "ok", Detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}
}

// Running 是否已就绪（builtin=本地监听已启动；caddy=Admin API 可达）。
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mode == ModeCaddy {
		if len(m.domains) == 0 {
			return false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return CaddyRunning(ctx, m.caddyAdmin, 2*time.Second)
	}
	return m.started
}

// Stop 停止监听（面板关闭时调用）。
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.httpSrv != nil {
		_ = m.httpSrv.Close()
	}
	if m.httpsSrv != nil {
		_ = m.httpsSrv.Close()
	}
	m.started = false
}
