-- 反代域名：Caddy 站点选项（JSON：{"encode":true,"security_headers":true,"log_access":true}）
ALTER TABLE rp_domains ADD COLUMN caddy_opts TEXT NOT NULL DEFAULT '';
