package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"firepanel/internal/fw"
	"firepanel/internal/notify"
	"firepanel/internal/scheduler"
	"firepanel/internal/store"
)

/* ------------------------- 限速规则 ------------------------- */

func validateRateLimit(r *store.RateLimitRule) error {
	if strings.TrimSpace(r.Name) == "" {
		return errStr("名称不能为空")
	}
	switch r.Proto {
	case "tcp", "udp", "both":
	default:
		return errStr("协议必须为 tcp/udp/both")
	}
	if r.Port != "" && parsePortRangeStr(r.Port) == nil {
		return errStr("端口格式错误")
	}
	if r.Rate <= 0 {
		return errStr("速率必须大于 0")
	}
	switch r.RateUnit {
	case "second", "minute", "hour":
	default:
		return errStr("速率单位必须为 second/minute/hour")
	}
	return nil
}

func (s *Server) ListRateLimits(c *gin.Context) {
	items, err := s.App.Store.ListRateLimits()
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if items == nil {
		items = []store.RateLimitRule{}
	}
	c.JSON(200, gin.H{"items": items})
}

type rlReq struct {
	Name      string `json:"name"`
	Proto     string `json:"proto"`
	Port      string `json:"port"`
	PerSrc    *bool  `json:"per_src"`
	Rate      int64  `json:"rate"`
	RateUnit  string `json:"rate_unit"`
	Burst     int64  `json:"burst"`
	ConnLimit int64  `json:"conn_limit"`
	Enabled   *bool  `json:"enabled"`
	Remark    string `json:"remark"`
}

func rlFromReq(c *gin.Context) (*store.RateLimitRule, error) {
	var q rlReq
	if err := c.ShouldBindJSON(&q); err != nil {
		return nil, errStr("参数不合法")
	}
	r := &store.RateLimitRule{
		Name: q.Name, Proto: q.Proto, Port: strings.TrimSpace(q.Port),
		Rate: q.Rate, RateUnit: q.RateUnit, Burst: q.Burst,
		ConnLimit: q.ConnLimit, Remark: q.Remark,
	}
	if q.PerSrc != nil {
		r.PerSrc = *q.PerSrc
	} else {
		r.PerSrc = true
	}
	if q.Enabled != nil {
		r.Enabled = *q.Enabled
	} else {
		r.Enabled = true
	}
	return r, validateRateLimit(r)
}

func (s *Server) CreateRateLimit(c *gin.Context) {
	r, err := rlFromReq(c)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error { return s.App.Store.CreateRateLimit(r) })
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "create", "ratelimit:"+r.Name, nil, r)
	c.JSON(200, gin.H{"item": r, "sync": res})
}

func (s *Server) UpdateRateLimit(c *gin.Context) {
	id := pathID(c)
	r, err := rlFromReq(c)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	r.ID = id
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error { return s.App.Store.UpdateRateLimit(r) })
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "update", "ratelimit:"+r.Name, nil, r)
	c.JSON(200, gin.H{"item": r, "sync": res})
}

func (s *Server) DeleteRateLimit(c *gin.Context) {
	id := pathID(c)
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error { return s.App.Store.DeleteRateLimit(id) })
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "delete", "ratelimit:"+strconv.FormatInt(id, 10), nil, nil)
	c.JSON(200, gin.H{"ok": true, "sync": res})
}

/* ------------------------- NAT ------------------------- */

func validateNAT(r *store.NATRule) error {
	if strings.TrimSpace(r.Name) == "" {
		return errStr("名称不能为空")
	}
	if r.Type != "SNAT" && r.Type != "MASQ" {
		return errStr("类型必须为 SNAT 或 MASQ")
	}
	if fw.NormalizeCIDRCheck(r.SrcCIDR) == "" {
		return errStr("来源网段必须是合法 CIDR")
	}
	if strings.TrimSpace(r.OutIface) == "" || !ifaceNameSafe(strings.TrimSpace(r.OutIface)) {
		return errStr("出口网卡名不合法（仅字母数字 . _ -）")
	}
	if r.Type == "SNAT" {
		if fw.NormalizeIPCheck(r.ToAddr) == "" {
			return errStr("SNAT 需要合法的出口 IP")
		}
	}
	return nil
}

func ifaceNameSafe(s string) bool {
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return len(s) > 0 && len(s) < 16
}

func (s *Server) ListNAT(c *gin.Context) {
	items, err := s.App.Store.ListNAT()
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if items == nil {
		items = []store.NATRule{}
	}
	// 附带 ip_forward 状态提示
	c.JSON(200, gin.H{
		"items": items,
		"ip_forward": s.App.IpForwardEnabled(),
	})
}

type natReq struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	SrcCIDR  string `json:"src_cidr"`
	OutIface string `json:"out_iface"`
	ToAddr   string `json:"to_addr"`
	Enabled  *bool  `json:"enabled"`
	Remark   string `json:"remark"`
}

func natFromReq(c *gin.Context) (*store.NATRule, error) {
	var q natReq
	if err := c.ShouldBindJSON(&q); err != nil {
		return nil, errStr("参数不合法")
	}
	r := &store.NATRule{
		Name: q.Name, Type: q.Type, SrcCIDR: strings.TrimSpace(q.SrcCIDR),
		OutIface: strings.TrimSpace(q.OutIface), ToAddr: strings.TrimSpace(q.ToAddr), Remark: q.Remark,
	}
	if q.Enabled != nil {
		r.Enabled = *q.Enabled
	} else {
		r.Enabled = true
	}
	return r, validateNAT(r)
}

func (s *Server) CreateNAT(c *gin.Context) {
	r, err := natFromReq(c)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error { return s.App.Store.CreateNAT(r) })
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "create", "nat:"+r.Name, nil, r)
	c.JSON(200, gin.H{"item": r, "sync": res})
}

func (s *Server) UpdateNAT(c *gin.Context) {
	id := pathID(c)
	r, err := natFromReq(c)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	r.ID = id
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error { return s.App.Store.UpdateNAT(r) })
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "update", "nat:"+r.Name, nil, r)
	c.JSON(200, gin.H{"item": r, "sync": res})
}

func (s *Server) DeleteNAT(c *gin.Context) {
	id := pathID(c)
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error { return s.App.Store.DeleteNAT(id) })
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "delete", "nat:"+strconv.FormatInt(id, 10), nil, nil)
	c.JSON(200, gin.H{"ok": true, "sync": res})
}

/* ------------------------- 定时任务 ------------------------- */

func (s *Server) ListTasks(c *gin.Context) {
	items, err := s.App.Store.ListTasks()
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if items == nil {
		items = []store.ScheduledTask{}
	}
	c.JSON(200, gin.H{"items": items})
}

type taskReq struct {
	Name     string `json:"name"`
	CronExpr string `json:"cron_expr"`
	Action   string `json:"action"`
	TargetID int64  `json:"target_id"`
	Enabled  *bool  `json:"enabled"`
	Remark   string `json:"remark"`
}

func (s *Server) CreateTask(c *gin.Context) {
	var q taskReq
	if err := c.ShouldBindJSON(&q); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	if _, err := scheduler.NextRun(q.CronExpr); err != nil {
		c.JSON(400, gin.H{"error": "cron 表达式无效（5 段：分 时 日 月 周）"})
		return
	}
	switch q.Action {
	case "forward_enable", "forward_disable", "iplist_expire", "sync":
	default:
		c.JSON(400, gin.H{"error": "动作不合法"})
		return
	}
	if (q.Action == "forward_enable" || q.Action == "forward_disable") && q.TargetID <= 0 {
		c.JSON(400, gin.H{"error": "该动作需要指定转发规则"})
		return
	}
	t := &store.ScheduledTask{
		Name: q.Name, CronExpr: q.CronExpr, Action: q.Action,
		TargetID: q.TargetID, Enabled: true, Remark: q.Remark,
	}
	if q.Enabled != nil {
		t.Enabled = *q.Enabled
	}
	if err := s.App.Store.CreateTask(t); err != nil {
		respErr(c, 500, err)
		return
	}
	_ = s.App.Sched.Reload()
	s.audit(c, "create", "task:"+t.Name, nil, t)
	c.JSON(200, gin.H{"item": t})
}

func (s *Server) UpdateTask(c *gin.Context) {
	id := pathID(c)
	var q taskReq
	if err := c.ShouldBindJSON(&q); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	if _, err := scheduler.NextRun(q.CronExpr); err != nil {
		c.JSON(400, gin.H{"error": "cron 表达式无效"})
		return
	}
	t := &store.ScheduledTask{
		ID: id, Name: q.Name, CronExpr: q.CronExpr, Action: q.Action,
		TargetID: q.TargetID, Enabled: true, Remark: q.Remark,
	}
	if q.Enabled != nil {
		t.Enabled = *q.Enabled
	}
	if err := s.App.Store.UpdateTask(t); err != nil {
		respErr(c, 500, err)
		return
	}
	_ = s.App.Sched.Reload()
	s.audit(c, "update", "task:"+t.Name, nil, t)
	c.JSON(200, gin.H{"item": t})
}

func (s *Server) DeleteTask(c *gin.Context) {
	id := pathID(c)
	if err := s.App.Store.DeleteTask(id); err != nil {
		respErr(c, 500, err)
		return
	}
	_ = s.App.Sched.Reload()
	s.audit(c, "delete", "task:"+strconv.FormatInt(id, 10), nil, nil)
	c.JSON(200, gin.H{"ok": true})
}

/* ------------------------- 告警 ------------------------- */

func (s *Server) ListAlerts(c *gin.Context) {
	items, err := s.App.Store.ListAlerts()
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if items == nil {
		items = []store.AlertConfig{}
	}
	c.JSON(200, gin.H{"items": items})
}

type alertReq struct {
	Channel    string   `json:"channel"`
	Config     any      `json:"config"`
	Events     []string `json:"events"`
	Enabled    bool     `json:"enabled"`
}

func (s *Server) UpsertAlert(c *gin.Context) {
	var q alertReq
	if err := c.ShouldBindJSON(&q); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	switch q.Channel {
	case "telegram", "webhook", "smtp":
	default:
		c.JSON(400, gin.H{"error": "渠道不合法"})
		return
	}
	cfgJSON, _ := json.Marshal(q.Config)
	eventsJSON, _ := json.Marshal(q.Events)
	if len(q.Events) == 0 {
		eventsJSON = []byte(`["apply_fail","drift","danger_pending","health_down","rollback","proxy_cert_error"]`)
	}
	a := &store.AlertConfig{
		Channel: q.Channel, ConfigJSON: string(cfgJSON),
		EventsJSON: string(eventsJSON), Enabled: q.Enabled,
	}
	if err := s.App.Store.UpsertAlert(a); err != nil {
		respErr(c, 500, err)
		return
	}
	s.App.LoadNotify()
	s.audit(c, "update", "alert:"+q.Channel, nil, map[string]any{"enabled": q.Enabled, "events": q.Events})
	c.JSON(200, gin.H{"item": a})
}

func (s *Server) DeleteAlert(c *gin.Context) {
	id := pathID(c)
	if err := s.App.Store.DeleteAlert(id); err != nil {
		respErr(c, 500, err)
		return
	}
	s.App.LoadNotify()
	c.JSON(200, gin.H{"ok": true})
}

// TestAlert 发送一条测试通知（按请求携带的配置，不落库）。
func (s *Server) TestAlert(c *gin.Context) {
	var q alertReq
	if err := c.ShouldBindJSON(&q); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	n := notify.New()
	cfgJSON, _ := json.Marshal(q.Config)
	var errs []error
	switch q.Channel {
	case "telegram":
		var cg notify.TelegramConfig
		if json.Unmarshal(cfgJSON, &cg) != nil {
			c.JSON(400, gin.H{"error": "telegram 配置格式错误"})
			return
		}
		errs = n.SendWith(notify.Event{Type: "test", Message: "FirePanel 测试通知", Time: time.Now()}, &cg, nil, nil)
	case "webhook":
		var cg notify.WebhookConfig
		if json.Unmarshal(cfgJSON, &cg) != nil {
			c.JSON(400, gin.H{"error": "webhook 配置格式错误"})
			return
		}
		errs = n.SendWith(notify.Event{Type: "test", Message: "FirePanel 测试通知", Time: time.Now()}, nil, &cg, nil)
	case "smtp":
		var cg notify.SMTPConfig
		if json.Unmarshal(cfgJSON, &cg) != nil {
			c.JSON(400, gin.H{"error": "smtp 配置格式错误"})
			return
		}
		errs = n.SendWith(notify.Event{Type: "test", Message: "FirePanel 测试通知", Time: time.Now()}, nil, nil, &cg)
	default:
		c.JSON(400, gin.H{"error": "渠道不合法"})
		return
	}
	if len(errs) > 0 {
		c.JSON(502, gin.H{"error": errs[0].Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

/* ------------------------- 辅助 ------------------------- */

func parsePortRangeStr(v string) *struct{ a, b int } {
	if strings.Contains(v, "-") {
		parts := strings.SplitN(v, "-", 2)
		a, e1 := strconv.Atoi(parts[0])
		b, e2 := strconv.Atoi(parts[1])
		if e1 != nil || e2 != nil || a > b {
			return nil
		}
		return &struct{ a, b int }{a, b}
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return nil
	}
	return &struct{ a, b int }{n, n}
}

