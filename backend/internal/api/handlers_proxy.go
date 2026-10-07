package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"firepanel/internal/fw"
	"firepanel/internal/proxy"
	"firepanel/internal/store"
)

/* ------------------------- 反向代理 ------------------------- */

var domainRe = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)

func (s *Server) ListProxyDomains(c *gin.Context) {
	domains, err := s.App.Store.ListRPDomains()
	if err != nil {
		respErr(c, 500, err)
		return
	}
	routes, err := s.App.Store.ListRPRoutes(0)
	if err != nil {
		respErr(c, 500, err)
		return
	}
	byDomain := map[int64][]store.RPRoute{}
	for _, r := range routes {
		byDomain[r.DomainID] = append(byDomain[r.DomainID], r)
	}
	type domWithRoutes struct {
		store.RPDomain
		Routes []store.RPRoute `json:"routes"`
	}
	items := []domWithRoutes{}
	for _, d := range domains {
		rs := byDomain[d.ID]
		if rs == nil {
			rs = []store.RPRoute{}
		}
		items = append(items, domWithRoutes{RPDomain: d, Routes: rs})
	}
	c.JSON(200, gin.H{"items": items, "running": s.App.Proxy.Running()})
}

type rpDomainReq struct {
	Domain    string          `json:"domain"`
	TLSMode   string          `json:"tls_mode"`
	Challenge string          `json:"challenge"`
	CaddyOpts json.RawMessage `json:"caddy_opts"`
	Enabled   *bool           `json:"enabled"`
	CertPEM   string          `json:"cert_pem"`
	KeyPEM    string          `json:"key_pem"`
	Remark    string          `json:"remark"`
}

// normalizeCaddyOpts 规整站点选项 JSON（空=全默认）。
func normalizeCaddyOpts(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" || string(raw) == "null" {
		return "", nil
	}
	var opts proxy.CaddySiteOpts
	if err := json.Unmarshal(raw, &opts); err != nil {
		return "", errStr("caddy_opts 格式错误")
	}
	b, _ := json.Marshal(opts)
	return string(b), nil
}

// validChallenge 校验挑战方式（tls_mode=auto 时生效；IP 名称自动忽略）。
func validChallenge(ch string) bool {
	switch ch {
	case "", "auto", "http", "alpn", "dns":
		return true
	}
	return false
}

func (s *Server) reloadProxyAsync() {
	go func() {
		if err := s.App.ReloadProxy(); err != nil {
			s.Log.Error("反代重载失败", "err", err)
		}
	}()
}

func validHost(h string) bool {
	if domainRe.MatchString(h) {
		return true
	}
	// 支持纯 IP（Let's Encrypt shortlived profile 的 IP 证书 / 自签证书）
	return net.ParseIP(strings.TrimSuffix(h, ".")) != nil
}

func (s *Server) CreateProxyDomain(c *gin.Context) {
	var req rpDomainReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if !validHost(req.Domain) {
		c.JSON(400, gin.H{"error": "域名或 IP 格式不合法"})
		return
	}
	switch req.TLSMode {
	case "auto", "manual", "none":
	default:
		c.JSON(400, gin.H{"error": "tls_mode 必须为 auto/manual/none"})
		return
	}
	if req.TLSMode == "manual" && (req.CertPEM == "" || req.KeyPEM == "") {
		c.JSON(400, gin.H{"error": "手动证书模式需要提供证书与私钥 PEM"})
		return
	}
	if !validChallenge(req.Challenge) {
		c.JSON(400, gin.H{"error": "challenge 必须为 auto/http/alpn/dns"})
		return
	}
	opts, err := normalizeCaddyOpts(req.CaddyOpts)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	d := &store.RPDomain{
		Domain: req.Domain, Enabled: enabled, TLSMode: req.TLSMode, Challenge: req.Challenge, CaddyOpts: opts,
		CertPEM: req.CertPEM, KeyPEM: req.KeyPEM,
		CertStatus: map[string]string{"auto": "pending", "manual": "ok", "none": "none"}[req.TLSMode],
		Remark:     req.Remark,
	}
	if err := s.App.Store.CreateRPDomain(d); err != nil {
		respErr(c, 500, err)
		return
	}
	s.reloadProxyAsync()
	s.audit(c, "create", "rp_domain:"+d.Domain, nil, map[string]any{"tls_mode": d.TLSMode})
	c.JSON(200, gin.H{"item": d})
}

func (s *Server) UpdateProxyDomain(c *gin.Context) {
	id := pathID(c)
	d, err := s.App.Store.GetRPDomain(id)
	if err == store.ErrNotFound {
		c.JSON(404, gin.H{"error": "域名不存在"})
		return
	}
	if err != nil {
		respErr(c, 500, err)
		return
	}
	var req rpDomainReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	if req.Domain != "" {
		d.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
		if !validHost(d.Domain) {
			c.JSON(400, gin.H{"error": "域名或 IP 格式不合法"})
			return
		}
	}
	if req.TLSMode != "" {
		switch req.TLSMode {
		case "auto", "manual", "none":
			d.TLSMode = req.TLSMode
		default:
			c.JSON(400, gin.H{"error": "tls_mode 不合法"})
			return
		}
	}
	if req.Challenge != "" || req.Domain != "" {
		if !validChallenge(req.Challenge) {
			c.JSON(400, gin.H{"error": "challenge 不合法（auto/http/alpn/dns）"})
			return
		}
		d.Challenge = req.Challenge
	}
	if req.CaddyOpts != nil {
		opts, err := normalizeCaddyOpts(req.CaddyOpts)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		d.CaddyOpts = opts
	}
	if req.CertPEM != "" {
		d.CertPEM = req.CertPEM
	}
	if req.KeyPEM != "" {
		d.KeyPEM = req.KeyPEM
	}
	if d.TLSMode == "manual" && (d.CertPEM == "" || d.KeyPEM == "") {
		c.JSON(400, gin.H{"error": "手动证书模式需要提供证书与私钥 PEM"})
		return
	}
	if req.Enabled != nil {
		d.Enabled = *req.Enabled
	}
	d.Remark = req.Remark
	if err := s.App.Store.UpdateRPDomain(d); err != nil {
		respErr(c, 500, err)
		return
	}
	s.reloadProxyAsync()
	s.audit(c, "update", "rp_domain:"+d.Domain, nil, map[string]any{"tls_mode": d.TLSMode, "enabled": d.Enabled})
	c.JSON(200, gin.H{"item": d})
}

func (s *Server) DeleteProxyDomain(c *gin.Context) {
	id := pathID(c)
	d, err := s.App.Store.GetRPDomain(id)
	if err == store.ErrNotFound {
		c.JSON(404, gin.H{"error": "域名不存在"})
		return
	}
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if err := s.App.Store.DeleteRPDomain(id); err != nil {
		respErr(c, 500, err)
		return
	}
	s.reloadProxyAsync()
	s.audit(c, "delete", "rp_domain:"+d.Domain, d, nil)
	c.JSON(200, gin.H{"ok": true})
}

type rpRouteReq struct {
	PathMatch   string `json:"path_match"`
	UpstreamURL string `json:"upstream_url"`
	WsEnabled   *bool  `json:"ws_enabled"`
	HealthPath  string `json:"health_path"`
	Enabled     *bool  `json:"enabled"`
}

func validUpstream(u string) bool {
	if u == "" {
		return false
	}
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

func (s *Server) CreateProxyRoute(c *gin.Context) {
	domainID := pathID(c)
	if _, err := s.App.Store.GetRPDomain(domainID); err == store.ErrNotFound {
		c.JSON(404, gin.H{"error": "域名不存在"})
		return
	}
	var req rpRouteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	req.PathMatch = strings.TrimSpace(req.PathMatch)
	if req.PathMatch == "" {
		req.PathMatch = "/"
	}
	if !strings.HasPrefix(req.PathMatch, "/") {
		c.JSON(400, gin.H{"error": "路径必须以 / 开头"})
		return
	}
	if !validUpstream(strings.TrimSpace(req.UpstreamURL)) {
		c.JSON(400, gin.H{"error": "上游地址必须为 http(s)://host[:port]"})
		return
	}
	ws := true
	if req.WsEnabled != nil {
		ws = *req.WsEnabled
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	r := &store.RPRoute{
		DomainID: domainID, PathMatch: req.PathMatch,
		UpstreamURL: strings.TrimSpace(req.UpstreamURL),
		WsEnabled: ws, HealthPath: strings.TrimSpace(req.HealthPath), Enabled: enabled,
	}
	if err := s.App.Store.CreateRPRoute(r); err != nil {
		respErr(c, 500, err)
		return
	}
	s.reloadProxyAsync()
	s.audit(c, "create", "rp_route:"+strconv.FormatInt(r.ID, 10), nil, r)
	c.JSON(200, gin.H{"item": r})
}

func (s *Server) UpdateProxyRoute(c *gin.Context) {
	id := pathID(c)
	r, err := s.App.Store.GetRPRoute(id)
	if err == store.ErrNotFound {
		c.JSON(404, gin.H{"error": "路由不存在"})
		return
	}
	if err != nil {
		respErr(c, 500, err)
		return
	}
	var req rpRouteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	if req.PathMatch != "" {
		r.PathMatch = strings.TrimSpace(req.PathMatch)
		if !strings.HasPrefix(r.PathMatch, "/") {
			c.JSON(400, gin.H{"error": "路径必须以 / 开头"})
			return
		}
	}
	if req.UpstreamURL != "" {
		if !validUpstream(strings.TrimSpace(req.UpstreamURL)) {
			c.JSON(400, gin.H{"error": "上游地址不合法"})
			return
		}
		r.UpstreamURL = strings.TrimSpace(req.UpstreamURL)
	}
	if req.WsEnabled != nil {
		r.WsEnabled = *req.WsEnabled
	}
	r.HealthPath = strings.TrimSpace(req.HealthPath)
	if req.Enabled != nil {
		r.Enabled = *req.Enabled
	}
	if err := s.App.Store.UpdateRPRoute(r); err != nil {
		respErr(c, 500, err)
		return
	}
	s.reloadProxyAsync()
	s.audit(c, "update", "rp_route:"+strconv.FormatInt(id, 10), nil, r)
	c.JSON(200, gin.H{"item": r})
}

func (s *Server) DeleteProxyRoute(c *gin.Context) {
	id := pathID(c)
	if err := s.App.Store.DeleteRPRoute(id); err != nil {
		respErr(c, 500, err)
		return
	}
	s.reloadProxyAsync()
	s.audit(c, "delete", "rp_route:"+strconv.FormatInt(id, 10), nil, nil)
	c.JSON(200, gin.H{"ok": true})
}

/* ------------------------- 环境探测 / DNS-01 凭据 ------------------------- */

// ProxyEnv 端口探测 + 引擎/监听现状 + 推荐（设计文档 §3.1 判定矩阵）。
func (s *Server) ProxyEnv(c *gin.Context) {
	rep := s.App.Proxy.Environment(s.App.Exec)
	c.JSON(200, gin.H{"env": rep})
}

// GetDNSConfig 返回 DNS-01 凭据（token 脱敏）。
func (s *Server) GetDNSConfig(c *gin.Context) {
	raw, err := s.App.Store.GetSetting("dns_provider")
	if err != nil || raw == "" {
		c.JSON(200, gin.H{"config": proxy.DNSProviderConfig{}})
		return
	}
	var cfg proxy.DNSProviderConfig
	if json.Unmarshal([]byte(raw), &cfg) != nil {
		c.JSON(200, gin.H{"config": proxy.DNSProviderConfig{}})
		return
	}
	c.JSON(200, gin.H{"config": cfg.Mask()})
}

// SetDNSConfig 保存 DNS-01 凭据（settings 存储），运行中生效。
func (s *Server) SetDNSConfig(c *gin.Context) {
	var cfg proxy.DNSProviderConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	if err := cfg.Validate(); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	// token 形如 ****1234 的脱敏值回传时保留旧值
	old, _ := s.App.Store.GetSetting("dns_provider")
	if old != "" {
		var prev proxy.DNSProviderConfig
		if json.Unmarshal([]byte(old), &prev) == nil {
			if cfg.APIToken == maskSecretFor(prev.APIToken) && cfg.APIToken != "" {
				cfg.APIToken = prev.APIToken
			}
			if cfg.SecretKey == maskSecretFor(prev.SecretKey) && cfg.SecretKey != "" {
				cfg.SecretKey = prev.SecretKey
			}
		}
	}
	b, _ := json.Marshal(cfg)
	if err := s.App.Store.SetSetting("dns_provider", string(b)); err != nil {
		respErr(c, 500, err)
		return
	}
	if err := s.App.Proxy.SetDNSProvider(cfg); err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "update", "proxy_dns_config", nil, map[string]any{"provider": cfg.Provider})
	s.reloadProxyAsync()
	c.JSON(200, gin.H{"config": cfg.Mask()})
}

func maskSecretFor(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}

// InstallCaddy 一键安装 Caddy（发行版包管理器，官方仓库回退）。同步执行，耗时可达数分钟。
func (s *Server) InstallCaddy(c *gin.Context) {
	if !s.caddyInstalling.CompareAndSwap(false, true) {
		c.JSON(http.StatusConflict, gin.H{"error": "Caddy 安装正在进行中，请稍候"})
		return
	}
	defer s.caddyInstalling.Store(false)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()
	version, err := proxy.InstallCaddy(ctx, s.App.Exec)
	if err != nil {
		s.audit(c, "install_fail", "caddy", nil, map[string]any{"error": err.Error()})
		respErr(c, 500, err)
		return
	}
	// 停掉包管理器自启的服务：避免默认 Caddyfile 抢占 80 端口；
	// 引擎切换到 caddy 时由 EnsureCaddyService 在端口释放后拉起
	_, _ = s.App.Exec.Run(ctx, "systemctl", "disable", "--now", "caddy")
	s.audit(c, "install", "caddy", nil, map[string]any{"version": version})
	c.JSON(200, gin.H{"version": version})
}

// SetProxyEngine 切换反代引擎与监听端口（写入 settings 持久化，重启后保持）。
func (s *Server) SetProxyEngine(c *gin.Context) {
	var req struct {
		Engine    string `json:"engine"`
		HTTPAddr  string `json:"http_addr"`
		HTTPSAddr string `json:"https_addr"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	if req.Engine != proxy.ModeBuiltin && req.Engine != proxy.ModeCaddy {
		c.JSON(400, gin.H{"error": "engine 必须为 builtin 或 caddy"})
		return
	}
	addrRe := regexp.MustCompile(`^$|^((\*|[0-9.]+|\[[0-9a-f:]+\])?:)?[0-9]{1,5}$`)
	if !addrRe.MatchString(req.HTTPAddr) || !addrRe.MatchString(req.HTTPSAddr) {
		c.JSON(400, gin.H{"error": "监听地址格式应为 :端口 或 主机:端口，留空保持不变"})
		return
	}
	if req.Engine == proxy.ModeCaddy {
		if _, err := s.App.Exec.Run(c.Request.Context(), "caddy", "version"); err != nil {
			c.JSON(400, gin.H{"error": "未检测到 Caddy：请在「环境探测」中使用一键安装，或参考 caddyserver.com/docs/install"})
			return
		}
	}
	sets := map[string]string{}
	if req.Engine != "" {
		sets["proxy_engine"] = req.Engine
	}
	if req.HTTPAddr != "" {
		sets["proxy_http_addr"] = req.HTTPAddr
	}
	if req.HTTPSAddr != "" {
		sets["proxy_https_addr"] = req.HTTPSAddr
	}
	for k, v := range sets {
		if err := s.App.Store.SetSetting(k, v); err != nil {
			respErr(c, 500, err)
			return
		}
	}
	if err := s.App.Proxy.Reconfigure(req.Engine, req.HTTPAddr, req.HTTPSAddr); err != nil {
		respErr(c, 500, err)
		return
	}
	// 引擎切换的服务编排：caddy 模式需拉起服务（内置监听已释放，端口让位）；
	// 切回内置则先停用 caddy 服务，避免其默认配置抢占 80/443
	ctxSvc, cancelSvc := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancelSvc()
	if req.Engine == proxy.ModeCaddy {
		if err := proxy.EnsureCaddyService(ctxSvc, s.App.Exec); err != nil {
			s.emitProxyEvent("proxy_caddy_error", "Caddy 服务启动失败", err.Error())
		}
	} else if req.Engine == proxy.ModeBuiltin {
		// 不自动停 Caddy：它可能是 80/443 的合法占用者（模式2 场景，如用户自装的 Caddy/Nginx）。
		// 面板只释放自己的监听；若 Caddy 仍占用目标端口，builtin 绑定失败会以事件报出。
		if proxy.CaddyRunning(ctxSvc, s.App.Cfg.Proxy.CaddyAdmin, 2*time.Second) {
			s.emitProxyEvent("proxy_caddy_running", "检测到 Caddy 仍在运行",
				"若其占用 80/443，内置引擎将无法绑定这些端口（可改用非标端口，或手动 systemctl stop caddy 后重试）")
		}
	}
	s.audit(c, "update", "proxy_engine", nil, req)
	s.reloadProxyAsync()
	c.JSON(200, gin.H{"ok": true})
}

// emitProxyEvent 反代事件广播（handler 内直接广播用）。
func (s *Server) emitProxyEvent(eventType, message, detail string) {
	s.App.BroadcastEvent(fw.Event{Type: eventType, Message: message, Detail: detail, Timestamp: time.Now()})
}

/* ------------------------- Caddyfile（完全接管） ------------------------- */

// GetCaddyfile 预览当前将下发的完整 Caddyfile（文本）+ 自定义片段。
func (s *Server) GetCaddyfile(c *gin.Context) {
	domains, err := s.App.Store.ListRPDomains()
	if err != nil {
		respErr(c, 500, err)
		return
	}
	routes, _ := s.App.Store.ListRPRoutes(0)
	byDomain := map[int64][]proxy.RouteConfig{}
	for _, r := range routes {
		byDomain[r.DomainID] = append(byDomain[r.DomainID], proxy.RouteConfig{
			ID: r.ID, PathMatch: r.PathMatch, UpstreamURL: r.UpstreamURL,
			WsEnabled: r.WsEnabled, HealthPath: r.HealthPath, Enabled: r.Enabled,
		})
	}
	var cfgs []proxy.DomainConfig
	for _, d := range domains {
		cfgs = append(cfgs, proxy.DomainConfig{
			ID: d.ID, Domain: d.Domain, Enabled: d.Enabled, TLSMode: d.TLSMode,
			CaddyOpts: d.CaddyOpts, Routes: byDomain[d.ID],
		})
	}
	cf := proxy.BuildCaddyfile(cfgs, s.App.Proxy.CaddyGlobal(), s.App.Proxy.CaddyCustom())
	c.JSON(200, gin.H{"caddyfile": cf, "custom": s.App.Proxy.CaddyCustom(), "path": proxy.CaddyfilePath})
}

// SetCaddyCustom 保存用户自定义片段（原样追加到 Caddyfile 末尾）并热重载。
func (s *Server) SetCaddyCustom(c *gin.Context) {
	var req struct {
		Custom string `json:"custom"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	if len(req.Custom) > 256*1024 {
		c.JSON(400, gin.H{"error": "自定义片段过大（上限 256KB）"})
		return
	}
	if err := s.App.Store.SetSetting("caddy_custom", req.Custom); err != nil {
		respErr(c, 500, err)
		return
	}
	s.App.Proxy.SetCaddyCustom(req.Custom)
	s.audit(c, "update", "proxy_caddy_custom", nil, map[string]any{"bytes": len(req.Custom)})
	s.reloadProxyAsync()
	c.JSON(200, gin.H{"ok": true})
}

// GetCaddyGlobal 全局选项。
func (s *Server) GetCaddyGlobal(c *gin.Context) {
	c.JSON(200, gin.H{"config": s.App.Proxy.CaddyGlobal()})
}

// SetCaddyGlobal 保存全局选项（email/debug）并热重载 Caddy。
func (s *Server) SetCaddyGlobal(c *gin.Context) {
	var g proxy.CaddyGlobalSettings
	if err := c.ShouldBindJSON(&g); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	if err := g.Validate(); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := s.App.SaveCaddyGlobal(g); err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "update", "proxy_caddy_global", nil, g)
	s.reloadProxyAsync()
	c.JSON(200, gin.H{"ok": true})
}

// SetCaddyfileRaw 整文件手改保存：校验语法 → 写入生效。
// 标记之后的内容回填为自定义片段（面板重新生成时保留）；其余为用户版本，
// 域名增删会重写生成块（UI 有说明）。保存本身不触发重新生成。
func (s *Server) SetCaddyfileRaw(c *gin.Context) {
	var req struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		c.JSON(400, gin.H{"error": "内容不能为空"})
		return
	}
	if len(req.Content) > 512*1024 {
		c.JSON(400, gin.H{"error": "文件过大（上限 512KB）"})
		return
	}
	custom := proxy.SplitCustom(req.Content)
	if err := s.App.Store.SetSetting("caddy_custom", custom); err != nil {
		respErr(c, 500, err)
		return
	}
	s.App.Proxy.SetCaddyCustom(custom)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	if err := s.App.Proxy.DeployRawCaddyfile(ctx, req.Content); err != nil {
		s.audit(c, "update_fail", "proxy_caddyfile_raw", nil, map[string]any{"error": err.Error()})
		respErr(c, 400, err)
		return
	}
	s.audit(c, "update", "proxy_caddyfile_raw", nil, map[string]any{"bytes": len(req.Content)})
	s.App.BroadcastEvent(fw.Event{
		Type: "proxy_caddy_ok", Timestamp: time.Now(),
		Message: "Caddyfile 已保存并重载（手改版本）",
	})
	c.JSON(200, gin.H{"ok": true})
}

/* ------------------------- 自签证书（内网 IP 兜底） ------------------------- */

// SelfSignCert 为 IP/域名生成自签名证书（SAN 覆盖全部输入，ECDSA P-256，10 年）。
// 公网 IP/域名优先用 Let's Encrypt（auto 模式）；内网 IP 无法通过 ACME 校验时用此路径。
func (s *Server) SelfSignCert(c *gin.Context) {
	var req struct {
		Hosts string `json:"hosts"` // 逗号分隔的 IP/域名
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Hosts) == "" {
		c.JSON(400, gin.H{"error": "请提供 hosts（逗号分隔的 IP/域名）"})
		return
	}
	var dnsNames []string
	var ipAddrs []net.IP
	for _, h := range strings.Split(req.Hosts, ",") {
		h = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(h, ".")))
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			ipAddrs = append(ipAddrs, ip)
		} else if domainRe.MatchString(h) {
			dnsNames = append(dnsNames, h)
		}
	}
	if len(dnsNames) == 0 && len(ipAddrs) == 0 {
		c.JSON(400, gin.H{"error": "未解析到合法的 IP 或域名"})
		return
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		respErr(c, 500, err)
		return
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: dnsOrIPStr(dnsNames, ipAddrs), Organization: []string{"FirePanel Self-Signed"}},
		DNSNames:     dnsNames,
		IPAddresses:  ipAddrs,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		respErr(c, 500, err)
		return
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		respErr(c, 500, err)
		return
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	c.JSON(200, gin.H{
		"cert_pem": string(certPEM),
		"key_pem":  string(keyPEM),
		"expires":  tpl.NotAfter.Format("2006-01-02"),
	})
}

func dnsOrIPStr(dns []string, ips []net.IP) string {
	if len(dns) > 0 {
		return dns[0]
	}
	if len(ips) > 0 {
		return ips[0].String()
	}
	return "firepanel"
}

/* ------------------------- 备份恢复 ------------------------- */

// Restore 从 JSON 备份恢复转发与黑/白名单（全量替换后同步内核）。
func (s *Server) Restore(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 8<<20))
	if err != nil {
		c.JSON(400, gin.H{"error": "读取请求体失败"})
		return
	}
	var snap struct {
		ForwardRules []store.ForwardRule `json:"forward_rules"`
		IPLists      []store.IPListEntry `json:"ip_lists"`
	}
	if err := json.Unmarshal(body, &snap); err != nil {
		c.JSON(400, gin.H{"error": "备份文件格式错误"})
		return
	}
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error {
		// 全量替换
		if old, _ := s.App.Store.ListForwards(); old != nil {
			for _, f := range old {
				_ = s.App.Store.DeleteForward(f.ID)
			}
		}
		if old, _ := s.App.Store.ListIPLists(""); old != nil {
			for _, e := range old {
				_ = s.App.Store.DeleteIPList(e.ID)
			}
		}
		for i := range snap.ForwardRules {
			f := snap.ForwardRules[i]
			f.ID = 0
			if err := fwValidateForward(&f); err != nil {
				return err
			}
			if err := s.App.Store.CreateForward(&f); err != nil {
				return err
			}
		}
		for i := range snap.IPLists {
			e := snap.IPLists[i]
			e.ID = 0
			if err := s.App.Store.CreateIPList(&e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "restore", "system", nil, map[string]any{
		"forwards": len(snap.ForwardRules), "ip_lists": len(snap.IPLists),
	})
	c.JSON(200, gin.H{"sync": res, "forwards": len(snap.ForwardRules), "ip_lists": len(snap.IPLists)})
}

func fwValidateForward(f *store.ForwardRule) error {
	// 备份恢复走与 API 相同的校验（复用 fw 层）
	return fw.ValidateForward(f.Name, f.Protocol, f.ListenPort, f.SrcIP, f.DstIP, f.DstDomain, f.DstPort)
}

/* ------------------------- 日志中心 ------------------------- */

func (s *Server) ListAuditFiltered(c *gin.Context) {
	keyword := strings.ToLower(c.Query("q"))
	action := c.Query("action")
	limit := intQuery(c, "limit", 200)
	items, err := s.App.Store.ListAudit(limit, 0)
	if err != nil {
		respErr(c, 500, err)
		return
	}
	out := []store.AuditEntry{}
	for _, e := range items {
		if action != "" && e.Action != action {
			continue
		}
		if keyword != "" {
			hay := strings.ToLower(e.Username + " " + e.Action + " " + e.Target + " " + e.Detail + " " + e.IP)
			if !strings.Contains(hay, keyword) {
				continue
			}
		}
		out = append(out, e)
	}
	c.JSON(200, gin.H{"items": out, "ts": time.Now().Unix()})
}
