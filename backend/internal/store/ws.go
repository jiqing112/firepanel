package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

/* ------------------------- Web 服务站点 ------------------------- */

// WSSite 一个 Web 服务站点。引擎配置序列化在 Config（webservice.Site 的子集：
// targets / locations / health / rate_limit / headers 等），证书与启停为独立列。
type WSSite struct {
	ID            int64           `json:"id"`
	ListenerID    int64           `json:"listener_id"`
	Name          string          `json:"name"`
	Domains       []string        `json:"domains"`
	Type          string          `json:"type"`
	Enabled       bool            `json:"enabled"`
	Default       bool            `json:"default"`
	Config        json.RawMessage `json:"config"`
	TLSMode       string          `json:"tls_mode"`
	Challenge     string          `json:"challenge"`
	CertPEM       string          `json:"-"`
	KeyPEM        string          `json:"-"`
	CertStatus    string          `json:"cert_status"`
	CertError     string          `json:"cert_error"`
	CertExpiresAt *time.Time      `json:"cert_expires_at"`
	Remark        string          `json:"remark"`
	CreatedAt     time.Time       `json:"created_at"`
}

const wsSiteCols = `id,listener_id,name,domains,type,enabled,is_default,config,tls_mode,challenge,cert_pem,key_pem,cert_status,cert_error,cert_expires_at,remark,created_at`

func scanWSSite(scan func(...any) error) (*WSSite, error) {
	var s WSSite
	var enabled, deflt int
	var created, expires sql.NullString
	var domains, cfg string
	if err := scan(&s.ID, &s.ListenerID, &s.Name, &domains, &s.Type, &enabled, &deflt, &cfg, &s.TLSMode, &s.Challenge, &s.CertPEM, &s.KeyPEM, &s.CertStatus, &s.CertError, &expires, &s.Remark, &created); err != nil {
		return nil, err
	}
	s.Enabled = enabled == 1
	s.Default = deflt == 1
	_ = json.Unmarshal([]byte(domains), &s.Domains)
	s.Config = json.RawMessage(cfg)
	s.CertExpiresAt = nullTime(expires)
	s.CreatedAt = nz(created)
	return &s, nil
}

func (s *Store) ListWSSites() ([]WSSite, error) {
	rows, err := s.db.Query(`SELECT ` + wsSiteCols + ` FROM ws_sites ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WSSite
	for rows.Next() {
		d, err := scanWSSite(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Store) GetWSSite(id int64) (*WSSite, error) {
	d, err := scanWSSite(s.db.QueryRow(`SELECT `+wsSiteCols+` FROM ws_sites WHERE id=?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (s *Store) CreateWSSite(d *WSSite) error {
	if d.Config == nil {
		d.Config = json.RawMessage(`{}`)
	}
	domains, _ := json.Marshal(d.Domains)
	res, err := s.db.Exec(`INSERT INTO ws_sites(listener_id,name,domains,type,enabled,is_default,config,tls_mode,challenge,cert_pem,key_pem,cert_status,cert_error,cert_expires_at,remark)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ListenerID, d.Name, string(domains), d.Type, boolInt(d.Enabled), boolInt(d.Default), string(d.Config),
		d.TLSMode, d.Challenge, d.CertPEM, d.KeyPEM, d.CertStatus, d.CertError, timePtr(d.CertExpiresAt), d.Remark)
	if err != nil {
		return err
	}
	d.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateWSSite(d *WSSite) error {
	if d.Config == nil {
		d.Config = json.RawMessage(`{}`)
	}
	domains, _ := json.Marshal(d.Domains)
	res, err := s.db.Exec(`UPDATE ws_sites SET listener_id=?,name=?,domains=?,type=?,enabled=?,is_default=?,config=?,tls_mode=?,challenge=?,cert_pem=?,key_pem=?,cert_status=?,cert_error=?,cert_expires_at=?,remark=? WHERE id=?`,
		d.ListenerID, d.Name, string(domains), d.Type, boolInt(d.Enabled), boolInt(d.Default), string(d.Config),
		d.TLSMode, d.Challenge, d.CertPEM, d.KeyPEM, d.CertStatus, d.CertError, timePtr(d.CertExpiresAt), d.Remark, d.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateWSSiteFields 局部更新：只写传入的列（编辑弹窗按节保存用）。
func (s *Store) UpdateWSSiteFields(id int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	sets, args := "", []any{}
	allowed := map[string]string{
		"listener_id": "listener_id=?", "name": "name=?", "domains": "domains=?", "type": "type=?", "enabled": "enabled=?",
		"is_default": "is_default=?", "config": "config=?", "tls_mode": "tls_mode=?",
		"challenge": "challenge=?", "cert_pem": "cert_pem=?", "key_pem": "key_pem=?",
		"cert_status": "cert_status=?", "cert_error": "cert_error=?",
		"cert_expires_at": "cert_expires_at=?", "remark": "remark=?",
	}
	for k, expr := range allowed {
		v, ok := fields[k]
		if !ok {
			continue
		}
		if b, err := json.Marshal(v); err == nil && (k == "domains" || k == "config") {
			v = string(b)
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
	res, err := s.db.Exec(`UPDATE ws_sites SET `+sets+` WHERE id=?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetWSSiteCert 回写证书状态（签发成功/失败、到期日巡检）。
func (s *Store) SetWSSiteCert(id int64, status, errMsg string, expires *time.Time) error {
	_, err := s.db.Exec(`UPDATE ws_sites SET cert_status=?,cert_error=?,cert_expires_at=? WHERE id=?`,
		status, errMsg, timePtr(expires), id)
	return err
}

// UnsetDefaultWSSite 清除其他站点的默认标记（默认站点至多一个）。
func (s *Store) UnsetDefaultWSSite(exceptID int64) error {
	_, err := s.db.Exec(`UPDATE ws_sites SET is_default=0 WHERE is_default=1 AND id<>?`, exceptID)
	return err
}

func (s *Store) DeleteWSSite(id int64) error {
	res, err := s.db.Exec(`DELETE FROM ws_sites WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

/* ------------------------- 旧反代模型迁移 ------------------------- */

// wsEngineConfig 是 webservice.Site 中存进 config 列的部分（与引擎 JSON 字段一致）。
// 放在 store 包是为了迁移旧数据与恢复旧备份时无需依赖引擎包。
type wsEngineConfig struct {
	Targets            []string `json:"targets"`
	LBPolicy           string   `json:"lb_policy"`
	Health             any      `json:"health"`
	Locations          []any    `json:"locations"`
	UseTargetHost      bool     `json:"use_target_host"`
	ForwardedHeaders   bool     `json:"forwarded_headers"`
	InsecureSkipVerify bool     `json:"insecure_skip_verify"`
	Timeouts           any      `json:"timeouts"`
	Compress           bool     `json:"compress"`
	RateLimit          any      `json:"rate_limit"`
	RequestHeaders     []string `json:"request_headers"`
	ResponseHeaders    []string `json:"response_headers"`
	IPMode             string   `json:"ip_mode"`
	IPList             []string `json:"ip_list"`
}

// ConvertRPDomain 把旧反代域名 + 路由转换为一个 Web 服务站点：
// "/" 路由的上游成为站点后端（多值即多后端），其余路由成为分路径规则；
// 首个带 health_path 的路由启用 HTTP 健康检查。
func ConvertRPDomain(d RPDomain, routes []RPRoute) *WSSite {
	cfg := wsEngineConfig{Health: map[string]any{"enable": false}, Timeouts: map[string]any{}, RateLimit: map[string]any{"enable": false}}
	var targets []string
	var locations []any
	for _, r := range routes {
		if !r.Enabled || r.UpstreamURL == "" {
			continue
		}
		pm := r.PathMatch
		if pm == "" || pm == "/" {
			targets = append(targets, r.UpstreamURL)
			continue
		}
		loc := map[string]any{"prefix": pm, "targets": []string{r.UpstreamURL}, "strip_prefix": false}
		locations = append(locations, loc)
	}
	if len(targets) == 0 {
		targets = []string{"http://127.0.0.1:80"}
	}
	cfg.Targets = targets
	// health_path 取首个 "/" 路由的设置
	for _, r := range routes {
		if (r.PathMatch == "" || r.PathMatch == "/") && r.HealthPath != "" {
			cfg.Health = map[string]any{"enable": true, "mode": "http", "path": r.HealthPath, "interval": 30, "timeout": 5, "fails": 3, "passes": 2}
			break
		}
	}
	if len(locations) > 0 {
		cfg.Locations = locations
	}
	cb, _ := json.Marshal(cfg)
	return &WSSite{
		Name: d.Domain, Domains: []string{d.Domain}, Type: "proxy", Enabled: d.Enabled,
		Config: json.RawMessage(cb), TLSMode: d.TLSMode, Challenge: d.Challenge,
		CertPEM: d.CertPEM, KeyPEM: d.KeyPEM, CertStatus: d.CertStatus, CertError: d.CertError,
		CertExpiresAt: d.CertExpiresAt, Remark: d.Remark, CreatedAt: d.CreatedAt,
	}
}
