package store

import (
	"database/sql"
	"errors"
	"time"
)

/* ------------------------- 入站放行规则 ------------------------- */

// PortAllowRule 一条入站放行规则：在 FPANEL_INPUT 里 ACCEPT 指定端口。
// 与 PortRecord（纯台账）不同，这里的规则会真正下发到内核。
type PortAllowRule struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Proto     string    `json:"proto"` // tcp|udp|both
	Port      string    `json:"port"`  // 单端口或 start-end 段
	SrcCIDR   string    `json:"src_cidr"`
	Enabled   bool      `json:"enabled"`
	Remark    string    `json:"remark"`
	CreatedAt time.Time `json:"created_at"`
}

const paCols = `id,name,proto,port,src_cidr,enabled,remark,created_at`

func scanPA(scan func(...any) error) (*PortAllowRule, error) {
	var r PortAllowRule
	var enabled int
	var created sql.NullString
	if err := scan(&r.ID, &r.Name, &r.Proto, &r.Port, &r.SrcCIDR, &enabled, &r.Remark, &created); err != nil {
		return nil, err
	}
	r.Enabled = enabled != 0
	r.CreatedAt = nz(created)
	return &r, nil
}

func (s *Store) ListPortAllowRules() ([]PortAllowRule, error) {
	rows, err := s.db.Query(`SELECT ` + paCols + ` FROM port_allow_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PortAllowRule
	for rows.Next() {
		r, err := scanPA(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) GetPortAllowRule(id int64) (*PortAllowRule, error) {
	r, err := scanPA(s.db.QueryRow(`SELECT `+paCols+` FROM port_allow_rules WHERE id=?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) CreatePortAllowRule(r *PortAllowRule) error {
	res, err := s.db.Exec(
		`INSERT INTO port_allow_rules(name,proto,port,src_cidr,enabled,remark) VALUES(?,?,?,?,?,?)`,
		r.Name, r.Proto, r.Port, r.SrcCIDR, boolInt(r.Enabled), r.Remark)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdatePortAllowRule(r *PortAllowRule) error {
	res, err := s.db.Exec(
		`UPDATE port_allow_rules SET name=?,proto=?,port=?,src_cidr=?,enabled=?,remark=? WHERE id=?`,
		r.Name, r.Proto, r.Port, r.SrcCIDR, boolInt(r.Enabled), r.Remark, r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeletePortAllowRule(id int64) error {
	res, err := s.db.Exec(`DELETE FROM port_allow_rules WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPortAllowEnabled 只切启停，用于列表上的开关。
func (s *Store) SetPortAllowEnabled(id int64, enabled bool) error {
	res, err := s.db.Exec(`UPDATE port_allow_rules SET enabled=? WHERE id=?`, boolInt(enabled), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
