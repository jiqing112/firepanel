// Package api：REST API 与静态资源服务。
// 安全约定：写接口一律 requireAdmin；会话以数据库为准。
package api

import (
	"crypto/tls"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"firepanel/internal/app"
	"firepanel/internal/web"
)

// Server HTTP 服务。
type Server struct {
	App    *app.App
	Log    *slog.Logger
	Engine *gin.Engine
	WS     *Hub

	// Caddy 一键安装并发守卫
	caddyInstalling atomic.Bool

	// JWT 签名密钥（进程内缓存，避免每请求读库/读库失败导致密钥漂移）
	jwtMu          sync.Mutex
	jwtSecretBytes []byte

	// 首次初始化串行化（防止并发 POST /setup 创建出两个管理员）
	setupMu sync.Mutex
}

func NewServer(a *app.App, log *slog.Logger) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	// gin 自身日志精简为错误级别
	r.Use(gin.LoggerWithWriter(gin.DefaultErrorWriter))

	s := &Server{App: a, Log: log, Engine: r, WS: NewHub()}
	a.Broadcast = s.broadcast

	// 只信任本机/显式配置的反向代理。Gin 默认信任所有来源，
	// 客户端可伪造 X-Forwarded-For 绕过登录限速并污染审计 IP。
	if err := r.SetTrustedProxies(a.Cfg.Server.TrustedProxies); err != nil {
		log.Warn("可信代理配置无效，已回退为不信任任何代理", "err", err, "proxies", a.Cfg.Server.TrustedProxies)
		_ = r.SetTrustedProxies(nil)
	}

	s.registerRoutes()
	return s
}

func (s *Server) broadcast(topic string, payload any) {
	s.WS.Publish(topic, payload)
}

func (s *Server) registerRoutes() {
	r := s.Engine

	// 整站 Basic Auth（配置了账号密码时启用；作为应用层登录之外的额外防护层）。
	// 凭据每次请求从 App 原子状态读取：设置页可运行中开关，无需重启。
	r.Use(s.basicAuthMiddleware())

	// 安全响应头：面板不加载外部资源，用较严格的 CSP；
	// style 需要 unsafe-inline（Svelte 组件内联样式与 Tailwind 运行时）。
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; font-src 'self' data:; "+
				"style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self' ws: wss:; "+
				"frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		c.Next()
	})

	// 请求体上限：所有 JSON 接口共用（导入/恢复另有大额上限的专用路径）。
	r.Use(func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<20)
		}
		c.Next()
	})

	api := r.Group("/api/v1")
	api.GET("/bootstrap", s.Bootstrap) // 公开：初始化状态检测
	api.POST("/setup", s.SetupSecure)
	api.POST("/auth/login", s.LoginSecure)

	auth := api.Group("", s.requireAuth())
	auth.GET("/me", s.Me)

	// 转发规则
	auth.GET("/forwards", s.ListForwards)
	auth.POST("/forwards", s.requireAdmin(), s.CreateForward)
	auth.PUT("/forwards/:id", s.requireAdmin(), s.UpdateForward)
	auth.PATCH("/forwards/:id/enabled", s.requireAdmin(), s.ToggleForward)
	auth.DELETE("/forwards/:id", s.requireAdmin(), s.DeleteForward)

	// IP 名单
	auth.GET("/ip-lists", s.ListIPLists)
	auth.POST("/ip-lists", s.requireAdmin(), s.CreateIPList)
	auth.PUT("/ip-lists/:id", s.requireAdmin(), s.UpdateIPList)
	auth.DELETE("/ip-lists/:id", s.requireAdmin(), s.DeleteIPList)
	auth.POST("/ip-lists/import", s.requireAdmin(), s.ImportIPLists)
	auth.GET("/ip-lists/export", s.ExportIPLists)

	// 防火墙规则视图 / 系统
	auth.GET("/firewall/rules", s.FirewallRules)
	auth.GET("/system/info", s.SystemInfo)
	auth.GET("/system/drift", s.GetDrift)
	auth.POST("/system/drift/check", s.requireAdmin(), s.CheckDriftNow)
	auth.POST("/system/sync", s.requireAdmin(), s.ForceSync)
	auth.POST("/system/danger/confirm", s.requireAdmin(), s.ConfirmDanger)
	auth.POST("/system/danger/cancel", s.requireAdmin(), s.CancelDanger)
	auth.GET("/system/interfaces", s.ListInterfaces)
	auth.POST("/system/ip-forward/enable", s.requireAdmin(), s.EnableIPForward)
	auth.GET("/system/backup", s.requireAdmin(), s.BackupBundle)
	auth.POST("/system/restore", s.requireAdmin(), s.RestoreBundle)
	auth.GET("/settings/basicauth", s.requireAdmin(), s.GetBasicAuth)
	auth.POST("/settings/basicauth", s.requireAdmin(), s.SetBasicAuth)
	auth.GET("/settings/ports", s.GetPanelPorts)
	auth.POST("/settings/ports", s.requireAdmin(), s.SetPanelPorts)
	auth.POST("/system/restart", s.requireAdmin(), s.RestartPanel)
	auth.GET("/ports/listening", s.ListListeningPorts)
	auth.GET("/traffic/overview", s.TrafficOverview)
	auth.GET("/port-records", s.ListPortRecords)
	auth.POST("/port-records", s.requireAdmin(), s.CreatePortRecord)
	auth.PUT("/port-records/:id", s.requireAdmin(), s.UpdatePortRecord)
	auth.DELETE("/port-records/:id", s.requireAdmin(), s.DeletePortRecord)
	auth.GET("/dashboard/summary", s.DashboardSummary)
	auth.GET("/audit", s.ListAudit)
	auth.POST("/audit/search", s.requireAdmin(), s.ListAuditFiltered)
	auth.GET("/users", s.ListUsers)
	auth.POST("/users", s.requireAdmin(), s.CreateUserSecure)
	auth.DELETE("/users/:id", s.requireAdmin(), s.DeleteUser)

	// 反向代理
	auth.GET("/proxy/domains", s.ListProxyDomains)
	auth.POST("/proxy/domains", s.requireAdmin(), s.CreateProxyDomain)
	auth.PUT("/proxy/domains/:id", s.requireAdmin(), s.UpdateProxyDomain)
	auth.DELETE("/proxy/domains/:id", s.requireAdmin(), s.DeleteProxyDomain)
	auth.POST("/proxy/domains/:id/routes", s.requireAdmin(), s.CreateProxyRoute)
	auth.PUT("/proxy/routes/:id", s.requireAdmin(), s.UpdateProxyRoute)
	auth.DELETE("/proxy/routes/:id", s.requireAdmin(), s.DeleteProxyRoute)
	auth.POST("/proxy/selfsign", s.requireAdmin(), s.SelfSignCert)
	auth.GET("/proxy/env", s.ProxyEnv)
	auth.POST("/proxy/engine", s.requireAdmin(), s.SetProxyEngine)
	auth.POST("/proxy/caddy/install", s.requireAdmin(), s.InstallCaddy)
	auth.GET("/proxy/dns-config", s.GetDNSConfig)
	auth.POST("/proxy/dns-config", s.requireAdmin(), s.SetDNSConfig)
	auth.GET("/proxy/caddyfile", s.GetCaddyfile)
	auth.POST("/proxy/caddyfile", s.requireAdmin(), s.SetCaddyfileRaw)
	auth.POST("/proxy/caddy-custom", s.requireAdmin(), s.SetCaddyCustom)
	auth.GET("/proxy/caddy-global", s.GetCaddyGlobal)
	auth.POST("/proxy/caddy-global", s.requireAdmin(), s.SetCaddyGlobal)

	// 防爆破 jail
	auth.GET("/jails", s.ListJails)
	auth.POST("/jails", s.requireAdmin(), s.CreateJail)
	auth.PUT("/jails/:id", s.requireAdmin(), s.UpdateJail)
	auth.DELETE("/jails/:id", s.requireAdmin(), s.DeleteJail)
	auth.DELETE("/jails/bans/:id", s.requireAdmin(), s.UnbanJailIP)

	// 限速 / NAT / 定时任务 / 告警
	auth.GET("/rate-limits", s.ListRateLimits)
	auth.POST("/rate-limits", s.requireAdmin(), s.CreateRateLimit)
	auth.PUT("/rate-limits/:id", s.requireAdmin(), s.UpdateRateLimit)
	auth.DELETE("/rate-limits/:id", s.requireAdmin(), s.DeleteRateLimit)
	auth.GET("/nat-rules", s.ListNAT)
	auth.POST("/nat-rules", s.requireAdmin(), s.CreateNAT)
	auth.PUT("/nat-rules/:id", s.requireAdmin(), s.UpdateNAT)
	auth.DELETE("/nat-rules/:id", s.requireAdmin(), s.DeleteNAT)
	auth.GET("/tasks", s.ListTasks)
	auth.POST("/tasks", s.requireAdmin(), s.CreateTask)
	auth.PUT("/tasks/:id", s.requireAdmin(), s.UpdateTask)
	auth.DELETE("/tasks/:id", s.requireAdmin(), s.DeleteTask)
	auth.GET("/alerts", s.ListAlerts)
	auth.POST("/alerts", s.requireAdmin(), s.UpsertAlert)
	auth.DELETE("/alerts/:id", s.requireAdmin(), s.DeleteAlert)
	auth.POST("/alerts/test", s.requireAdmin(), s.TestAlert)

	// WebSocket（复用 JWT 查询参数鉴权）
	r.GET("/api/ws", s.WS.Handler(s.authFromQuery))
}

// Run 启动 HTTP 服务。
// Run 启动面板 HTTP 服务；httpsAddr 非空时启用整站 HTTP→HTTPS 跳转（保留 /.well-known/acme-challenge 明文路径）。
func (s *Server) Run(listen string, httpsAddr ...string) error {
	var handler http.Handler = s.Engine
	if len(httpsAddr) > 0 && httpsAddr[0] != "" {
		target := httpsAddr[0]
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// ACME HTTP-01 挑战必须走明文 80/HTTP 端口，不跳转
			if strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
				s.Engine.ServeHTTP(w, r)
				return
			}
			host := r.Host
			if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host, "]") {
				host = host[:i] // 去掉原端口，追加 HTTPS 端口
			}
			http.Redirect(w, r, "https://"+host+target+r.URL.RequestURI(), http.StatusTemporaryRedirect)
		})
	}
	srv := &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

// RunTLS 启动面板 HTTPS 服务（证书由 proxy 层 certmagic 自动管理）。
func (s *Server) RunTLS(listen string, tlsCfg *tls.Config) error {
	srv := &http.Server{
		Addr:              listen,
		Handler:           s.Engine,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         tlsCfg,
	}
	return srv.ListenAndServeTLS("", "")
}

// RunTLSFiles 启动面板 HTTPS 服务（手动证书文件）。
func (s *Server) RunTLSFiles(listen, certFile, keyFile string) error {
	srv := &http.Server{
		Addr:              listen,
		Handler:           s.Engine,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServeTLS(certFile, keyFile)
}

// basicAuthMiddleware 整站 Basic Auth（每次请求读取原子状态；常量时间比较防时序侧信道）。
func (s *Server) basicAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		st := s.App.GetBasicAuth()
		if !st.Enabled() {
			c.Next()
			return
		}
		u, p, ok := c.Request.BasicAuth()
		if !ok || !st.Match(u, p) {
			c.Header("WWW-Authenticate", `Basic realm="FirePanel", charset="UTF-8"`)
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	}
}

// RegisterStatic 挂载内嵌前端（SPA fallback）。
func (s *Server) RegisterStatic() {
	s.Engine.NoRoute(func(c *gin.Context) {
		staticHandler().ServeHTTP(c.Writer, c.Request)
	})
}

// staticHandler 返回内嵌前端（SPA fallback 到 index.html）。
func staticHandler() http.Handler {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(fmt.Errorf("嵌入前端资源不可用: %w", err))
	}
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(dist, path); err != nil {
			// SPA 路由回退
			req.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, req)
	})
}
