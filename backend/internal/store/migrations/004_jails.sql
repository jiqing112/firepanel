-- 阶段4.5：fail2ban 原生防爆破

CREATE TABLE jail_configs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    source_type TEXT NOT NULL CHECK(source_type IN ('file','journald','audit')),
    log_path TEXT NOT NULL DEFAULT '',
    unit TEXT NOT NULL DEFAULT '',
    match_regex TEXT NOT NULL DEFAULT '',
    threshold INTEGER NOT NULL DEFAULT 5,
    find_time INTEGER NOT NULL DEFAULT 600,
    ban_time INTEGER NOT NULL DEFAULT 1800,
    ignore_cidrs TEXT NOT NULL DEFAULT '192.168.0.0/16,10.0.0.0/8,172.16.0.0/12,127.0.0.0/8',
    enabled INTEGER NOT NULL DEFAULT 1,
    remark TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);
