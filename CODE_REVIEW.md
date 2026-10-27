# FirePanel 代码审查报告

> **状态更新（2026-10-27）**：本报告中的 P0/P1 已在本轮修复（含干净检出的可构建性、Docker host 网络、高危变更持久化、nft/iptables 正确性等）。原文保留作为历史记录，修复清单与验证方式见 [docs/FIXES.md](docs/FIXES.md)。

- 审查日期：2026-10-27
- 审查对象：`firepanel`（Go 后端 + Svelte 5 前端 + SQLite，Linux 防火墙面板）
- 审查范围：构建与可复现性、并发/事务、防火墙与反代核心逻辑、认证与授权、部署脚本、文档一致性、测试与工程卫生
- 结论：**设计思路扎实，但当前 HEAD 不具备可发布状态。** 有 4 个阻断级问题（干净检出无法构建、恢复备份可能清空规则、Docker 下防火墙不生效、高危变更对域名目标失效），以及若干安全与工程缺口。

## 0. 验证方式与基线

| 项目 | 命令 | 结果 |
|---|---|---|
| 后端编译（工作区） | `cd backend && go build ./...` | 通过 |
| 静态检查 | `go vet ./...` | 无输出 |
| 单元测试 | `go test ./...` | 全部通过 |
| 覆盖率 | `go test -cover ./...` | api 1.1%、store 23.6%、jail 22.0%、proxy 46.6%、fw 53.4%、sysinfo 56.5%；app/config/scheduler/notify/monitor 无测试 |
| 格式 | `gofmt -l ./cmd ./internal` | **28 个文件未格式化** |
| 前端类型检查 | `cd frontend && npm run check` | 0 errors / 0 warnings |
| 干净检出编译 | `git worktree add --detach <tmp> HEAD` + `go build ./...` | **失败**（见 P0-1） |

工具链：Windows + Go 1.22.10（`go.mod` 要求 1.26.0，自动切换工具链）、Node 24.20、npm 11.19。

## 1. P0 阻断问题

### P0-1 干净检出无法构建（两处独立原因）

**(a) 后端 embed 目录不入库**

`backend/internal/web/embed.go:8` 使用 `//go:embed all:dist`，但 `backend/internal/web/dist` 未被 Git 跟踪，且根 `.gitignore` 忽略了 `dist/`。在 `HEAD` 的干净 worktree 中实测：

```
internal\web\embed.go:8:12: pattern all:dist: no matching files found
```

`README` 的「开发」章节让用户直接 `cd backend && go test ./...`，在新克隆上必然失败。CI 能过只是因为它在构建前手工 `cp -r frontend/dist backend/internal/web/dist`。

**(b) 已提交代码引用未跟踪文件**

`frontend/src/lib/components/layout/Topbar.svelte:7`（已提交）导入 `$lib/stores/fwinfo.svelte`，但 `frontend/src/lib/stores/fwinfo.svelte.ts` 是未跟踪文件（`git ls-files` 无记录，`HEAD` worktree 中不存在）。因此干净克隆的前端构建会报模块解析失败，CI 的 `npm run build` 同样会失败。当前只有本机工作区能构建。

**修复**
- 提交 `fwinfo.svelte.ts`；
- 为 embed 目录提供可入库的占位（如 `backend/internal/web/dist/.gitkeep`，并把 `//go:embed all:dist` 改为仅在发布构建时校验），或在 `README`/`Makefile` 中明确「先 `scripts/build.sh`，再跑后端测试」；
- CI 增加一步「干净检出构建」校验，避免再次发生。

### P0-2 恢复备份可能清空规则且不回滚（数据丢失）

`backend/internal/api/handlers_proxy.go:772` 的 `Restore`：

1. 在 `App.Mutate` 闭包内先**删除全部**转发规则与黑白名单（约 790-801 行）；
2. 再逐条校验并重建（805、819 行）；
3. 任一条非法即 `return err`。

而 `backend/internal/app/app.go:582`：

```go
snap := a.snapshotRows()
if err := mutate(); err != nil {
    return nil, err        // 不回滚
}
```

`Mutate` 的注释写「失败恢复」，但**闭包自身出错时直接返回，不执行 `restoreRows(snap)`**。结果：用户上传一个含 1 条非法 IP 的备份文件，数据库已被清空/半恢复，内核仍是旧规则，界面只显示 500。

**修复**
- `Restore` 先做**全量校验**（纯内存），通过后再写入；
- `Mutate` 在 `mutate()` 失败时同样回滚；
- 更彻底：让 `Mutate` 的数据库部分跑在单个 SQL 事务里（当前 `store.Store` 未暴露事务接口，`restoreRows` 是逐条 delete/create，且忽略错误）。

### P0-3 Docker 部署下防火墙功能不生效（与文档矛盾）

`deploy/docker-compose.yml` 只声明了 `cap_add: NET_ADMIN/NET_RAW`，没有 `network_mode: host`。容器拥有自己的 network namespace：

- 面板在容器内创建的 `iptables`/`nft` 规则只作用于**容器 netns**，不会管理宿主防火墙；
- 80/443 由 Docker 的端口映射接管，不是面板反代在监听；
- 因此「统一管理宿主 iptables/nftables」这一核心卖点在 Docker 方式下不成立。

`docker-compose.yml:3` 与 `README`「容器需要 NET_ADMIN 能力操作宿主 netfilter」的说法具有误导性。

附带问题：
- 运行镜像未安装 `iproute2`（`ss`）和 `caddy`，容器内「端口台账/连接级流量」「Caddy 引擎」不可用；
- `deploy/Dockerfile:15` 创建了 `fp` 用户但从未 `USER fp`，实际以 root 运行（功能性上必要，但文档/代码给人「已降权」的错觉）；
- 无 healthcheck。

**修复**：要么 `network_mode: host`（并移除 `ports`），要么在文档中把 Docker 明确定位为「仅容器内 netns 演示」，同时补 `iproute2`。

### P0-4 高危变更对「域名目标」规则失效

`backend/internal/fw/reconcile.go:136`：

```go
Staged: mustJSON(st),   // st 是未解析的期望态，域名目标的 DstIP 仍为空
```

`applyPending`（167 行）直接 `BuildRules(&st)`；而 `translate.go:105` 对 `f.DstIP == ""` 的转发规则 `continue`（跳过）。结果：

- 涉及 SSH 端口 + 域名目标的变更进入危险队列；
- 到期「生效」时该条 DNAT 根本没下发；
- 但 `lastGood` 已更新、界面显示「高危规则已生效」，用户以为已放行/已封禁。

**修复**：`Staged` 存 `resolved` 后的状态，或在 `applyPending` 内重新 `ResolveDomains` 再 `BuildRules`。

## 2. P1 高优先级

### P1-1 变更编排没有串行化，也不是事务

`app.go:577` 的 `Mutate`：先读快照 → 写库 → 同步内核。全流程没有互斥锁（`a.mu` 只保护 `drift`/`pendingSnap` 等字段），两个并发写请求可以：

1. 都通过 `HasPendingDanger()` 检查；
2. 各自拿到旧快照；
3. A 成功、B 失败 → B 的 `restoreRows` 删掉 A 新建的行。

另外 `restoreRows`（app.go 中）忽略所有 `error`，且 `CreateForward` 等不保留原 ID，恢复后审计/关联引用会错位。

**修复**：`sync.Mutex` 串行化 `Mutate`（规则量级小，代价可接受）；`restoreRows` 记录并上报失败；写库改为事务。

### P1-2 last-good 快照是死代码

`app.go:601-612` 的 `saveSyncOK` 只把**旧值**写回：

```go
lastGood := ""
if prev != nil { lastGood = prev.LastGoodJSON }
... LastGoodJSON: lastGood
```

`sync_state.last_good_json` 永远为空（`res` 参数也未被使用）。同一函数还把 `DriftJSON` 固定写成 `""`。数据库里「上一份成功应用的期望状态快照（回滚用）」这个设计没有落地。

**修复**：写入本次成功后的 `Desired()` JSON；或删除该字段与注释，避免误导。

### P1-3 危险变更失败后永久挂起

`reconcile.go:167` 的 `applyPending`：`backend.Apply` 失败时仅 `emit(apply_fail)` 后返回，**不清 `rc.pending`**。此后 `PendingExists()` 永远为 true：

- 所有写接口返回 409「存在待确认的高危变更」；
- 延时 watchdog 已触发、不会重试；
- 只能人工调用 `/system/danger/cancel` 恢复。

`rollbackLocked` 失败时同样保留 `pending`。建议失败时清空挂起状态并广播明确事件。

### P1-4 认证与审计可被伪造 IP 绕过

`server.go` 未设置 `SetTrustedProxies`，Gin 默认信任所有代理，`c.ClientIP()` 会采用客户端自带的 `X-Forwarded-For`：

- 登录限速（`auth.go` 的 `rateLimitLogin`，每 IP 10 次/分）可被伪造头绕过；
- 审计日志中的 `IP` 字段可被伪造；
- 面板若直接暴露公网，暴力破解防护形同虚设。

**修复**：`r.SetTrustedProxies([]string{"127.0.0.1"})` 或按部署配置可信代理；登录限速改为「IP + 用户名」双维度；用户名不存在时执行一次 dummy bcrypt，消除枚举时间差。

### P1-5 测试覆盖与危险路径完全不匹配

- `internal/api` 覆盖率 **1.1%**（仅有 jail 相关一个小测试），而认证、授权、Restore、危险确认都在这里；
- `internal/app`（Mutate/快照/回滚）、`config`、`scheduler`、`notify`、`monitor` 无任何测试；
- 前端无测试、无 lint 配置（`npm run check` 只做类型检查）。

对一个以 root 运行、直接改内核防火墙的面板，这是最高风险项之一。建议至少覆盖：`Mutate` 失败回滚、Restore 非法输入、危险变更两阶段确认、`requireAdmin` 全路由矩阵。

### P1-6 CI 未做质量门禁

`.github/workflows/ci.yml` 只跑前端 build + `go test` + `go build`：

- 无 `gofmt -l`、`go vet`、`staticcheck`；
- 无前端测试；
- 当前仓库 **28 个 Go 文件未 gofmt**（含 `app.go`、`reconcile.go`、`proxy.go`、`server.go` 等核心文件），说明本地也没有统一格式钩子。

## 3. P2 中优先级

1. **示例配置的 `ssh_ports` 不生效**：`deploy/config.example.yaml:48` 把它放在 `server:` 下，而代码读取 `firewall.ssh_ports`（`config.go:57`）。`yaml.v3` 默认忽略未知字段，用户按示例改成 2222 后，防锁死保护仍只认 22；`README:202` 的表格与示例自相矛盾。
2. **面板端口放行不持久**：`deploy/install.sh:96-105` 用 `iptables -I INPUT 1` 或临时 nft 表放行 18088，但既不 `iptables-save` 也不写 `/etc/nftables.conf`。重启后规则丢失，而 README 所说的「启动重放」只覆盖数据库里的期望态，不含这条 bootstrap 规则 → 可能把自己锁在门外。另外 nft 回退链 `priority -200` 的 `accept` 不能覆盖后续 base chain 的 `drop`（nft 的 accept 不跨链终局），在 firewalld/ufw 环境下未必生效。
3. **Caddyfile 路径注入面**：`handlers_proxy.go:276-283` 只校验 `PathMatch` 以 `/` 开头，未拒绝换行/`{`/`}`；`caddyfile.go` 把它原样拼进 `reverse_proxy <path>/* <dial>`。仅 admin 可触发，但可跳出站点块篡改整份配置，违背项目「结构化输入 + argv 防注入」的既有原则。
4. **Caddy 备份保留 off-by-one**：`caddydeploy.go:226` 用 `tail -n +11` 清理，实际保留 11 份而非 `caddyBackupKeep=10`；`cp` 落盘非原子；`sh -c` 字符串拼接与项目「argv 数组传参」约定不一致（当前路径是常量，暂无注入）。
5. **「JSON 全量备份」名不副实**：`handlers_system.go:251-263` 只导出 `forward_rules` 与 `ip_lists`，`README:18` 称「全量备份/恢复」。NAT、限速、反代域名/路由、定时任务、告警、用户、设置、端口台账都不在包内；跨机恢复会静默丢配置。
6. **WebSocket 细节**：token 走查询串（`ws.go:59`）会落在代理访问日志；任何已认证用户默认订阅 `stats/events/logs`（`ws.go:72`），`subscribe` 接受任意 topic 名导致每连接 map 无界增长；`authFromQuery` 不校验用户是否已被删除（与 HTTP 侧「以数据库为准」不一致）。
7. **安全响应头不完整**：面板自身只有 `X-Content-Type-Options`/`X-Frame-Options`，缺 HSTS、Referrer-Policy、CSP；HSTS 只在 Caddy 托管的站点块里补。
8. **JWT 存 localStorage**（`api.ts:16`）：一旦出现 XSS 即可窃取；当前未发现 `{@html}`，风险可控，但更稳妥是 HttpOnly Cookie + CSRF token。
9. **密码策略偏弱**：`Setup`/`CreateUser` 只要求 6 位，而面板默认监听公网端口。
10. **请求体无统一上限**：多数 `ShouldBindJSON` 端点未限制 body 大小（仅 Restore 8MB、Caddyfile 512KB），大 JSON 可造成内存压力。
11. **文件过长/职责错位**：`handlers_proxy.go` 796 行（其中含系统 Restore）、`proxy.go` 715 行、`app.go` 680 行；建议按资源拆分。

## 4. P3 工程卫生

- `backend/.gocache` **257 MB** 位于仓库内且未被 `.gitignore` 忽略；`.workbuddy-ai/`（AI 记忆）与 `.zwork/` 日志同样未忽略，容易误提交内部信息。
- 工作区有 **27 个已修改 + 5 个未跟踪文件**未提交，HEAD 与本地不一致；两个新增测试文件（`caddydeploy_test.go`、`caddymerge_test.go`）尚未入库。
- 无 `.gitattributes`：Windows 下 `core.autocrlf` 已提示 LF→CRLF，`install.sh`/`build.sh` 下次提交有被转成 CRLF 的风险（当前实际为 LF）。建议 `*.sh text eol=lf`。
- 无 `LICENSE` 文件；二进制未注入版本号（无 `-version`），发布产物无法自证版本。
- `.dockerignore` 未排除 `.gocache`/`.workbuddy-ai`/`.zwork`，构建上下文含 257MB 缓存。
- `go.mod` 声明 `go 1.26.0`，本机 Go 1.22 会触发工具链自动下载；CI 用 `go-version-file` 没问题，但团队需统一版本，否则「本地能跑/CI 不能跑」的差异会持续。
- `store.Open` 使用 `SetMaxOpenConns(1)`（`store.go:29`）：任何忘记 `rows.Close()` 的查询都会让整个面板的数据库访问排队，属于高风险约定，建议至少加 `SetConnMaxLifetime` 与查询超时，或用 context 版本接口。

## 5. 值得肯定的部分

- 架构方向正确：SQLite 存期望态 → reconciler → 自有链（`FPANEL_*` / `table inet firepanel`），不碰 Docker/firewalld 既有规则；
- 命令执行统一走 `exec.CommandContext` + argv 数组，未发现 shell 注入；`ruleTokenSafe` 对拼入防火墙脚本的字段做了兜底校验；
- 危险变更「延时生效 + 二次确认 + 超时回滚」的产品设计在防火墙面板里是正确取舍；
- Caddyfile 采用 `managed begin/end` 托管段 + validate + 备份 + 合并写入，避免整盘覆盖，思路优于多数同类面板；
- 前端 `svelte-check` 零错误，路由守卫、WS 重连、错误提示等细节完整。

## 6. 建议修复顺序

| 顺序 | 事项 | 预估 | 风险 |
|---|---|---|---|
| 1 | 提交 `fwinfo.svelte.ts`；补 embed dist 占位与构建说明；CI 加干净检出构建 | 0.5d | 阻断发布 |
| 2 | `Mutate` 失败回滚 + Restore 先校验后写（最好事务化） | 1d | 数据丢失 |
| 3 | `Staged` 改存 resolved 状态；`applyPending` 失败清 pending | 0.5d | 高危规则失效/面板卡死 |
| 4 | Docker 改 `network_mode: host` 或降级文档；镜像补 `iproute2` | 0.5d | 功能不可用 |
| 5 | `SetTrustedProxies` + 登录限速双维度 + dummy bcrypt | 0.5d | 认证绕过 |
| 6 | 修正 `config.example.yaml` 的 `ssh_ports` 层级（并考虑兼容旧键） | 0.5d | 防锁死失效 |
| 7 | `install.sh` 放行规则持久化 | 0.5d | 重启锁死 |
| 8 | 补 api/app 关键路径测试；CI 加 `gofmt -l`、`go vet` | 2d | 回归风险 |
| 9 | 备份覆盖全资源 + 文档对齐；Caddy 路径白名单；WS 鉴权/订阅收敛 | 1.5d | 一致性与安全 |

## 7. 复现命令

```bash
# 干净检出构建（会失败）
git worktree add --detach /tmp/fp-clean HEAD
cd /tmp/fp-clean/backend && go build ./...      # embed.go:8 pattern all:dist: no matching files found

# 工作区基线
cd backend && go build ./... && go vet ./... && go test ./...
gofmt -l ./cmd ./internal | wc -l               # 28
go test -cover ./...                            # api 1.1% ...
cd ../frontend && npm run check                 # 0 errors

# 前端缺失文件
git ls-files frontend/src/lib/stores/           # 只有 session.svelte.ts
```
