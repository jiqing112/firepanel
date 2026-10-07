# FirePanel API 文档

Base URL: `http://<host>:18088/api/v1`（与面板同源部署）
认证：除 `GET /bootstrap`、`POST /setup`、`POST /auth/login` 外，全部接口需要 JWT，优先用请求头 `X-Panel-Token: <token>`（与整站 Basic Auth 共存），兼容 `Authorization: Bearer <token>`。
Token 通过登录/初始化获取，有效期默认 12 小时。
若在设置页开启了整站 Basic Auth，所有请求（含登录）还需通过 HTTP Basic 认证（脚本示例：`curl -u user:pass ...`）。

## 通用约定

- 成功：`200`，JSON 对象。
- 失败：`4xx/5xx`，`{"error": "原因"}`。
- 变更类接口（POST/PUT/DELETE 规则资源）返回 `sync` 字段，表示内核同步结果：

```json
"sync": { "applied": true, "danger": false }
```

- `danger=true` 时表示触发了高危保护（涉及 SSH 端口 / 封禁管理 IP / 全网段封禁），
  变更已入库但**未生效**，需走危险确认流程（见下）。挂起期间其他变更会被拒绝（409）。

## 危险变更流程

1. 变更返回 `sync.danger=true` + `token` + `reason` + `delay_sec`（默认 60s）。
2. 到期自动生效，随后进入 `confirm_sec`（默认 90s）确认窗口。
3. 窗口内确认 → 保留；超时未确认 → 自动回滚到上一份规则。

```
POST /system/danger/confirm   {"token": "...", "stage": 1}   # 生效前：立即应用
POST /system/danger/confirm   {"token": "...", "stage": 2}   # 生效后：确认保留（取消自动回滚）
POST /system/danger/cancel    {"token": "..."}               # 撤销：数据库恢复变更前状态
```

## 认证与初始化

| 方法 | 路径 | 说明 |
|---|---|---|
| GET  | /bootstrap | 是否需要初始化 `{needs_setup}`（无需认证） |
| POST | /setup | 首次初始化 `{username, password}` → `{token, user}`（仅无用户时可用） |
| POST | /auth/login | 登录 `{username, password}` → `{token, user}`（每 IP 限 10 次/分） |
| GET  | /me | 当前用户 `{uid, username, role}` |
| GET  | /users | 用户列表（admin） |
| POST | /users | 创建用户 `{username, password, role: admin|viewer}`（admin） |
| DELETE | /users/:id | 删除用户（admin，不能删自己） |

## 系统与内核

| 方法 | 路径 | 说明 |
|---|---|---|
| GET  | /system/info | 主机/内核/后端/探测结果/漂移状态/`needs_setup` |
| GET  | /system/interfaces | 网卡枚举（NAT 出口下拉） |
| POST | /system/ip-forward/enable | 开启内核 IPv4 转发并写入 /etc/sysctl.conf 持久化（幂等） |
| GET  | /firewall/rules | 面板链内核快照（规则、标签、计数、链挂接状态） |
| GET  | /system/drift | 最近一次漂移报告（缓存） |
| POST | /system/drift/check | 立即执行漂移检测 |
| POST | /system/sync | 用面板期望状态覆盖内核（可能触发 danger） |
| GET  | /dashboard/summary | 仪表盘：连接数/速率/今日流量/命中排行/采样 |
| GET  | /system/backup?format=json\|iptables-save\|nft | 导出备份（下载） |
| POST | /system/restore | 从 JSON 备份恢复（全量替换转发与名单，可能触发 danger） |
| GET  | /audit?limit=200 | 操作审计（新→旧） |
| GET  | /settings/basicauth | 整站 Basic Auth 状态（密码不回传；`source`: file/config） |
| POST | /settings/basicauth | `{enabled, username, password?}`（password 留空保持原密码；立即生效）。凭据持久化在**程序目录 basicauth.yaml**（0600），手动编辑该文件后重启面板生效 |

## 端口转发 /forwards

| 方法 | 路径 | 说明 |
|---|---|---|
| GET  | /forwards | 列表 |
| POST | /forwards | 创建（见下） |
| PUT  | /forwards/:id | 更新 |
| PATCH | /forwards/:id/enabled | `{enabled: bool}` 启停 |
| DELETE | /forwards/:id | 删除 |

创建/更新字段：

```json
{
  "name": "Web 服务",
  "protocol": "tcp",            // tcp | udp | both
  "listen_port": "80",          // 单端口或 "30000-30010"
  "src_ip": "",                 // 可选来源限制（IP/CIDR）
  "dst_ip": "192.168.1.100",    // 与 dst_domain 二选一
  "dst_domain": "",             // 域名目标（DDNS 自动解析）
  "dst_port": "8080",           // 端口段转发时需与监听段等宽
  "enabled": true,
  "expires_at": "2026-12-31 23:59",  // 可选，到期自动停用
  "remark": ""
}
```

## IP 名单 /ip-lists

| 方法 | 路径 | 说明 |
|---|---|---|
| GET  | /ip-lists?type=black\|white | 列表 |
| POST | /ip-lists | 添加 `{list_type, ip_or_cidr, group_tag?, country?, expires_at?, remark?}` |
| PUT  | /ip-lists/:id | 更新标签/备注/过期时间 |
| DELETE | /ip-lists/:id | 删除（过期名单由后台每 30s 自动清理） |
| POST | /ip-lists/import | `{list_type, format: text|json, content}` 批量导入（# 为注释） |
| GET  | /ip-lists/export?type=&format=txt\|csv\|json | 导出 |

## 端口 /ports、/port-records

监听端口扫描与端口记录台账（纯台账，不参与内核规则生成）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET  | /ports/listening | 实时扫描监听端口：`{proto, port, address, process, pid, docker}`（需 iproute2 的 ss） |
| GET  | /traffic/overview | 流量监控快照：网卡速率 + 5 分钟趋势窗口 + TCP 连接级速率（2s 采样差分） |
| GET  | /port-records | 端口记录列表 |
| POST | /port-records | `{name, proto: tcp\|udp\|both, port, group_tag?, remark?}` |
| PUT  | /port-records/:id | 更新记录 |
| DELETE | /port-records/:id | 删除记录 |

## 反向代理 /proxy

| 方法 | 路径 | 说明 |
|---|---|---|
| GET  | /proxy/domains | 域名+路由+健康状态，`running` 为监听状态；`challenge` 为 ACME 挑战方式（空=自动） |
| POST | /proxy/domains | `{domain, tls_mode: auto|manual|none, challenge?: auto|http|alpn|dns, cert_pem?, key_pem?, remark?}` |
| PUT  | /proxy/domains/:id | 更新（含挑战方式；cert_pem/key_pem 留空保持原证书） |
| DELETE | /proxy/domains/:id | 删除（级联路由） |
| POST | /proxy/domains/:id/routes | `{path_match, upstream_url, ws_enabled?, health_path?}` |
| PUT  | /proxy/routes/:id | 更新路由 |
| DELETE | /proxy/routes/:id | 删除路由 |
| GET  | /proxy/env | 环境探测：80/443 可用性与占用进程、当前引擎与监听、推荐监听端口与挑战方式、Caddy 安装检测 |
| POST | /proxy/engine | 切换引擎与监听端口 `{engine: builtin\|caddy, http_addr?: ":端口", https_addr?: ":端口"}`，立即重建监听并持久化（重启保持）；切 caddy 需本机已安装 Caddy，切换时自动启停 Caddy 服务 |
| POST | /proxy/caddy/install | 一键安装 Caddy（按发行版选包管理器，Debian/Ubuntu 回退官方仓库；同步执行可达数分钟；装完自动停用其自启服务，由引擎切换拉起） |
| GET  | /proxy/caddyfile | 预览将下发的完整 Caddyfile（文本；含全局块/站点块/opts） |
| GET  | /proxy/caddy-global | Caddyfile 全局选项（email/debug） |
| POST | /proxy/caddy-global | `{email, debug}`，保存后自动重载 Caddy |
| GET  | /proxy/dns-config | DNS-01 凭据（token 脱敏） |
| POST | /proxy/dns-config | `{provider: cloudflare\|alidns\|dnspod, api_token, secret_key?}`，保存后热生效 |

变更后自动热重载；`tls_mode=manual` 需同时提供 PEM 证书与私钥。挑战方式说明：HTTP-01 需 80 可达（被占用时由持有者代答——本面板自身监听或寄生前置转发均可）；TLS-ALPN-01 需 443 由面板监听或 DNAT；DNS-01 无端口要求且泛域名必选，需先配置凭据；纯 IP 证书仅支持 HTTP-01/TLS-ALPN（shortlived profile）。证书到期由后台每 6 小时巡检，过期/3 天内到期发 `proxy_cert_error` 事件。

## 限速 /rate-limits

```json
{
  "name": "web-cc",
  "proto": "tcp",            // tcp|udp|both
  "port": "80",              // 空=全部端口
  "per_src": true,           // 按 IP（true）或总量（false）
  "rate": 30, "rate_unit": "minute",  // second|minute|hour
  "burst": 10,
  "conn_limit": 20,          // 0=不限连接；>0 生成独立连接数限制规则
  "enabled": true, "remark": ""
}
```

GET / POST / PUT /:id / DELETE /:id。超限动作：丢弃（INPUT 链）。

## NAT /nat-rules

```json
{ "name": "lan-out", "type": "MASQ", "src_cidr": "192.168.1.0/24",
  "out_iface": "eth0", "to_addr": "", "enabled": true, "remark": "" }
```

`type=SNAT` 时 `to_addr` 必填。GET 返回附 `ip_forward` 内核状态。

## 定时任务 /tasks

```json
{ "name": "夜间同步", "cron_expr": "0 3 * * *", "action": "sync", "target_id": 0, "enabled": true, "remark": "" }
```

`action`: `forward_enable` / `forward_disable`（需 `target_id` 为转发规则 ID）、
`iplist_expire`（清理过期名单并同步）、`sync`（全量同步）。

## 防爆破 /jails

| 方法 | 路径 | 说明 |
|---|---|---|
| GET  | /jails | Jail 列表（含窗口内命中计数 `hits`）+ 当前封禁 `banned` |
| POST | /jails | 创建（见下） |
| PUT  | /jails/:id | 更新（自动重载监听） |
| DELETE | /jails/:id | 删除 |
| DELETE | /jails/bans/:id | 手动解封（删除 jail 来源的黑名单条目并同步） |

```json
{
  "name": "SSH 爆破防护",
  "source_type": "journald",     // journald | file | audit（面板审计）
  "log_path": "",                // file 源必填
  "unit": "ssh",                 // journald 源的单元
  "match_regex": "(Failed password for|Invalid user).* from ([0-9]{1,3}([.][0-9]{1,3}){3})",
  "threshold": 5,                // 阈值（次）
  "find_time": 600,              // 统计窗口（秒）
  "ban_time": 1800,              // 封禁时长（秒）
  "ignore_cidrs": "192.168.0.0/16,10.0.0.0/8,172.16.0.0/12,127.0.0.0/8",
  "enabled": true, "remark": ""
}
```

封禁动作 = 写入黑名单（带 `expires_at`，到期 janitor 自动解封）→ 同步内核 → 广播
`jail_ban` 事件（可被告警渠道订阅）。

## 告警 /alerts

| 方法 | 路径 | 说明 |
|---|---|---|
| GET  | /alerts | 渠道配置列表 |
| POST | /alerts | `{channel: telegram|webhook|smtp, config: {...}, events: ["apply_fail",...], enabled}`（upsert） |
| DELETE | /alerts/:id | 删除渠道 |
| POST | /alerts/test | `{channel, config}` 发送测试通知（不落库） |

config 形态：

```json
// telegram
{ "bot_token": "123:ABC", "chat_id": "-100123" }
// webhook：POST JSON {type, message, detail, time, text}
{ "url": "https://hooks.example.com/x" }
// smtp
{ "host": "smtp.example.com", "port": 587, "username": "...", "password": "...", "from": "", "to": "ops@a.com,b@b.com" }
```

可订阅事件：`apply_fail`（应用失败）、`drift`（内核漂移）、`danger_pending`（高危待确认）、
`health_down`（反代上游不可达）、`rollback`（自动回滚）、`proxy_cert_error`（证书失败）。

## WebSocket /api/ws

连接：`/api/ws?token=<jwt>`。消息格式：

```json
{ "topic": "stats", "data": {...}, "ts": 1696500000000 }
```

- `stats`：约 5s 一次 `{ts, conn_count, rx_bps, tx_bps}`
- `events`：规则应用/漂移/危险/健康等实时事件
- `logs`：预留

客户端可发送 `{"action":"subscribe","topics":["stats","events"]}` 订阅。
