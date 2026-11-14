package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"firepanel/internal/proxy"
	"firepanel/internal/store"
	"firepanel/internal/webservice"
)

/* ------------------------- Web 服务站点 ------------------------- */

var domainRe = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)

// wsSitePayload 站点新增/编辑请求：与引擎模型一致 + 手动证书字段。
type wsSitePayload struct {
	webservice.Site
	ListenerID int64  `json:"listener_id"`
	CertPEM    string `json:"cert_pem"`
	KeyPEM     string `json:"key_pem"`
}

// toModel 把引擎模型转为存库行：引擎配置序列化进 config 列。
func toModel(s *webservice.Site, payload *wsSitePayload, listenerID int64) *store.WSSite {
	return &store.WSSite{
		ID: s.ID, ListenerID: listenerID, Name: s.Name, Domains: s.Domains, Type: s.Type, Enabled: s.Enabled,
		Default: s.Default, Config: webservice.MarshalConfig(s),
		TLSMode: s.TLSMode, Challenge: s.Challenge,
		CertPEM: payload.CertPEM, KeyPEM: payload.KeyPEM, Remark: s.Remark,
	}
}

// fromModel 把存库行转为引擎模型（config 列反序列化）。
func fromModel(s *store.WSSite) (*webservice.Site, error) {
	ws := &webservice.Site{
		ID: s.ID, Name: s.Name, Domains: s.Domains, Type: s.Type, Enabled: s.Enabled,
		Default: s.Default, TLSMode: s.TLSMode, Challenge: s.Challenge,
		CertPEM: s.CertPEM, KeyPEM: s.KeyPEM, Remark: s.Remark,
	}
	if err := webservice.UnmarshalConfig(s.Config, ws); err != nil {
		return nil, err
	}
	return ws, nil
}

func (s *Server) ListWSSites(c *gin.Context) {
	sites, err := s.App.Store.ListWSSites()
	if err != nil {
		respErr(c, 500, err)
		return
	}
	items := make([]gin.H, 0, len(sites))
	for i := range sites {
		ws, err := fromModel(&sites[i])
		if err != nil {
			continue
		}
		items = append(items, gin.H{
			"id": ws.ID, "listener_id": sites[i].ListenerID, "name": ws.Name, "domains": ws.Domains, "type": ws.Type,
			"enabled": ws.Enabled, "default": ws.Default, "remark": ws.Remark,
			"tls_mode": ws.TLSMode, "challenge": ws.Challenge,
			"cert_pem": sites[i].CertPEM, "key_pem": sites[i].KeyPEM,
			"cert_status": sites[i].CertStatus, "cert_error": sites[i].CertError,
			"cert_expires_at": sites[i].CertExpiresAt, "created_at": sites[i].CreatedAt,
			"targets": ws.Targets, "lb_policy": ws.LBPolicy, "health": ws.Health,
			"locations": ws.Locations, "use_target_host": ws.UseTargetHost,
			"forwarded_headers": ws.ForwardedHeaders, "insecure_skip_verify": ws.InsecureSkipVerify,
			"timeouts": ws.Timeouts, "redirect_to": ws.RedirectTo, "redirect_code": ws.RedirectCode,
			"keep_path": ws.KeepPath, "root": ws.Root, "list_dir": ws.ListDir,
			"text": ws.Text, "content_type": ws.ContentType, "status_code": ws.StatusCode,
			"ip_mode": ws.IPMode, "ip_list": ws.IPList, "compress": ws.Compress,
			"rate_limit": ws.RateLimit, "request_headers": ws.RequestHeaders,
			"response_headers": ws.ResponseHeaders,
		})
	}
	c.JSON(200, gin.H{"items": items, "running": s.App.WS.Running()})
}

func (s *Server) reloadWSAsync() {
	go func() {
		if err := s.App.ReloadSites(); err != nil {
			s.Log.Error("Web 服务重载失败", "err", err)
		}
	}()
}

func validWSHost(h string) bool {
	if domainRe.MatchString(h) || h == "*" || strings.HasPrefix(h, "*.") {
		return true
	}
	return net.ParseIP(strings.TrimSuffix(h, ".")) != nil
}

func (s *Server) CreateWSSite(c *gin.Context) {
	var payload wsSitePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	payload.ID = 0
	payload.Enabled = true
	if err := payload.Validate(); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if payload.TLSMode == "auto" && payload.Challenge == "dns" {
		if _, err := s.dnsProvider(); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
	}
	if payload.TLSMode == "manual" && payload.CertPEM == "" {
		c.JSON(400, gin.H{"error": "手动证书需要提供证书与私钥 PEM"})
		return
	}
	listenerID := payload.ListenerID
	if listenerID <= 0 {
		// 未指定监听：挂到第一个监听
		if ls, err := s.App.Store.ListWSListeners(); err == nil && len(ls) > 0 {
			listenerID = ls[0].ID
		}
	}
	if _, err := s.App.Store.GetWSListener(listenerID); err != nil {
		c.JSON(400, gin.H{"error": "监听不存在，请先创建监听"})
		return
	}
	model := toModel(&payload.Site, &payload, listenerID)
	model.CertStatus = map[string]string{"auto": "pending", "manual": "ok", "none": "none"}[payload.TLSMode]
	if payload.Default {
		_ = s.App.Store.UnsetDefaultWSSite(0)
	}
	if err := s.App.Store.CreateWSSite(model); err != nil {
		respErr(c, 500, err)
		return
	}
	s.reloadWSAsync()
	s.audit(c, "create", "ws_site:"+firstDomainStr(model.Domains), nil, map[string]any{"type": model.Type, "listener": listenerID})
	c.JSON(200, gin.H{"id": model.ID})
}

func (s *Server) UpdateWSSite(c *gin.Context) {
	id := pathID(c)
	old, err := s.App.Store.GetWSSite(id)
	if err == store.ErrNotFound {
		c.JSON(404, gin.H{"error": "站点不存在"})
		return
	}
	if err != nil {
		respErr(c, 500, err)
		return
	}
	var payload wsSitePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	payload.ID = id
	if err := payload.Validate(); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	// 编辑时未传证书：沿用库里的
	if payload.TLSMode == "manual" && payload.CertPEM == "" {
		payload.CertPEM = old.CertPEM
		payload.KeyPEM = old.KeyPEM
	}
	if payload.TLSMode == "auto" && payload.Challenge == "dns" {
		if _, err := s.dnsProvider(); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
	}
	model := toModel(&payload.Site, &payload, old.ListenerID)
	model.CertStatus = old.CertStatus
	model.CertError = old.CertError
	model.CertExpiresAt = old.CertExpiresAt
	if payload.TLSMode == "manual" {
		model.CertStatus = "ok"
		model.CertError = ""
	} else if payload.TLSMode != old.TLSMode {
		model.CertStatus = map[string]string{"auto": "pending", "manual": "ok", "none": "none"}[payload.TLSMode]
		model.CertError = ""
	}
	if payload.Default {
		_ = s.App.Store.UnsetDefaultWSSite(id)
	}
	if err := s.App.Store.UpdateWSSite(model); err != nil {
		respErr(c, 500, err)
		return
	}
	s.reloadWSAsync()
	s.audit(c, "update", "ws_site:"+firstDomainStr(model.Domains), nil, map[string]any{"type": model.Type, "enabled": model.Enabled})
	c.JSON(200, gin.H{"ok": true})
}

// SetWSSiteEnabled 启停开关（卡片上的 Switch：只发 enabled，不动其他字段）。
func (s *Server) SetWSSiteEnabled(c *gin.Context) {
	id := pathID(c)
	model, err := s.App.Store.GetWSSite(id)
	if err == store.ErrNotFound {
		c.JSON(404, gin.H{"error": "站点不存在"})
		return
	}
	if err != nil {
		respErr(c, 500, err)
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	if err := s.App.Store.UpdateWSSiteFields(id, map[string]any{"enabled": *req.Enabled}); err != nil {
		respErr(c, 500, err)
		return
	}
	s.reloadWSAsync()
	s.audit(c, "update", "ws_site:"+firstDomainStr(model.Domains), nil, map[string]any{"enabled": *req.Enabled})
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) DeleteWSSite(c *gin.Context) {
	id := pathID(c)
	model, err := s.App.Store.GetWSSite(id)
	if err == store.ErrNotFound {
		c.JSON(404, gin.H{"error": "站点不存在"})
		return
	}
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if err := s.App.Store.DeleteWSSite(id); err != nil {
		respErr(c, 500, err)
		return
	}
	s.reloadWSAsync()
	s.audit(c, "delete", "ws_site:"+firstDomainStr(model.Domains), nil, nil)
	c.JSON(200, gin.H{"ok": true})
}

// GetWSStatus 引擎状态快照（站点统计、后端健康与转发计数）。
func (s *Server) GetWSStatus(c *gin.Context) {
	c.JSON(200, gin.H{"status": s.App.WS.Status()})
}

/* ------------------------- 监听与环境 ------------------------- */

// WSEnv 端口探测 + 当前监听现状 + 推荐。
func (s *Server) WSEnv(c *gin.Context) {
	st := s.App.WS.Status()
	httpAddr, httpsAddr, mode := "", "", "serve"
	for _, l := range st.Listeners {
		if l.TLS && httpsAddr == "" {
			httpsAddr = ":" + strconv.Itoa(l.Port)
		}
		if !l.TLS && httpAddr == "" {
			httpAddr = ":" + strconv.Itoa(l.Port)
		}
		if l.TLS && l.PlainHTTP == "redirect" {
			mode = "redirect"
		}
		if l.HTTPPort > 0 && l.HTTPPortMode == "redirect" {
			mode = "redirect"
		}
	}
	rep := s.App.Proxy.Environment(s.App.Exec, httpAddr, httpsAddr, mode)
	c.JSON(200, gin.H{"env": rep})
}

/* ------------------------- 监听器 ------------------------- */

// wsListenerReq 监听新增/编辑请求。
type wsListenerReq struct {
	Name         string `json:"name"`
	Enabled      *bool  `json:"enabled"`
	ListenIP     string `json:"listen_ip"`
	Port         int    `json:"port"`
	TLS          bool   `json:"tls"`
	MinTLS       string `json:"min_tls"`
	PlainHTTP    string `json:"plain_http"`
	HTTPPort     int    `json:"http_port"`
	HTTPPortMode string `json:"http_port_mode"`
	Remark       string `json:"remark"`
}

// ListWSListeners 监听列表（含站点计数）。
func (s *Server) ListWSListeners(c *gin.Context) {
	listeners, err := s.App.Store.ListWSListeners()
	if err != nil {
		respErr(c, 500, err)
		return
	}
	sites, _ := s.App.Store.ListWSSites()
	counts := map[int64]int{}
	for _, s := range sites {
		counts[s.ListenerID]++
	}
	items := make([]gin.H, 0, len(listeners))
	for _, l := range listeners {
		items = append(items, gin.H{
			"id": l.ID, "name": l.Name, "enabled": l.Enabled, "config": l.Config,
			"remark": l.Remark, "created_at": l.CreatedAt, "site_count": counts[l.ID],
		})
	}
	c.JSON(200, gin.H{"items": items, "running": s.App.WS.Running()})
}

func (s *Server) CreateWSListener(c *gin.Context) {
	var req wsListenerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	l := &store.WSListener{
		Name: req.Name, Enabled: true, Remark: req.Remark,
		Config: store.WSListenerConfig{
			ListenIP: req.ListenIP, Port: req.Port, TLS: req.TLS, MinTLS: req.MinTLS,
			PlainHTTP: req.PlainHTTP, HTTPPort: req.HTTPPort, HTTPPortMode: req.HTTPPortMode,
		},
	}
	if req.Enabled != nil {
		l.Enabled = *req.Enabled
	}
	// 与引擎同一套校验
	lc := webservice.ListenerConfig{Port: l.Config.Port, TLS: l.Config.TLS, MinTLS: l.Config.MinTLS,
		PlainHTTP: l.Config.PlainHTTP, HTTPPort: l.Config.HTTPPort, HTTPPortMode: l.Config.HTTPPortMode, ListenIP: l.Config.ListenIP}
	if err := lc.Validate(); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := s.App.Store.CreateWSListener(l); err != nil {
		respErr(c, 500, err)
		return
	}
	s.reloadWSAsync()
	s.audit(c, "create", "ws_listener:"+strconv.FormatInt(l.ID, 10), nil, map[string]any{"port": l.Config.Port, "tls": l.Config.TLS})
	c.JSON(200, gin.H{"id": l.ID})
}

func (s *Server) UpdateWSListener(c *gin.Context) {
	id := pathID(c)
	old, err := s.App.Store.GetWSListener(id)
	if err == store.ErrNotFound {
		c.JSON(404, gin.H{"error": "监听不存在"})
		return
	}
	if err != nil {
		respErr(c, 500, err)
		return
	}
	var req wsListenerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	l := *old
	l.Name = req.Name
	l.Remark = req.Remark
	l.Config = store.WSListenerConfig{
		ListenIP: req.ListenIP, Port: req.Port, TLS: req.TLS, MinTLS: req.MinTLS,
		PlainHTTP: req.PlainHTTP, HTTPPort: req.HTTPPort, HTTPPortMode: req.HTTPPortMode,
	}
	lc := webservice.ListenerConfig{Port: l.Config.Port, TLS: l.Config.TLS, MinTLS: l.Config.MinTLS,
		PlainHTTP: l.Config.PlainHTTP, HTTPPort: l.Config.HTTPPort, HTTPPortMode: l.Config.HTTPPortMode, ListenIP: l.Config.ListenIP}
	if err := lc.Validate(); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := s.App.Store.UpdateWSListener(&l); err != nil {
		respErr(c, 500, err)
		return
	}
	s.reloadWSAsync()
	s.audit(c, "update", "ws_listener:"+strconv.FormatInt(id, 10), nil, map[string]any{"port": l.Config.Port, "tls": l.Config.TLS})
	c.JSON(200, gin.H{"ok": true})
}

// SetWSListenerEnabled 启停开关。
func (s *Server) SetWSListenerEnabled(c *gin.Context) {
	id := pathID(c)
	if _, err := s.App.Store.GetWSListener(id); err == store.ErrNotFound {
		c.JSON(404, gin.H{"error": "监听不存在"})
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	if err := s.App.Store.UpdateWSListenerFields(id, map[string]any{"enabled": *req.Enabled}); err != nil {
		respErr(c, 500, err)
		return
	}
	s.reloadWSAsync()
	s.audit(c, "update", "ws_listener:"+strconv.FormatInt(id, 10), nil, map[string]any{"enabled": *req.Enabled})
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) DeleteWSListener(c *gin.Context) {
	id := pathID(c)
	l, err := s.App.Store.GetWSListener(id)
	if err == store.ErrNotFound {
		c.JSON(404, gin.H{"error": "监听不存在"})
		return
	}
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if n, _ := s.App.Store.ListWSSites(); true {
		count := 0
		for _, s := range n {
			if s.ListenerID == id {
				count++
			}
		}
		if count > 0 {
			c.JSON(400, gin.H{"error": fmt.Sprintf("该监听下还有 %d 个站点，请先删除或移动站点", count)})
			return
		}
	}
	if err := s.App.Store.DeleteWSListener(id); err != nil {
		respErr(c, 500, err)
		return
	}
	s.reloadWSAsync()
	s.audit(c, "delete", "ws_listener:"+strconv.FormatInt(id, 10), l, nil)
	c.JSON(200, gin.H{"ok": true})
}

/* ------------------------- 拓扑 ------------------------- */

// GetWSTopology 拓扑快照（前端每 2 秒轮询）。
func (s *Server) GetWSTopology(c *gin.Context) {
	focus := c.Query("focus")
	if len(focus) > 512 {
		focus = focus[:512]
	}
	var wantIP netip.Addr
	if raw := strings.TrimSpace(c.Query("ip")); raw != "" {
		a, err := netip.ParseAddr(raw)
		if err != nil {
			c.JSON(400, gin.H{"error": "IP 地址无效"})
			return
		}
		wantIP = a.Unmap()
	}
	c.JSON(200, gin.H{"view": s.App.WS.Topology(time.Now().Unix(), focus, wantIP)})
}

/* ------------------------- DNS-01 凭据 ------------------------- */

// dnsProvider 读取并校验 DNS-01 凭据（未配置时返回明确错误）。
func (s *Server) dnsProvider() (proxy.DNSProviderConfig, error) {
	raw, err := s.App.Store.GetSetting("dns_provider")
	var cfg proxy.DNSProviderConfig
	if err != nil || raw == "" {
		return cfg, errWS("尚未配置 DNS-01 凭据")
	}
	if json.Unmarshal([]byte(raw), &cfg) != nil || cfg.Provider == "" {
		return cfg, errWS("尚未配置 DNS-01 凭据")
	}
	return cfg, nil
}

type wsError struct{ msg string }

func (e wsError) Error() string { return e.msg }

func errWS(msg string) error { return wsError{msg} }

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
	s.audit(c, "update", "ws_dns_config", nil, map[string]any{"provider": cfg.Provider})
	s.reloadWSAsync()
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

func firstDomainStr(list []string) string {
	if len(list) > 0 {
		return list[0]
	}
	return ""
}

/* ------------------------- 日志中心 ------------------------- */

// ListAuditFiltered 审计日志过滤查询。
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
