package webservice

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CertProvider 提供自动证书的签发与查找（由证书层实现，certmagic 驱动）。
type CertProvider interface {
	// Manage 确保域名的自动证书已签发可用（秒级网络操作，调用方需在锁外执行）。
	Manage(ctx context.Context, domain, challenge string) error
	// GetCertificate 按 SNI 取自动管理的证书。
	GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error)
	// Expiry 查询自动证书的到期时间。
	Expiry(domain string) (time.Time, bool)
	// HTTPChallengeHandler 让 HTTP-01 挑战在 HTTP 监听上可达，非挑战请求直接透传。
	HTTPChallengeHandler(next http.Handler) http.Handler
}

// OnCertStatusFunc 证书申请结果回写（app 层更新数据库 cert_status/cert_error）。
type OnCertStatusFunc func(siteID int64, status, errMsg string)

// ListenerConfig 是一个监听端口及其站点（gately 式监听模型）。
type ListenerConfig struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Enable   bool   `json:"enable"`
	ListenIP string `json:"listen_ip"` // 空=全部地址
	Port     int    `json:"port"`
	// TLS 开启时监听 HTTPS；TLS 端口收到明文请求按 PlainHTTP 处理（嗅探首个字节分发）
	TLS    bool   `json:"tls"`
	MinTLS string `json:"min_tls"` // "1.0".."1.3"，空=1.2
	// PlainHTTP 为 TLS 端口收到明文请求的处理：空=不接受、serve=按站点提供、redirect=跳转 HTTPS
	PlainHTTP string `json:"plain_http"`
	// HTTPPort 为额外的明文 HTTP 端口（0=不开），与主端口共用全部站点，常用于 HTTPS 监听 443 时再开 80
	HTTPPort     int    `json:"http_port"`
	HTTPPortMode string `json:"http_port_mode"` // redirect（跳转主端口 https）| serve（直接提供站点）
	Remark       string `json:"remark"`
	// Sites 为该监听的站点；Default 标记的站点处理未匹配任何站点域名的请求（至多一个）
	Sites []Site `json:"sites"`
}

// validate 校验监听配置。
func (l ListenerConfig) Validate() error {
	if len(strings.TrimSpace(l.Name)) > 60 {
		return fmt.Errorf("名称过长")
	}
	if l.Port < 1 || l.Port > 65535 {
		return fmt.Errorf("端口需在 1 到 65535 之间")
	}
	if l.HTTPPort < 0 || l.HTTPPort > 65535 {
		return fmt.Errorf("HTTP 端口不合法")
	}
	if l.HTTPPort == l.Port {
		return fmt.Errorf("HTTP 端口不能与主端口相同")
	}
	ip := strings.TrimSpace(l.ListenIP)
	if ip != "" && net.ParseIP(ip) == nil {
		return fmt.Errorf("监听地址 %q 不是合法的 IP", l.ListenIP)
	}
	l.ListenIP = ip
	switch l.PlainHTTP {
	case "", HTTPServe, HTTPRedirect:
	default:
		return fmt.Errorf("明文处理方式必须为 serve 或 redirect")
	}
	switch l.HTTPPortMode {
	case "", HTTPServe, HTTPRedirect:
	default:
		return fmt.Errorf("HTTP 端口行为必须为 serve 或 redirect")
	}
	if l.HTTPPort > 0 && l.HTTPPortMode == "" {
		l.HTTPPortMode = HTTPServe
		if l.TLS {
			l.HTTPPortMode = HTTPRedirect
		}
	}
	switch l.MinTLS {
	case "", "1.0", "1.1", "1.2", "1.3":
	default:
		return fmt.Errorf("最低 TLS 版本必须为 1.0/1.1/1.2/1.3")
	}
	defaults := 0
	for i := range l.Sites {
		if l.Sites[i].Default {
			defaults++
		}
	}
	if defaults > 1 {
		return fmt.Errorf("默认站点至多一个")
	}
	return nil
}

// Options Manager 构造参数。
type Options struct {
	Log     *slog.Logger
	OnEvent eventFunc
	Certs   CertProvider
}

// Manager Web 服务运行时：运行多个监听器，按域名路由站点，热重载配置。
type Manager struct {
	log      *logger
	certs    CertProvider
	onStatus OnCertStatusFunc

	mu        sync.RWMutex
	cfgs      []ListenerConfig // 全部配置（含停用），拓扑视图从中读取
	runners   map[int64]*listenerRunner
	bgCancel  context.CancelFunc
	started   bool
	startedAt time.Time
	lastErr   string
}

// New 构建 Web 服务管理器。
func New(log *slog.Logger, opts Options) *Manager {
	return &Manager{
		log:     &logger{log: log, onEvent: opts.OnEvent},
		certs:   opts.Certs,
		runners: map[int64]*listenerRunner{},
	}
}

// SetCertStatusCallback 注入证书状态回写。
func (m *Manager) SetCertStatusCallback(fn OnCertStatusFunc) {
	m.onStatus = fn
}

func (m *Manager) onCertStatus(siteID int64, status, errMsg string) {
	if m.onStatus != nil {
		m.onStatus(siteID, status, errMsg)
	}
}

/* ------------------------- 配置热重载 ------------------------- */

// Reload 用全量配置替换运行态。秒级网络操作（证书签发）在锁外执行，
// 不会阻塞站点请求与状态查询。
func (m *Manager) Reload(ctx context.Context, listeners []ListenerConfig) error {
	type pendingCert struct {
		siteID    int64
		domain    string
		challenge string
	}
	var toManage []pendingCert

	// 锁内完成编译；秒级网络操作（停止/启动监听、证书签发）在锁外执行。
	m.mu.Lock()

	// 沿用旧处理器统计：同一站点热更新时计数与进行中的请求不清零
	oldHandlers := map[int64]*siteHandler{}
	for _, r := range m.runners {
		for _, h := range r.current().all() {
			oldHandlers[h.site.ID] = h
		}
		if r.deflt != nil {
			oldHandlers[r.deflt.site.ID] = r.deflt
		}
	}

	var firstErr error
	nextCfgs := make([]ListenerConfig, 0, len(listeners))
	newRunners := map[int64]*listenerRunner{}
	var toStop []*listenerRunner
	for i := range listeners {
		l := &listeners[i]
		if err := l.Validate(); err != nil {
			m.log.warn("监听 %s(%d) 配置无效: %v", l.Name, l.ID, err)
			if firstErr == nil {
				firstErr = fmt.Errorf("监听 :%d: %w", l.Port, err)
			}
			continue
		}
		nextCfgs = append(nextCfgs, *l)
		if !l.Enable {
			// 停用的监听不编译；旧 runner 在锁外停止
			if r := m.runners[l.ID]; r != nil {
				toStop = append(toStop, r)
			}
			continue
		}
		if old := m.runners[l.ID]; old != nil && old.sameSocket(l) {
			// 套接字不变：只热换站点表，监听与已有连接不受影响
			old.swapSites(l, oldHandlers)
			newRunners[l.ID] = old
		} else {
			if old != nil {
				toStop = append(toStop, old)
			}
			bgCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
			r, err := m.buildRunner(l, oldHandlers, bgCtx, cancel)
			if err != nil {
				m.log.warn("监听 :%d 构建失败: %v", l.Port, err)
				r.err = err.Error()
				cancel()
				if firstErr == nil {
					firstErr = err
				}
				newRunners[l.ID] = r
				continue
			}
			newRunners[l.ID] = r
		}
		for _, s := range l.Sites {
			if !s.Enabled || s.TLSMode != "auto" || m.certs == nil {
				continue
			}
			for _, d := range s.Domains {
				if d == "*" || strings.HasPrefix(d, "*.") {
					// 通配域名证书需要 DNS-01；challenge 不是 dns 时签发必然失败，提前拦截
					if s.Challenge != "" && s.Challenge != "dns" {
						msg := "通配域名 " + d + " 需使用 DNS-01 挑战（泛域名无法走 80/443 校验）"
						m.log.event("proxy_cert_error", "无法为 "+d+" 自动签发证书", msg)
						m.onCertStatus(s.ID, "error", msg)
						continue
					}
				}
				toManage = append(toManage, pendingCert{siteID: s.ID, domain: d, challenge: s.Challenge})
			}
		}
	}
	m.cfgs = nextCfgs
	m.runners = newRunners
	m.mu.Unlock()

	// ---- 锁外：停止旧监听 → 启动新监听 → 证书签发 ----
	var startErr error
	for _, r := range toStop {
		r.stop()
	}
	for _, r := range newRunners {
		if r.ln != nil {
			continue
		}
		// 上次启动失败的监听每次 Reload 都重试（端口可能已被释放）
		r.err = ""
		if err := r.start(m.certs); err != nil {
			r.err = err.Error()
			m.log.warn("监听 :%d 启动失败: %v", r.cfg.Port, err)
			if startErr == nil {
				startErr = err
			}
		}
	}

	seen := map[string]bool{}
	var manageErr error
	for _, pc := range toManage {
		if seen[pc.domain] {
			continue
		}
		seen[pc.domain] = true
		if err := m.certs.Manage(ctx, pc.domain, pc.challenge); err != nil {
			m.log.event("proxy_cert_error", "自动证书申请失败: "+pc.domain, err.Error())
			m.onCertStatus(pc.siteID, "error", err.Error())
			if manageErr == nil {
				manageErr = err
			}
			continue
		}
		m.onCertStatus(pc.siteID, "ok", "")
	}

	m.mu.Lock()
	m.started = len(newRunners) > 0 && startErr == nil
	if m.started && m.startedAt.IsZero() {
		m.startedAt = time.Now()
	}
	m.lastErr = ""
	if startErr != nil {
		m.lastErr = startErr.Error()
	}
	m.mu.Unlock()

	if firstErr != nil {
		return firstErr
	}
	// 启动失败必须向上返回：调用方（启动/保存接口）要能感知监听没起来
	if startErr != nil {
		return startErr
	}
	return manageErr
}

// buildRunner 编译一个监听器（未启动）。配置无效的站点跳过（事件已记录）。
func (m *Manager) buildRunner(l *ListenerConfig, oldHandlers map[int64]*siteHandler, bgCtx context.Context, cancel context.CancelFunc) (*listenerRunner, error) {
	r := &listenerRunner{cfg: *l, key: "l:" + strconv.FormatInt(l.ID, 10), log: m.log, onStatus: m.onCertStatus, baseCtx: bgCtx, cancel: cancel}
	r.acts.redirect.recent = newRecentIPs()
	r.acts.notFound.recent = newRecentIPs()
	r.table.Store(buildListenerTable(m.log, m.onCertStatus, l, oldHandlers, bgCtx))
	r.deflt = r.defltFromTable()
	return r, nil
}

// swapSites 套接字不变时热换站点表：已有连接与监听级动作计数保持。
func (r *listenerRunner) swapSites(l *ListenerConfig, oldHandlers map[int64]*siteHandler) {
	r.cfg = *l
	r.table.Store(buildListenerTable(r.log, r.onStatus, l, oldHandlers, r.baseCtx))
	r.deflt = r.defltFromTable()
}

// defltFromTable 取编译时标记为默认站点的处理器。
func (r *listenerRunner) defltFromTable() *siteHandler {
	for _, h := range r.current().all() {
		if h.deflt {
			return h
		}
	}
	return nil
}

// buildTable 编译监听的站点表。
func buildListenerTable(log *logger, onCertStatus OnCertStatusFunc, l *ListenerConfig, oldHandlers map[int64]*siteHandler, bgCtx context.Context) *siteTable {
	build := func(s *Site, deflt bool) (*siteHandler, error) {
		h, err := newSiteHandler(s, log)
		if err != nil {
			return nil, fmt.Errorf("站点 %s: %w", siteLabel(s), err)
		}
		if o := oldHandlers[s.ID]; o != nil {
			h.inherit(o)
		}
		h.listenerID = l.ID
		h.deflt = deflt
		h.startBackground(bgCtx)
		return h, nil
	}
	_ = log
	t := &siteTable{exact: map[string]*siteHandler{}}
	for i := range l.Sites {
		s := &l.Sites[i]
		if !s.Enabled {
			continue
		}
		if err := s.Validate(); err != nil {
			log.warn("站点 %s(%d) 配置无效: %v", siteLabel(s), s.ID, err)
			continue
		}
		h, err := build(s, s.Default)
		if err != nil {
			log.warn("%v", err)
			continue
		}
		if s.TLSMode == "manual" && s.CertPEM != "" && s.KeyPEM != "" {
			cert, err := tls.X509KeyPair([]byte(s.CertPEM), []byte(s.KeyPEM))
			if err != nil {
				log.event("proxy_cert_error", "手动证书解析失败: "+firstDomain(s.Domains), err.Error())
				if onCertStatus != nil {
					onCertStatus(s.ID, "error", err.Error())
				}
			} else {
				h.cert = &cert
				if onCertStatus != nil {
					onCertStatus(s.ID, "ok", "")
				}
			}
		}
		for _, d := range s.Domains {
			if strings.Contains(d, "*") {
				t.wild = append(t.wild, wildDomain{h: h, domain: d})
			} else {
				if _, dup := t.exact[d]; !dup {
					t.exact[d] = h
				}
			}
		}
		t.sites = append(t.sites, h)
	}
	return t
}

/* ------------------------- 监听器运行 ------------------------- */

// siteTable 是一个监听器在某一时刻的全部站点。
type siteTable struct {
	sites []*siteHandler
	exact map[string]*siteHandler
	wild  []wildDomain
}

// wildDomain 是一个含通配符的站点域名。
type wildDomain struct {
	h      *siteHandler
	domain string
}

// lookup 按 Host 选择站点：精确匹配优先，其次通配（先登记的优先）。
func (t *siteTable) lookup(host string) *siteHandler {
	if h, ok := t.exact[host]; ok {
		return h
	}
	for _, w := range t.wild {
		if matchDomain(w.domain, host) > 0 {
			return w.h
		}
	}
	return nil
}

// all 返回全部站点。
func (t *siteTable) all() []*siteHandler {
	return t.sites
}

// listenerRunner 运行一个监听器。
type listenerRunner struct {
	cfg      ListenerConfig
	key      string
	log      *logger
	onStatus OnCertStatusFunc

	// table 为当前生效的站点表。修改站点时整体替换，监听端口与已建立的连接不受影响
	table atomicTable
	deflt *siteHandler

	srv      *http.Server // 主端口（TLS 或明文）
	httpSrv  *http.Server // TLS 监听的明文分发服务（PlainHTTP=serve）
	httpSrv2 *http.Server // 额外明文端口（HTTPPort）
	ln       net.Listener
	httpLn   net.Listener

	// acts 为监听级动作：明文跳转 HTTPS、未匹配站点 404
	acts struct {
		redirect listenerAct
		notFound listenerAct
	}

	baseCtx context.Context
	cancel  context.CancelFunc
	err     string
}

// sameSocket 判断新配置能否沿用正在运行的监听（端口、TLS 与明文处理都不变）：
// 可以时只热换站点表，不断开已有连接、不重绑端口。
func (r *listenerRunner) sameSocket(l *ListenerConfig) bool {
	return r.cfg.Port == l.Port && r.cfg.TLS == l.TLS && r.cfg.PlainHTTP == l.PlainHTTP &&
		r.cfg.HTTPPort == l.HTTPPort && r.cfg.HTTPPortMode == l.HTTPPortMode && r.cfg.ListenIP == l.ListenIP &&
		r.cfg.MinTLS == l.MinTLS
}

// atomicTable 包装 siteTable 的原子替换。
type atomicTable struct {
	mu sync.RWMutex
	t  *siteTable
}

func (a *atomicTable) Store(t *siteTable) {
	a.mu.Lock()
	a.t = t
	a.mu.Unlock()
}

func (a *atomicTable) Load() *siteTable {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.t
}

// current 返回当前生效的站点表。
func (r *listenerRunner) current() *siteTable {
	if t := r.table.Load(); t != nil {
		return t
	}
	return &siteTable{}
}

// start 启动监听（幂等：已运行时跳过）。
func (r *listenerRunner) start(certs CertProvider) error {
	if r.ln != nil {
		return nil
	}
	addr := net.JoinHostPort(r.cfg.ListenIP, strconv.Itoa(r.cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", addr, err)
	}
	r.ln = ln
	var plainHandler http.Handler = http.HandlerFunc(r.servePlain)
	if certs != nil {
		plainHandler = certs.HTTPChallengeHandler(plainHandler)
	}

	if !r.cfg.TLS {
		// 明文监听：直接服务站点
		r.srv = &http.Server{Handler: plainHandler, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := r.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				r.log.errorf("监听 :%d 退出: %v", r.cfg.Port, err)
			}
		}()
	} else {
		// TLS 监听：嗅探首个字节，TLS 握手交给 TLS 服务，明文按 PlainHTTP 处理
		tlsSrv := &http.Server{
			Handler:           http.HandlerFunc(r.serveTLS),
			ReadHeaderTimeout: 10 * time.Second,
			TLSConfig:         r.tlsConfig(certs),
		}
		plainSrv := &http.Server{Handler: plainHandler, ReadHeaderTimeout: 10 * time.Second}
		r.srv = tlsSrv
		r.httpSrv = plainSrv
		tlsCh, plainCh := newChanListener(), newChanListener()
		go func() {
			if err := tlsSrv.Serve(tlsCh); err != nil && err != http.ErrServerClosed {
				r.log.errorf("监听 :%d TLS 退出: %v", r.cfg.Port, err)
			}
		}()
		go func() {
			if err := plainSrv.Serve(plainCh); err != nil && err != http.ErrServerClosed {
				r.log.errorf("监听 :%d 明文退出: %v", r.cfg.Port, err)
			}
		}()
		go r.acceptSniff(ln, r.tlsConfig(certs), tlsCh, plainCh)
	}

	// 额外明文端口
	if r.cfg.HTTPPort > 0 {
		hAddr := net.JoinHostPort(r.cfg.ListenIP, strconv.Itoa(r.cfg.HTTPPort))
		hLn, herr := net.Listen("tcp", hAddr)
		if herr != nil {
			r.log.warn("HTTP 端口 %d 监听失败: %v", r.cfg.HTTPPort, herr)
		} else {
			r.httpLn = hLn
			var hHandler http.Handler
			if r.cfg.HTTPPortMode == HTTPRedirect {
				hHandler = http.HandlerFunc(r.serveRedirect)
			} else {
				hHandler = plainHandler
			}
			r.httpSrv2 = &http.Server{Handler: hHandler, ReadHeaderTimeout: 10 * time.Second}
			go func() {
				if err := r.httpSrv2.Serve(hLn); err != nil && err != http.ErrServerClosed {
					r.log.errorf("HTTP 端口 %d 退出: %v", r.cfg.HTTPPort, err)
				}
			}()
		}
	}
	r.err = ""
	r.log.info("监听 :%d 已启动 tls=%v httpPort=%d mode=%s", r.cfg.Port, r.cfg.TLS, r.cfg.HTTPPort, r.cfg.HTTPPortMode)
	return nil
}

func (r *listenerRunner) stop() {
	if r.cancel != nil {
		r.cancel()
	}
	if r.srv != nil {
		_ = r.srv.Close()
	}
	if r.httpSrv != nil {
		_ = r.httpSrv.Close()
	}
	if r.httpSrv2 != nil {
		_ = r.httpSrv2.Close()
	}
	if r.ln != nil {
		_ = r.ln.Close()
	}
	if r.httpLn != nil {
		_ = r.httpLn.Close()
	}
}

// tlsConfig 构建监听的 TLS 配置：SNI 找手动证书，其余交给证书层。
func (r *listenerRunner) tlsConfig(certs CertProvider) *tls.Config {
	cfg := &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := strings.ToLower(hello.ServerName)
			t := r.current()
			if h := t.lookup(name); h != nil && h.cert != nil {
				return h.cert, nil
			}
			if r.deflt != nil && r.deflt.cert != nil {
				return r.deflt.cert, nil
			}
			if certs != nil {
				return certs.GetCertificate(hello)
			}
			return nil, fmt.Errorf("no certificate for %s", name)
		},
		NextProtos: []string{"h2", "http/1.1", "acme-tls/1"},
	}
	switch r.cfg.MinTLS {
	case "1.0":
		cfg.MinVersion = tls.VersionTLS10
	case "1.1":
		cfg.MinVersion = tls.VersionTLS11
	case "1.3":
		cfg.MinVersion = tls.VersionTLS13
	default:
		cfg.MinVersion = tls.VersionTLS12
	}
	return cfg
}

// chanListener 把分发的连接交给对应的 http.Server。
type chanListener struct {
	ch     chan net.Conn
	done   chan struct{}
	closed sync.Once
}

func newChanListener() *chanListener {
	return &chanListener{ch: make(chan net.Conn, 64), done: make(chan struct{})}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, errors.New("listener closed")
	}
}

func (l *chanListener) Close() error {
	l.closed.Do(func() { close(l.done) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return &net.TCPAddr{} }

// peekedConn 回放已读取的首字节。
type peekedConn struct {
	net.Conn
	b []byte
}

func (c *peekedConn) Read(p []byte) (int, error) {
	if len(c.b) > 0 {
		n := copy(p, c.b)
		c.b = c.b[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

// acceptSniff 接受连接并按首字节分发：0x16（TLS ClientHello）走 TLS，其余走明文处理器。
func (r *listenerRunner) acceptSniff(ln net.Listener, cfg *tls.Config, tlsCh, plainCh *chanListener) {
	for {
		raw, err := ln.Accept()
		if err != nil {
			if isUseOfClosed(err) {
				tlsCh.Close()
				plainCh.Close()
				return
			}
			r.log.warn("接受连接失败: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		// 嗅探首个字节：握手卡住的连接限时关闭
		first := make([]byte, 1)
		_ = raw.SetReadDeadline(time.Now().Add(10 * time.Second))
		n, rerr := ioReadFull(raw, first)
		_ = raw.SetReadDeadline(time.Time{})
		if rerr != nil || n == 0 {
			_ = raw.Close()
			continue
		}
		// 两条路径都要回放首字节：TLS 握手与明文请求都从第一字节开始
		pc := &peekedConn{Conn: raw, b: first}
		if first[0] == 0x16 {
			tlsCh.ch <- tls.Server(pc, cfg)
		} else {
			plainCh.ch <- pc
		}
	}
}

func ioReadFull(c net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := c.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func isUseOfClosed(err error) bool {
	return strings.Contains(err.Error(), "use of closed") || strings.Contains(err.Error(), "closed network")
}

/* ------------------------- 请求分发 ------------------------- */

// maxHostLen 是 Host 请求头的最大长度：域名最长 253 个字符，加上端口与 IPv6 方括号留有余量。
const maxHostLen = 300

// serveTLS TLS 端口上的请求。
func (r *listenerRunner) serveTLS(w http.ResponseWriter, req *http.Request) {
	r.dispatch(w, req)
}

// servePlain 明文监听上的请求；TLS 监听嗅探出的明文请求也走这里（按 PlainHTTP 处理）。
func (r *listenerRunner) servePlain(w http.ResponseWriter, req *http.Request) {
	if r.cfg.TLS {
		// TLS 端口收到的明文请求按 PlainHTTP 处理（ACME 挑战已在处理器外层应答）
		if r.cfg.PlainHTTP == HTTPRedirect {
			r.acts.redirect.hit(clientAddr(req).Addr(), time.Now().Unix())
			http.Redirect(w, req, "https://"+hostOnly(req.Host)+req.URL.RequestURI(), http.StatusMovedPermanently)
			return
		}
		if r.cfg.PlainHTTP != HTTPServe {
			// 未开启明文处理：拒绝（防止扫描器确认 HTTPS 监听的存在）
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("404 page not found"))
			return
		}
	}
	r.dispatch(w, req)
}

// serveRedirect 额外 HTTP 端口的 redirect 行为：跳转到主端口的 https 地址。
func (r *listenerRunner) serveRedirect(w http.ResponseWriter, req *http.Request) {
	r.acts.redirect.hit(clientAddr(req).Addr(), time.Now().Unix())
	target := "https://" + hostOnly(req.Host)
	if r.cfg.Port != 443 {
		target += ":" + strconv.Itoa(r.cfg.Port)
	}
	target += req.URL.RequestURI()
	http.Redirect(w, req, target, http.StatusMovedPermanently)
}

// dispatch 按 Host 路由到站点；未匹配且无默认站点时计入监听级 notFound 动作。
func (r *listenerRunner) dispatch(w http.ResponseWriter, req *http.Request) {
	if len(req.Host) > maxHostLen {
		http.Error(w, "400 Bad Request", http.StatusBadRequest)
		return
	}
	host := hostOnly(req.Host)
	t := r.current()
	h := t.lookup(host)
	if h == nil {
		h = r.deflt
	}
	if h == nil {
		r.acts.notFound.hit(clientAddr(req).Addr(), time.Now().Unix())
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("FirePanel: no site for " + host))
		return
	}
	h.ServeHTTP(w, req)
}

/* ------------------------- 状态与查询 ------------------------- */

// SiteStatus 是站点的状态快照。
type SiteStatus struct {
	ID       int64    `json:"id"`
	Listener int64    `json:"listener"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Domains  []string `json:"domains"`
	Default  bool     `json:"default"`
	Requests int64    `json:"requests"`
	Active   int64    `json:"active"`
	Errors   int64    `json:"errors"`
	Blocked  int64    `json:"blocked"`
	QPS      float64  `json:"qps"`
	Rate60   []int64  `json:"rate60"`
	Cert     string   `json:"cert,omitempty"`
	// Upstreams 为代理站点的后端健康快照（UI 行内「N 后端不可用」用）
	Upstreams []UpstreamStatus `json:"upstreams,omitempty"`
}

// StatusReport 是引擎的状态快照。
type StatusReport struct {
	Running   bool             `json:"running"`
	StartedAt *time.Time       `json:"started_at,omitempty"`
	Listeners []ListenerStatus `json:"listeners"`
	Sites     []SiteStatus     `json:"sites"`
}

// ListenerStatus 是监听的状态快照。
type ListenerStatus struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Port         int    `json:"port"`
	TLS          bool   `json:"tls"`
	HTTPPort     int    `json:"http_port"`
	HTTPPortMode string `json:"http_port_mode,omitempty"`
	PlainHTTP    string `json:"plain_http,omitempty"`
	Running      bool   `json:"running"`
	Error        string `json:"error,omitempty"`
}

// Status 返回引擎状态快照。
func (m *Manager) Status() StatusReport {
	m.mu.RLock()
	cfgs := m.cfgs
	runners := m.runners
	started := m.started
	startedAt := m.startedAt
	m.mu.RUnlock()
	rep := StatusReport{Running: started}
	if started {
		t := startedAt
		rep.StartedAt = &t
	}
	now := time.Now().Unix()
	for _, l := range cfgs {
		ls := ListenerStatus{ID: l.ID, Name: l.Name, Port: l.Port, TLS: l.TLS, HTTPPort: l.HTTPPort,
			HTTPPortMode: l.HTTPPortMode, PlainHTTP: l.PlainHTTP}
		r := runners[l.ID]
		if r != nil {
			ls.Running = r.ln != nil
			ls.Error = r.err
		}
		rep.Listeners = append(rep.Listeners, ls)
		if r == nil {
			continue
		}
		appendSite := func(h *siteHandler) {
			ss := SiteStatus{
				ID: h.site.ID, Listener: l.ID, Name: h.site.Name, Type: h.site.Type,
				Domains: h.site.Domains, Default: h.deflt,
				Requests: h.stats.Requests.Load(), Active: h.stats.Active.Load(),
				Errors:  h.stats.Errors.Load(),
				Blocked: h.stats.BlockedIP.Load() + h.stats.BlockedRate.Load(),
				QPS:     h.rate.avg(now, 10),
				Rate60:  h.rate.rate60(now, 60),
			}
			if h.cert != nil {
				ss.Cert = "manual"
			} else if h.site.TLSMode == "auto" {
				ss.Cert = "auto"
			}
			ss.Upstreams = h.upstreamStatus()
			rep.Sites = append(rep.Sites, ss)
		}
		for _, h := range r.current().all() {
			appendSite(h)
		}
		if r.deflt != nil {
			appendSite(r.deflt)
		}
	}
	return rep
}

// Running 是否已启动监听。
func (m *Manager) Running() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.started {
		return false
	}
	for _, r := range m.runners {
		if r.ln != nil {
			return true
		}
	}
	return false
}

// Stop 停止全部监听（面板关闭时调用）。
func (m *Manager) Stop() {
	m.mu.Lock()
	runners := m.runners
	m.runners = map[int64]*listenerRunner{}
	m.started = false
	m.mu.Unlock()
	for _, r := range runners {
		r.stop()
	}
}

// CertExpiry 查询站点证书到期时间：手动证书解析叶子，自动证书查证书层。
func (m *Manager) CertExpiry(s *Site) (time.Time, bool) {
	if s.TLSMode == "manual" && s.CertPEM != "" {
		if leaf, err := leafOf(s.CertPEM); err == nil {
			return leaf.NotAfter, true
		}
		return time.Time{}, false
	}
	if s.TLSMode != "auto" || m.certs == nil {
		return time.Time{}, false
	}
	var exp time.Time
	ok := false
	for _, d := range s.Domains {
		if t, o := m.certs.Expiry(d); o && (!ok || t.Before(exp)) {
			exp, ok = t, o
		}
	}
	return exp, ok
}

// leafOf 解析 PEM 证书的叶子证书。
func leafOf(pemCert string) (*x509.Certificate, error) {
	for block, rest := pem.Decode([]byte(pemCert)); block != nil; block, rest = pem.Decode(rest) {
		if block.Type == "CERTIFICATE" {
			return x509.ParseCertificate(block.Bytes)
		}
	}
	return nil, errors.New("未找到证书")
}

func firstDomain(domains []string) string {
	if len(domains) > 0 {
		return domains[0]
	}
	return ""
}
