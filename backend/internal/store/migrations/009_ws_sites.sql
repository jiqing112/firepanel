-- Web 服务站点（替代 rp_domains / rp_routes）。
-- 站点的引擎配置（targets / locations / health / rate_limit 等）为 JSON 存于 config 列，
-- 结构由 internal/webservice.Site 定义；证书与启停为独立列，便于查询与回写。

CREATE TABLE ws_sites (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL DEFAULT '',
    domains TEXT NOT NULL DEFAULT '[]',
    type TEXT NOT NULL DEFAULT 'proxy',
    enabled INTEGER NOT NULL DEFAULT 1,
    is_default INTEGER NOT NULL DEFAULT 0,
    config TEXT NOT NULL DEFAULT '{}',
    tls_mode TEXT NOT NULL DEFAULT 'none',
    challenge TEXT NOT NULL DEFAULT '',
    cert_pem TEXT NOT NULL DEFAULT '',
    key_pem TEXT NOT NULL DEFAULT '',
    cert_status TEXT NOT NULL DEFAULT 'none',
    cert_error TEXT NOT NULL DEFAULT '',
    cert_expires_at TEXT,
    remark TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);
