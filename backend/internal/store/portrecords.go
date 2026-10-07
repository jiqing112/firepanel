package store

import (
	"database/sql"
	"errors"
	"time"
)

/* ------------------------- 端口记录（台账） ------------------------- */

// PortRecord 一条端口记录：登记端口用途，纯台账（不参与内核规则生成）。
type PortRecord struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Proto     string    `json:"proto"` // tcp|udp|both
	Port      int       `json:"port"`
	GroupTag  string    `json:"group_tag"`
	Remark    string    `json:"remark"`
	CreatedAt time.Time `json:"created_at"`
}

const prCols = `id,name,proto,port,group_tag,remark,created_at`

func scanPR(scan func(...any) error) (*PortRecord, error) {
	var r PortRecord
	var created sql.NullString
	if err := scan(&r.ID, &r.Name, &r.Proto, &r.Port, &r.GroupTag, &r.Remark, &created); err != nil {
		return nil, err
	}
	r.CreatedAt = nz(created)
	return &r, nil
}

func (s *Store) ListPortRecords() ([]PortRecord, error) {
	rows, err := s.db.Query(`SELECT ` + prCols + ` FROM port_records ORDER BY port, proto`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PortRecord
	for rows.Next() {
		r, err := scanPR(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) GetPortRecord(id int64) (*PortRecord, error) {
	r, err := scanPR(s.db.QueryRow(`SELECT `+prCols+` FROM port_records WHERE id=?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) CreatePortRecord(r *PortRecord) error {
	res, err := s.db.Exec(`INSERT INTO port_records(name,proto,port,group_tag,remark) VALUES(?,?,?,?,?)`,
		r.Name, r.Proto, r.Port, r.GroupTag, r.Remark)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdatePortRecord(r *PortRecord) error {
	res, err := s.db.Exec(`UPDATE port_records SET name=?,proto=?,port=?,group_tag=?,remark=? WHERE id=?`,
		r.Name, r.Proto, r.Port, r.GroupTag, r.Remark, r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeletePortRecord(id int64) error {
	res, err := s.db.Exec(`DELETE FROM port_records WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
