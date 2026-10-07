-- 阶段2 初始 schema：用户、设置、端口转发、IP 名单、审计、同步状态、统计采样

CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    pass_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'admin' CHECK(role IN ('admin','viewer')),
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE forward_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    protocol TEXT NOT NULL CHECK(protocol IN ('tcp','udp','both')),
    listen_port TEXT NOT NULL,          -- 单端口 "80" 或端口段 "30000-30010"
    src_ip TEXT NOT NULL DEFAULT '',    -- 可选来源限制（IP/CIDR）
    dst_ip TEXT NOT NULL DEFAULT '',
    dst_domain TEXT NOT NULL DEFAULT '',-- 域名目标（DDNS），非空时优先于 dst_ip
    dst_port TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    expires_at TEXT DEFAULT NULL,       -- 临时规则到期自动停用
    remark TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);
CREATE INDEX idx_forward_enabled ON forward_rules(enabled);

CREATE TABLE ip_lists (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    list_type TEXT NOT NULL CHECK(list_type IN ('black','white')),
    ip_or_cidr TEXT NOT NULL,
    set_name TEXT NOT NULL DEFAULT '',  -- 预留：归属 ipset/nft set 名称
    group_tag TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'manual', -- manual|import|api|geo
    country TEXT NOT NULL DEFAULT '',
    expires_at TEXT DEFAULT NULL,
    remark TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime')),
    UNIQUE(list_type, ip_or_cidr)
);
CREATE INDEX idx_iplists_type ON ip_lists(list_type);

CREATE TABLE audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER,
    username TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,               -- create|update|delete|login|sync|apply|...
    target TEXT NOT NULL DEFAULT '',    -- 资源类型:ID
    before_json TEXT NOT NULL DEFAULT '',
    after_json TEXT NOT NULL DEFAULT '',
    detail TEXT NOT NULL DEFAULT '',
    ip TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);
CREATE INDEX idx_audit_time ON audit_logs(created_at DESC);

CREATE TABLE sync_state (
    id INTEGER PRIMARY KEY CHECK(id = 1),
    backend TEXT NOT NULL,
    last_ok_at TEXT,
    drift_json TEXT NOT NULL DEFAULT '',
    last_good_json TEXT NOT NULL DEFAULT '' -- 上一份成功应用的期望状态快照（回滚用）
);

CREATE TABLE rule_stats (
    rule_id INTEGER NOT NULL,
    ts INTEGER NOT NULL,                -- unix 秒
    packets INTEGER NOT NULL DEFAULT 0,
    bytes INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (rule_id, ts)
);

CREATE TABLE stats_samples (
    ts INTEGER PRIMARY KEY,             -- unix 秒（分钟粒度）
    conn_count INTEGER NOT NULL DEFAULT 0,
    rx_bps INTEGER NOT NULL DEFAULT 0,
    tx_bps INTEGER NOT NULL DEFAULT 0
);
