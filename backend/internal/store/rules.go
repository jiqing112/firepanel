package store

import (
	"database/sql"
	"errors"
	"time"
)

/* ------------------------- 端口转发 ------------------------- */

type ForwardRule struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Protocol   string     `json:"protocol"` // tcp|udp|both
	ListenPort string     `json:"listen_port"`
	SrcIP      string     `json:"src_ip"`
	DstIP      string     `json:"dst_ip"`
	DstDomain  string     `json:"dst_domain"`
	DstPort    string     `json:"dst_port"`
	Enabled    bool       `json:"enabled"`
	ExpiresAt  *time.Time `json:"expires_at"`
	Remark     string     `json:"remark"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

const forwardCols = `id,name,protocol,listen_port,src_ip,dst_ip,dst_domain,dst_port,enabled,expires_at,remark,created_at,updated_at`

func scanForward(scan func(...any) error) (*ForwardRule, error) {
	var r ForwardRule
	var enabled int
	var created, updated sql.NullString
	var expires sql.NullString
	if err := scan(&r.ID, &r.Name, &r.Protocol, &r.ListenPort, &r.SrcIP, &r.DstIP, &r.DstDomain, &r.DstPort,
		&enabled, &expires, &r.Remark, &created, &updated); err != nil {
		return nil, err
	}
	r.Enabled = enabled == 1
	r.ExpiresAt = nullTime(expires)
	r.CreatedAt = nz(created)
	r.UpdatedAt = nz(updated)
	return &r, nil
}

func (s *Store) ListForwards() ([]ForwardRule, error) {
	rows, err := s.db.Query(`SELECT ` + forwardCols + ` FROM forward_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ForwardRule
	for rows.Next() {
		r, err := scanForward(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) GetForward(id int64) (*ForwardRule, error) {
	r, err := scanForward(s.db.QueryRow(`SELECT `+forwardCols+` FROM forward_rules WHERE id=?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) CreateForward(r *ForwardRule) error {
	now := time.Now().Format("2006-01-02 15:04:05")
	res, err := s.db.Exec(`INSERT INTO forward_rules(name,protocol,listen_port,src_ip,dst_ip,dst_domain,dst_port,enabled,expires_at,remark,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.Name, r.Protocol, r.ListenPort, r.SrcIP, r.DstIP, r.DstDomain, r.DstPort, boolInt(r.Enabled), timePtr(r.ExpiresAt), r.Remark, now, now)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateForward(r *ForwardRule) error {
	res, err := s.db.Exec(`UPDATE forward_rules SET name=?,protocol=?,listen_port=?,src_ip=?,dst_ip=?,dst_domain=?,dst_port=?,enabled=?,expires_at=?,remark=?,updated_at=datetime('now','localtime')
		WHERE id=?`,
		r.Name, r.Protocol, r.ListenPort, r.SrcIP, r.DstIP, r.DstDomain, r.DstPort, boolInt(r.Enabled), timePtr(r.ExpiresAt), r.Remark, r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetForwardEnabled(id int64, enabled bool) error {
	res, err := s.db.Exec(`UPDATE forward_rules SET enabled=?, updated_at=datetime('now','localtime') WHERE id=?`, boolInt(enabled), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteForward(id int64) error {
	res, err := s.db.Exec(`DELETE FROM forward_rules WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

/* ------------------------- IP 名单 ------------------------- */

type IPListEntry struct {
	ID        int64      `json:"id"`
	ListType  string     `json:"list_type"` // black|white
	IPOrCIDR  string     `json:"ip_or_cidr"`
	SetName   string     `json:"set_name"`
	GroupTag  string     `json:"group_tag"`
	Source    string     `json:"source"`
	Country   string     `json:"country"`
	ExpiresAt *time.Time `json:"expires_at"`
	Remark    string     `json:"remark"`
	CreatedAt time.Time  `json:"created_at"`
}

const ipListCols = `id,list_type,ip_or_cidr,set_name,group_tag,source,country,expires_at,remark,created_at`

func scanIPList(scan func(...any) error) (*IPListEntry, error) {
	var e IPListEntry
	var created, expires sql.NullString
	if err := scan(&e.ID, &e.ListType, &e.IPOrCIDR, &e.SetName, &e.GroupTag, &e.Source, &e.Country, &expires, &e.Remark, &created); err != nil {
		return nil, err
	}
	e.ExpiresAt = nullTime(expires)
	e.CreatedAt = nz(created)
	return &e, nil
}

func (s *Store) ListIPLists(listType string) ([]IPListEntry, error) {
	q := `SELECT ` + ipListCols + ` FROM ip_lists`
	var args []any
	if listType != "" {
		q += ` WHERE list_type=?`
		args = append(args, listType)
	}
	rows, err := s.db.Query(q+` ORDER BY id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IPListEntry
	for rows.Next() {
		e, err := scanIPList(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

func (s *Store) CreateIPList(e *IPListEntry) error {
	res, err := s.db.Exec(`INSERT INTO ip_lists(list_type,ip_or_cidr,set_name,group_tag,source,country,expires_at,remark)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(list_type,ip_or_cidr) DO UPDATE SET group_tag=excluded.group_tag, remark=excluded.remark, expires_at=excluded.expires_at`,
		e.ListType, e.IPOrCIDR, e.SetName, e.GroupTag, e.Source, e.Country, timePtr(e.ExpiresAt), e.Remark)
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateIPList(e *IPListEntry) error {
	res, err := s.db.Exec(`UPDATE ip_lists SET group_tag=?, country=?, expires_at=?, remark=? WHERE id=?`,
		e.GroupTag, e.Country, timePtr(e.ExpiresAt), e.Remark, e.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteIPList(id int64) error {
	res, err := s.db.Exec(`DELETE FROM ip_lists WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteIPListsExpired(now time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM ip_lists WHERE expires_at IS NOT NULL AND expires_at < ?`, now.Format("2006-01-02 15:04:05"))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
