// FirePanel — Linux 防火墙可视化管理面板。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"firepanel/internal/api"
	"firepanel/internal/app"
	"firepanel/internal/config"
	"firepanel/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置错误:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.Server.DataDir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "创建数据目录失败:", err)
		os.Exit(1)
	}

	log := newLogger(cfg.Logging.Level)
	log.Info("FirePanel 启动", "listen", cfg.Server.Listen, "data_dir", cfg.Server.DataDir)

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	go func() {
		log.Info("HTTP 服务就绪", "addr", cfg.Server.Listen)
		redirect := ""
		if cfg.Server.PanelHTTPS != "" || (cfg.Server.PanelTLSCert != "" && cfg.Server.PanelTLSKey != "") {
			redirect = cfg.Server.PanelHTTPSPort
		}
		if err := srv.Run(cfg.Server.Listen, redirect); err != nil {
			log.Error("HTTP 服务退出", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("收到退出信号，正在关闭")
	a.Sched.Stop()
	a.Jail.Stop()
	a.Proxy.Stop()
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
