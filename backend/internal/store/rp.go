package store

import (
	"database/sql"
	"errors"
	"time"
)

/* ------------------------- 反代域名 ------------------------- */

type RPDomain struct {
	ID            int64      `json:"id"`
	Domain        string     `json:"domain"`
	Enabled       bool       `json:"enabled"`
	TLSMode       string     `json:"tls_mode"` // auto|manual|none
	Challenge     string     `json:"challenge"` // ""=auto | http | alpn | dns（tls_mode=auto 时生效）
	CaddyOpts     string     `json:"caddy_opts"` // Caddy 站点选项 JSON：encode/security_headers/log_access
	CertPEM       string     `json:"-"`
	KeyPEM        string     `json:"-"`
	CertStatus    string     `json:"cert_status"` // none|pending|ok|error
	CertError     string     `json:"cert_error"`
	CertExpiresAt *time.Time `json:"cert_expires_at"`
	Remark        string     `json:"remark"`
	CreatedAt     time.Time  `json:"created_at"`
}

const rpDomainCols = `id,domain,enabled,tls_mode,challenge,caddy_opts,cert_pem,key_pem,cert_status,cert_error,cert_expires_at,remark,created_at`

func scanRPDomain(scan func(...any) error) (*RPDomain, error) {
	var d RPDomain
	var enabled int
	var created, expires sql.NullString
	if err := scan(&d.ID, &d.Domain, &enabled, &d.TLSMode, &d.Challenge, &d.CaddyOpts, &d.CertPEM, &d.KeyPEM, &d.CertStatus, &d.CertError, &expires, &d.Remark, &created); err != nil {
		return nil, err
	}
	d.Enabled = enabled == 1
	d.CertExpiresAt = nullTime(expires)
	d.CreatedAt = nz(created)
	return &d, nil
}

func (s *Store) ListRPDomains() ([]RPDomain, error) {
	rows, err := s.db.Query(`SELECT ` + rpDomainCols + ` FROM rp_domains ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RPDomain
	for rows.Next() {
		d, err := scanRPDomain(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Store) GetRPDomain(id int64) (*RPDomain, error) {
	d, err := scanRPDomain(s.db.QueryRow(`SELECT `+rpDomainCols+` FROM rp_domains WHERE id=?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (s *Store) CreateRPDomain(d *RPDomain) error {
	res, err := s.db.Exec(`INSERT INTO rp_domains(domain,enabled,tls_mode,challenge,caddy_opts,cert_pem,key_pem,cert_status,cert_error,cert_expires_at,remark)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		d.Domain, boolInt(d.Enabled), d.TLSMode, d.Challenge, d.CaddyOpts, d.CertPEM, d.KeyPEM, d.CertStatus, d.CertError, timePtr(d.CertExpiresAt), d.Remark)
	if err != nil {
		return err
	}
	d.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateRPDomain(d *RPDomain) error {
	res, err := s.db.Exec(`UPDATE rp_domains SET domain=?,enabled=?,tls_mode=?,challenge=?,caddy_opts=?,cert_pem=?,key_pem=?,cert_status=?,cert_error=?,cert_expires_at=?,remark=? WHERE id=?`,
		d.Domain, boolInt(d.Enabled), d.TLSMode, d.Challenge, d.CaddyOpts, d.CertPEM, d.KeyPEM, d.CertStatus, d.CertError, timePtr(d.CertExpiresAt), d.Remark, d.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetRPDomainCert(id int64, status, errMsg string, expires *time.Time) error {
	_, err := s.db.Exec(`UPDATE rp_domains SET cert_status=?,cert_error=?,cert_expires_at=? WHERE id=?`,
		status, errMsg, timePtr(expires), id)
	return err
}

func (s *Store) DeleteRPDomain(id int64) error {
	res, err := s.db.Exec(`DELETE FROM rp_domains WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

/* ------------------------- 反代路由 ------------------------- */

type RPRoute struct {
	ID              int64      `json:"id"`
	DomainID        int64      `json:"domain_id"`
	PathMatch       string     `json:"path_match"`
	UpstreamURL     string     `json:"upstream_url"`
	WsEnabled       bool       `json:"ws_enabled"`
	HealthPath      string     `json:"health_path"`
	Enabled         bool       `json:"enabled"`
	HealthStatus    string     `json:"health_status"` // unknown|ok|down
	HealthCheckedAt *time.Time `json:"health_checked_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

const rpRouteCols = `id,domain_id,path_match,upstream_url,ws_enabled,health_path,enabled,health_status,health_checked_at,created_at`

func scanRPRoute(scan func(...any) error) (*RPRoute, error) {
	var r RPRoute
	var ws, enabled int
	var created, checked sql.NullString
	if err := scan(&r.ID, &r.DomainID, &r.PathMatch, &r.UpstreamURL, &ws, &r.HealthPath, &enabled, &r.HealthStatus, &checked, &created); err != nil {
		return nil, err
	}
	r.WsEnabled = ws == 1
	r.Enabled = enabled == 1
	r.HealthCheckedAt = nullTime(checked)
	r.CreatedAt = nz(created)
	return &r, nil
}

func (s *Store) ListRPRoutes(domainID int64) ([]RPRoute, error) {
	q := `SELECT ` + rpRouteCols + ` FROM rp_routes`
	var args []any
	if domainID > 0 {
		q += ` WHERE domain_id=?`
		args = append(args, domainID)
	}
	rows, err := s.db.Query(q+` ORDER BY domain_id, LENGTH(path_match) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RPRoute
	for rows.Next() {
		r, err := scanRPRoute(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) GetRPRoute(id int64) (*RPRoute, error) {
	r, err := scanRPRoute(s.db.QueryRow(`SELECT `+rpRouteCols+` FROM rp_routes WHERE id=?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) CreateRPRoute(r *RPRoute) error {
	res, err := s.db.Exec(`INSERT INTO rp_routes(domain_id,path_match,upstream_url,ws_enabled,health_path,enabled)
		VALUES(?,?,?,?,?,?)`,
		r.DomainID, r.PathMatch, r.UpstreamURL, boolInt(r.WsEnabled), r.HealthPath, boolInt(r.Enabled))
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateRPRoute(r *RPRoute) error {
	res, err := s.db.Exec(`UPDATE rp_routes SET path_match=?,upstream_url=?,ws_enabled=?,health_path=?,enabled=? WHERE id=?`,
		r.PathMatch, r.UpstreamURL, boolInt(r.WsEnabled), r.HealthPath, boolInt(r.Enabled), r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetRPRouteHealth(id int64, status string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE rp_routes SET health_status=?,health_checked_at=? WHERE id=?`,
		status, at.Format("2006-01-02 15:04:05"), id)
	return err
}

func (s *Store) DeleteRPRoute(id int64) error {
	res, err := s.db.Exec(`DELETE FROM rp_routes WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
