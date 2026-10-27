package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"firepanel/internal/fw"
	"firepanel/internal/scheduler"
	"firepanel/internal/store"
)

// validPathMatch 校验反代路径匹配：必须以 / 开头，且不能含控制字符或
// Caddyfile 结构性字符——否则一个 path_match 就能闭合站点块、注入任意指令。
func validPathMatch(p string) bool {
	if p == "" || len(p) > 200 || !strings.HasPrefix(p, "/") {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return false
		}
		if strings.ContainsRune("{}#\"'\\", r) {
			return false
		}
	}
	return true
}

// backupBundle 组装导出包：全量结构化配置 + 版本/后端信息。
func (s *Server) backupBundle() (store.Bundle, error) {
	b, err := s.App.Store.ExportBundle()
	if err != nil {
		return b, err
	}
	b.Backend = s.App.Backend.Name()
	return b, nil
}

// validateBundle 在写入前做全量校验。
// 恢复是「先清空再写入」，任何一条非法数据都必须在清空之前被拦下。
func validateBundle(b *store.Bundle) error {
	for i := range b.Forwards {
		f := &b.Forwards[i]
		if err := fw.ValidateForward(f.Name, f.Protocol, f.ListenPort, f.SrcIP, f.DstIP, f.DstDomain, f.DstPort); err != nil {
			return fmt.Errorf("转发规则 #%d（%s）: %w", i+1, f.Name, err)
		}
	}
	for i := range b.IPLists {
		e := &b.IPLists[i]
		if e.ListType != "black" && e.ListType != "white" {
			return fmt.Errorf("名单 #%d: 类型必须为 black 或 white", i+1)
		}
		if err := fw.ValidateIPOrCIDR(e.IPOrCIDR); err != nil {
			return fmt.Errorf("名单 #%d（%s）: %w", i+1, e.IPOrCIDR, err)
		}
		// 备份里可能是旧版未规范化的写法：恢复时统一成 1.2.3.4 / 10.0.0.0/8
		e.IPOrCIDR = fw.CanonicalIPOrCIDR(e.IPOrCIDR)
	}
	for i := range b.RateLimits {
		if err := validateRateLimit(&b.RateLimits[i]); err != nil {
			return fmt.Errorf("限速规则 #%d（%s）: %w", i+1, b.RateLimits[i].Name, err)
		}
	}
	for i := range b.NATRules {
		if err := validateNAT(&b.NATRules[i]); err != nil {
			return fmt.Errorf("NAT 规则 #%d（%s）: %w", i+1, b.NATRules[i].Name, err)
		}
	}
	for i := range b.Tasks {
		t := &b.Tasks[i]
		if _, err := scheduler.NextRun(t.CronExpr); err != nil {
			return fmt.Errorf("定时任务 #%d（%s）: cron 表达式无效", i+1, t.Name)
		}
		switch t.Action {
		case "forward_enable", "forward_disable", "iplist_expire", "sync":
		default:
			return fmt.Errorf("定时任务 #%d（%s）: 动作不合法", i+1, t.Name)
		}
		if (t.Action == "forward_enable" || t.Action == "forward_disable") && t.TargetID <= 0 {
			return fmt.Errorf("定时任务 #%d（%s）: 需要指定转发规则", i+1, t.Name)
		}
	}
	for i := range b.Alerts {
		a := &b.Alerts[i]
		switch a.Channel {
		case "telegram", "webhook", "smtp":
		default:
			return fmt.Errorf("告警渠道 #%d: 不支持的渠道 %q", i+1, a.Channel)
		}
		if a.ConfigJSON != "" && !json.Valid([]byte(a.ConfigJSON)) {
			return fmt.Errorf("告警渠道 #%d（%s）: 配置不是合法 JSON", i+1, a.Channel)
		}
		if a.EventsJSON != "" && !json.Valid([]byte(a.EventsJSON)) {
			return fmt.Errorf("告警渠道 #%d（%s）: 事件列表不是合法 JSON", i+1, a.Channel)
		}
	}
	for i := range b.Jails {
		j := &b.Jails[i]
		switch j.SourceType {
		case "file", "journald", "audit":
		default:
			return fmt.Errorf("防爆破 #%d（%s）: 日志来源不合法", i+1, j.Name)
		}
		if j.Threshold <= 0 || j.FindTime <= 0 || j.BanTime <= 0 {
			return fmt.Errorf("防爆破 #%d（%s）: 阈值/时间必须大于 0", i+1, j.Name)
		}
		if j.MatchRegex != "" {
			if _, err := regexp.Compile(j.MatchRegex); err != nil {
				return fmt.Errorf("防爆破 #%d（%s）: 正则无效: %w", i+1, j.Name, err)
			}
		}
	}
	for i := range b.PortRecords {
		p := &b.PortRecords[i]
		if p.Proto != "tcp" && p.Proto != "udp" && p.Proto != "both" {
			return fmt.Errorf("端口记录 #%d（%s）: 协议不合法", i+1, p.Name)
		}
		if p.Port < 1 || p.Port > 65535 {
			return fmt.Errorf("端口记录 #%d（%s）: 端口超出 1-65535", i+1, p.Name)
		}
	}

	domains := map[int64]bool{}
	domainNames := map[string]bool{}
	for i := range b.RPDomains {
		d := &b.RPDomains[i]
		if !validHost(d.Domain) {
			return fmt.Errorf("反代域名 #%d: %q 不是合法域名", i+1, d.Domain)
		}
		if domainNames[strings.ToLower(d.Domain)] {
			return fmt.Errorf("反代域名 #%d: 域名 %s 重复", i+1, d.Domain)
		}
		domainNames[strings.ToLower(d.Domain)] = true
		switch d.TLSMode {
		case "auto", "manual", "none":
		default:
			return fmt.Errorf("反代域名 #%d（%s）: tls_mode 不合法", i+1, d.Domain)
		}
		switch d.Challenge {
		case "", "http", "alpn", "dns":
		default:
			return fmt.Errorf("反代域名 #%d（%s）: challenge 不合法", i+1, d.Domain)
		}
		if d.CaddyOpts != "" && !json.Valid([]byte(d.CaddyOpts)) {
			return fmt.Errorf("反代域名 #%d（%s）: caddy_opts 不是合法 JSON", i+1, d.Domain)
		}
		domains[d.ID] = true
	}
	for i := range b.RPRoutes {
		r := &b.RPRoutes[i]
		if r.DomainID <= 0 || !domains[r.DomainID] {
			return fmt.Errorf("反代路由 #%d: 所属域名不存在（domain_id=%d）", i+1, r.DomainID)
		}
		if !validPathMatch(r.PathMatch) {
			return fmt.Errorf("反代路由 #%d: 路径 %q 不合法", i+1, r.PathMatch)
		}
		if !validUpstream(strings.TrimSpace(r.UpstreamURL)) {
			return fmt.Errorf("反代路由 #%d: 上游地址必须为 http(s)://host[:port]", i+1)
		}
		if hp := strings.TrimSpace(r.HealthPath); hp != "" && !strings.HasPrefix(hp, "/") {
			return fmt.Errorf("反代路由 #%d: 健康检查路径必须以 / 开头", i+1)
		}
	}
	return nil
}

// Restore 从 JSON 备份恢复全量结构化配置。
// 顺序很关键：先解析 + 全量校验，再在单事务里替换，最后同步内核；
// 任一步失败，App.Mutate 都会把数据库恢复到操作前的快照。
func (s *Server) RestoreBundle(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 16<<20))
	if err != nil {
		c.JSON(400, gin.H{"error": "读取请求体失败"})
		return
	}
	var b store.Bundle
	if err := json.Unmarshal(body, &b); err != nil {
		c.JSON(400, gin.H{"error": "备份文件格式错误"})
		return
	}
	if err := validateBundle(&b); err != nil {
		c.JSON(400, gin.H{"error": "备份文件校验失败: " + err.Error()})
		return
	}
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error {
		return s.App.Store.ReplaceAll(b)
	})
	if err != nil {
		respErr(c, 500, err)
		return
	}
	// 数据库已整体替换，但反代/定时任务/告警/防爆破的内存态不会自动更新；
	// 不重载的话「恢复成功」的配置要等重启才生效（甚至永不生效）。
	s.App.ReloadAuxAsync()
	s.audit(c, "restore", "system", nil, map[string]any{
		"forwards": len(b.Forwards), "ip_lists": len(b.IPLists),
		"rate_limits": len(b.RateLimits), "nat_rules": len(b.NATRules),
		"tasks": len(b.Tasks), "alerts": len(b.Alerts), "jails": len(b.Jails),
		"port_records": len(b.PortRecords), "rp_domains": len(b.RPDomains), "rp_routes": len(b.RPRoutes),
	})
	c.JSON(200, gin.H{
		"sync": res, "forwards": len(b.Forwards), "ip_lists": len(b.IPLists),
		"rate_limits": len(b.RateLimits), "nat_rules": len(b.NATRules),
		"tasks": len(b.Tasks), "alerts": len(b.Alerts), "jails": len(b.Jails),
		"port_records": len(b.PortRecords), "rp_domains": len(b.RPDomains), "rp_routes": len(b.RPRoutes),
	})
}
