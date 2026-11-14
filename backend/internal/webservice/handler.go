package webservice

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// eventFunc 状态事件回调（WS 广播 / 告警订阅用）。
type eventFunc func(eventType, message, detail string)

// logger 引擎的日志与事件出口。
type logger struct {
	log     *slog.Logger
	onEvent eventFunc
}

func (l *logger) info(format string, args ...any)  { l.log.Info(fmt.Sprintf(format, args...)) }
func (l *logger) warn(format string, args ...any)  { l.log.Warn(fmt.Sprintf(format, args...)) }
func (l *logger) errorf(format string, args ...any) { l.log.Error(fmt.Sprintf(format, args...)) }

func (l *logger) event(eventType, message, detail string) {
	if l.onEvent != nil {
		l.onEvent(eventType, message, detail)
	}
}

// errConnClosed 标记管理员断开或访客消失导致的错误，不算后端故障。
var errConnClosed = errors.New("connection closed")

// ctxKeyAttempt / ctxKeyClientAddr / ctxKeyActiveReq 是请求上下文的键。
type ctxKeyAttempt struct{}
type ctxKeyClientAddr struct{}
type ctxKeyActiveReq struct{}

// activeReqFrom 取请求的进行中登记项。
func activeReqFrom(ctx context.Context) *activeReq {
	ar, _ := ctx.Value(ctxKeyActiveReq{}).(*activeReq)
	return ar
}

func attemptFrom(ctx context.Context) *proxyAttempt {
	att, _ := ctx.Value(ctxKeyAttempt{}).(*proxyAttempt)
	return att
}

// clientAddr 返回请求的客户端地址。
func clientAddr(r *http.Request) netip.AddrPort {
	ap, _ := netip.ParseAddrPort(r.RemoteAddr)
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
}

// proxyAttempt 记录一次请求当前选中的后端，供转发与失败重试使用。
type proxyAttempt struct {
	pool *pool
	in   *http.Request
	up   *upstream
}

func (att *proxyAttempt) current() *upstream { return att.up }

func (att *proxyAttempt) use(u *upstream) { att.up = u }

// SiteStats 是站点的请求统计。
type SiteStats struct {
	Requests atomic.Int64
	Active   atomic.Int64
	Errors   atomic.Int64
	// BlockedIP 为访问控制（IP 黑白名单）拒绝数，BlockedRate 为限流拒绝数（拓扑视图按原因分列）
	BlockedIP   atomic.Int64
	BlockedRate atomic.Int64
	// Status 为按响应类别分的计数：下标 2=2xx、3=3xx、4=4xx、5=5xx（拓扑详情用）
	Status [6]atomic.Int64
}

// blocked 返回按原因分的拦截计数。
func (s *SiteStats) blocked() map[string]int64 {
	return map[string]int64{
		"ip":        s.BlockedIP.Load(),
		"rateLimit": s.BlockedRate.Load(),
	}
}

// countStatus 按最终状态码累加分类计数（断开连接记 444 → 4xx）。
func (s *SiteStats) countStatus(code int) {
	cls := code / 100
	if cls < 2 || cls > 5 {
		return
	}
	s.Status[cls].Add(1)
}

// siteHandler 是一个站点编译后的处理器。
type siteHandler struct {
	site   *Site
	ipList []netip.Prefix
	stats  *SiteStats
	log     *logger
	handler http.Handler
	pool    *pool
	// locations 为按路径前缀分流的子后端（最长前缀在前）
	locations []*locationHandler
	// idleClosers 关闭各反向代理的空闲后端连接，站点被替换时调用
	idleClosers []func()
	limiter     *limiter
	cert        *tls.Certificate // manual 模式
	rate        *rateRing
	// recent 为近期到达过本站点的访客地址（拓扑视图的活动 IP）
	recent *recentIPs
	// conns 为进行中的请求登记表（拓扑视图列出进行中的请求）
	conns *connTracker
	// listenerID 为所属监听的 ID（拓扑节点编号用）
	listenerID int64
	// deflt 表示这是引擎的默认站点（处理未匹配任何站点域名的请求）
	deflt bool
}

func newSiteHandler(s *Site, log *logger) (*siteHandler, error) {
	h := &siteHandler{site: s, stats: &SiteStats{}, log: log, rate: &rateRing{}, recent: newRecentIPs(), conns: newConnTracker()}
	if s.Type == TypeClose {
		// 断开连接的站点在 ServeHTTP 中直接断开；保留一个只会断开的处理器，
		// 以防其他调用路径意外写出响应
		h.handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic(http.ErrAbortHandler)
		})
		return h, nil
	}
	var err error
	if h.ipList, err = parsePrefixes(s.IPList); err != nil {
		return nil, err
	}
	if s.RateLimit.Enable {
		h.limiter = newLimiter(s.RateLimit)
	}
	switch s.Type {
	case TypeProxy:
		if h.pool, err = newPool(s, log); err != nil {
			return nil, err
		}
		h.handler = h.newProxy(s, h.pool, "")
		if len(s.Locations) > 0 {
			if err := h.buildLocations(s); err != nil {
				return nil, err
			}
			h.handler = h.dispatchLocations(h.locations, h.handler)
		}
	case TypeRedirect:
		h.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			to, err := redirectTarget(s, r)
			if err != nil {
				// 引用变量展开后缺少主机，跳到半成品地址没有意义；原因只写日志，不展示给访客
				h.log.warn("重定向目标暂不可用: %v", err)
				http.Error(w, "503 Service Unavailable", http.StatusServiceUnavailable)
				return
			}
			applyHeaders(w.Header(), s.ResponseHeaders)
			http.Redirect(w, r, to, s.RedirectCode)
		})
	case TypeStatic:
		h.handler = h.newStatic(s)
	case TypeText:
		h.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			applyHeaders(w.Header(), s.ResponseHeaders)
			w.Header().Set("Content-Type", s.ContentType)
			w.WriteHeader(s.StatusCode)
			_, _ = w.Write([]byte(s.Text))
		})
	}
	if s.Compress && s.Type != TypeRedirect {
		h.handler = compressHandler(h.handler)
	}
	return h, nil
}

// locationHandler 是编译后的分路径规则。
type locationHandler struct {
	prefix   string
	// pool 为该路径自己的后端，沿用站点后端时为 nil
	pool    *pool
	handler http.Handler
}

// buildLocations 为每条分路径规则创建独立的反向代理。
func (h *siteHandler) buildLocations(s *Site) error {
	for _, loc := range s.Locations {
		ls := *s
		// 请求头先应用站点的，再应用路径的，同名时以路径为准
		ls.RequestHeaders = append(append([]string{}, s.RequestHeaders...), loc.RequestHeaders...)
		p := h.pool
		if len(loc.Targets) > 0 {
			ls.Targets = loc.Targets
			var err error
			if p, err = newPool(&ls, h.log); err != nil {
				return err
			}
			p.site = siteLabel(s) + " " + loc.Prefix
			p.scope, p.match = "location", loc.Prefix
		}
		strip := ""
		if loc.StripPrefix {
			strip = loc.Prefix
		}
		lh := &locationHandler{prefix: loc.Prefix, handler: h.newProxy(&ls, p, strip)}
		if p != h.pool {
			lh.pool = p
		}
		h.locations = append(h.locations, lh)
	}
	// 最长前缀优先
	for i := 1; i < len(h.locations); i++ {
		for j := i; j > 0 && len(h.locations[j].prefix) > len(h.locations[j-1].prefix); j-- {
			h.locations[j], h.locations[j-1] = h.locations[j-1], h.locations[j]
		}
	}
	return nil
}

// dispatchLocations 按路径前缀在 locs 中选择后端，未命中时交给 fallback。
func (h *siteHandler) dispatchLocations(locs []*locationHandler, fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := cleanReqPath(r.URL.Path)
		for _, l := range locs {
			if prefixMatch(l.prefix, p) {
				l.handler.ServeHTTP(w, r)
				return
			}
		}
		fallback.ServeHTTP(w, r)
	})
}

// prefixMatch 按路径段边界判断前缀：/api 匹配 /api 与 /api/x，不匹配 /apix。
func prefixMatch(prefix, p string) bool {
	if prefix == "/" {
		return true
	}
	if !strings.HasPrefix(p, prefix) {
		return false
	}
	return len(p) == len(prefix) || p[len(prefix)] == '/' || strings.HasSuffix(prefix, "/")
}

// cleanReqPath 合并 //、解析 . 与 ..，避免用 /api/../x 这类写法越过前缀匹配。
func cleanReqPath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.Contains(p, "//") && !strings.Contains(p, "/.") {
		return p
	}
	p = path.Clean("/" + p)
	if p == "" {
		return "/"
	}
	return p
}

// stripLocationPrefix 去掉请求路径的前缀并保留查询串，结果始终以 / 开头。
// 去掉前缀后原路径若以 / 结尾则保留，后端对 /dir 与 /dir/ 的处理可能不同。
func stripLocationPrefix(u *url.URL, prefix string) {
	if prefix == "" || prefix == "/" {
		return
	}
	p := cleanReqPath(u.Path)
	if !prefixMatch(prefix, p) {
		return
	}
	rest := strings.TrimPrefix(p, strings.TrimRight(prefix, "/"))
	if rest == "" {
		rest = "/"
	}
	if strings.HasSuffix(u.Path, "/") && !strings.HasSuffix(rest, "/") {
		rest += "/"
	}
	u.Path = rest
	// 路径已按解码后的值重建，原始编码需让标准库重新生成
	u.RawPath = ""
}

// redirectTarget 计算重定向地址：{host} 替换为访客请求的域名；KeepPath 时附加原路径与查询串。
func redirectTarget(s *Site, r *http.Request) (string, error) {
	host := hostOnly(r.Host)
	if host == "" {
		return "", fmt.Errorf("请求的域名不合法")
	}
	to := strings.ReplaceAll(s.RedirectTo, "{host}", host)
	if s.KeepPath {
		to = strings.TrimRight(to, "/") + r.URL.RequestURI()
	}
	return to, nil
}

// hostOnly 返回 Host 头的主机名部分（去端口与 IPv6 方括号），小写。
func hostOnly(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if h == "" {
		return ""
	}
	if i := strings.LastIndexByte(h, ']'); i >= 0 {
		return strings.TrimPrefix(strings.TrimSuffix(h[:i+1], "]"), "[")
	}
	if i := strings.LastIndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	return strings.TrimSuffix(h, ".")
}

// newStatic 静态文件处理器。
func (h *siteHandler) newStatic(s *Site) http.Handler {
	root := http.Dir(s.Root)
	fileServer := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applyHeaders(w.Header(), s.ResponseHeaders)
		p := cleanReqPath(r.URL.Path)
		if strings.Contains(p, "..") {
			http.Error(w, "400 Bad Request", http.StatusBadRequest)
			return
		}
		full := path.Join(s.Root, path.Clean("/"+p))
		st, err := os.Stat(full)
		if err != nil {
			http.Error(w, "404 Not Found", http.StatusNotFound)
			return
		}
		if st.IsDir() && !s.ListDir {
			// 目录且未开启列目录：尝试 index.html，没有则 404
			idx := path.Join(full, "index.html")
			if f, err := os.Open(idx); err == nil {
				f.Close()
				http.ServeFile(w, r, idx)
				return
			}
			http.Error(w, "404 Not Found", http.StatusNotFound)
			return
		}
		if st.IsDir() && !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// proxyBufferPool 复用反向代理的拷贝缓冲区，避免每个请求分配 32 KB 带来的 GC 压力。
type proxyBufferPool struct{ p sync.Pool }

func (b *proxyBufferPool) Get() []byte {
	if v, ok := b.p.Get().(*[]byte); ok {
		return *v
	}
	return make([]byte, 32*1024)
}

func (b *proxyBufferPool) Put(buf []byte) {
	if cap(buf) < 32*1024 {
		return
	}
	buf = buf[:32*1024]
	b.p.Put(&buf)
}

var sharedBufferPool = &proxyBufferPool{}

// sharedSessionCache 缓存到 HTTPS 后端的 TLS 会话，新连接可以复用会话，省去完整握手。
var sharedSessionCache = tls.NewLRUClientSessionCache(512)

// newTransport 为一个后端创建连接池。每个后端独立一个 Transport，保证连接不被串用。
func newTransport(s *Site) *http.Transport {
	tr := &http.Transport{
		Proxy:                 nil,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: s.InsecureSkipVerify, ClientSessionCache: sharedSessionCache},
		ForceAttemptHTTP2:     true,
		// 本机后端的并发可能很高，空闲连接上限放宽到与 nginx keepalive 常用值相当，减少突发时的重复建连
		MaxIdleConnsPerHost:   256,
		IdleConnTimeout:       90 * time.Second,
		// TLS 握手单独计时（拨号超时不含握手），避免卡在握手的后端无限占用连接
		TLSHandshakeTimeout:   time.Duration(s.Timeouts.tlsHandshake()) * time.Second,
		// 0 表示不限；只限制请求发完到收到响应头之间的等待
		ResponseHeaderTimeout: time.Duration(s.Timeouts.responseHeader()) * time.Second,
	}
	return tr
}

// dialFailed 判断错误是否为建连失败（可换下一个后端重试）。
func dialFailed(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") || strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "TLS handshake")
}

// isUpstreamTimeout 判断代理错误是否为连接后端时的超时：拨号、TLS 握手与等待响应头超时都实现 net.Error 的 Timeout。
func isUpstreamTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// retryBody 保持请求体可读，建连失败换后端重试时重新发送。
type retryBody struct {
	io.ReadCloser
	read atomic.Bool
}

func (b *retryBody) Read(p []byte) (int, error) {
	b.read.Store(true)
	return b.ReadCloser.Read(p)
}

func (b *retryBody) Close() error { return nil }

// upstreamRoundTripper 按请求选中的后端使用对应的连接池；与后端建连失败时换下一个后端重试。
type upstreamRoundTripper struct {
	trs  map[*upstream]*http.Transport
	pool *pool
	// target 把请求改写为发往指定后端（去路径前缀、URL 与 Host），重试换后端时使用
	target func(pr *httputil.ProxyRequest, up *upstream)
	log    func(msg string)
}

func (rt *upstreamRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	att := attemptFrom(req.Context())
	if att == nil {
		return nil, errors.New("缺少后端选择")
	}
	out := req
	var body *retryBody
	if req.Body != nil && req.Body != http.NoBody && len(rt.pool.ups) > 1 {
		// 失败时 Transport 会关闭请求体，重试需要它保持可读；读过的请求体不能重发，此时不再重试。
		// 访客请求体最终由 net/http 在请求结束时关闭
		body = &retryBody{ReadCloser: req.Body}
		out = shallowRequest(req)
		out.Body = body
	}
	for {
		up := att.current()
		resp, err := rt.trs[up].RoundTrip(out)
		if err == nil || !dialFailed(err) || req.Context().Err() != nil || (body != nil && body.read.Load()) {
			return resp, err
		}
		next := rt.nextAfter(up)
		if next == nil {
			return resp, err
		}
		rt.pool.fail(up, err)
		rt.log(fmt.Sprintf("%s %s%s 连接后端 %s 失败（%v），改用 %s", req.Method, req.Host, req.URL.Path, up.url.Host, err, next.url.Host))
		att.use(next)
		retry := shallowRequest(out)
		pr := &httputil.ProxyRequest{In: att.in, Out: retry}
		// 从访客请求的地址重新拼接，避免叠加上一个后端的路径前缀
		u := *att.in.URL
		retry.URL = &u
		rt.target(pr, next)
		out = retry
	}
}

// nextAfter 返回 up 之后的下一个可用后端（循环查找，跳过当前与已试过的）。
func (rt *upstreamRoundTripper) nextAfter(cur *upstream) *upstream {
	c := rt.pool.candidates()
	if len(c) < 2 {
		return nil
	}
	start := 0
	for i, u := range c {
		if u == cur {
			start = i + 1
			break
		}
	}
	for i := range c {
		u := c[(start+i)%len(c)]
		if u != cur {
			return u
		}
	}
	return nil
}

// shallowRequest 复制请求的浅层字段，用于重试时重建请求。
func shallowRequest(r *http.Request) *http.Request {
	r2 := new(http.Request)
	*r2 = *r
	return r2
}

// upstreamBody 统计后端响应体的字节数。
type upstreamBody struct {
	io.ReadCloser
	st *upstreamStats
}

func (b *upstreamBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.st.bytes.Add(int64(n))
	}
	return n, err
}

// forwardedHeaders 是表示访客来源与访问方式的请求头，只应由引擎写入。
var forwardedHeaders = []string{
	"X-Real-Ip", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port",
	"X-Forwarded-Ssl", "X-Forwarded-Scheme", "Forwarded",
}

// stripForwardedHeaders 删除访客自带的来源类请求头：部分后端（PHP、WSGI、CGI 一类）
// 把 - 与 _ 映射为同一个变量，访客自带的变体会冒充或覆盖引擎写入的值。
func stripForwardedHeaders(h http.Header) {
	for _, k := range forwardedHeaders {
		delHeaderVariants(h, k)
	}
}

// delHeaderVariants 删除请求头及其下划线写法变体。
func delHeaderVariants(h http.Header, name string) {
	h.Del(name)
	if i := strings.IndexByte(name, '-'); i >= 0 {
		h.Del(name[:i] + "_" + name[i+1:])
	}
}

// applyHeaders 按 "Key: Value" 设置头，值为空时删除。
func applyHeaders(h http.Header, list []string) {
	for _, line := range list {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if v == "" {
			h.Del(k)
		} else {
			h.Set(k, v)
		}
	}
}

// parsePrefixes 解析 IP/CIDR 列表。
func parsePrefixes(list []string) ([]netip.Prefix, error) {
	if len(list) == 0 {
		return nil, nil
	}
	var out []netip.Prefix
	for _, s := range list {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		if a, err := netip.ParseAddr(s); err == nil {
			bits := 32
			if a.Is6() {
				bits = 128
			}
			p, _ := a.Unmap().Prefix(bits)
			out = append(out, p)
			continue
		}
		return nil, fmt.Errorf("访问控制条目 %q 不是合法的 IP 或 CIDR", s)
	}
	return out, nil
}

// ipAllowed 按访问控制模式判断客户端是否允许访问。
func (h *siteHandler) ipAllowed(a netip.Addr) bool {
	switch h.site.IPMode {
	case IPModeWhitelist:
		return prefixMatchAddr(h.ipList, a)
	case IPModeBlacklist:
		return !prefixMatchAddr(h.ipList, a)
	}
	return true
}

func prefixMatchAddr(list []netip.Prefix, a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range list {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// statusRecorder 记录响应状态码与字节数。
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	if !s.wrote {
		s.status = http.StatusOK
		s.wrote = true
	}
	n, err := s.ResponseWriter.Write(p)
	s.bytes += int64(n)
	return n, err
}

// Unwrap 让 http.ResponseController 能访问底层连接（WebSocket 升级等需要）。
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// statusClosed 是断开连接时记入统计的状态码，沿用 nginx 的 444。
const statusClosed = 444

// ServeHTTP 站点入口：统计 → 断开/访问控制 → 限流 → 站点处理器。
func (h *siteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.stats.Requests.Add(1)
	h.stats.Active.Add(1)
	defer h.stats.Active.Add(-1)
	start := time.Now()
	client := clientAddr(r)
	now := start.Unix()
	h.rate.add(now, 1)
	h.recent.touch(client.Addr(), now)
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	// 登记进行中的请求（拓扑视图据此列出访客地址与所选后端）
	ar := &activeReq{client: client, start: start}
	h.conns.begin(ar)
	defer h.conns.end(ar)
	r = r.WithContext(context.WithValue(r.Context(), ctxKeyActiveReq{}, ar))

	defer func() {
		h.stats.countStatus(rec.status)
		h.rate.addBytes(time.Now().Unix(), rec.bytes)
	}()

	// 断开连接的站点不经过访问控制，直接断开（HTTP/1.x 断开连接，HTTP/2 重置请求流）；
	// 统计照常记录为 444
	if h.site.Type == TypeClose {
		rec.status = statusClosed
		panic(http.ErrAbortHandler)
	}

	// 访问控制：IP 黑白名单
	if !h.ipAllowed(client.Addr()) {
		h.stats.BlockedIP.Add(1)
		h.log.warn("拒绝 %s 访问 %s（访问控制）", client.Addr(), r.Host)
		http.Error(w, "403 Forbidden", http.StatusForbidden)
		return
	}
	// 限流
	if h.limiter != nil {
		rel, retry, ok := h.limiter.acquire(client.Addr())
		if !ok {
			h.stats.BlockedRate.Add(1)
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
			http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
			return
		}
		defer rel()
	}
	h.handler.ServeHTTP(rec, r)
}

// newProxy 创建反向代理处理器，s 为生效的站点配置（分路径规则会传入合并后的配置），p 为其后端集合。
// strip 非空时在转发前去掉该路径前缀（分路径后端的 stripPrefix）。
func (h *siteHandler) newProxy(s *Site, p *pool, strip string) http.Handler {
	// target 把发往后端的请求指向 up：URL 改为后端地址，按设置保留访客的 Host
	target := func(pr *httputil.ProxyRequest, up *upstream) {
		pr.SetURL(up.url)
		if !s.UseTargetHost {
			// SetURL 会改写 Host，这里恢复客户端请求的 Host
			pr.Out.Host = pr.In.Host
		}
	}
	// retarget 先去前缀再指向后端；首次转发与建连失败换后端重试都走它，重试时才不会把前缀带给下一个后端
	retarget := func(pr *httputil.ProxyRequest, up *upstream) {
		stripLocationPrefix(pr.Out.URL, strip)
		target(pr, up)
	}
	rt := &upstreamRoundTripper{
		trs:    map[*upstream]*http.Transport{},
		pool:   p,
		target: retarget,
		log: func(msg string) { h.log.warn("%s", msg) },
	}
	for _, up := range p.ups {
		tr := newTransport(s)
		rt.trs[up] = tr
		h.idleClosers = append(h.idleClosers, tr.CloseIdleConnections)
	}
	rp := &httputil.ReverseProxy{
		Transport:  rt,
		BufferPool: sharedBufferPool,
		Rewrite: func(pr *httputil.ProxyRequest) {
			retarget(pr, attemptFrom(pr.In.Context()).current())
			// 客户端自带的 X-Real-IP、X-Forwarded-* 不可信，只在开启转发头时由引擎写入
			stripForwardedHeaders(pr.Out.Header)
			if s.ForwardedHeaders {
				pr.SetXForwarded()
				pr.Out.Header.Set("X-Real-Ip", clientAddr(pr.In).Addr().String())
			}
			applyHeaders(pr.Out.Header, s.RequestHeaders)
		},
		ModifyResponse: func(resp *http.Response) error {
			applyHeaders(resp.Header, s.ResponseHeaders)
			if att := attemptFrom(resp.Request.Context()); att != nil {
				up := att.current()
				p.ok(up)
				if ar := activeReqFrom(resp.Request.Context()); ar != nil {
					up.stats.recent.touch(ar.client.Addr(), time.Now().Unix())
				}
				// 统计后端返回的响应体字节数。101 协议升级时 ReverseProxy 要求响应体是可读写的连接，包装会破坏升级，不统计
				if resp.StatusCode != http.StatusSwitchingProtocols && resp.Body != nil && resp.Body != http.NoBody {
					resp.Body = &upstreamBody{ReadCloser: resp.Body, st: up.stats}
					up.stats.requests.Add(1)
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(context.Cause(r.Context()), errConnClosed) {
				return
			}
			h.stats.Errors.Add(1)
			if att := attemptFrom(r.Context()); att != nil && r.Context().Err() == nil {
				p.fail(att.current(), err)
			}
			h.log.warn("%s %s%s 后端错误: %v", r.Method, r.Host, r.URL.Path, err)
			// 连接、TLS 握手或等待响应头超时返回 504（与 nginx 一致），其余错误返回 502
			if isUpstreamTimeout(err) {
				http.Error(w, "504 Gateway Timeout", http.StatusGatewayTimeout)
				return
			}
			http.Error(w, "502 Bad Gateway", http.StatusBadGateway)
		},
		// 流式响应（SSE 等）及时刷新
		FlushInterval: -1,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client := clientAddr(r)
		att := &proxyAttempt{pool: p}
		att.use(p.pick(client.Addr()))
		ctx := context.WithValue(r.Context(), ctxKeyClientAddr{}, client)
		ctx = context.WithValue(ctx, ctxKeyAttempt{}, att)
		in := r.WithContext(ctx)
		att.in = in
		up := att.current()
		up.conns.Add(1)
		up.stats.requests.Add(1)
		// 进行中的请求登记当前后端：拓扑视图的 IP 链路据此精确到后端
		if ar := activeReqFrom(in.Context()); ar != nil {
			ar.upstream.store(up)
		}
		defer up.conns.Add(-1)
		rp.ServeHTTP(w, in)
	})
}

// inherit 在热更新站点时沿用旧处理器的统计数据，避免修改配置后计数清零。
func (h *siteHandler) inherit(old *siteHandler) {
	h.stats = old.stats
	h.rate = old.rate
	h.conns = old.conns
	h.recent = old.recent
	// 同一分流下地址相同的后端沿用转发统计，修改其他设置后累计数不清零
	key := func(p *pool, u *upstream, seen map[string]int) string {
		k := p.scope + "\x00" + p.match + "\x00" + u.url.String()
		seen[k]++
		return k + "\x00" + strconv.Itoa(seen[k])
	}
	prev := map[string]*upstreamStats{}
	seen := map[string]int{}
	for _, p := range old.pools() {
		for _, u := range p.ups {
			prev[key(p, u, seen)] = u.stats
		}
	}
	seen = map[string]int{}
	for _, p := range h.pools() {
		for _, u := range p.ups {
			if st := prev[key(p, u, seen)]; st != nil {
				u.stats = st
			}
		}
	}
}

// pools 返回站点的全部后端组：站点后端与分路径规则的后端，同一组只出现一次。
func (h *siteHandler) pools() []*pool {
	var out []*pool
	if h.pool != nil {
		out = append(out, h.pool)
	}
	for _, l := range h.locations {
		if l.pool != nil {
			out = append(out, l.pool)
		}
	}
	return out
}

// upstreamStatus 返回站点全部后端组的状态（含分路径的后端），非代理站点为空。
func (h *siteHandler) upstreamStatus() []UpstreamStatus {
	var out []UpstreamStatus
	for _, p := range h.pools() {
		out = append(out, p.status()...)
	}
	return out
}

// startBackground 启动站点的后台任务（主动健康检查）。
func (h *siteHandler) startBackground(ctx context.Context) {
	for _, p := range h.pools() {
		p.startChecks(ctx)
	}
}

// closeIdle 关闭站点各反向代理的空闲后端连接。
func (h *siteHandler) closeIdle() {
	for _, f := range h.idleClosers {
		f()
	}
}
