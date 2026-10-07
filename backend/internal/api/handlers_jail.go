package api

import (
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"firepanel/internal/jail"
	"firepanel/internal/store"
)

/* ------------------------- 防爆破 Jail ------------------------- */

func validateJail(j *store.JailConfig) error {
	if strings.TrimSpace(j.Name) == "" || len(j.Name) > 50 {
		return errStr("名称不能为空且不超过 50 字符")
	}
	switch j.SourceType {
	case "file":
		if strings.TrimSpace(j.LogPath) == "" {
			return errStr("日志文件源需要提供文件路径")
		}
		if _, err := jail.CompilePatternCheck(j.MatchRegex); err != nil {
			return errStr("匹配正则无效: " + err.Error())
		}
	case "journald":
		// 引擎依赖正则提取 IP，空正则会在启动时报"正则无效"，提前拦截
		if _, err := jail.CompilePatternCheck(j.MatchRegex); err != nil {
			return errStr("匹配正则无效: " + err.Error())
		}
	case "audit":
		// 审计源不需要正则（直接取登录失败条目的来源 IP）
	default:
		return errStr("来源类型必须为 file/journald/audit")
	}
	if j.Threshold < 1 || j.Threshold > 10000 {
		return errStr("阈值必须为 1-10000")
	}
	if j.FindTime < 5 || j.FindTime > 86400*7 {
		return errStr("统计窗口必须为 5 秒 - 7 天")
	}
	if j.BanTime < 5 || j.BanTime > 86400*365 {
		return errStr("封禁时长必须为 5 秒 - 1 年")
	}
	for _, cidr := range strings.Split(j.IgnoreCIDRs, ",") {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return errStr("忽略网段包含非法 CIDR: " + cidr)
		}
	}
	return nil
}

func (s *Server) ListJails(c *gin.Context) {
	items, err := s.App.Store.ListJails()
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if items == nil {
		items = []store.JailConfig{}
	}
	type jailView struct {
		store.JailConfig
		Hits map[string]int `json:"hits"`
	}
	views := []jailView{}
	for _, j := range items {
		views = append(views, jailView{JailConfig: j, Hits: s.App.Jail.HitCount(j.ID)})
	}
	// 当前封禁列表（来源 jail:*）
	all, _ := s.App.Store.ListIPLists("black")
	now := time.Now()
	banned := []store.IPListEntry{}
	for _, e := range all {
		if strings.HasPrefix(e.Source, "jail:") && e.ExpiresAt != nil && e.ExpiresAt.After(now) {
			banned = append(banned, e)
		}
	}
	c.JSON(200, gin.H{"items": views, "banned": banned})
}

type jailReq struct {
	Name        string `json:"name"`
	SourceType  string `json:"source_type"`
	LogPath     string `json:"log_path"`
	Unit        string `json:"unit"`
	MatchRegex  string `json:"match_regex"`
	Threshold   int    `json:"threshold"`
	FindTime    int    `json:"find_time"`
	BanTime     int    `json:"ban_time"`
	IgnoreCIDRs string `json:"ignore_cidrs"`
	Enabled     *bool  `json:"enabled"`
	Remark      string `json:"remark"`
}

func jailFromReq(c *gin.Context) (*store.JailConfig, error) {
	var q jailReq
	if err := c.ShouldBindJSON(&q); err != nil {
		return nil, errStr("参数不合法")
	}
	j := &store.JailConfig{
		Name: q.Name, SourceType: q.SourceType,
		LogPath: strings.TrimSpace(q.LogPath), Unit: strings.TrimSpace(q.Unit),
		MatchRegex: strings.TrimSpace(q.MatchRegex),
		Threshold:  q.Threshold, FindTime: q.FindTime, BanTime: q.BanTime,
		IgnoreCIDRs: strings.TrimSpace(q.IgnoreCIDRs), Remark: q.Remark,
	}
	if j.IgnoreCIDRs == "" {
		j.IgnoreCIDRs = "192.168.0.0/16,10.0.0.0/8,172.16.0.0/12,127.0.0.0/8"
	}
	if j.Threshold == 0 {
		j.Threshold = 5
	}
	if j.FindTime == 0 {
		j.FindTime = 600
	}
	if j.BanTime == 0 {
		j.BanTime = 1800
	}
	if q.Enabled != nil {
		j.Enabled = *q.Enabled
	} else {
		j.Enabled = true
	}
	return j, validateJail(j)
}

func (s *Server) CreateJail(c *gin.Context) {
	j, err := jailFromReq(c)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := s.App.Store.CreateJail(j); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			c.JSON(409, gin.H{"error": "同名 Jail 已存在"})
			return
		}
		respErr(c, 500, err)
		return
	}
	s.App.Jail.Reload()
	s.audit(c, "create", "jail:"+j.Name, nil, j)
	c.JSON(200, gin.H{"item": j})
}

func (s *Server) UpdateJail(c *gin.Context) {
	id := pathID(c)
	j, err := jailFromReq(c)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	j.ID = id
	if err := s.App.Store.UpdateJail(j); err != nil {
		respErr(c, 500, err)
		return
	}
	s.App.Jail.Reload()
	s.audit(c, "update", "jail:"+j.Name, nil, j)
	c.JSON(200, gin.H{"item": j})
}

func (s *Server) DeleteJail(c *gin.Context) {
	id := pathID(c)
	if err := s.App.Store.DeleteJail(id); err != nil {
		respErr(c, 500, err)
		return
	}
	s.App.Jail.Reload()
	s.audit(c, "delete", "jail:"+strconv.FormatInt(id, 10), nil, nil)
	c.JSON(200, gin.H{"ok": true})
}

// UnbanJailIP 手动解除封禁（删除 jail 来源的黑名单条目并同步）。
func (s *Server) UnbanJailIP(c *gin.Context) {
	id := pathID(c)
	items, err := s.App.Store.ListIPLists("black")
	if err != nil {
		respErr(c, 500, err)
		return
	}
	for _, e := range items {
		if e.ID == id && strings.HasPrefix(e.Source, "jail:") {
			if s.App.HasPendingDanger() {
				c.JSON(409, gin.H{"error": "存在待确认的高危变更，请先确认或撤销"})
				return
			}
			if _, err := s.App.Mutate(c.ClientIP(), func() error {
				return s.App.Store.DeleteIPList(e.ID)
			}); err != nil {
				respErr(c, 500, err)
				return
			}
			s.audit(c, "unban", "iplist:"+e.IPOrCIDR, e, nil)
			c.JSON(200, gin.H{"ok": true})
			return
		}
	}
	c.JSON(404, gin.H{"error": "封禁条目不存在"})
}
