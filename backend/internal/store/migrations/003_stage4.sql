-- 阶段4：限速、NAT、定时任务、告警

CREATE TABLE rate_limit_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    proto TEXT NOT NULL DEFAULT 'tcp' CHECK(proto IN ('tcp','udp','both')),
    port TEXT NOT NULL DEFAULT '',        -- 空=所有端口
    per_src INTEGER NOT NULL DEFAULT 1,   -- 按 IP 限（1）或总量限（0）
    rate INTEGER NOT NULL,                -- 单位时间次数
    rate_unit TEXT NOT NULL DEFAULT 'second' CHECK(rate_unit IN ('second','minute','hour')),
    burst INTEGER NOT NULL DEFAULT 10,
    conn_limit INTEGER NOT NULL DEFAULT 0, -- >0 时同时限制单 IP 并发连接
    enabled INTEGER NOT NULL DEFAULT 1,
    remark TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);

CREATE TABLE nat_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    type TEXT NOT NULL CHECK(type IN ('SNAT','MASQ')),
    src_cidr TEXT NOT NULL,               -- 需要伪装/转换的来源网段
    out_iface TEXT NOT NULL,              -- 出口网卡
    to_addr TEXT NOT NULL DEFAULT '',     -- SNAT 目标地址（MASQ 留空）
    enabled INTEGER NOT NULL DEFAULT 1,
    remark TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);

CREATE TABLE scheduled_tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    cron_expr TEXT NOT NULL,
    action TEXT NOT NULL CHECK(action IN ('forward_enable','forward_disable','iplist_expire','sync')),
    target_id INTEGER NOT NULL DEFAULT 0, -- forward_* 动作的目标规则 ID
    enabled INTEGER NOT NULL DEFAULT 1,
    last_run TEXT,
    next_run TEXT,
    remark TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);

CREATE TABLE alert_configs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel TEXT NOT NULL UNIQUE CHECK(channel IN ('telegram','webhook','smtp')),
    config_json TEXT NOT NULL DEFAULT '{}',
    events_json TEXT NOT NULL DEFAULT '[]', -- 订阅的事件类型列表
    enabled INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);
