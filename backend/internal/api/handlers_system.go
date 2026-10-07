package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"firepanel/internal/app"
	"firepanel/internal/fw"
	"firepanel/internal/monitor"
	"firepanel/internal/store"
	"firepanel/internal/sysinfo"
)

/* ------------------------- 系统信息 / 漂移 / 同步 ------------------------- */

// Bootstrap 公开端点：仅暴露初始化状态（登录前需要）。
func (s *Server) Bootstrap(c *gin.Context) {
	var needs bool
	if n, err := s.App.Store.CountUsers(); err == nil {
		needs = n == 0
	}
	c.JSON(200, gin.H{"needs_setup": needs})
}

// ListInterfaces 枚举系统网卡（NAT 出口下拉用）。
func (s *Server) ListInterfaces(c *gin.Context) {
	c.JSON(200, gin.H{"items": sysinfo.NetInterfaces()})
}

// EnableIPForward 开启内核 IPv4 转发并写入 /etc/sysctl.conf 持久化（端口转发/NAT 依赖）。
func (s *Server) EnableIPForward(c *gin.Context) {
	if _, err := s.App.Exec.Run(c.Request.Context(), "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		respErr(c, 500, fmt.Errorf("sysctl 执行失败: %w", err))
		return
	}
	persisted := sysctlConfHasForwarding("/etc/sysctl.conf")
	warn := ""
	if !persisted {
		f, err := os.OpenFile("/etc/sysctl.conf", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			warn = "已开启但写入 /etc/sysctl.conf 失败（重启后可能失效）：" + err.Error()
		} else {
			_, _ = f.WriteString("\n# Added by FirePanel: IPv4 转发（端口转发/NAT 依赖）\nnet.ipv4.ip_forward=1\n")
			_ = f.Close()
			persisted = true
		}
	}
	s.audit(c, "enable", "ip_forward", nil, map[string]any{"persisted": persisted})
	c.JSON(200, gin.H{"ip_forward": s.App.IpForwardEnabled(), "persisted": persisted, "warning": warn})
}

// sysctlConfHasForwarding /etc/sysctl.conf 是否已含未注释的 ip_forward=1（容忍空格写法）。
func sysctlConfHasForwarding(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.ReplaceAll(line, " ", "") == "net.ipv4.ip_forward=1" {
			return true
		}
	}
	return false
}

func (s *Server) SystemInfo(c *gin.Context) {
	info := sysinfo.GetInfo()
	// 有无可初始化（setup）
	var needsSetup bool
	if n, err := s.App.Store.CountUsers(); err == nil {
		needsSetup = n == 0
	}
	drift := s.App.CachedDrift()
	driftStatus := "unknown"
	if drift != nil {
		if drift.Drifted {
			driftStatus = "drifted"
		} else {
			driftStatus = "clean"
		}
	}
	c.JSON(200, gin.H{
		"hostname":    info.Hostname,
		"distro":      info.Distro,
		"kernel":      info.Kernel,
		"arch":        info.Arch,
		"uptime":      info.Uptime,
		"backend":     s.App.Backend.Name(),
		"detected":    s.App.Detect,
		"drift":       driftStatus,
		"needs_setup": needsSetup,
		"ssh_ports":   s.App.Cfg.Firewall.SSHPorts,
		"time":        time.Now().Format(time.RFC3339),
	})
}

// TrafficOverview 流量监控快照：网卡速率 + 趋势窗口 + TCP 连接级速率。
func (s *Server) TrafficOverview(c *gin.Context) {
	snap := s.App.Traffic.Snapshot()
	if snap.Interfaces == nil {
		snap.Interfaces = []monitor.IfaceRate{}
	}
	if snap.Conns == nil {
		snap.Conns = []monitor.ConnRate{}
	}
	rx, tx := monitor.SumBps(snap.Interfaces)
	c.JSON(200, gin.H{
		"interfaces": snap.Interfaces,
		"window":     snap.Window,
		"conns":      snap.Conns,
		"conns_error": snap.ConnsErr,
		"ts":         snap.Ts,
		"rx_bps":     rx,
		"tx_bps":     tx,
	})
}

func (s *Server) GetDrift(c *gin.Context) {
	c.JSON(200, gin.H{"report": s.App.CachedDrift()})
}

func (s *Server) CheckDriftNow(c *gin.Context) {
	rep, err := s.App.CheckDrift()
	if err != nil {
		respErr(c, 500, err)
		return
	}
	c.JSON(200, gin.H{"report": rep})
}

func (s *Server) ForceSync(c *gin.Context) {
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Sync(c.ClientIP())
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "sync", "system", nil, res)
	c.JSON(200, gin.H{"sync": res})
}

// ConfirmDanger 高危变更确认：stage=1 生效前（立即应用），stage=2 生效后（取消自动回滚）。
func (s *Server) ConfirmDanger(c *gin.Context) {
	var req struct {
		Token string `json:"token" binding:"required"`
		Stage int    `json:"stage"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "缺少令牌"})
		return
	}
	res, err := s.App.Rec.ConfirmDanger(req.Token, req.Stage)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, "danger_confirm", "system", nil, map[string]any{"token": req.Token, "stage": req.Stage})
	c.JSON(200, gin.H{"sync": res})
}

func (s *Server) CancelDanger(c *gin.Context) {
	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "缺少令牌"})
		return
	}
	ok := s.App.Rec.CancelDanger(req.Token)
	if ok {
		s.App.OnDangerCancelled()
	}
	s.audit(c, "danger_cancel", "system", nil, map[string]any{"token": req.Token, "cancelled": ok})
	c.JSON(200, gin.H{"cancelled": ok})
}

/* ------------------------- 防火墙规则视图 / 备份 ------------------------- */

// FirewallRules 面板链规则视图 + 每条规则的期望映射与命中计数。
func (s *Server) FirewallRules(c *gin.Context) {
	snap, err := s.App.Backend.Snapshot(c.Request.Context())
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if snap.Chains == nil {
		snap.Chains = []fw.ChainView{}
	}
	if snap.Rules == nil {
		snap.Rules = []fw.KernelRule{}
	}
	c.JSON(200, gin.H{"snapshot": snap})
}

// Backup 导出备份：format=json（期望状态）| iptables-save | nft。
func (s *Server) Backup(c *gin.Context) {
	format := c.Query("format")
	switch format {
	case "iptables-save":
		if s.App.Backend.Name() != fw.BackendIPTables {
			c.JSON(400, gin.H{"error": "当前后端不支持 iptables-save 导出"})
			return
		}
		out, err := s.App.Exec.Run(c.Request.Context(), "iptables-save")
		if err != nil {
			respErr(c, 500, err)
			return
		}
		c.Header("Content-Disposition", "attachment; filename=firepanel-iptables.v4")
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(out))
	case "nft":
		if s.App.Backend.Name() != fw.BackendNftables {
			c.JSON(400, gin.H{"error": "当前后端不支持 nft 导出"})
			return
		}
		out, err := s.App.Exec.Run(c.Request.Context(), "nft", "list", "ruleset")
		if err != nil {
			respErr(c, 500, err)
			return
		}
		c.Header("Content-Disposition", "attachment; filename=firepanel-nft-ruleset.nft")
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(out))
	default:
		snap := s.snapshotRowsForBackup()
		b, _ := json.MarshalIndent(snap, "", "  ")
		c.Header("Content-Disposition", "attachment; filename=firepanel-backup.json")
		c.Data(http.StatusOK, "application/json", b)
	}
}

func (s *Server) snapshotRowsForBackup() map[string]any {
	out := map[string]any{
		"version":   1,
		"exported":  time.Now().Format(time.RFC3339),
		"backend":   s.App.Backend.Name(),
	}
	if fwds, err := s.App.Store.ListForwards(); err == nil {
		out["forward_rules"] = fwds
	}
	if lists, err := s.App.Store.ListIPLists(""); err == nil {
		out["ip_lists"] = lists
	}
	return out
}

/* ------------------------- 访问防护（Basic Auth） ------------------------- */

// GetBasicAuth 当前整站 Basic Auth 状态（密码不回传，仅回是否已设置）。
func (s *Server) GetBasicAuth(c *gin.Context) {
	st := s.App.GetBasicAuth()
	out := gin.H{"enabled": false, "username": "", "password_set": false, "source": ""}
	if st != nil && st.Enabled() {
		out["enabled"] = true
		out["username"] = st.User
		out["password_set"] = true
	} else if u, _, ok := app.LoadBasicAuthFile(); ok {
		// 文件里保存过凭据但当前停用：回显用户名与"已有密码"
		out["username"] = u
		out["password_set"] = true
		out["source"] = "file"
	}
	switch s.App.BasicAuthSource() {
	case "file":
		out["source"] = "file"
	case "config":
		if out["source"] == "" {
			out["source"] = "config"
		}
	}
	c.JSON(200, out)
}

// SetBasicAuth 配置整站 Basic Auth：启用/停用 + 凭据。密码留空表示保持原密码。
func (s *Server) SetBasicAuth(c *gin.Context) {
	var req struct {
		Enabled  bool   `json:"enabled"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	prev := s.App.GetBasicAuth()
	user := strings.TrimSpace(req.Username)
	if user == "" && prev != nil {
		user = prev.User
	}
	pass := req.Password
	if pass == "" && prev != nil {
		pass = prev.Pass
	}
	if req.Enabled {
		if user == "" {
			c.JSON(400, gin.H{"error": "启用 Basic Auth 需要用户名"})
			return
		}
		if pass == "" {
			c.JSON(400, gin.H{"error": "首次启用需要设置密码"})
			return
		}
		if err := s.App.SaveBasicAuth(true, user, pass); err != nil {
			respErr(c, 500, err)
			return
		}
		s.audit(c, "update", "settings:basicauth", nil, map[string]any{"enabled": true, "username": user})
		c.JSON(200, gin.H{"ok": true, "enabled": true})
		return
	}
	// 停用：凭据保留在 basicauth.yaml（enabled=false），便于下次直接开启
	if err := s.App.SaveBasicAuth(false, user, pass); err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "update", "settings:basicauth", nil, map[string]any{"enabled": false})
	c.JSON(200, gin.H{"ok": true, "enabled": false})
}

/* ------------------------- 仪表盘 / 审计 ------------------------- */

func (s *Server) DashboardSummary(c *gin.Context) {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	samples, _ := s.App.Store.ListStatsSince(midnight.Unix())

	var conn int64
	var rxPeak, txPeak int64
	var trafficTotal float64
	for _, sm := range samples {
		if sm.ConnCount > conn {
			conn = sm.ConnCount
		}
		if sm.RxBps > rxPeak {
			rxPeak = sm.RxBps
		}
		if sm.TxBps > txPeak {
			txPeak = sm.TxBps
		}
		trafficTotal += float64(sm.RxBps+sm.TxBps) * float64(s.App.Cfg.Sync.StatsInterval.Seconds())
	}
	rxNow, txNow := s.App.Net.Tick()
	if conn == 0 {
		conn = sysinfo.ConnCount()
	}

	// 规则命中排行
	counters, _ := s.App.Backend.Counters(c.Request.Context())
	fwds, _ := s.App.Store.ListForwards()
	type hitRow struct {
		ID   int64  `json:"rule_id"`
		Name string `json:"name"`
		Hits uint64 `json:"hits"`
	}
	var hits []hitRow
	for _, f := range fwds {
		var total uint64
		for tag, ct := range counters {
			if kind, id, _, ok := fw.ParseTag(tag); ok && kind == "fw" && id == f.ID {
				total += ct.Packets
			}
		}
		hits = append(hits, hitRow{ID: f.ID, Name: f.Name, Hits: total})
	}
	// 按命中排序
	for i := 0; i < len(hits); i++ {
		for j := i + 1; j < len(hits); j++ {
			if hits[j].Hits > hits[i].Hits {
				hits[i], hits[j] = hits[j], hits[i]
			}
		}
	}
	if hits == nil {
		hits = []hitRow{}
	}

	c.JSON(200, gin.H{
		"conn_count":     conn,
		"rx_bps":         rxNow,
		"tx_bps":         txNow,
		"traffic_today":  int64(trafficTotal),
		"samples":        samples,
		"hit_ranks":      hits,
		"backend":        s.App.Backend.Name(),
	})
}

func (s *Server) ListAudit(c *gin.Context) {
	limit := intQuery(c, "limit", 100)
	offset := intQuery(c, "offset", 0)
	items, err := s.App.Store.ListAudit(limit, offset)
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if items == nil {
		items = []store.AuditEntry{}
	}
	c.JSON(200, gin.H{"items": items})
}

func intQuery(c *gin.Context, key string, def int) int {
	v := c.Query(key)
	if v == "" {
		return def
	}
	n, err := strconvAtoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

func strconvAtoi(s string) (int, error) {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, errStr("非数字")
		}
		n = n*10 + int(ch-'0')
	}
	return n, nil
}
