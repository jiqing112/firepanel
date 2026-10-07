-- 端口记录（台账）：记录本机端口的用途，与监听扫描联动展示

CREATE TABLE port_records (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,                    -- 服务/用途名
    proto TEXT NOT NULL DEFAULT 'tcp' CHECK(proto IN ('tcp','udp','both')),
    port INTEGER NOT NULL,                 -- 单端口 1-65535
    group_tag TEXT NOT NULL DEFAULT '',    -- 分组标签（web / 数据库 / docker …）
    remark TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);
