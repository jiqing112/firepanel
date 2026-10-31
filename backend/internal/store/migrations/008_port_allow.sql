-- 入站放行规则：在面板链 FPANEL_INPUT 里放行指定端口
--
-- 与 port_records（端口台账，纯记录）不同：这里的规则会真正下发到内核。
-- 用于 INPUT 默认策略为 DROP 的机器（部分 VPS 镜像、加固过的系统）上
-- 打开对外的服务端口 —— 面板此前只能拦，不能放。

CREATE TABLE port_allow_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,                              -- 规则名，如「Web 80」
    proto TEXT NOT NULL DEFAULT 'tcp' CHECK(proto IN ('tcp','udp','both')),
    port TEXT NOT NULL,                              -- 单端口 80 或端口段 8000-8010
    src_cidr TEXT NOT NULL DEFAULT '',               -- 空 = 任意来源；否则限定 IP/CIDR
    enabled INTEGER NOT NULL DEFAULT 1,
    remark TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);

CREATE INDEX idx_port_allow_enabled ON port_allow_rules(enabled);
