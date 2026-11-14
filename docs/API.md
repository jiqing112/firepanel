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
POST /system/danger/cancel    {"token": "..."}               # 撤销：未生效则丢弃；已生效则立即回滚
```

- 登录限速按「来源 IP + 用户名」双维度计数（伪造 `X-Forwarded-For` 无效，仅信任 `server.trusted_proxies`）。
- 挂起状态与变更前快照会持久化到 settings (`pending_danger`)：进程/主机在延时或确认窗口内重启时，
  启动阶段会**自动回滚到变更前状态**（未确认的危险变更视为未发生），避免绕过二次确认。

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
| GET  | /system/backup?format=json\|iptables-save\|nft | 导出备份（下载）。JSON 含转发/名单/限速/NAT/定时任务/告警/防爆破/台账/Web 服务站点；不含用户与设置（含 JWT 密钥、DNS-01 凭据） |
| POST | /system/restore | 从 JSON 备份恢复（全量替换上述结构化配置，可能触发 danger）。先全量校验再单事务写入；旧版反代备份自动转换为站点；成功后热重载站点/定时任务/告警/防爆破 |
| GET  | /audit?limit=200 | 操作审计（新→旧） |
| GET  | /settings/basicauth | 整站 Basic Auth 状态（密码不回传；`source`: file/config） |
| POST | /settings/basicauth | `{enabled, username, password?}`（password 留空保持原密码；立即生效）。凭据持久化在**数据目录 basicauth.yaml**（0600；旧版程序目录文件仍兼容读取），手动编辑后约 2 秒热生效 |

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

## Web 服务 /ws

站点模型（gately 式）：站点 = 域名列表 + 类型 + 引擎配置。类型：`proxy`（反向代理）/ `redirect`（重定向）/ `static`（静态文件）/ `text`（固定文本）/ `close`（断开连接，444）。引擎配置（targets / locations / health / rate_limit / timeouts / headers 等）与后端 `webservice.Site` 结构一致，字段见下。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET  | /ws/sites | 全部站点（含 listener_id） |
| POST | /ws/sites | 创建站点：`{listener_id, domains: [], type, targets?, locations?, ...}` |
| GET  | /ws/listeners | 监听列表（含站点计数） |
| POST | /ws/listeners | 创建监听 `{name?, listen_ip?, port, tls, min_tls?, plain_http?, http_port?, http_port_mode?, remark?, enabled?}` |
| PUT  | /ws/listeners/:id | 更新监听 |
| PATCH | /ws/listeners/:id/enabled | `{enabled}` 启停 |
| DELETE | /ws/listeners/:id | 删除（有站点时拒绝） |
| GET  | /ws/topology | 拓扑快照：`?focus=<节点id>&ip=<访客IP>`，返回监听/站点/后端的速率、累计、健康与活动 IP；`focus` 指定节点时附 60 秒曲线 |
| PUT  | /ws/sites/:id | 全量更新（cert_pem/key_pem 留空保持原证书） |
| PATCH | /ws/sites/:id/enabled | `{enabled}` 启停开关 |
| DELETE | /ws/sites/:id | 删除 |
| GET  | /ws/status | 引擎状态快照：每站点 requests/active/errors/blocked/qps/rate60 + 每后端 healthy/conns/requests/bytes |
| GET  | /ws/env | 环境探测：80/443 可用性与占用进程、当前监听、推荐端口与挑战方式 |
| POST | /ws/listen | 监听调整 `{http_addr?: ":端口", https_addr?: ":端口", http_mode?: serve\|redirect}`，持久化并在重载后生效 |
| GET  | /ws/dns-config | DNS-01 凭据（token 脱敏） |
| POST | /ws/dns-config | `{provider: cloudflare\|alidns\|dnspod, api_token, secret_key?}`，保存后热生效 |
| POST | /ws/selfsign | 为 IP/域名生成自签证书（ECDSA P-256，10 年） |

反向代理站点的关键字段：

- `targets`：后端列表，每项 `http(s)://host[:port] [weight=N] [backup]`；多项时 `lb_policy` 可选 `random` / `ip_hash` / `least_conn`（空=加权轮询）
- `health`：`{enable, mode: http|tcp, path, interval, timeout, fails, passes}`；未启用时仍有被动检查（连续 3 次建连失败摘除 30 秒）
- `locations`：分路径分流 `[{prefix, targets, strip_prefix}]`，最长前缀优先
- `forwarded_headers`：向后端写入 X-Real-IP / X-Forwarded-*（访客自带的同名头一律剥除）
- `timeouts`：`{connect, tls_handshake, response_header}`（秒，0=默认）
- `rate_limit`：`{enable, rate, burst, max_conns}` 按 IP 限流
- `ip_mode` / `ip_list`：站点级白名单/黑名单（IP 或 CIDR）
- `request_headers` / `response_headers`：`"Key: Value"` 行，值为空表示删除该头
- 建连失败自动换下一个后端重试；配置变更热重载，不断开已有连接，统计跨重载沿用

证书说明：HTTP-01 需 80 可达（被占用时由持有者代答——本面板自身监听或寄生前置转发均可）；TLS-ALPN-01 需 443 由面板监听或 DNAT；DNS-01 无端口要求且泛域名必选；纯 IP 证书仅支持 HTTP-01/TLS-ALPN（shortlived profile，约 6.7 天自动续期）。证书到期由后台每 6 小时巡检，过期/3 天内到期发 `proxy_cert_error` 事件。
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
`health_down`（上游不可达）、`rollback`（自动回滚）、`proxy_cert_error`（证书失败）。

## WebSocket /api/ws

连接：`/api/ws?token=<jwt>`。消息格式：

```json
{ "topic": "stats", "data": {...}, "ts": 1696500000000 }
```

- `stats`：约 5s 一次 `{ts, conn_count, rx_bps, tx_bps}`
- `events`：规则应用/漂移/危险/健康等实时事件
- `logs`：预留

客户端可发送 `{"action":"subscribe","topics":["stats","events"]}` 订阅。
