// FirePanel — Linux 防火墙可视化管理面板。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"

	"firepanel/internal/api"
	"firepanel/internal/app"
	"firepanel/internal/config"
	"firepanel/internal/store"
)

// version 由构建时注入：-ldflags "-X main.version=v1.2.3"。
var version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置错误:", err)
		os.Exit(1)
	}
	if cfg.PrintVersion {
		fmt.Println("firepanel " + version)
		return
	}
	if err := os.MkdirAll(cfg.Server.DataDir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "创建数据目录失败:", err)
		os.Exit(1)
	}

	log := newLogger(cfg.Logging.Level)
	log.Info("FirePanel 启动", "listen", cfg.Server.Listen, "data_dir", cfg.Server.DataDir)
	for _, w := range cfg.Warnings {
		log.Warn("配置告警", "detail", w)
	}

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		log.Error("打开数据库失败", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	a, err := app.New(cfg, st, log)
	if err != nil {
		log.Error("初始化防火墙后端失败", "err", err)
		os.Exit(1)
	}
	if v6, ok := a.Backend.(interface{ IPv6Enabled() bool }); ok {
		log.Info("IPv6 规则同步", "enabled", v6.IPv6Enabled())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 先处理上次未完成的高危变更（回滚到变更前状态），再启动重放。
	// 否则启动重放会跳过延时/确认流程，把可能锁死 SSH 的规则直接写进内核。
	a.RecoverPendingDanger()

	// 反向代理：加载数据库中的域名/路由（无域名则不监听）
	if err := a.ReloadProxy(); err != nil {
		log.Warn("反向代理启动失败（不影响防火墙功能）", "err", err)
	}
	a.ProxyHealthLoop(ctx)
	a.LoadNotify()
	a.Jail.Reload()
	if err := a.Sched.Start(); err != nil {
		log.Warn("定时任务加载失败", "err", err)
	}
	a.StartBackground(ctx)

	srv := api.NewServer(a, log)
	srv.RegisterStatic()

	// 面板 HTTPS：优先手动证书文件，其次按 panel_https 主机自动签发
	// （IP → Let's Encrypt shortlived 6.7 天证书，自动续期；域名 → 默认 profile）
	if cfg.Server.PanelTLSCert != "" && cfg.Server.PanelTLSKey != "" {
		go func() {
			log.Info("面板 HTTPS（手动证书）", "addr", cfg.Server.PanelHTTPSPort)
			if err := srv.RunTLSFiles(cfg.Server.PanelHTTPSPort, cfg.Server.PanelTLSCert, cfg.Server.PanelTLSKey); err != nil {
				log.Error("面板 HTTPS 退出", "err", err)
			}
		}()
	} else if cfg.Server.PanelHTTPS != "" {
		go func() {
			tlsCfg, err := a.Proxy.PanelTLSConfig(ctx, strings.ToLower(strings.TrimSuffix(cfg.Server.PanelHTTPS, ".")))
			if err != nil {
				log.Warn("面板证书自动签发失败，HTTPS 不可用（仅 HTTP）", "host", cfg.Server.PanelHTTPS, "err", err)
				return
			}
			log.Info("面板 HTTPS 就绪", "addr", cfg.Server.PanelHTTPSPort, "host", cfg.Server.PanelHTTPS)
			if err := srv.RunTLS(cfg.Server.PanelHTTPSPort, tlsCfg); err != nil {
				log.Error("面板 HTTPS 退出", "err", err)
			}
		}()
	}

	if s := cfg.Server.BasicAuthUser; s != "" && cfg.Server.BasicAuthPass != "" {
		log.Info("面板整站 Basic Auth 已启用", "user", s)
	}

	var serveFailed atomic.Bool
	go func() {
		log.Info("HTTP 服务就绪", "addr", cfg.Server.Listen)
		redirect := ""
		if cfg.Server.PanelHTTPS != "" || (cfg.Server.PanelTLSCert != "" && cfg.Server.PanelTLSKey != "") {
			redirect = cfg.Server.PanelHTTPSPort
		}
		if err := srv.Run(cfg.Server.Listen, redirect); err != nil {
			log.Error("HTTP 服务退出", "err", err)
			// 监听失败（端口占用等）必须以非零码退出：systemd 的 Restart=on-failure
			// 只在失败时重启，退出码 0 会让「面板根本没起来」被当成正常退出。
			serveFailed.Store(true)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("收到退出信号，正在关闭")
	a.Sched.Stop()
	a.Jail.Stop()
	a.Proxy.Stop()
	if serveFailed.Load() {
		os.Exit(1)
	}
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lv})
	return slog.New(h)
}
