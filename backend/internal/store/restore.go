package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Bundle 面板结构化配置的全量快照。
// 用于两处：备份导出/恢复，以及变更失败或高危撤销时的数据库回滚。
// 故意不含 users（密码哈希）与 settings（含 jwt_secret / DNS 凭据），
// 避免备份文件成为凭据载体；这两类数据由部署方单独迁移。
type Bundle struct {
	Version     int             `json:"version"`
	Exported    string          `json:"exported,omitempty"`
	Backend     string          `json:"backend,omitempty"`
	Forwards    []ForwardRule   `json:"forward_rules"`
	IPLists     []IPListEntry   `json:"ip_lists"`
	RateLimits  []RateLimitRule `json:"rate_limits"`
	NATRules    []NATRule       `json:"nat_rules"`
	Tasks       []ScheduledTask `json:"tasks"`
	Alerts      []AlertConfig   `json:"alerts"`
	Jails       []JailConfig    `json:"jails"`
	PortRecords []PortRecord    `json:"port_records"`
	PortAllows  []PortAllowRule `json:"port_allow_rules"`
	WSListeners []WSListener    `json:"ws_listeners"`
	WSSites     []WSSiteBackup  `json:"ws_sites"`
	// RPDomains / RPRoutes 为旧版反代模型的备份字段：恢复时转换为站点写入，
	// 新备份不再生成这两组数据
	RPDomains []RPDomainBackup `json:"rp_domains,omitempty"`
	RPRoutes  []RPRoute        `json:"rp_routes,omitempty"`
}

// WSSiteBackup Web 服务站点 + 证书（WSSite 的 cert_pem/key_pem 平时不参与 JSON，
// 只在备份/恢复结构中显式带出，避免列表接口泄露私钥）。
type WSSiteBackup struct {
	WSSite
	CertPEM string `json:"cert_pem,omitempty"`
	KeyPEM  string `json:"key_pem,omitempty"`
}

func toBackupWSSite(s WSSite) WSSiteBackup {
	return WSSiteBackup{WSSite: s, CertPEM: s.CertPEM, KeyPEM: s.KeyPEM}
}

func fromBackupWSSite(b WSSiteBackup) WSSite {
	s := b.WSSite
	s.CertPEM = b.CertPEM
	s.KeyPEM = b.KeyPEM
	return s
}

// ExportBundle 读取全量结构化配置（用于备份与回滚快照）。
// 任一类读取失败都返回错误：回滚快照若悄悄少了一部分，
// 失败恢复时就会把这些数据删掉（宁可放弃本次变更，也不能用残缺快照覆盖）。
func (s *Store) ExportBundle() (Bundle, error) {
	b := Bundle{Version: 1, Exported: time.Now().Format(time.RFC3339)}
	var err error
	if b.Forwards, err = s.ListForwards(); err != nil {
		return b, fmt.Errorf("读取转发规则: %w", err)
	}
	if b.IPLists, err = s.ListIPLists(""); err != nil {
		return b, fmt.Errorf("读取 IP 名单: %w", err)
	}
	if b.RateLimits, err = s.ListRateLimits(); err != nil {
		return b, fmt.Errorf("读取限速规则: %w", err)
	}
	if b.NATRules, err = s.ListNAT(); err != nil {
		return b, fmt.Errorf("读取 NAT 规则: %w", err)
	}
	if b.Tasks, err = s.ListTasks(); err != nil {
		return b, fmt.Errorf("读取定时任务: %w", err)
	}
	if b.Alerts, err = s.ListAlerts(); err != nil {
		return b, fmt.Errorf("读取告警配置: %w", err)
	}
	if b.Jails, err = s.ListJails(); err != nil {
		return b, fmt.Errorf("读取防爆破配置: %w", err)
	}
	if b.PortRecords, err = s.ListPortRecords(); err != nil {
		return b, fmt.Errorf("读取端口台账: %w", err)
	}
	if b.PortAllows, err = s.ListPortAllowRules(); err != nil {
		return b, fmt.Errorf("读取入站放行规则: %w", err)
	}
	if b.WSListeners, err = s.ListWSListeners(); err != nil {
		return b, fmt.Errorf("读取监听: %w", err)
	}
	sites, err := s.ListWSSites()
	if err != nil {
		return b, fmt.Errorf("读取站点: %w", err)
	}
	for _, d := range sites {
		b.WSSites = append(b.WSSites, toBackupWSSite(d))
	}
	return b, nil
}

// IsEmpty 快照是否为空（用于判断是否需要回滚）。
func (b Bundle) IsEmpty() bool {
	return len(b.Forwards) == 0 && len(b.IPLists) == 0 && len(b.RateLimits) == 0 &&
		len(b.NATRules) == 0 && len(b.Tasks) == 0 && len(b.Alerts) == 0 &&
		len(b.Jails) == 0 && len(b.PortRecords) == 0 && len(b.PortAllows) == 0 &&
		len(b.WSListeners) == 0 && len(b.WSSites) == 0 && len(b.RPDomains) == 0
}

func ts(t time.Time) string {
	if t.IsZero() {
		return time.Now().Format("2006-01-02 15:04:05")
	}
	return t.Format("2006-01-02 15:04:05")
}

func tsPtr(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.Format("2006-01-02 15:04:05")
}

func idOrNil(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}

func boolIntStore(v bool) int {
	if v {
		return 1
	}
	return 0
}

// ReplaceAll 在单个事务内全量替换结构化配置：要么全部成功，要么一条不动。
// 保留原 ID（转发规则被定时任务/审计引用；反代路由挂在域名下）。
// 注意：会清空现有配置，仅供「恢复备份」与「失败回滚」使用。
func (s *Store) ReplaceAll(b Bundle) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开启恢复事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, q := range []string{
		`DELETE FROM ws_sites`,
		`DELETE FROM ws_listeners`,
		`DELETE FROM forward_rules`,
		`DELETE FROM ip_lists`,
		`DELETE FROM rate_limit_rules`,
		`DELETE FROM nat_rules`,
		`DELETE FROM scheduled_tasks`,
		`DELETE FROM alert_configs`,
		`DELETE FROM jail_configs`,
		`DELETE FROM port_records`,
		`DELETE FROM port_allow_rules`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("清空配置失败: %w", err)
		}
	}

	for i := range b.Forwards {
		f := b.Forwards[i]
		if _, err := tx.Exec(`INSERT INTO forward_rules
			(id,name,protocol,listen_port,src_ip,dst_ip,dst_domain,dst_port,enabled,expires_at,remark,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			idOrNil(f.ID), f.Name, f.Protocol, f.ListenPort, f.SrcIP, f.DstIP, f.DstDomain, f.DstPort,
			boolIntStore(f.Enabled), tsPtr(f.ExpiresAt), f.Remark, ts(f.CreatedAt), ts(f.UpdatedAt)); err != nil {
			return fmt.Errorf("恢复转发规则 %d: %w", f.ID, err)
		}
	}
	for i := range b.IPLists {
		e := b.IPLists[i]
		if _, err := tx.Exec(`INSERT INTO ip_lists
			(id,list_type,ip_or_cidr,set_name,group_tag,source,country,expires_at,remark,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?)`,
			idOrNil(e.ID), e.ListType, e.IPOrCIDR, e.SetName, e.GroupTag, e.Source, e.Country,
			tsPtr(e.ExpiresAt), e.Remark, ts(e.CreatedAt)); err != nil {
			return fmt.Errorf("恢复名单 %d: %w", e.ID, err)
		}
	}
	for i := range b.RateLimits {
		r := b.RateLimits[i]
		if _, err := tx.Exec(`INSERT INTO rate_limit_rules
			(id,name,proto,port,per_src,rate,rate_unit,burst,conn_limit,enabled,remark,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			idOrNil(r.ID), r.Name, r.Proto, r.Port, boolIntStore(r.PerSrc), r.Rate, r.RateUnit,
			r.Burst, r.ConnLimit, boolIntStore(r.Enabled), r.Remark, ts(r.CreatedAt)); err != nil {
			return fmt.Errorf("恢复限速规则 %d: %w", r.ID, err)
		}
	}
	for i := range b.NATRules {
		n := b.NATRules[i]
		if _, err := tx.Exec(`INSERT INTO nat_rules
			(id,name,type,src_cidr,out_iface,to_addr,enabled,remark,created_at)
			VALUES(?,?,?,?,?,?,?,?,?)`,
			idOrNil(n.ID), n.Name, n.Type, n.SrcCIDR, n.OutIface, n.ToAddr,
			boolIntStore(n.Enabled), n.Remark, ts(n.CreatedAt)); err != nil {
			return fmt.Errorf("恢复 NAT 规则 %d: %w", n.ID, err)
		}
	}
	for i := range b.Tasks {
		t := b.Tasks[i]
		if _, err := tx.Exec(`INSERT INTO scheduled_tasks
			(id,name,cron_expr,action,target_id,enabled,last_run,next_run,remark,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?)`,
			idOrNil(t.ID), t.Name, t.CronExpr, t.Action, t.TargetID, boolIntStore(t.Enabled),
			tsPtr(t.LastRun), tsPtr(t.NextRun), t.Remark, ts(t.CreatedAt)); err != nil {
			return fmt.Errorf("恢复定时任务 %d: %w", t.ID, err)
		}
	}
	for i := range b.Alerts {
		a := b.Alerts[i]
		if _, err := tx.Exec(`INSERT INTO alert_configs
			(id,channel,config_json,events_json,enabled,created_at) VALUES(?,?,?,?,?,?)`,
			idOrNil(a.ID), a.Channel, a.ConfigJSON, a.EventsJSON, boolIntStore(a.Enabled), ts(a.CreatedAt)); err != nil {
			return fmt.Errorf("恢复告警渠道 %d: %w", a.ID, err)
		}
	}
	for i := range b.Jails {
		j := b.Jails[i]
		if _, err := tx.Exec(`INSERT INTO jail_configs
			(id,name,source_type,log_path,unit,match_regex,threshold,find_time,ban_time,ignore_cidrs,enabled,remark,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			idOrNil(j.ID), j.Name, j.SourceType, j.LogPath, j.Unit, j.MatchRegex, j.Threshold,
			j.FindTime, j.BanTime, j.IgnoreCIDRs, boolIntStore(j.Enabled), j.Remark, ts(j.CreatedAt)); err != nil {
			return fmt.Errorf("恢复防爆破配置 %d: %w", j.ID, err)
		}
	}
	for i := range b.PortRecords {
		p := b.PortRecords[i]
		if _, err := tx.Exec(`INSERT INTO port_records
			(id,name,proto,port,group_tag,remark,created_at) VALUES(?,?,?,?,?,?,?)`,
			idOrNil(p.ID), p.Name, p.Proto, p.Port, p.GroupTag, p.Remark, ts(p.CreatedAt)); err != nil {
			return fmt.Errorf("恢复端口记录 %d: %w", p.ID, err)
		}
	}
	for i := range b.PortAllows {
		p := b.PortAllows[i]
		if _, err := tx.Exec(`INSERT INTO port_allow_rules
			(id,name,proto,port,src_cidr,enabled,remark,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			idOrNil(p.ID), p.Name, p.Proto, p.Port, p.SrcCIDR, boolIntStore(p.Enabled), p.Remark,
			ts(p.CreatedAt)); err != nil {
			return fmt.Errorf("恢复入站放行规则 %d: %w", p.ID, err)
		}
	}
	for i := range b.WSListeners {
		l := b.WSListeners[i]
		cfg, _ := json.Marshal(l.Config)
		if _, err := tx.Exec(`INSERT INTO ws_listeners
			(id,name,enabled,config,remark,created_at) VALUES(?,?,?,?,?,?)`,
			idOrNil(l.ID), l.Name, boolIntStore(l.Enabled), string(cfg), l.Remark, ts(l.CreatedAt)); err != nil {
			return fmt.Errorf("恢复监听 %d: %w", l.ID, err)
		}
	}
	for i := range b.WSSites {
		s := fromBackupWSSite(b.WSSites[i])
		if err := insertWSSite(tx, &s); err != nil {
			return fmt.Errorf("恢复站点 %d: %w", s.ID, err)
		}
	}
	// 旧版反代备份：转换为站点写入（路由聚合在 RPDomain 侧完成不了，
	// 这里逐域名转换；RPDomainBackup 只在旧文件中出现）
	if len(b.RPDomains) > 0 {
		routesByDomain := map[int64][]RPRoute{}
		for _, r := range b.RPRoutes {
			routesByDomain[r.DomainID] = append(routesByDomain[r.DomainID], r)
		}
		for i := range b.RPDomains {
			d := fromBackupDomain(b.RPDomains[i])
			s := ConvertRPDomain(d, routesByDomain[d.ID])
			if err := insertWSSite(tx, s); err != nil {
				return fmt.Errorf("恢复旧反代域名 %s: %w", d.Domain, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交恢复事务: %w", err)
	}
	return nil
}

// fromBackupDomain 兼容旧备份结构（RPDomainBackup 平铺字段）。
func fromBackupDomain(b RPDomainBackup) RPDomain {
	d := b.RPDomain
	d.CertPEM = b.CertPEM
	d.KeyPEM = b.KeyPEM
	return d
}

// RPDomainBackup 旧版反代域名的备份结构（只用于读取旧备份文件）。
type RPDomainBackup struct {
	RPDomain
	CertPEM string `json:"cert_pem,omitempty"`
	KeyPEM  string `json:"key_pem,omitempty"`
}

// insertWSSite 在事务内插入站点行（恢复备份与回滚快照用）。
func insertWSSite(tx *sql.Tx, s *WSSite) error {
	if s.Config == nil {
		s.Config = json.RawMessage(`{}`)
	}
	domains, _ := json.Marshal(s.Domains)
	if _, err := tx.Exec(`INSERT INTO ws_sites
		(id,listener_id,name,domains,type,enabled,is_default,config,tls_mode,challenge,cert_pem,key_pem,cert_status,cert_error,cert_expires_at,remark,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		idOrNil(s.ID), idOrNil(s.ListenerID), s.Name, string(domains), s.Type, boolIntStore(s.Enabled), boolIntStore(s.Default),
		string(s.Config), s.TLSMode, s.Challenge, s.CertPEM, s.KeyPEM, s.CertStatus, s.CertError,
		tsPtr(s.CertExpiresAt), s.Remark, ts(s.CreatedAt)); err != nil {
		return err
	}
	return nil
}
