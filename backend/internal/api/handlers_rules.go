package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"firepanel/internal/fw"
	"firepanel/internal/store"
)

func pathID(c *gin.Context) int64 {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	return id
}

func fmtUser(id int64) string { return "user:" + strconv.FormatInt(id, 10) }

// parseTimeLoose 兼容 RFC3339 与 "2006-01-02 15:04" 两种格式。
func parseTimeLoose(v string) (*time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return &t, nil
		}
	}
	return nil, errStr("时间格式错误")
}

func errStr(s string) error { return strErr(s) }

type strErr string

func (e strErr) Error() string { return string(e) }

/* ------------------------- 端口转发 ------------------------- */

func (s *Server) ListForwards(c *gin.Context) {
	items, err := s.App.Store.ListForwards()
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if items == nil {
		items = []store.ForwardRule{}
	}
	c.JSON(200, gin.H{"items": items})
}

type forwardReq struct {
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	ListenPort string `json:"listen_port"`
	SrcIP      string `json:"src_ip"`
	DstIP      string `json:"dst_ip"`
	DstDomain  string `json:"dst_domain"`
	DstPort    string `json:"dst_port"`
	Enabled    *bool  `json:"enabled"`
	ExpiresAt  string `json:"expires_at"`
	Remark     string `json:"remark"`
}

func (s *Server) forwardFromReq(c *gin.Context) (*store.ForwardRule, error) {
	var req forwardReq
	if err := c.ShouldBindJSON(&req); err != nil {
		return nil, errStr("参数不完整或格式错误")
	}
	if err := fw.ValidateForward(req.Name, req.Protocol, req.ListenPort, req.SrcIP, req.DstIP, req.DstDomain, req.DstPort); err != nil {
		return nil, err
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	r := &store.ForwardRule{
		Name: req.Name, Protocol: req.Protocol, ListenPort: req.ListenPort,
		SrcIP: req.SrcIP, DstIP: req.DstIP, DstDomain: req.DstDomain, DstPort: req.DstPort,
		Enabled: enabled, Remark: req.Remark,
	}
	if req.ExpiresAt != "" {
		t, err := parseTimeLoose(req.ExpiresAt)
		if err != nil {
			return nil, err
		}
		r.ExpiresAt = t
	}
	return r, nil
}

// 冲突检测：同监听端口 + 协议重叠
func (s *Server) forwardConflict(r *store.ForwardRule, excludeID int64) *store.ForwardRule {
	existing, _ := s.App.Store.ListForwards()
	for _, e := range existing {
		if e.ID == excludeID {
			continue
		}
		if e.ListenPort == r.ListenPort && protocolsOverlap(e.Protocol, r.Protocol) {
			return &e
		}
	}
	return nil
}

func (s *Server) CreateForward(c *gin.Context) {
	r, err := s.forwardFromReq(c)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if other := s.forwardConflict(r, 0); other != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "监听端口与现有规则冲突：「" + other.Name + "」"})
		return
	}
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error {
		return s.App.Store.CreateForward(r)
	})
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "create", "forward:"+strconv.FormatInt(r.ID, 10), nil, r)
	c.JSON(200, gin.H{"item": r, "sync": res})
}

func (s *Server) UpdateForward(c *gin.Context) {
	id := pathID(c)
	before, err := s.App.Store.GetForward(id)
	if err == store.ErrNotFound {
		c.JSON(404, gin.H{"error": "规则不存在"})
		return
	}
	if err != nil {
		respErr(c, 500, err)
		return
	}
	r, err := s.forwardFromReq(c)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if other := s.forwardConflict(r, id); other != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "监听端口与现有规则冲突：「" + other.Name + "」"})
		return
	}
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	r.ID = id
	r.CreatedAt = before.CreatedAt
	r.UpdatedAt = before.UpdatedAt
	res, err := s.App.Mutate(c.ClientIP(), func() error {
		return s.App.Store.UpdateForward(r)
	})
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "update", "forward:"+strconv.FormatInt(id, 10), before, r)
	c.JSON(200, gin.H{"item": r, "sync": res})
}

func (s *Server) ToggleForward(c *gin.Context) {
	id := pathID(c)
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	before, err := s.App.Store.GetForward(id)
	if err == store.ErrNotFound {
		c.JSON(404, gin.H{"error": "规则不存在"})
		return
	}
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error {
		return s.App.Store.SetForwardEnabled(id, req.Enabled)
	})
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "toggle", "forward:"+strconv.FormatInt(id, 10),
		map[string]any{"enabled": before.Enabled}, map[string]any{"enabled": req.Enabled})
	c.JSON(200, gin.H{"ok": true, "sync": res})
}

func (s *Server) DeleteForward(c *gin.Context) {
	id := pathID(c)
	before, err := s.App.Store.GetForward(id)
	if err == store.ErrNotFound {
		c.JSON(404, gin.H{"error": "规则不存在"})
		return
	}
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error {
		return s.App.Store.DeleteForward(id)
	})
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "delete", "forward:"+strconv.FormatInt(id, 10), before, nil)
	c.JSON(200, gin.H{"ok": true, "sync": res})
}

func protocolsOverlap(a, b string) bool {
	set := func(p string) map[string]bool {
		switch p {
		case "tcp", "udp":
			return map[string]bool{p: true}
		default:
			return map[string]bool{"tcp": true, "udp": true}
		}
	}
	for k := range set(a) {
		if set(b)[k] {
			return true
		}
	}
	return false
}

/* ------------------------- IP 名单 ------------------------- */

func (s *Server) ListIPLists(c *gin.Context) {
	listType := c.Query("type")
	items, err := s.App.Store.ListIPLists(listType)
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if items == nil {
		items = []store.IPListEntry{}
	}
	c.JSON(200, gin.H{"items": items})
}

type ipListReq struct {
	ListType  string `json:"list_type"`
	IPOrCIDR  string `json:"ip_or_cidr"`
	GroupTag  string `json:"group_tag"`
	Country   string `json:"country"`
	ExpiresAt string `json:"expires_at"`
	Remark    string `json:"remark"`
}

func (s *Server) CreateIPList(c *gin.Context) {
	var req ipListReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	if req.ListType != "black" && req.ListType != "white" {
		c.JSON(400, gin.H{"error": "list_type 必须为 black 或 white"})
		return
	}
	if err := fw.ValidateIPOrCIDR(req.IPOrCIDR); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	e := &store.IPListEntry{
		ListType: req.ListType, IPOrCIDR: req.IPOrCIDR, GroupTag: req.GroupTag,
		Source: "manual", Country: req.Country, Remark: req.Remark,
	}
	if req.ExpiresAt != "" {
		t, err := parseTimeLoose(req.ExpiresAt)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		e.ExpiresAt = t
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error {
		return s.App.Store.CreateIPList(e)
	})
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "create", "iplist:"+e.ListType+":"+e.IPOrCIDR, nil, e)
	c.JSON(200, gin.H{"item": e, "sync": res})
}

func (s *Server) UpdateIPList(c *gin.Context) {
	id := pathID(c)
	items, err := s.App.Store.ListIPLists("")
	if err != nil {
		respErr(c, 500, err)
		return
	}
	var before *store.IPListEntry
	for i := range items {
		if items[i].ID == id {
			before = &items[i]
		}
	}
	if before == nil {
		c.JSON(404, gin.H{"error": "条目不存在"})
		return
	}
	var req ipListReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	before.GroupTag = req.GroupTag
	before.Country = req.Country
	before.Remark = req.Remark
	if req.ExpiresAt != "" {
		if t, err := parseTimeLoose(req.ExpiresAt); err == nil {
			before.ExpiresAt = t
		}
	}
	if err := s.App.Store.UpdateIPList(before); err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "update", "iplist:"+strconv.FormatInt(id, 10), nil, before)
	c.JSON(200, gin.H{"item": before})
}

func (s *Server) DeleteIPList(c *gin.Context) {
	id := pathID(c)
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	res, err := s.App.Mutate(c.ClientIP(), func() error {
		return s.App.Store.DeleteIPList(id)
	})
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "delete", "iplist:"+strconv.FormatInt(id, 10), nil, nil)
	c.JSON(200, gin.H{"ok": true, "sync": res})
}

// ImportIPLists 批量导入：text（每行一个，# 注释）或 json 数组。
func (s *Server) ImportIPLists(c *gin.Context) {
	var req struct {
		ListType string `json:"list_type"`
		Format   string `json:"format"` // text|json
		Content  string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "参数不合法"})
		return
	}
	if req.ListType != "black" && req.ListType != "white" {
		c.JSON(400, gin.H{"error": "list_type 必须为 black 或 white"})
		return
	}
	var entries []string
	if req.Format == "json" {
		var arr []string
		if err := json.Unmarshal([]byte(req.Content), &arr); err != nil {
			c.JSON(400, gin.H{"error": "JSON 格式错误"})
			return
		}
		entries = arr
	} else {
		entries = strings.Split(req.Content, "\n")
	}
	if s.App.HasPendingDanger() {
		c.JSON(http.StatusConflict, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
		return
	}
	imported, skipped := 0, 0
	res, err := s.App.Mutate(c.ClientIP(), func() error {
		for _, line := range entries {
			line = strings.TrimSpace(line)
			if i := strings.Index(line, "#"); i >= 0 {
				line = strings.TrimSpace(line[:i])
			}
			if line == "" {
				continue
			}
			if fw.ValidateIPOrCIDR(line) != nil {
				skipped++
				continue
			}
			e := &store.IPListEntry{ListType: req.ListType, IPOrCIDR: line, Source: "import"}
			if err := s.App.Store.CreateIPList(e); err != nil {
				skipped++
				continue
			}
			imported++
		}
		return nil
	})
	if err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "import", "iplist:"+req.ListType, nil, map[string]any{"imported": imported, "skipped": skipped})
	c.JSON(200, gin.H{"imported": imported, "skipped": skipped, "sync": res})
}

func (s *Server) ExportIPLists(c *gin.Context) {
	listType := c.Query("type")
	format := c.Query("format")
	if format == "" {
		format = "text"
	}
	items, err := s.App.Store.ListIPLists(listType)
	if err != nil {
		respErr(c, 500, err)
		return
	}
	switch format {
	case "json":
		c.JSON(200, gin.H{"items": items})
	case "csv":
		c.Header("Content-Disposition", "attachment; filename=firepanel-ip-lists.csv")
		var sb strings.Builder
		sb.WriteString("id,list_type,ip_or_cidr,group_tag,source,expires_at\n")
		for _, e := range items {
			exp := ""
			if e.ExpiresAt != nil {
				exp = e.ExpiresAt.Format("2006-01-02 15:04")
			}
			sb.WriteString(strconv.FormatInt(e.ID, 10) + "," + e.ListType + "," + e.IPOrCIDR + "," +
				e.GroupTag + "," + e.Source + "," + exp + "\n")
		}
		c.Data(http.StatusOK, "text/csv; charset=utf-8", []byte(sb.String()))
	default:
		c.Header("Content-Disposition", "attachment; filename=firepanel-ip-lists.txt")
		var sb strings.Builder
		for _, e := range items {
			sb.WriteString(e.IPOrCIDR + "\n")
		}
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(sb.String()))
	}
}
