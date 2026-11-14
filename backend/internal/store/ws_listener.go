package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// WSListenerConfig 是监听的引擎配置部分（与 webservice.ListenerConfig 的监听字段一致）。
type WSListenerConfig struct {
	ListenIP     string `json:"listen_ip"`
	Port         int    `json:"port"`
	TLS          bool   `json:"tls"`
	MinTLS       string `json:"min_tls"`
	PlainHTTP    string `json:"plain_http"`
	HTTPPort     int    `json:"http_port"`
	HTTPPortMode string `json:"http_port_mode"`
}

// WSListener 一个 Web 服务监听。
type WSListener struct {
	ID        int64            `json:"id"`
	Name      string           `json:"name"`
	Enabled   bool             `json:"enabled"`
	Config    WSListenerConfig `json:"config"`
	Remark    string           `json:"remark"`
	CreatedAt time.Time        `json:"created_at"`
}

const wsListenerCols = `id,name,enabled,config,remark,created_at`

func scanWSListener(scan func(...any) error) (*WSListener, error) {
	var l WSListener
	var enabled int
	var created sql.NullString
	var cfg string
	if err := scan(&l.ID, &l.Name, &enabled, &cfg, &l.Remark, &created); err != nil {
		return nil, err
	}
	l.Enabled = enabled == 1
	_ = json.Unmarshal([]byte(cfg), &l.Config)
	l.CreatedAt = nz(created)
	return &l, nil
}

func (s *Store) ListWSListeners() ([]WSListener, error) {
	rows, err := s.db.Query(`SELECT ` + wsListenerCols + ` FROM ws_listeners ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WSListener
	for rows.Next() {
		l, err := scanWSListener(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

func (s *Store) GetWSListener(id int64) (*WSListener, error) {
	l, err := scanWSListener(s.db.QueryRow(`SELECT `+wsListenerCols+` FROM ws_listeners WHERE id=?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return l, err
}

func (s *Store) CreateWSListener(l *WSListener) error {
	cfg, _ := json.Marshal(l.Config)
	res, err := s.db.Exec(`INSERT INTO ws_listeners(name,enabled,config,remark) VALUES(?,?,?,?)`,
		l.Name, boolInt(l.Enabled), string(cfg), l.Remark)
	if err != nil {
		return err
	}
	l.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateWSListener(l *WSListener) error {
	cfg, _ := json.Marshal(l.Config)
	res, err := s.db.Exec(`UPDATE ws_listeners SET name=?,enabled=?,config=?,remark=? WHERE id=?`,
		l.Name, boolInt(l.Enabled), string(cfg), l.Remark, l.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateWSListenerFields 局部更新（启停开关用）。
func (s *Store) UpdateWSListenerFields(id int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	sets, args := "", []any{}
	for k, expr := range map[string]string{"name": "name=?", "enabled": "enabled=?", "config": "config=?", "remark": "remark=?"} {
		v, ok := fields[k]
		if !ok {
			continue
		}
		if k == "config" {
			if b, err := json.Marshal(v); err == nil {
				v = string(b)
			}
		}
		if sets != "" {
			sets += ","
		}
		sets += expr
		args = append(args, v)
	}
	if sets == "" {
		return nil
	}
	args = append(args, id)
	res, err := s.db.Exec(`UPDATE ws_listeners SET `+sets+` WHERE id=?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteWSListener(id int64) error {
	res, err := s.db.Exec(`DELETE FROM ws_listeners WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	// 站点一并删除（外键级联未启用，手工清理）
	_, err = s.db.Exec(`DELETE FROM ws_sites WHERE listener_id=?`, id)
	return err
}

// EnsureDefaultWSListener 确保至少有一个监听；没有时按旧设置（proxy_http_addr/proxy_https_addr）
// 创建默认监听：HTTPS 主端口 + 额外 HTTP 端口（提供站点，与旧行为一致）。
func (s *Store) EnsureDefaultWSListener(httpAddr, httpsAddr string) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ws_listeners`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	httpsPort := portOfAddr(httpsAddr, 443)
	httpPort := portOfAddr(httpAddr, 80)
	if httpPort == httpsPort {
		httpPort = 0
	}
	l := &WSListener{
		Name: "", Enabled: true, Remark: "默认监听（迁移自旧反代设置）",
		Config: WSListenerConfig{
			Port: httpsPort, TLS: true, MinTLS: "1.2",
			PlainHTTP: "serve", HTTPPort: httpPort, HTTPPortMode: serveOrEmpty(httpsPort > 0 && httpPort > 0),
		},
	}
	if l.Config.HTTPPort > 0 {
		l.Config.HTTPPortMode = "serve"
	}
	return s.CreateWSListener(l)
}

func serveOrEmpty(b bool) string {
	if b {
		return "serve"
	}
	return ""
}

// portOfAddr 从监听地址取端口（":80" / "0.0.0.0:80" / "80"）。
func portOfAddr(addr string, def int) int {
	a := strings.TrimSpace(addr)
	if a == "" {
		return def
	}
	if i := strings.LastIndex(a, ":"); i >= 0 {
		a = a[i+1:]
	}
	n, err := strconv.Atoi(a)
	if err != nil || n < 1 || n > 65535 {
		return def
	}
	return n
}

// AssignSitesToListener 把还没有监听的站点挂到默认监听（迁移用）。
func (s *Store) AssignSitesToListener(listenerID int64) error {
	_, err := s.db.Exec(`UPDATE ws_sites SET listener_id=? WHERE listener_id=0`, listenerID)
	return err
}
