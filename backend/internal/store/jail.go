package store

import (
	"database/sql"
	"errors"
	"time"
)

/* ------------------------- 防爆破 Jail ------------------------- */

type JailConfig struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	SourceType  string    `json:"source_type"` // file|journald|audit
	LogPath     string    `json:"log_path"`
	Unit        string    `json:"unit"`      // journald -u
	MatchRegex  string    `json:"match_regex"`
	Threshold   int       `json:"threshold"`
	FindTime    int       `json:"find_time"` // 秒
	BanTime     int       `json:"ban_time"`  // 秒
	IgnoreCIDRs string    `json:"ignore_cidrs"`
	Enabled     bool      `json:"enabled"`
	Remark      string    `json:"remark"`
	CreatedAt   time.Time `json:"created_at"`
}

const jailCols = `id,name,source_type,log_path,unit,match_regex,threshold,find_time,ban_time,ignore_cidrs,enabled,remark,created_at`

func scanJail(scan func(...any) error) (*JailConfig, error) {
	var j JailConfig
	var enabled int
	var created sql.NullString
	if err := scan(&j.ID, &j.Name, &j.SourceType, &j.LogPath, &j.Unit, &j.MatchRegex,
		&j.Threshold, &j.FindTime, &j.BanTime, &j.IgnoreCIDRs, &enabled, &j.Remark, &created); err != nil {
		return nil, err
	}
	j.Enabled = enabled == 1
	j.CreatedAt = nz(created)
	return &j, nil
}

func (s *Store) ListJails() ([]JailConfig, error) {
	rows, err := s.db.Query(`SELECT ` + jailCols + ` FROM jail_configs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JailConfig
	for rows.Next() {
		j, err := scanJail(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (s *Store) GetJail(id int64) (*JailConfig, error) {
	j, err := scanJail(s.db.QueryRow(`SELECT `+jailCols+` FROM jail_configs WHERE id=?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return j, err
}

func (s *Store) CreateJail(j *JailConfig) error {
	res, err := s.db.Exec(`INSERT INTO jail_configs(name,source_type,log_path,unit,match_regex,threshold,find_time,ban_time,ignore_cidrs,enabled,remark)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		j.Name, j.SourceType, j.LogPath, j.Unit, j.MatchRegex, j.Threshold, j.FindTime, j.BanTime, j.IgnoreCIDRs, boolInt(j.Enabled), j.Remark)
	if err != nil {
		return err
	}
	j.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateJail(j *JailConfig) error {
	res, err := s.db.Exec(`UPDATE jail_configs SET name=?,source_type=?,log_path=?,unit=?,match_regex=?,threshold=?,find_time=?,ban_time=?,ignore_cidrs=?,enabled=?,remark=? WHERE id=?`,
		j.Name, j.SourceType, j.LogPath, j.Unit, j.MatchRegex, j.Threshold, j.FindTime, j.BanTime, j.IgnoreCIDRs, boolInt(j.Enabled), j.Remark, j.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteJail(id int64) error {
	res, err := s.db.Exec(`DELETE FROM jail_configs WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
