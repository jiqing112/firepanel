package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"firepanel/internal/store"
	"firepanel/internal/sysinfo"
)

/* ------------------------- 监听端口扫描 / 端口记录台账 ------------------------- */

// ListListeningPorts 扫描本机监听端口（优先 ss；无 iproute2 时回退 netstat，兼容 Alpine 最小安装）。
func (s *Server) ListListeningPorts(c *gin.Context) {
	items := []sysinfo.ListenPort{}
	out, err := s.App.Exec.Run(c.Request.Context(), "ss", "-H", "-tlnp")
	if err == nil {
		items = append(items, sysinfo.ParseSSOutput(out, "tcp")...)
	} else if nb, nerr := s.App.Exec.Run(c.Request.Context(), "netstat", "-tlnp"); nerr == nil {
		items = append(items, sysinfo.ParseNetstatListening(nb, "tcp")...)
	} else {
		respErr(c, 500, errStr("扫描失败：需要 ss（iproute2）或 netstat 命令（"+err.Error()+"）"))
		return
	}
	if out, err = s.App.Exec.Run(c.Request.Context(), "ss", "-H", "-ulnp"); err == nil {
		items = append(items, sysinfo.ParseSSOutput(out, "udp")...)
	} else if nb, nerr := s.App.Exec.Run(c.Request.Context(), "netstat", "-ulnp"); nerr == nil {
		items = append(items, sysinfo.ParseNetstatListening(nb, "udp")...)
	}
	c.JSON(200, gin.H{"items": items})
}

func validatePortRecord(r *store.PortRecord) error {
	if strings.TrimSpace(r.Name) == "" {
		return errStr("名称不能为空")
	}
	switch r.Proto {
	case "tcp", "udp", "both":
	default:
		return errStr("协议必须为 tcp/udp/both")
	}
	if r.Port < 1 || r.Port > 65535 {
		return errStr("端口必须在 1-65535 之间")
	}
	return nil
}

type portRecordReq struct {
	Name     string `json:"name"`
	Proto    string `json:"proto"`
	Port     int    `json:"port"`
	GroupTag string `json:"group_tag"`
	Remark   string `json:"remark"`
}

func portRecordFromReq(c *gin.Context) (*store.PortRecord, error) {
	var q portRecordReq
	if err := c.ShouldBindJSON(&q); err != nil {
		return nil, errStr("参数不合法")
	}
	r := &store.PortRecord{
		Name: strings.TrimSpace(q.Name), Proto: q.Proto, Port: q.Port,
		GroupTag: strings.TrimSpace(q.GroupTag), Remark: q.Remark,
	}
	return r, validatePortRecord(r)
}

func (s *Server) ListPortRecords(c *gin.Context) {
	items, err := s.App.Store.ListPortRecords()
	if err != nil {
		respErr(c, 500, err)
		return
	}
	if items == nil {
		items = []store.PortRecord{}
	}
	c.JSON(200, gin.H{"items": items})
}

func (s *Server) CreatePortRecord(c *gin.Context) {
	r, err := portRecordFromReq(c)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := s.App.Store.CreatePortRecord(r); err != nil {
		respErr(c, 500, err)
		return
	}
	s.audit(c, "create", "port_record:"+r.Name+"("+strconv.Itoa(r.Port)+"/"+r.Proto+")", nil, r)
	c.JSON(200, gin.H{"item": r})
}

func (s *Server) UpdatePortRecord(c *gin.Context) {
	id := pathID(c)
	r, err := portRecordFromReq(c)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	r.ID = id
	if err := s.App.Store.UpdatePortRecord(r); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "记录不存在"})
		} else {
			respErr(c, 500, err)
		}
		return
	}
	s.audit(c, "update", "port_record:"+r.Name, nil, r)
	c.JSON(200, gin.H{"item": r})
}

func (s *Server) DeletePortRecord(c *gin.Context) {
	id := pathID(c)
	if err := s.App.Store.DeletePortRecord(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "记录不存在"})
		return
	}
	s.audit(c, "delete", "port_record:"+strconv.FormatInt(id, 10), nil, nil)
	c.JSON(200, gin.H{"ok": true})
}
