-- Web 服务监听器（gately 式监听模型）。
-- 监听的端口/ TLS/ 明文处理为 JSON 存于 config 列；站点挂在 listener_id 下。
-- 旧反代数据迁移时自动创建默认监听（HTTPS 端口 + 额外 HTTP 端口）。

CREATE TABLE ws_listeners (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    config TEXT NOT NULL DEFAULT '{}',
    remark TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);

ALTER TABLE ws_sites ADD COLUMN listener_id INTEGER NOT NULL DEFAULT 0;
