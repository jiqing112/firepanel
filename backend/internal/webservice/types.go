// Package webservice：Web 服务引擎（移植自 gately 的站点模型）。
// 按域名路由站点，站点类型：反向代理 / 重定向 / 静态文件 / 固定文本 / 断开连接。
// 反向代理支持多后端（权重、备用、负载均衡）、主动与被动健康检查、分路径分流、
// 请求/响应头改写、限流、压缩与后端超时；配置热重载不断开已有连接。
package webservice

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpguts"
)

// 站点类型
const (
	TypeProxy    = "proxy"
	TypeRedirect = "redirect"
	TypeStatic   = "static"
	TypeText     = "text"
	// TypeClose 不返回任何响应，直接断开连接（相当于 nginx 的 return 444），
	// 常用于默认站点拒绝未知域名的扫描
	TypeClose = "close"
)

// 负载均衡策略
const (
	LBRoundRobin = ""
	LBRandom     = "random"
	LBIPHash     = "ip_hash"
	LBLeastConn  = "least_conn"
)

// 访问控制模式
const (
	IPModeOff       = ""
	IPModeWhitelist = "whitelist"
	IPModeBlacklist = "blacklist"
)

// HTTP 监听的行为
const (
	HTTPServe    = "serve"
	HTTPRedirect = "redirect"
)

// Site 是一个按域名匹配的站点。
type Site struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name"`
	Domains []string `json:"domains"`
	Type    string   `json:"type"`
	Enabled bool     `json:"enabled"`
	// Default 为 true 时处理没有匹配任何站点域名的请求（每个引擎至多一个默认站点）
	Default bool `json:"default"`

	// TLS：auto（自动证书）/ manual（手动证书）/ none（仅 HTTP）
	TLSMode   string `json:"tls_mode"`
	Challenge string `json:"challenge"` // auto|http|alpn|dns，仅 tls_mode=auto 生效
	CertPEM   string `json:"-"`
	KeyPEM    string `json:"-"`

	// 反向代理
	Targets []string `json:"targets"`
	// LBPolicy 为 ""（加权轮询）、random、ip_hash、least_conn
	LBPolicy string      `json:"lb_policy"`
	Health   HealthCheck `json:"health"`
	// Locations 为按路径前缀分流到不同后端的规则，最长前缀优先，未命中时使用站点后端
	Locations []Location `json:"locations"`
	// UseTargetHost 为 true 时 Host 头使用后端地址，否则保留访客请求的 Host
	UseTargetHost bool `json:"use_target_host"`
	// ForwardedHeaders 为 true 时向后端添加 X-Forwarded-* 与 X-Real-IP（值只由引擎写入，
	// 访客自带的同名头一律剥除）
	ForwardedHeaders bool `json:"forwarded_headers"`
	// InsecureSkipVerify 不校验 HTTPS 后端的证书
	InsecureSkipVerify bool `json:"insecure_skip_verify"`
	// Timeouts 为连接后端的超时（秒），0 表示默认值
	Timeouts BackendTimeouts `json:"timeouts"`

	// 重定向：RedirectTo 为目标地址前缀，{host} 代表访客请求的域名；KeepPath 时附加原路径与查询串
	RedirectTo   string `json:"redirect_to"`
	RedirectCode int    `json:"redirect_code"`
	KeepPath     bool   `json:"keep_path"`

	// 静态文件
	Root    string `json:"root"`
	ListDir bool   `json:"list_dir"`

	// 固定文本
	Text        string `json:"text"`
	ContentType string `json:"content_type"`
	StatusCode  int    `json:"status_code"`

	// 访问控制：单 IP 或 CIDR，whitelist 之外一律拒绝，blacklist 之中一律拒绝
	IPMode string   `json:"ip_mode"`
	IPList []string `json:"ip_list"`

	Compress bool `json:"compress"`
	// RateLimit 按客户端 IP 限流
	RateLimit RateLimit `json:"rate_limit"`

	// RequestHeaders / ResponseHeaders 每项为 "Key: Value"，Value 为空表示删除该头
	RequestHeaders  []string `json:"request_headers"`
	ResponseHeaders []string `json:"response_headers"`

	Remark string `json:"remark"`
}

// Location 把站点下某个路径前缀的请求分到另一组后端（nginx 的 location 前缀匹配）。
type Location struct {
	// Prefix 按路径段边界匹配：/api 与 /api/ 都匹配 /api 及 /api/x，不匹配 /apix
	Prefix string `json:"prefix"`
	// Targets 为该路径的后端，格式同站点后端
	Targets []string `json:"targets"`
	// StripPrefix 为 true 时转发前去掉路径前缀：/api/abc 发往后端为 /abc
	StripPrefix bool `json:"strip_prefix"`
	// RequestHeaders 追加在站点请求头之后，同名时覆盖
	RequestHeaders []string `json:"request_headers"`
}

// HealthCheck 是后端健康检查设置。未启用时仍做被动检查（连续连接失败后暂时摘除）。
type HealthCheck struct {
	Enable bool `json:"enable"`
	// Mode 为 http（请求 Path，状态码 < 400 视为健康）或 tcp（只测试连接）
	Mode string `json:"mode"`
	Path string `json:"path"`
	// Interval / Timeout 单位为秒
	Interval int `json:"interval"`
	Timeout  int `json:"timeout"`
	// Fails 连续失败多少次判为不健康，Passes 连续成功多少次恢复
	Fails  int `json:"fails"`
	Passes int `json:"passes"`
}

// BackendTimeouts 是连接后端的超时设置（秒），0 表示默认值。
type BackendTimeouts struct {
	// Connect 为建立 TCP 连接的超时，默认 10 秒
	Connect int `json:"connect"`
	// TLSHandshake 为 HTTPS 后端 TLS 握手的超时，默认 10 秒
	TLSHandshake int `json:"tls_handshake"`
	// ResponseHeader 为请求发完后等待后端响应头的超时，0 表示不限；
	// 只限制响应头到达前，不影响之后的响应体传输与 WebSocket、SSE 等长连接
	ResponseHeader int `json:"response_header"`
}

const (
	defaultConnectTimeout      = 10
	defaultTLSHandshakeTimeout = 10
	maxBackendTimeout          = 300
)

// connect 返回生效的建连超时（秒）。
func (t BackendTimeouts) connect() int {
	if t.Connect > 0 {
		return t.Connect
	}
	return defaultConnectTimeout
}

// tlsHandshake 返回生效的 TLS 握手超时（秒）。
func (t BackendTimeouts) tlsHandshake() int {
	if t.TLSHandshake > 0 {
		return t.TLSHandshake
	}
	return defaultTLSHandshakeTimeout
}

// responseHeader 返回生效的等待响应头超时（秒），0 表示不限。
func (t BackendTimeouts) responseHeader() int {
	return t.ResponseHeader
}

func (t BackendTimeouts) validate() error {
	for _, v := range []struct {
		name string
		n    int
	}{{"连接", t.Connect}, {"TLS 握手", t.TLSHandshake}} {
		if v.n < 0 || v.n > maxBackendTimeout {
			return fmt.Errorf("后端%s超时需在 1 到 %d 秒之间（0 为默认 10 秒）", v.name, maxBackendTimeout)
		}
	}
	if t.ResponseHeader < 0 || t.ResponseHeader > 86400 {
		return fmt.Errorf("等待后端响应头超时需在 1 到 86400 秒之间（0 为不限）")
	}
	return nil
}

// RateLimit 按客户端 IP 限流。
type RateLimit struct {
	Enable bool `json:"enable"`
	// Rate 为每秒允许的请求数，Burst 为突发容量
	Rate  float64 `json:"rate"`
	Burst int     `json:"burst"`
	// MaxConns 为单个 IP 的并发请求上限，0 表示不限
	MaxConns int `json:"max_conns"`
}

// hostRe 为域名/通配规则的合法形式：精确域名、*.example.com、api-*.example.com、* 与纯 IP。
var hostRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9_-]*[a-z0-9])?\.)+[a-z]{2,}$`)

// Validate 校验并规范化站点配置。
func (s *Site) Validate() error {
	if s.Name = strings.TrimSpace(s.Name); len(s.Name) > 60 {
		return fmt.Errorf("名称过长")
	}
	if len(s.Domains) == 0 {
		return fmt.Errorf("至少填写一个域名")
	}
	seen := map[string]bool{}
	for i, d := range s.Domains {
		n, ok := normalizeDomain(d, true)
		if !ok {
			return fmt.Errorf("域名 %q 不合法", d)
		}
		if seen[n] {
			return fmt.Errorf("域名 %s 重复", n)
		}
		seen[n] = true
		s.Domains[i] = n
	}
	switch s.Type {
	case TypeProxy, TypeRedirect, TypeStatic, TypeText, TypeClose:
	default:
		return fmt.Errorf("未知站点类型 %q", s.Type)
	}
	switch s.TLSMode {
	case "auto", "manual", "none":
	case "":
		s.TLSMode = "none"
	default:
		return fmt.Errorf("tls_mode 必须为 auto/manual/none")
	}
	if s.TLSMode == "manual" && (s.CertPEM == "" || s.KeyPEM == "") {
		return fmt.Errorf("手动证书需要提供证书与私钥 PEM")
	}
	switch s.Challenge {
	case "", "auto", "http", "alpn", "dns":
	default:
		return fmt.Errorf("challenge 必须为 auto/http/alpn/dns")
	}
	switch s.IPMode {
	case IPModeOff, IPModeWhitelist, IPModeBlacklist:
	default:
		return fmt.Errorf("未知的访问控制模式 %q", s.IPMode)
	}
	s.IPList = cleanList(s.IPList)
	for _, p := range s.IPList {
		if _, err := netip.ParsePrefix(p); err != nil {
			if _, err := netip.ParseAddr(p); err != nil {
				return fmt.Errorf("访问控制条目 %q 不是合法的 IP 或 CIDR", p)
			}
		}
	}
	if err := s.validateTargets(); err != nil {
		return err
	}
	if err := validateLocations(s); err != nil {
		return err
	}
	switch s.LBPolicy {
	case LBRoundRobin, LBRandom, LBIPHash, LBLeastConn:
	default:
		return fmt.Errorf("未知负载均衡策略 %q", s.LBPolicy)
	}
	if s.Health.Enable {
		if s.Health.Mode != "http" && s.Health.Mode != "tcp" {
			return fmt.Errorf("健康检查方式必须为 http 或 tcp")
		}
		if s.Health.Interval <= 0 || s.Health.Interval > 3600 {
			return fmt.Errorf("健康检查间隔需在 1 到 3600 秒之间")
		}
		if s.Health.Timeout <= 0 || s.Health.Timeout > s.Health.Interval {
			return fmt.Errorf("健康检查超时需在 1 到 %d 秒之间", s.Health.Interval)
		}
		if s.Health.Fails <= 0 || s.Health.Fails > 100 {
			return fmt.Errorf("判为不健康的连续失败次数需在 1 到 100 之间")
		}
		if s.Health.Passes <= 0 || s.Health.Passes > 100 {
			return fmt.Errorf("恢复健康的连续成功次数需在 1 到 100 之间")
		}
		if s.Health.Mode == "http" {
			if s.Health.Path == "" {
				s.Health.Path = "/"
			}
			if !strings.HasPrefix(s.Health.Path, "/") {
				return fmt.Errorf("健康检查路径需以 / 开头")
			}
		}
	}
	if err := s.Timeouts.validate(); err != nil {
		return err
	}
	switch s.Type {
	case TypeRedirect:
		if s.RedirectTo == "" {
			return fmt.Errorf("重定向目标不能为空")
		}
		if !strings.Contains(s.RedirectTo, "://") && !strings.Contains(s.RedirectTo, "{host}") {
			return fmt.Errorf("重定向目标需为完整地址（可含 {host} 代表访客域名）")
		}
		if s.RedirectCode == 0 {
			s.RedirectCode = 302
		}
		switch s.RedirectCode {
		case 301, 302, 303, 307, 308:
		default:
			return fmt.Errorf("重定向状态码需为 301/302/303/307/308")
		}
		s.KeepPath = s.KeepPath && strings.Contains(s.RedirectTo, "{host}")
	case TypeStatic:
		if s.Root = filepath.Clean(strings.TrimSpace(s.Root)); s.Root == "" || !filepath.IsAbs(s.Root) {
			return fmt.Errorf("静态站点需填写服务器上的绝对路径目录")
		}
	case TypeText:
		if s.StatusCode == 0 {
			s.StatusCode = 200
		}
		if s.StatusCode < 200 || s.StatusCode > 599 {
			return fmt.Errorf("状态码需在 200 到 599 之间")
		}
		if s.ContentType == "" {
			s.ContentType = "text/plain; charset=utf-8"
		}
	}
	if s.RateLimit.Enable {
		if s.RateLimit.Rate <= 0 || s.RateLimit.Rate > 100000 {
			return fmt.Errorf("限流速率需在 0.1 到 100000 之间")
		}
		if s.RateLimit.Burst <= 0 {
			s.RateLimit.Burst = int(s.RateLimit.Rate) + 1
		}
	}
	if err := validateHeaderLines(s.RequestHeaders); err != nil {
		return err
	}
	if err := validateHeaderLines(s.ResponseHeaders); err != nil {
		return err
	}
	return nil
}

// validateTargets 校验反向代理后端。
func (s *Site) validateTargets() error {
	if s.Type != TypeProxy {
		s.Targets = nil
		return nil
	}
	s.Targets = cleanList(s.Targets)
	if len(s.Targets) == 0 {
		return fmt.Errorf("反向代理站点需至少填写一个后端")
	}
	for _, t := range s.Targets {
		if _, err := ParseUpstream(t); err != nil {
			return err
		}
	}
	return nil
}

// UpstreamSpec 是一条后端配置的解析结果：http(s)://host[:port] [weight=N] [backup]。
type UpstreamSpec struct {
	URL    *url.URL
	Weight int
	Backup bool
}

// ParseUpstream 解析后端配置：地址后可跟空格分隔的 weight=N 与 backup 参数。
func ParseUpstream(spec string) (*UpstreamSpec, error) {
	fields := strings.Fields(spec)
	if len(fields) == 0 {
		return nil, fmt.Errorf("后端地址不能为空")
	}
	u, err := url.Parse(fields[0])
	if err != nil {
		return nil, fmt.Errorf("后端地址 %q 不合法", fields[0])
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("后端协议仅支持 http/https: %q", fields[0])
	}
	if u.Host == "" {
		return nil, fmt.Errorf("后端地址缺少主机: %q", fields[0])
	}
	out := &UpstreamSpec{URL: u, Weight: 1}
	for _, f := range fields[1:] {
		if f == "backup" {
			out.Backup = true
			continue
		}
		if w, ok := strings.CutPrefix(f, "weight="); ok {
			n, err := strconv.Atoi(w)
			if err != nil || n < 1 || n > 100 {
				return nil, fmt.Errorf("权重需在 1 到 100 之间: %q", f)
			}
			out.Weight = n
			continue
		}
		return nil, fmt.Errorf("未知参数 %q（支持 weight=N 与 backup）", f)
	}
	return out, nil
}

// normalizeDomain 规范化域名/通配/IP 写法：小写、去空白与末尾点；IPv6 去方括号。
func normalizeDomain(d string, wildcard bool) (string, bool) {
	d = strings.ToLower(strings.Trim(strings.TrimSpace(d), "."))
	if d == "" {
		return "", false
	}
	if d == "*" {
		return d, wildcard
	}
	if rest, found := strings.CutPrefix(d, "*."); found {
		if !wildcard {
			return "", false
		}
		if net.ParseIP(rest) != nil {
			return "", false
		}
		if !hostRe.MatchString("*." + rest) {
			return "", false
		}
		return "*." + rest, true
	}
	d = strings.TrimSuffix(strings.TrimPrefix(d, "["), "]")
	if ip := net.ParseIP(d); ip != nil {
		return ip.String(), true
	}
	if strings.ContainsAny(d, ":[]") {
		return "", false
	}
	if hostRe.MatchString(d) {
		return d, true
	}
	return "", false
}

// matchDomain 判断 host 是否匹配规则。返回匹配优先级：精确 3、通配 2、全部 1，不匹配 0。
func matchDomain(rule, host string) int {
	switch {
	case rule == host:
		return 3
	case rule == "*":
		return 1
	}
	if rest, found := strings.CutPrefix(rule, "*."); found {
		// *.example.com 匹配任意级子域名；example.com 本身不匹配（需单独填写）
		if strings.HasSuffix(host, "."+rest) {
			return 2
		}
	}
	return 0
}

// validateLocations 校验分路径规则。
func validateLocations(s *Site) error {
	if len(s.Locations) == 0 {
		return nil
	}
	if s.Type != TypeProxy {
		s.Locations = nil
		return nil
	}
	seen := map[string]bool{}
	for i := range s.Locations {
		l := &s.Locations[i]
		l.Prefix = strings.TrimSpace(l.Prefix)
		if !strings.HasPrefix(l.Prefix, "/") || strings.ContainsAny(l.Prefix, " ?#") {
			return fmt.Errorf("路径前缀 %q 需以 / 开头且不含空格、? 或 #", l.Prefix)
		}
		if l.Prefix != "/" {
			l.Prefix = strings.TrimRight(l.Prefix, "/")
		}
		if seen[l.Prefix] {
			return fmt.Errorf("路径前缀 %s 重复", l.Prefix)
		}
		seen[l.Prefix] = true
		l.Targets = cleanList(l.Targets)
		for _, t := range l.Targets {
			if _, err := ParseUpstream(t); err != nil {
				return err
			}
		}
		if err := validateHeaderLines(l.RequestHeaders); err != nil {
			return err
		}
	}
	return nil
}

// validateHeaderLines 校验 "Key: Value" 形式的请求头 / 响应头。
// 名称或值含非法字符时 Go 的 Transport 会拒绝整个请求（站点每次都返回 502），保存时就要拦下。
func validateHeaderLines(list []string) error {
	for _, h := range list {
		k, v, ok := strings.Cut(h, ":")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || k == "" {
			return fmt.Errorf("请求头格式应为 Key: Value，实际 %q", h)
		}
		if !httpguts.ValidHeaderFieldName(k) {
			return fmt.Errorf("请求头名称 %q 含有非法字符", k)
		}
		if !httpguts.ValidHeaderFieldValue(v) {
			return fmt.Errorf("请求头 %s 的值含有非法字符", k)
		}
	}
	return nil
}

func cleanList(in []string) []string {
	var out []string
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
