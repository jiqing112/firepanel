package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"firepanel/internal/fw"
	"firepanel/internal/store"
)

/* ------------------------- 入站放行（FPANEL_INPUT ACCEPT） ------------------------- */

// ListPortAllow 列出入站放行规则。
func (s *Server) ListPortAllow(c *gin.Context) {
	items, err := s.App.Store.ListPortAllowRules()
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if items == nil {
		items = []store.PortAllowRule{}
	}
	c.JSON(200, gin.H{"items": items})
}

type portAllowReq struct {
	Name    string `json:"name"`
	Proto   string `json:"proto"`
	Port    string `json:"port"`
	SrcCIDR string `json:"src_cidr"`
	Enabled *bool  `json:"enabled"`
	Remark  string `json:"remark"`
}

// validPortSpec 校验 "80" 或 "8000-8010"。
func validPortSpec(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	parse := func(s string) (int, bool) {
		n, err := strconv.Atoi(s)
		return n, err == nil && n >= 1 && n <= 65535
	}
	if a, b, ok := strings.Cut(v, "-"); ok {
		start, ok1 := parse(a)
		end, ok2 := parse(b)
		return ok1 && ok2 && start <= end
	}
	_, ok := parse(v)
	return ok
}

func portAllowFromReq(c *gin.Context) (*store.PortAllowRule, error) {
	var q portAllowReq
	if err := c.ShouldBindJSON(&q); err != nil {
		return nil, errStr("参数不合法")
	}
	r := &store.PortAllowRule{
		Name:    strings.TrimSpace(q.Name),
		Proto:   strings.TrimSpace(q.Proto),
		Port:    strings.TrimSpace(q.Port),
		SrcCIDR: strings.TrimSpace(q.SrcCIDR),
		Remark:  q.Remark,
	}
	if q.Enabled != nil {
		r.Enabled = *q.Enabled
	} else {
		r.Enabled = true
	}
	if r.Name == "" || len(r.Name) > 100 {
		return nil, errStr("规则名称不能为空且不超过 100 字符")
	}
	switch r.Proto {
	case "tcp", "udp", "both":
	default:
		return nil, errStr("协议必须为 tcp/udp/both")
	}
	if !validPortSpec(r.Port) {
		return nil, errStr("端口格式错误（1-65535 单端口或 start-end 端口段）")
	}
	// 来源留空表示对所有人放行；填了就必须合法，否则拼进内核规则会被拒绝
	if r.SrcCIDR != "" {
		normalized := fw.NormalizeCIDRCheck(r.SrcCIDR)
		if normalized == "" {
			return nil, errStr("来源限制必须是合法 IP 或 CIDR")
		}
		r.SrcCIDR = normalized
	}
	return r, nil
}

func (s *Server) CreatePortAllow(c *gin.Context) {
	r, err := portAllowFromReq(c)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error { return s.App.Store.CreatePortAllowRule(r) })
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "create", "port-allow:"+r.Name, nil, r)
	c.JSON(200, gin.H{"item": r, "sync": res})
}

func (s *Server) UpdatePortAllow(c *gin.Context) {
	id := pathID(c)
	r, err := portAllowFromReq(c)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	r.ID = id
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error { return s.App.Store.UpdatePortAllowRule(r) })
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "update", "port-allow:"+r.Name, nil, r)
	c.JSON(200, gin.H{"item": r, "sync": res})
}

// TogglePortAllow 只切启停。
func (s *Server) TogglePortAllow(c *gin.Context) {
	id := pathID(c)
	var q struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&q); err != nil || q.Enabled == nil {
		c.JSON(400, gin.H{"error": "缺少 enabled 字段"})
		return
	}
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error {
		return s.App.Store.SetPortAllowEnabled(id, *q.Enabled)
	})
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "toggle", "port-allow", nil, map[string]any{"id": id, "enabled": *q.Enabled})
	c.JSON(200, gin.H{"sync": res})
}

func (s *Server) DeletePortAllow(c *gin.Context) {
	id := pathID(c)
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error { return s.App.Store.DeletePortAllowRule(id) })
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "delete", "port-allow", nil, map[string]any{"id": id})
	c.JSON(200, gin.H{"sync": res})
}
