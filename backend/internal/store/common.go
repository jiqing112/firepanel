package store

import (
	"database/sql"
	"errors"
	"time"
)

// ErrNotFound 通用未找到错误。
var ErrNotFound = errors.New("not found")

func nullTime(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s.String, time.Local)
	if err != nil {
		return nil
	}
	return &t
}

func timePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format("2006-01-02 15:04:05")
}

// nz 将可空时间列转为零值安全 time.Time。
func nz(s sql.NullString) time.Time {
	if t := nullTime(s); t != nil {
		return *t
	}
	return time.Time{}
}

/* ------------------------- 用户 ------------------------- */

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	PassHash  string    `json:"-"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(u *User) error {
	res, err := s.db.Exec(`INSERT INTO users(username,pass_hash,role) VALUES(?,?,?)`, u.Username, u.PassHash, u.Role)
	if err != nil {
		return err
	}
	u.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) GetUserByName(username string) (*User, error) {
	u := &User{}
	var created sql.NullString
	err := s.db.QueryRow(`SELECT id,username,pass_hash,role,created_at FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.PassHash, &u.Role, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.CreatedAt = nz(created)
	return u, nil
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT id,username,pass_hash,role,created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var created sql.NullString
		if err := rows.Scan(&u.ID, &u.Username, &u.PassHash, &u.Role, &created); err != nil {
			return nil, err
		}
		u.CreatedAt = nz(created)
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) UpdateUserPassword(id int64, passHash string) error {
	res, err := s.db.Exec(`UPDATE users SET pass_hash=? WHERE id=?`, passHash, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUser(id int64) error {
	res, err := s.db.Exec(`DELETE FROM users WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

/* ------------------------- 设置 ------------------------- */

func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key=?`, key)
	return err
}

/* ------------------------- 审计日志 ------------------------- */

type AuditEntry struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	Username    string    `json:"username"`
	Action      string    `json:"action"`
	Target      string    `json:"target"`
	BeforeJSON  string    `json:"-"`
	AfterJSON   string    `json:"-"`
	Detail      string    `json:"detail"`
	IP          string    `json:"ip"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Store) AddAudit(e *AuditEntry) error {
	res, err := s.db.Exec(`INSERT INTO audit_logs(user_id,username,action,target,before_json,after_json,detail,ip)
		VALUES(?,?,?,?,?,?,?,?)`,
		e.UserID, e.Username, e.Action, e.Target, e.BeforeJSON, e.AfterJSON, e.Detail, e.IP)
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) ListAudit(limit, offset int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id,user_id,username,action,target,before_json,after_json,detail,ip,created_at
		FROM audit_logs ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var created sql.NullString
		if err := rows.Scan(&e.ID, &e.UserID, &e.Username, &e.Action, &e.Target, &e.BeforeJSON, &e.AfterJSON, &e.Detail, &e.IP, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = nz(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

/* ------------------------- 同步状态 ------------------------- */

type SyncState struct {
	Backend      string
	LastOKAt     *time.Time
	DriftJSON    string
	LastGoodJSON string
}

func (s *Store) GetSyncState() (*SyncState, error) {
	st := &SyncState{}
	var lastOK sql.NullString
	err := s.db.QueryRow(`SELECT backend,last_ok_at,drift_json,last_good_json FROM sync_state WHERE id=1`).
		Scan(&st.Backend, &lastOK, &st.DriftJSON, &st.LastGoodJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	st.LastOKAt = nullTime(lastOK)
	return st, nil
}

func (s *Store) UpsertSyncState(st *SyncState) error {
	_, err := s.db.Exec(`INSERT INTO sync_state(id,backend,last_ok_at,drift_json,last_good_json) VALUES(1,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET backend=excluded.backend, last_ok_at=excluded.last_ok_at,
		drift_json=excluded.drift_json, last_good_json=excluded.last_good_json`,
		st.Backend, timePtr(st.LastOKAt), st.DriftJSON, st.LastGoodJSON)
	return err
}

/* ------------------------- 统计采样 ------------------------- */

func (s *Store) AddStatsSample(ts int64, conn, rxBps, txBps int64) error {
	_, err := s.db.Exec(`INSERT INTO stats_samples(ts,conn_count,rx_bps,tx_bps) VALUES(?,?,?,?)
		ON CONFLICT(ts) DO UPDATE SET conn_count=excluded.conn_count, rx_bps=excluded.rx_bps, tx_bps=excluded.tx_bps`,
		ts, conn, rxBps, txBps)
	return err
}

type StatsSample struct {
	TS        int64 `json:"ts"`
	ConnCount int64 `json:"conn_count"`
	RxBps     int64 `json:"rx_bps"`
	TxBps     int64 `json:"tx_bps"`
}

func (s *Store) ListStatsSince(ts int64) ([]StatsSample, error) {
	rows, err := s.db.Query(`SELECT ts,conn_count,rx_bps,tx_bps FROM stats_samples WHERE ts>=? ORDER BY ts`, ts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatsSample
	for rows.Next() {
		var v StatsSample
		if err := rows.Scan(&v.TS, &v.ConnCount, &v.RxBps, &v.TxBps); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) PruneStatsBefore(ts int64) error {
	_, err := s.db.Exec(`DELETE FROM stats_samples WHERE ts<?`, ts)
	return err
}

func (s *Store) SaveRuleStats(ruleID int64, ts int64, packets, bytes int64) error {
	_, err := s.db.Exec(`INSERT INTO rule_stats(rule_id,ts,packets,bytes) VALUES(?,?,?,?)
		ON CONFLICT(rule_id,ts) DO UPDATE SET packets=excluded.packets, bytes=excluded.bytes`,
		ruleID, ts, packets, bytes)
	return err
}
