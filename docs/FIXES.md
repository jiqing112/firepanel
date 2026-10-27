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

## 新增测试

- `app`：变更闭包失败回滚、内核应用失败回滚、高危挂起态持久化 + 重启回滚
- `fw`：无端口限速的 nft 语法、`iptables-save -c` 计数前缀解析与挂接识别、IPv6 缺失时只维护 IPv4
- `api`：路径注入校验（换行/花括号/引号/反斜杠）、非法备份在写库前被拒、合法备份通过
- `store`：`ReplaceAll` 失败整体回滚、恢复保留原 ID
- `config`：`ssh_ports` 新键优先/旧键兜底/默认值
- `proxy`：Caddyfile 路径 matcher 同时匹配精确路径与子路径

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
