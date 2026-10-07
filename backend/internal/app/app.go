// Package app：装配 store + fw + sysinfo，提供事务化变更编排与后台任务。
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"firepanel/internal/config"
	"firepanel/internal/fw"
	"firepanel/internal/jail"
	"firepanel/internal/monitor"
	"firepanel/internal/notify"
	"firepanel/internal/proxy"
	"firepanel/internal/scheduler"
	"firepanel/internal/store"
	"firepanel/internal/sysinfo"
)

// App 面板运行时。
type App struct {
	Cfg     *config.Config
	Store   *store.Store
	Log     *slog.Logger
	Exec    fw.Executor
	Detect  *fw.DetectResult
	Backend fw.FirewallBackend
	Rec     *fw.Reconciler
	Net     *sysinfo.NetSampler
	Traffic *monitor.Monitor
	Proxy   *proxy.Manager
	Sched   *scheduler.Scheduler
	Notify  *notify.Notifier
	Jail    *jail.Engine

	// 广播事件（WS events / logs topic 接线）
	Broadcast func(topic string, payload any)

	mu          sync.Mutex
	drift       *fw.DriftReport
	pendingSnap snapshotState // 危险变更挂起期间的数据库快照（撤销时恢复）
	notifyEvents map[string]bool
	lastErr     string

	// 整站 Basic Auth（运行时可变，中间件每次请求读取）
	basicAuth atomic.Pointer[BasicAuthState]
}

// SetBasicAuth 运行中更新整站 Basic Auth 凭据（nil=关闭）。
func (a *App) SetBasicAuth(st *BasicAuthState) { a.basicAuth.Store(st) }

// GetBasicAuth 当前凭据状态（可能为 nil）。
func (a *App) GetBasicAuth() *BasicAuthState { return a.basicAuth.Load() }

// New 构建运行时：探测环境 → 选后端 → 初始化。
func New(cfg *config.Config, st *store.Store, log *slog.Logger) (*App, error) {
	exec := fw.NewRealExecutor()
	det, err := fw.Detect(context.Background(), exec)
	if err != nil {
		return nil, err
	}
	name, err := fw.PickBackend(cfg.Firewall.Backend, det)
	if err != nil {
		return nil, err
	}
	log.Info("防火墙后端", "selected", name, "iptables_variant", det.IPTablesVariant,
		"firewalld", det.FirewalldActive, "ufw", det.UfwActive, "docker", det.DockerPresent)

	var backend fw.FirewallBackend
	if name == fw.BackendNftables {
		backend = fw.NewNftablesBackend(exec)
	} else {
		backend = fw.NewIPTablesBackend(exec)
	}
	if err := backend.Init(context.Background()); err != nil {
		return nil, fmt.Errorf("初始化防火墙链: %w", err)
	}

	a := &App{
		Cfg: cfg, Store: st, Log: log, Exec: exec,
		Detect: det, Backend: backend,
		Rec:     fw.NewReconciler(backend, cfg.Firewall.SSHPorts, cfg.Sync.DangerDelay, cfg.Sync.ConfirmWindow),
		Net:     sysinfo.NewNetSampler(),
		Traffic: monitor.New(exec, 2*time.Second),
	}
	mode := cfg.Proxy.Backend
	if mode != proxy.ModeCaddy {
		mode = proxy.ModeBuiltin
	}
	// 引擎与监听端口的设置页覆盖（settings 持久化，优先于配置文件）
	if v, err := st.GetSetting("proxy_engine"); err == nil && (v == proxy.ModeBuiltin || v == proxy.ModeCaddy) {
		mode = v
	}
	httpAddr := cfg.Server.ProxyHTTP
	if v, err := st.GetSetting("proxy_http_addr"); err == nil && v != "" {
		httpAddr = v
	}
	httpsAddr := cfg.Server.ProxyHTTPS
	if v, err := st.GetSetting("proxy_https_addr"); err == nil && v != "" {
		httpsAddr = v
	}
	a.Proxy = proxy.NewManager(log, proxy.Options{
		Mode:       mode,
		HTTPAddr:   httpAddr,
		HTTPSAddr:  httpsAddr,
		CertDir:    filepath.Join(cfg.Server.DataDir, "certs"),
		CaddyAdmin: cfg.Proxy.CaddyAdmin,
	})
	a.Proxy.OnEvent = func(eventType, message, detail string) {
		a.broadcast("events", fw.Event{Type: eventType, Message: message, Detail: detail, Timestamp: time.Now()})
	}
	a.Proxy.SetRunner(exec)
	var caddyGlobal proxy.CaddyGlobalSettings
	if v, err := st.GetSetting("caddy_global"); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &caddyGlobal)
	}
	a.Proxy.SetCaddyGlobal(caddyGlobal)
	if v, err := st.GetSetting("caddy_custom"); err == nil {
		a.Proxy.SetCaddyCustom(v)
	}
	a.Proxy.OnCertStatus = func(domainID int64, status, errMsg string) {
		var expires *time.Time
		_ = a.Store.SetRPDomainCert(domainID, status, errMsg, expires)
	}
	a.Notify = notify.New()
	a.SetBasicAuth(LoadBasicAuth(st, cfg))
	a.Jail = jail.New(st, log, fw.NewRealExecutor())
	a.Jail.OnBan = func(j store.JailConfig, ip string, until time.Time) {
		// 自动封禁：同步内核 + 广播事件（走告警订阅）
		if _, err := a.Sync(""); err != nil {
			a.Log.Error("jail 封禁同步内核失败", "err", err)
		}
		a.broadcast("events", fw.Event{
			Type:    "jail_ban",
			Message: fmt.Sprintf("防爆破「%s」已封禁 %s，至 %s 解封", j.Name, ip, until.Format("15:04:05")),
			Detail:  fmt.Sprintf("阈值 %d 次 / %d 秒，封禁时长 %d 秒", j.Threshold, j.FindTime, j.BanTime),
			Timestamp: time.Now(),
		})
		_ = a.Store.AddAudit(&store.AuditEntry{
			Username: "system", Action: "jail_ban",
			Target: "iplist:black:" + ip, Detail: j.Name, IP: ip,
		})
	}
	a.Sched = scheduler.New(st, log, scheduler.Actions{
		SetForward: func(id int64, enabled bool) error {
			if err := st.SetForwardEnabled(id, enabled); err != nil {
				return err
			}
			_, err := a.Sync("")
			return err
		},
		ExpireIPList: func() error {
			n, err := st.DeleteIPListsExpired(time.Now())
			if err != nil || n == 0 {
				return err
			}
			_, _ = a.Sync("")
			return nil
		},
		Sync: func() error {
			_, err := a.Sync("")
			return err
		},
	})
	a.Rec.Events = func(e fw.Event) {
		a.BroadcastEvent(e)
	}
	return a, nil
}

// LoadNotify 从数据库装载告警渠道并接线事件订阅。
func (a *App) LoadNotify() {
	alerts, err := a.Store.ListAlerts()
	if err != nil {
		return
	}
	var tgCfg *notify.TelegramConfig
	var whCfg *notify.WebhookConfig
	var smCfg *notify.SMTPConfig
	subscribed := map[string]bool{}
	for _, al := range alerts {
		if !al.Enabled {
			continue
		}
		var events []string
		_ = json.Unmarshal([]byte(al.EventsJSON), &events)
		for _, e := range events {
			subscribed[e] = true
		}
		switch al.Channel {
		case "telegram":
			var c notify.TelegramConfig
			if json.Unmarshal([]byte(al.ConfigJSON), &c) == nil {
				tgCfg = &c
			}
		case "webhook":
			var c notify.WebhookConfig
			if json.Unmarshal([]byte(al.ConfigJSON), &c) == nil {
				whCfg = &c
			}
		case "smtp":
			var c notify.SMTPConfig
			if json.Unmarshal([]byte(al.ConfigJSON), &c) == nil {
				smCfg = &c
			}
		}
	}
	a.Notify.Load(tgCfg, whCfg, smCfg)

	a.mu.Lock()
	a.notifyEvents = subscribed
	a.mu.Unlock()
}

// subscribedEvents 返回当前订阅的事件类型集合。
func (a *App) subscribedEvents() map[string]bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.notifyEvents
}

// dispatchNotify 按订阅把事件发往通知渠道（异步、单事件单渠道合并发送）。
func (a *App) dispatchNotify(e fw.Event) {
	sub := a.subscribedEvents()
	if len(sub) == 0 {
		return
	}
	// health_down / apply_fail / drift / danger 类事件默认映射
	if !sub[e.Type] {
		// 前缀匹配：如 health_down 订阅 health_*
		matched := false
		for k := range sub {
			if strings.HasSuffix(k, "_*") && strings.HasPrefix(e.Type, strings.TrimSuffix(k, "*")) {
				matched = true
				break
			}
		}
		if !matched {
			return
		}
	}
	ev := notify.Event{Type: e.Type, Message: e.Message, Detail: e.Detail, Time: e.Timestamp}
	go func() {
		for _, err := range a.Notify.Send(ev) {
			a.Log.Warn("告警发送失败", "err", err)
		}
	}()
}

// loadProxyDNS 从 settings 装载 DNS-01 凭据到反代管理器（幂等）。
func (a *App) loadProxyDNS() {
	raw, err := a.Store.GetSetting("dns_provider")
	if err != nil || raw == "" {
		return
	}
	var cfg proxy.DNSProviderConfig
	if json.Unmarshal([]byte(raw), &cfg) == nil && cfg.Provider != "" {
		_ = a.Proxy.SetDNSProvider(cfg)
	}
}

// CertExpiryScan 巡检反代证书到期日：刷新 cert_expires_at；
// 已过期或 3 天内到期（含续期失败场景）时发事件进告警通道。
func (a *App) CertExpiryScan() {
	domains, err := a.Store.ListRPDomains()
	if err != nil {
		return
	}
	now := time.Now()
	for _, d := range domains {
		if d.TLSMode != "auto" || !d.Enabled {
			continue
		}
		// 已标记 error 的域名不覆盖：签发失败时旧证书可能仍在服务，
		// 巡检把它刷成 ok 会掩盖诊断信息（到期告警依旧覆盖此场景）
		if d.CertStatus == "error" {
			continue
		}
		exp, ok := a.Proxy.CertExpiry(d.Domain)
		if !ok {
			continue
		}
		_ = a.Store.SetRPDomainCert(d.ID, "ok", "", &exp)
		switch {
		case exp.Before(now):
			a.broadcast("events", fw.Event{
				Type: "proxy_cert_error", Timestamp: now,
				Message: fmt.Sprintf("反代证书已过期: %s（到期 %s）", d.Domain, exp.Format("2006-01-02 15:04")),
				Detail:  "自动续期未成功，请检查该域名的挑战方式与环境（80/443 可达性、DNS 凭据）",
			})
		case exp.Before(now.Add(72 * time.Hour)):
			a.broadcast("events", fw.Event{
				Type: "proxy_cert_error", Timestamp: now,
				Message: fmt.Sprintf("反代证书即将到期: %s（剩 %d 小时）", d.Domain, int(exp.Sub(now).Hours())),
				Detail:  "IP 短期证书（约 6.7 天）属正常短周期，续期由 certmagic 自动进行",
			})
		}
	}
}

// SaveCaddyGlobal 持久化 Caddyfile 全局选项并热更新管理器。
func (a *App) SaveCaddyGlobal(g proxy.CaddyGlobalSettings) error {
	b, _ := json.Marshal(g)
	if err := a.Store.SetSetting("caddy_global", string(b)); err != nil {
		return err
	}
	a.Proxy.SetCaddyGlobal(g)
	return nil
}

// ReloadProxy 从 store 装载反代配置并热重载；返回每个域名的证书状态已更新的行。
func (a *App) ReloadProxy() error {
	a.loadProxyDNS()
	domains, err := a.Store.ListRPDomains()
	if err != nil {
		return err
	}
	routes, err := a.Store.ListRPRoutes(0)
	if err != nil {
		return err
	}
	routesByDomain := map[int64][]proxy.RouteConfig{}
	for _, r := range routes {
		routesByDomain[r.DomainID] = append(routesByDomain[r.DomainID], proxy.RouteConfig{
			ID: r.ID, PathMatch: r.PathMatch, UpstreamURL: r.UpstreamURL,
			WsEnabled: r.WsEnabled, HealthPath: r.HealthPath, Enabled: r.Enabled,
		})
	}
	var cfgs []proxy.DomainConfig
	for _, d := range domains {
		cfgs = append(cfgs, proxy.DomainConfig{
			ID: d.ID, Domain: d.Domain, Enabled: d.Enabled, TLSMode: d.TLSMode,
			Challenge: d.Challenge, CaddyOpts: d.CaddyOpts,
			CertPEM: d.CertPEM, KeyPEM: d.KeyPEM, CertStatus: d.CertStatus,
			Routes: routesByDomain[d.ID],
		})
		// 手动证书在 Reload 中解析；成功与否由事件体现
		if d.TLSMode == "manual" && d.Enabled {
			_ = a.Store.SetRPDomainCert(d.ID, "ok", "", nil)
		}
	}
	if err := a.Proxy.Reload(context.Background(), cfgs); err != nil {
		// 监听失败等致命错误：把所有 auto 域名标记错误
		for _, d := range domains {
			if d.TLSMode == "auto" {
				_ = a.Store.SetRPDomainCert(d.ID, "error", err.Error(), nil)
			}
		}
		return err
	}
	return nil
}

// ProxyHealthLoop 周期探测上游健康并回写数据库 + 广播。
func (a *App) ProxyHealthLoop(ctx context.Context) {
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !a.Proxy.Running() {
					continue
				}
				for _, tgt := range a.Proxy.HealthTargets() {
					res := tgt.Probe(5 * time.Second)
					prev := ""
					if r, err := a.Store.GetRPRoute(tgt.RouteID); err == nil {
						prev = r.HealthStatus
					}
					_ = a.Store.SetRPRouteHealth(tgt.RouteID, res.Status, time.Now())
					if prev != res.Status {
						a.broadcast("events", fw.Event{
							Type: "health_" + res.Status,
							Message: fmt.Sprintf("上游 %s（%s）状态变更: %s", tgt.Upstream, tgt.Domain, res.Status),
							Detail:  res.Detail,
							Timestamp: time.Now(),
						})
					}
				}
			}
		}
	}()
}

// BroadcastEvent 通过 events/logs topic 广播内核相关事件，并分发告警。
func (a *App) BroadcastEvent(e fw.Event) {
	a.mu.Lock()
	if e.Type == "drift" {
		var rep fw.DriftReport
		if json.Unmarshal([]byte(e.Detail), &rep) == nil {
			a.drift = &rep
		}
	}
	a.mu.Unlock()
	a.broadcast("events", e)
	a.dispatchNotify(e)
}

func (a *App) broadcast(topic string, payload any) {
	if a.Broadcast != nil {
		a.Broadcast(topic, payload)
	}
}

// Desired 从 store 装载期望状态（仅启用且未过期的规则）。
func (a *App) Desired() *fw.DesiredState {
	st := &fw.DesiredState{}
	now := time.Now()
	if fwds, err := a.Store.ListForwards(); err == nil {
		for _, f := range fwds {
			if !f.Enabled {
				continue
			}
			if f.ExpiresAt != nil && f.ExpiresAt.Before(now) {
				continue
			}
			st.Forwards = append(st.Forwards, fw.ForwardSpec{
				ID: f.ID, Name: f.Name, Protocol: f.Protocol, ListenPort: f.ListenPort,
				SrcIP: f.SrcIP, DstIP: f.DstIP, DstDomain: f.DstDomain, DstPort: f.DstPort, Enabled: true,
			})
		}
	}
	if lists, err := a.Store.ListIPLists(""); err == nil {
		for _, e := range lists {
			if e.ExpiresAt != nil && e.ExpiresAt.Before(now) {
				continue
			}
			st.IPLists = append(st.IPLists, fw.IPListSpec{
				ID: e.ID, ListType: e.ListType, IP: e.IPOrCIDR,
			})
		}
	}
	if rls, err := a.Store.ListRateLimits(); err == nil {
		for _, r := range rls {
			if !r.Enabled {
				continue
			}
			st.RateLimits = append(st.RateLimits, fw.RateLimitSpec{
				ID: r.ID, Name: r.Name, Proto: r.Proto, Port: r.Port,
				PerSrc: r.PerSrc, Rate: r.Rate, RateUnit: r.RateUnit,
				Burst: r.Burst, ConnLimit: r.ConnLimit, Enabled: true,
			})
		}
	}
	if nats, err := a.Store.ListNAT(); err == nil {
		for _, n := range nats {
			if !n.Enabled {
				continue
			}
			st.NAT = append(st.NAT, fw.NATSpec{
				ID: n.ID, Name: n.Name, Type: n.Type,
				SrcCIDR: n.SrcCIDR, OutIface: n.OutIface, ToAddr: n.ToAddr, Enabled: true,
			})
		}
	}
	return st
}

// Sync 把当前期望状态同步到内核（含危险判定）。clientIP 为发起方（可为空）。
func (a *App) Sync(clientIP string) (*fw.SyncResult, error) {
	return a.Rec.Sync(context.Background(), a.Desired(), clientIP)
}

// beforeSnapshot 变更前的期望状态（用于失败回滚与审计）。
func (a *App) beforeSnapshot() json.RawMessage {
	return json.RawMessage(mustMarshal(a.Desired()))
}

type snapshotState struct {
	Forwards   []store.ForwardRule   `json:"forwards"`
	IPLists    []store.IPListEntry   `json:"ip_lists"`
	RateLimits []store.RateLimitRule `json:"rate_limits"`
	NATRules   []store.NATRule       `json:"nat_rules"`
}

func (a *App) snapshotRows() snapshotState {
	s := snapshotState{}
	if fwds, err := a.Store.ListForwards(); err == nil {
		s.Forwards = fwds
	}
	if lists, err := a.Store.ListIPLists(""); err == nil {
		s.IPLists = lists
	}
	if rls, err := a.Store.ListRateLimits(); err == nil {
		s.RateLimits = rls
	}
	if nats, err := a.Store.ListNAT(); err == nil {
		s.NATRules = nats
	}
	return s
}

// restoreRows 失败回滚：恢复行数据并尽力同步内核。
func (a *App) restoreRows(s snapshotState) {
	// 简化实现：逐条重建（规则量级小，可接受）
	if fwds, _ := a.Store.ListForwards(); fwds != nil {
		for _, f := range fwds {
			_ = a.Store.DeleteForward(f.ID)
		}
	}
	if lists, _ := a.Store.ListIPLists(""); lists != nil {
		for _, e := range lists {
			_ = a.Store.DeleteIPList(e.ID)
		}
	}
	if rls, _ := a.Store.ListRateLimits(); rls != nil {
		for _, r := range rls {
			_ = a.Store.DeleteRateLimit(r.ID)
		}
	}
	if nats, _ := a.Store.ListNAT(); nats != nil {
		for _, n := range nats {
			_ = a.Store.DeleteNAT(n.ID)
		}
	}
	for i := range s.Forwards {
		_ = a.Store.CreateForward(&s.Forwards[i])
	}
	for i := range s.IPLists {
		_ = a.Store.CreateIPList(&s.IPLists[i])
	}
	for i := range s.RateLimits {
		_ = a.Store.CreateRateLimit(&s.RateLimits[i])
	}
	for i := range s.NATRules {
		_ = a.Store.CreateNAT(&s.NATRules[i])
	}
	_, _ = a.Rec.Sync(context.Background(), a.Desired(), "")
}

// HasPendingDanger 是否存在待确认的高危变更（挂起期间拒绝新的变更请求）。
func (a *App) HasPendingDanger() bool { return a.Rec.PendingExists() }

// OnDangerCancelled 高危变更被撤销：恢复挂起期间的数据库快照并重新同步。
func (a *App) OnDangerCancelled() {
	a.mu.Lock()
	snap := a.pendingSnap
	a.pendingSnap = snapshotState{}
	a.mu.Unlock()
	if snap.Forwards != nil || snap.IPLists != nil || snap.RateLimits != nil || snap.NATRules != nil {
		a.restoreRows(snap)
	}
}

// Mutate 事务化变更编排：快照 → 变更 → 同步内核；失败恢复。
// 危险变更：保留数据库新状态（期望态），内核在确认/延时后生效；撤销时恢复快照。
func (a *App) Mutate(clientIP string, mutate func() error) (*fw.SyncResult, error) {
	if a.HasPendingDanger() {
		return nil, errors.New("存在待确认的高危变更，请先确认或撤销")
	}
	snap := a.snapshotRows()
	if err := mutate(); err != nil {
		return nil, err
	}
	res, err := a.Sync(clientIP)
	if err != nil {
		a.Log.Error("规则同步失败，回滚数据库", "err", err)
		a.restoreRows(snap)
		return nil, fmt.Errorf("规则应用失败已回滚: %w", err)
	}
	if res != nil && res.Danger {
		a.mu.Lock()
		a.pendingSnap = snap
		a.mu.Unlock()
	}
	// 成功后保存 last-good 快照到 sync_state
	a.saveSyncOK(res)
	return res, nil
}

func (a *App) saveSyncOK(res *fw.SyncResult) {
	prev, _ := a.Store.GetSyncState()
	lastGood := ""
	if prev != nil {
		lastGood = prev.LastGoodJSON
	}
	_ = a.Store.UpsertSyncState(&store.SyncState{
		Backend:      a.Backend.Name(),
		LastOKAt:     ptrTime(time.Now()),
		DriftJSON:    "",
		LastGoodJSON: lastGood,
	})
}

// CheckDrift 执行漂移检测并缓存结果。
func (a *App) CheckDrift() (*fw.DriftReport, error) {
	rep, err := a.Rec.Drift(context.Background(), a.Desired())
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.drift = rep
	a.mu.Unlock()
	if rep.Drifted {
		b, _ := json.Marshal(rep)
		a.broadcast("events", fw.Event{Type: "drift", Message: "检测到内核规则漂移", Detail: string(b), Timestamp: time.Now()})
	}
	return rep, nil
}

func (a *App) CachedDrift() *fw.DriftReport {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.drift
}

// IpForwardEnabled 检测内核 IPv4 转发是否开启（NAT/转发需要）。
func (a *App) IpForwardEnabled() bool {
	b, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(b)) == "1"
}

// StartBackground 启动采样、漂移检测、过期清理循环，并做启动重放（开机自动加载）。
func (a *App) StartBackground(ctx context.Context) {
	// 流量监控采样（网卡速率 + TCP 连接级差分）
	go a.Traffic.Run(ctx)

	// 启动重放：把 SQLite 期望状态应用到内核（服务重启/开机后自动恢复规则）
	go func() {
		if _, err := a.Sync(""); err != nil {
			a.Log.Error("启动重放期望状态失败", "err", err)
		} else {
			a.Log.Info("启动重放完成，面板规则已加载")
		}
	}()

	statsInterval := a.Cfg.Sync.StatsInterval
	driftInterval := a.Cfg.Sync.DriftInterval

	// 证书到期巡检（续期失败兜底告警 + expires 回填）：启动 15 秒后首扫，之后每 6 小时
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		time.Sleep(15 * time.Second)
		a.CertExpiryScan()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.CertExpiryScan()
			}
		}
	}()

	// basicauth.yaml 文件监听：手动编辑后自动热装载（无需重启）
	go a.WatchBasicAuthFile(ctx)

	go func() {
		t := time.NewTicker(statsInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				rx, tx := a.Net.Tick()
				conn := sysinfo.ConnCount()
				a.broadcast("stats", map[string]any{
					"ts": time.Now().Unix(), "conn_count": conn, "rx_bps": rx, "tx_bps": tx,
				})
				// 每分钟落盘一次样本
				if time.Now().Unix()%60 < int64(statsInterval.Seconds()) {
					_ = a.Store.AddStatsSample(time.Now().Unix(), conn, rx, tx)
				}
				if err := a.Store.PruneStatsBefore(time.Now().Add(-48 * time.Hour).Unix()); err != nil {
					a.Log.Warn("清理统计样本失败", "err", err)
				}
			}
		}
	}()

	go func() {
		t := time.NewTicker(driftInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if _, err := a.CheckDrift(); err != nil {
					a.Log.Warn("漂移检测失败", "err", err)
				}
			}
		}
	}()

	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				// 过期名单自动清理 + 过期转发停用
				if n, err := a.Store.DeleteIPListsExpired(time.Now()); err == nil && n > 0 {
					a.Log.Info("自动清理过期黑/白名单", "count", n)
					_, _ = a.Sync("")
				}
				a.expireForwards()
			}
		}
	}()
}

func (a *App) expireForwards() {
	fwds, err := a.Store.ListForwards()
	if err != nil {
		return
	}
	now := time.Now()
	changed := false
	for _, f := range fwds {
		if f.Enabled && f.ExpiresAt != nil && f.ExpiresAt.Before(now) {
			if err := a.Store.SetForwardEnabled(f.ID, false); err == nil {
				a.Log.Info("临时转发规则到期自动停用", "id", f.ID, "name", f.Name)
				changed = true
			}
		}
	}
	if changed {
		_, _ = a.Sync("")
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
