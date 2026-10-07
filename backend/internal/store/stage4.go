package store

import (
	"database/sql"
	"errors"
	"time"
)

/* ------------------------- 限速规则 ------------------------- */

type RateLimitRule struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Proto     string    `json:"proto"` // tcp|udp|both
	Port      string    `json:"port"`  // 空=全部端口
	PerSrc    bool      `json:"per_src"`
	Rate      int64     `json:"rate"`
	RateUnit  string    `json:"rate_unit"` // second|minute|hour
	Burst     int64     `json:"burst"`
	ConnLimit int64     `json:"conn_limit"` // 0=不限连接
	Enabled   bool      `json:"enabled"`
	Remark    string    `json:"remark"`
	CreatedAt time.Time `json:"created_at"`
}

const rlCols = `id,name,proto,port,per_src,rate,rate_unit,burst,conn_limit,enabled,remark,created_at`

func scanRL(scan func(...any) error) (*RateLimitRule, error) {
	var r RateLimitRule
	var perSrc, enabled int
	var created sql.NullString
	if err := scan(&r.ID, &r.Name, &r.Proto, &r.Port, &perSrc, &r.Rate, &r.RateUnit, &r.Burst, &r.ConnLimit, &enabled, &r.Remark, &created); err != nil {
		return nil, err
	}
	r.PerSrc = perSrc == 1
	r.Enabled = enabled == 1
	r.CreatedAt = nz(created)
	return &r, nil
}

func (s *Store) ListRateLimits() ([]RateLimitRule, error) {
	rows, err := s.db.Query(`SELECT ` + rlCols + ` FROM rate_limit_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RateLimitRule
	for rows.Next() {
		r, err := scanRL(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) CreateRateLimit(r *RateLimitRule) error {
	res, err := s.db.Exec(`INSERT INTO rate_limit_rules(name,proto,port,per_src,rate,rate_unit,burst,conn_limit,enabled,remark)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		r.Name, r.Proto, r.Port, boolInt(r.PerSrc), r.Rate, r.RateUnit, r.Burst, r.ConnLimit, boolInt(r.Enabled), r.Remark)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateRateLimit(r *RateLimitRule) error {
	res, err := s.db.Exec(`UPDATE rate_limit_rules SET name=?,proto=?,port=?,per_src=?,rate=?,rate_unit=?,burst=?,conn_limit=?,enabled=?,remark=? WHERE id=?`,
		r.Name, r.Proto, r.Port, boolInt(r.PerSrc), r.Rate, r.RateUnit, r.Burst, r.ConnLimit, boolInt(r.Enabled), r.Remark, r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteRateLimit(id int64) error {
	res, err := s.db.Exec(`DELETE FROM rate_limit_rules WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

/* ------------------------- NAT 规则 ------------------------- */

type NATRule struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"` // SNAT|MASQ
	SrcCIDR   string    `json:"src_cidr"`
	OutIface  string    `json:"out_iface"`
	ToAddr    string    `json:"to_addr"` // SNAT 用
	Enabled   bool      `json:"enabled"`
	Remark    string    `json:"remark"`
	CreatedAt time.Time `json:"created_at"`
}

const natCols = `id,name,type,src_cidr,out_iface,to_addr,enabled,remark,created_at`

func scanNAT(scan func(...any) error) (*NATRule, error) {
	var r NATRule
	var enabled int
	var created sql.NullString
	if err := scan(&r.ID, &r.Name, &r.Type, &r.SrcCIDR, &r.OutIface, &r.ToAddr, &enabled, &r.Remark, &created); err != nil {
		return nil, err
	}
	r.Enabled = enabled == 1
	r.CreatedAt = nz(created)
	return &r, nil
}

func (s *Store) ListNAT() ([]NATRule, error) {
	rows, err := s.db.Query(`SELECT ` + natCols + ` FROM nat_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NATRule
	for rows.Next() {
		r, err := scanNAT(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) CreateNAT(r *NATRule) error {
	res, err := s.db.Exec(`INSERT INTO nat_rules(name,type,src_cidr,out_iface,to_addr,enabled,remark) VALUES(?,?,?,?,?,?,?)`,
		r.Name, r.Type, r.SrcCIDR, r.OutIface, r.ToAddr, boolInt(r.Enabled), r.Remark)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateNAT(r *NATRule) error {
	res, err := s.db.Exec(`UPDATE nat_rules SET name=?,type=?,src_cidr=?,out_iface=?,to_addr=?,enabled=?,remark=? WHERE id=?`,
		r.Name, r.Type, r.SrcCIDR, r.OutIface, r.ToAddr, boolInt(r.Enabled), r.Remark, r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteNAT(id int64) error {
	res, err := s.db.Exec(`DELETE FROM nat_rules WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

/* ------------------------- 定时任务 ------------------------- */

type ScheduledTask struct {
	ID       int64      `json:"id"`
	Name     string     `json:"name"`
	CronExpr string     `json:"cron_expr"`
	Action   string     `json:"action"` // forward_enable|forward_disable|iplist_expire|sync
	TargetID int64      `json:"target_id"`
	Enabled  bool       `json:"enabled"`
	LastRun  *time.Time `json:"last_run"`
	NextRun  *time.Time `json:"next_run"`
	Remark   string     `json:"remark"`
	CreatedAt time.Time `json:"created_at"`
}

const taskCols = `id,name,cron_expr,action,target_id,enabled,last_run,next_run,remark,created_at`

func scanTask(scan func(...any) error) (*ScheduledTask, error) {
	var t ScheduledTask
	var enabled int
	var created, lastRun, nextRun sql.NullString
	if err := scan(&t.ID, &t.Name, &t.CronExpr, &t.Action, &t.TargetID, &enabled, &lastRun, &nextRun, &t.Remark, &created); err != nil {
		return nil, err
	}
	t.Enabled = enabled == 1
	t.LastRun = nullTime(lastRun)
	t.NextRun = nullTime(nextRun)
	t.CreatedAt = nz(created)
	return &t, nil
}

func (s *Store) ListTasks() ([]ScheduledTask, error) {
	rows, err := s.db.Query(`SELECT ` + taskCols + ` FROM scheduled_tasks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScheduledTask
	for rows.Next() {
		t, err := scanTask(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (s *Store) GetTask(id int64) (*ScheduledTask, error) {
	t, err := scanTask(s.db.QueryRow(`SELECT `+taskCols+` FROM scheduled_tasks WHERE id=?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func (s *Store) CreateTask(t *ScheduledTask) error {
	res, err := s.db.Exec(`INSERT INTO scheduled_tasks(name,cron_expr,action,target_id,enabled,remark) VALUES(?,?,?,?,?,?)`,
		t.Name, t.CronExpr, t.Action, t.TargetID, boolInt(t.Enabled), t.Remark)
	if err != nil {
		return err
	}
	t.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateTask(t *ScheduledTask) error {
	res, err := s.db.Exec(`UPDATE scheduled_tasks SET name=?,cron_expr=?,action=?,target_id=?,enabled=?,remark=? WHERE id=?`,
		t.Name, t.CronExpr, t.Action, t.TargetID, boolInt(t.Enabled), t.Remark, t.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetTaskRun(id int64, last, next time.Time) error {
	_, err := s.db.Exec(`UPDATE scheduled_tasks SET last_run=?, next_run=? WHERE id=?`,
		last.Format("2006-01-02 15:04:05"), next.Format("2006-01-02 15:04:05"), id)
	return err
}

func (s *Store) DeleteTask(id int64) error {
	res, err := s.db.Exec(`DELETE FROM scheduled_tasks WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

/* ------------------------- 告警配置 ------------------------- */

type AlertConfig struct {
	ID         int64     `json:"id"`
	Channel    string    `json:"channel"` // telegram|webhook|smtp
	ConfigJSON string    `json:"config_json"`
	EventsJSON string    `json:"events_json"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Store) ListAlerts() ([]AlertConfig, error) {
	rows, err := s.db.Query(`SELECT id,channel,config_json,events_json,enabled,created_at FROM alert_configs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertConfig
	for rows.Next() {
		var a AlertConfig
		var enabled int
		var created sql.NullString
		if err := rows.Scan(&a.ID, &a.Channel, &a.ConfigJSON, &a.EventsJSON, &enabled, &created); err != nil {
			return nil, err
		}
		a.Enabled = enabled == 1
		a.CreatedAt = nz(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) UpsertAlert(a *AlertConfig) error {
	res, err := s.db.Exec(`INSERT INTO alert_configs(channel,config_json,events_json,enabled) VALUES(?,?,?,?)
		ON CONFLICT(channel) DO UPDATE SET config_json=excluded.config_json, events_json=excluded.events_json, enabled=excluded.enabled`,
		a.Channel, a.ConfigJSON, a.EventsJSON, boolInt(a.Enabled))
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) DeleteAlert(id int64) error {
	res, err := s.db.Exec(`DELETE FROM alert_configs WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
