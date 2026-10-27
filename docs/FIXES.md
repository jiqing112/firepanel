# 本轮问题排查与修复（2026-10-27）

- 范围：可交付性（干净检出能否构建）、高危变更/防锁死、防火墙后端正确性、恢复与回滚的数据安全、部署脚本、认证与接口边界、测试
- 基线：`go build ./...`、`go vet ./...`、`go test ./...`、`cd frontend && npm run check && npm run build` 全部通过
- 结论：此前的两个阻断项（干净检出不可构建、未完成的高危变更可被重启绕过）已修复；另修复 1 个 nft 语法错误（会导致整批规则下发失败）、1 个 iptables 命中计数恒为 0、1 个 IPv6 缺失导致面板无法启动、以及一批恢复/部署/接口问题

## P0 交付与安全

| # | 问题 | 后果 | 修复 |
|---|---|---|---|
| 1 | 关键源码只在本地工作区，`backend/internal/web/dist` 不入库 | `HEAD` 干净检出 `go build` 直接失败（`//go:embed all:dist`），他人克隆无法构建 | 提交全部源码；`dist/.gitkeep` 占位 + `.gitignore` 例外；CI 增加「干净检出构建」 |
| 2 | 高危变更挂起态只在内存，数据库已先写入新期望态 | 延时/确认窗口内重启、`systemctl restart` 或主机重启后，启动重放会**跳过二次确认直接生效**（可锁死 SSH） | 挂起态 + 变更前快照持久化到 settings(`pending_danger`)；启动时先回滚到变更前状态再重放；确认/撤销/失败后清除标记 |
| 3 | `api/restore.go` 带 UTF-8 BOM | `go test -cover` 构建失败，覆盖率/门禁工具链被打断 | 去 BOM |
| 4 | 撤销已生效的高危变更返回 `cancelled:false` | 确认窗口内只能等超时自动回滚，无法主动回滚 | `CancelDanger` 对已生效变更执行立即回滚 |

## P1 功能正确性

| # | 问题 | 后果 | 修复 |
|---|---|---|---|
| 5 | nft 后端对「有协议、无端口」的限速规则生成 `tcp dport `（空端口） | nft 解析失败 → **整批规则**（含所有正常转发）都下发不了 | 无端口时用 `meta l4proto <proto>`；有端口才用 `<proto> dport` |
| 6 | `iptables-save` 未带 `-c` | 每条规则命中计数恒为 0，仪表盘命中排行与规则视图计数无意义 | 带 `-c` 采集；解析兼容 `[pkts:bytes]` 前缀（含挂接检测） |
| 7 | IPv6 不可用时 `Init/Apply` 直接失败 | 纯 IPv4 / 无 ip6tables 的系统上面板**起不来** | 启动探测 ip6tables；缺失时只管理 IPv4 |
| 8 | `Desired()` 读库失败时按空集合处理 | 一次数据库抖动就可能把内核里的转发/黑名单全部清空 | 读取失败即返回错误，`Sync` 中止（不再下发空规则） |
| 9 | `ExportBundle` 吞掉读取错误 | 回滚快照可能残缺，失败恢复时把缺失数据当「本来没有」删掉 | 返回错误；`Mutate` 取快照失败直接取消变更 |
| 10 | 失败回滚同步内核时未解析域名 | 域名目标（DDNS）的转发规则回滚后从内核消失 | 回滚前先 `ResolveDomains` |
| 11 | 恢复备份后不重载反代/定时任务/告警/防爆破 | 恢复成功但配置要等重启才生效（或永不生效） | `App.ReloadAux()`；恢复成功与危险撤销后触发 |
| 12 | 配置 `server.ssh_ports`（旧键）无条件覆盖 `firewall.ssh_ports` | 按 README 配 `firewall.ssh_ports=[2222]` 的用户实际仍只保护 22，防锁死失效 | 新键优先、旧键仅兜底，冲突时启动告警；示例配置改正 |
| 13 | FORWARD 放行规则在「目标端口留空」时无端口匹配 | 放行到目标 IP 的所有端口，比实际转发范围宽 | 使用实际生效的目标端口（缺省回退监听端口） |
| 29 | nft 后端 `Apply` 只用 `flush table`：规则被清但**命名 set（nft meter）残留** | meter 名由规则 id 派生，删除规则后再建同 id 规则会复用同名旧 set，里面残留的每 IP 限速元素立即生效——**实测可把管理 IP 按旧速率限流**（线上主机残留了 `192.168.68.3 limit rate over 30/minute`） | 改为单事务「删表 + 重建表/链 + 全部规则」（`nft -f` 原子，无中间态；只动面板自有表）；表被外部删除时自动 Init 重试 |
| 30 | IPv6 转发目标端口拼接错误：`::1:9000` | ip6tables/nft 都把 `::1:9000` 解析成**地址** `::0.1.144.0`（端口丢失），且不报错；另外注入防御把 `[]` 判为非法字符，修复格式后会直接 500 | `dnatTarget` 对 IPv6 生成 `[addr]:port`；注入防御仅 `to` 字段放行方括号；`portOfTo`/漂移比较兼容方括号 |

## P2 部署 / 接口 / 可用性

| # | 问题 | 修复 |
|---|---|---|
| 14 | Docker 用 bridge 网络 + 端口映射：容器内 iptables/nft 只作用于容器 netns，宿主防火墙根本不受管 | `network_mode: host` + `pid: host`；移除 `ports`；镜像补 `iproute2`（`ss`）与 healthcheck；去掉从未使用的降权用户 |
| 15 | `install.sh` 只插一条临时 iptables 规则，重启后丢失，面板失联 | ufw/firewalld 优先；iptables 顺带发行版持久化；新增 `firepanel-bootstrap.service` 每次开机在面板启动前重放 |
| 16 | 面板监听失败时进程以 0 退出 | 以非零码退出，systemd `Restart=on-failure` 才会重试 |
| 17 | WebSocket 把 JWT 放查询串 | 改用 `Sec-WebSocket-Protocol: fp.<JWT>`；同时按数据库校验账号有效性（删除账号立即失效） |
| 18 | 反代路由路径只在存储层校验，API 返回 500 或延迟到 Caddyfile 下发才失败 | API 边界统一用 `validPathMatch`（400），含健康检查路径 |
| 19 | 定时任务“更新”不校验动作/目标 | 更新与创建共用校验，非法动作不再入库 |
| 20 | IP 名单“更新”只改库不同步内核（改过期时间=立即解封不生效） | 走 `Mutate` 同步；非法时间返回 400 |
| 21 | 反代所有请求都走互斥锁（读多写少） | 改 `RWMutex`，请求路由与证书查询并发 |
| 22 | SMTP `UseTLS` 字段被忽略（465 永远发不出去） | 支持隐式 TLS（465）与 STARTTLS（587），多收件人去空白 |
| 23 | 未挂路由的旧实现（`Setup/Login/CreateUser`、旧 `Restore`、旧 `Backup`）与新实现并存 | 删除，避免误接线路造成弱口令/先清空后校验等回退 |
| 24 | 30+ Go 文件未 gofmt；CI 无格式/vet/类型检查 | 全量 gofmt；CI 增加 `gofmt -l`、`go vet`、`svelte-check`、干净检出构建 |
| 25 | `.gocache`（265MB）、`.workbuddy-ai`、`.zwork` 未忽略；无 `.gitattributes` | 补 ignore（含 Docker 构建上下文）；脚本/源码固定 LF |
| 26 | `build.sh` 只产出 amd64，版本号恒为 dev | 支持 `arm64`，注入 `-X main.version`（可用 `FIREPANEL_VERSION` 覆盖） |
| 27 | Basic Auth 凭据写在二进制同目录 | Docker 下写在 `/usr/local/bin`（镜像层，容器重建即丢失，整站防护悄悄失效）；改为**数据目录** `basicauth.yaml`，旧版程序目录文件仍兼容读取 |
| 28 | `EnsureCaddyService` 不检查服务是否真的起来 | 切换 caddy 引擎时 UI 显示成功、实际未运行；改为 `is-active` 校验并给出 journalctl 提示 |
| 31 | caddy 模式下每次面板启动/切引擎都重新备份并 reload，即使 Caddyfile 内容完全没变 | 线上实测每次启动多一份 `.bak-*`、Caddy 被无谓重载；`DeployGenerated` 在合并结果与磁盘一致时跳过写盘/备份/重载，并记录日志 |

## 前端交互修复（只读审计 + 逐条核实）

| # | 问题 | 修复 |
|---|---|---|
| 32 | 任意接口 401 只清 token 不清 `session.user`：路由守卫把用户从 `/login` 弹回 `/dashboard`，立刻又 401 → **无限跳转/请求风暴** | 401 时调用会话失效回调清空内存用户；登录/初始化接口的 401 视为业务错误，保留「用户名或密码错误」原文 |
| 33 | 任务表单里 Select 把 `target_id` 写成字符串 → 后端 `int64` 解码失败，创建"启停转发"任务**必 400** | Select 用 `onValueChange` 转 `Number`，提交时再 `Number()` 兜底 |
| 34 | 备份恢复「选中文件即全量替换」，无确认；viewer 也能点 | 先解析并展示将替换的条目数，AlertDialog 二次确认后才提交；非管理员禁用入口 |
| 35 | 防火墙规则页导出用裸 `<a href>`，不带 `X-Panel-Token` → 必然 401 | 改用带鉴权的 `downloadFile()`，失败给出 toast |
| 36 | 反代域名启停开关只发 `{enabled}`，后端无条件 `d.Remark = req.Remark` → **备注被清空并落库** | 后端 `remark` 改为指针：仅显式传入才更新；显式传空串仍可清空 |
| 37 | 编辑转发/NAT/限速时硬编码 `enabled: true` → 保存备注也会把已停用规则**静默重新启用** | 编辑时保留原启停状态 |
| 38 | 前端密码下限 6 位、后端 8 位 → 初始化向导输 7 位被 400 挡住 | 前后端统一 8 位，文案同步 |
| 39 | 防爆破「剩余封禁」是快照，不走秒；封禁到期后仍留在列表 | 1 秒定时推进 `now`；检测到有封禁到期即重新拉取 |
| 40 | SMTP 默认「587 + 隐式 TLS」且无 UI 开关 → 587/STARTTLS 邮箱测试必失败 | 默认 STARTTLS（587），端口 465 自动切隐式 TLS，新增开关与发件人字段 |
| 41 | IP 名单导出用 `Authorization: Bearer`，开启整站 Basic Auth 时会覆盖浏览器 Basic 凭据 → 401 | 统一走 `downloadFile()`（`X-Panel-Token`） |
| 42 | HTTP 明文下面板没有 `navigator.clipboard`，复制按钮抛错且无提示 | 检测剪贴板可用性并提示「需 HTTPS 或 localhost，请手动复制」 |

## 新增测试

- `app`：变更闭包失败回滚、内核应用失败回滚、高危挂起态持久化 + 重启回滚
- `fw`：无端口限速的 nft 语法、nft 整表重建脚本（删表+建链，不再 flush）、IPv6 DNAT 目标 `[addr]:port` 渲染与注入白名单、端口提取/漂移比较兼容方括号、`iptables-save -c` 计数前缀解析与挂接识别、IPv6 缺失时只维护 IPv4
- `api`：路径注入校验（换行/花括号/引号/反斜杠）、非法备份在写库前被拒、合法备份通过
- `store`：`ReplaceAll` 失败整体回滚、恢复保留原 ID
- `config`：`ssh_ports` 新键优先/旧键兜底/默认值
- `proxy`：Caddyfile 路径 matcher 同时匹配精确路径与子路径；内容未变化时跳过写入/备份/重载

## 线上实测（Debian 13 / 192.168.68.28）

在局域网真实主机（Debian 13 trixie，内核 6.12，systemd 257，nftables 后端）上做了**原地升级 + 全流程实测**：

- 升级：旧版（10/15 构建，无 `-version`）→ 本分支构建；保留原 DB，服务启动、启动重放、漂移检测正常
- 累计 84 项断言通过（分多轮）：
  - 认证/WS：登录、`/me`、系统信息；WS 子协议握手 101 且回显子协议、查询串兼容、非法令牌 401
  - 转发：创建 18099→本机 9000 的 DNAT；**从另一台机器实际连入，TCP 握手成功，nft 命中计数 = 3**
  - 限速：无端口（全协议）限速规则下发成功，内核为 `meta l4proto tcp`（旧构建此处整批失败）
  - 名单：创建下发、改过期时间为过去后**即时从内核移除**、删除
  - 漂移：手工 `nft delete rule` 后检测到 missing，一键同步恢复
  - 防锁死：SSH 端口转发进入高危队列 → 数据库已写入但内核无规则 → **`systemctl restart` 后自动回滚**（规则消失、`pending_danger` 清除、日志提示）
  - 恢复：非法备份 400 且数据零变化；自身备份导出/恢复成功（备份不含 users）
  - Basic Auth：运行中开启后无凭据 401 / 带凭据 200，凭据落在数据目录；关闭后恢复
  - 内置反代：切到 builtin（:18085/:18445）→ 建域名/路由 → **真实 HTTP 请求经反代拿到上游内容**；重启后自动恢复
  - WS 实际帧：子协议连接后收到 `stats` 推送（非仅握手 101）
  - 域名目标转发：`dst_domain=localhost`（DNS 解析路径）创建成功，重启重放后内核规则仍在
  - IPv6：目标 `::1` 的转发下发成功，内核为 `dnat ip6 to [::1]:9000`（修复后）
  - 告警：本地 webhook 接收器收到「测试通知」与真实 `danger_pending` 事件
  - iptables 后端：独立实例（`-backend iptables`）建 v4/v6 规则，入向流量后 `/firewall/rules` 计数 15 包（验证 `-c` 修复）；停止后 FPANEL 链全部清理干净
  - 清理后内核表只剩空链（**无残留 meter 集合**），DB 只剩原 admin、零规则
- 实测发现并修复：第 29 条「meter 集合残留复用旧限速状态」、第 30 条「IPv6 DNAT 目标被当成地址」、第 31 条「Caddyfile 每次启动都备份+重载」
- Caddyfile 迁移验证：旧版「完全接管」生成物被识别为 legacy → 自动 `.fp-orig` + `.bak-时间戳` 备份 → 仅改写托管段；Caddy 侧配置仍为空且服务正常

## 验证命令

```bash
cd backend && go build ./... && go vet ./... && go test ./...
gofmt -l ./cmd ./internal        # 空
cd frontend && npm run check && npm run build
git worktree add --detach /tmp/fp-clean HEAD
cd /tmp/fp-clean/backend && CGO_ENABLED=0 go build ./...   # 干净检出可构建
```

## 仍然存在（已知取舍，已在 README / API 文档标注）

- JSON 备份不含用户与设置（JWT 密钥、Basic Auth、DNS-01 凭据）；反代手动证书会连同私钥写入备份，按密钥保管
- JWT 存 localStorage（当前无 `{@html}`，CSP 已收紧）；更稳妥是 HttpOnly Cookie + CSRF
- SQLite 单连接（`SetMaxOpenConns(1)`）：恢复大事务期间 API 会短暂排队
- nft-only 且 INPUT 策略为 drop 的主机：安装脚本的放行规则可能被宿主自己的 base chain 覆盖，需在宿主防火墙配置中显式放行面板端口
- 修改面板端口只做试绑校验与 UI 告警，防火墙/安全组放行需用户自己先做（否则重启后无法从浏览器进入）
- viewer 的写操作只由后端 403 兜底，前端按钮未按角色隐藏/禁用（可点击但会提示「需要管理员权限」）；属交互体验问题，不影响数据安全
