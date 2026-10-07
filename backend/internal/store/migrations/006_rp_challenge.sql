-- 反代域名：ACME 挑战方式（''=auto 按探测自动选；http / alpn / dns 为显式指定）
ALTER TABLE rp_domains ADD COLUMN challenge TEXT NOT NULL DEFAULT '';
