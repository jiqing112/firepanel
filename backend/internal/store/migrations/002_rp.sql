-- 阶段3：反向代理域名与路由

CREATE TABLE rp_domains (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    domain TEXT NOT NULL UNIQUE,
    enabled INTEGER NOT NULL DEFAULT 1,
    tls_mode TEXT NOT NULL DEFAULT 'auto' CHECK(tls_mode IN ('auto','manual','none')),
    cert_pem TEXT NOT NULL DEFAULT '',
    key_pem TEXT NOT NULL DEFAULT '',
    cert_status TEXT NOT NULL DEFAULT 'none',
    cert_error TEXT NOT NULL DEFAULT '',
    cert_expires_at TEXT,
    remark TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);

CREATE TABLE rp_routes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    domain_id INTEGER NOT NULL REFERENCES rp_domains(id) ON DELETE CASCADE,
    path_match TEXT NOT NULL DEFAULT '/',
    upstream_url TEXT NOT NULL,
    ws_enabled INTEGER NOT NULL DEFAULT 1,
    health_path TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    health_status TEXT NOT NULL DEFAULT 'unknown',
    health_checked_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);
CREATE INDEX idx_rp_routes_domain ON rp_routes(domain_id);
