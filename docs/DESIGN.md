# FirePanel 设计说明

## 1. 架构

```
┌────────────────────── 单二进制 firepanel ──────────────────────┐
│  前端 SPA（Svelte 5 + shadcn-svelte + Tailwind 4, go:embed）     │
│  REST /api/v1 + WebSocket /api/ws（单连接多 topic）              │
├─────────────────────────────────────────────────────────────────┤
│  api      gin handlers · JWT/审计中间件 · WS Hub                 │
│  app      运行时装配 · 后台循环（采样/漂移/过期/健康）· Mutate    │
│  proxy    L7 反代（域名+路径路由 · WS 透传 · certmagic · 健康）  │
│  scheduler cron 调度      notify  TG/Webhook/SMTP               │
├─────────────────────────────────────────────────────────────────┤
│  store    SQLite（modernc 纯 Go）· 内嵌迁移 · 期望状态           │
│  fw       FirewallBackend 接口 → iptables / nftables 实现        │
│           reconciler：翻译 · 原子应用 · 漂移 · 危险守护           │
│  sysinfo  发行版/内核/conntrack/网卡速率                         │
└─────────────────────────────────────────────────────────────────┘
```

## 2. Reconcile 模型（核心）

SQLite = 期望状态，内核 = 实际状态。

```
Desired()                    BuildRules()            backend.Apply()
store 行 ────────▶ fw.DesiredState ────────▶ []Rule ────────▶ FPANEL_* / table inet firepanel
（仅启用未过期）    （与后端无关）              （带 fp: 标签）      （--noflush / flush 自有表，原子）
```

- **标签系统** `fp:<kind>:<id>:<seq>`：每条规则带 comment 标签 → 命中计数映射到规则、漂移精确到条、UI 可展示。
- **原子性**：变更走 `Mutate(快照 → 写库 → Sync → 失败恢复)`；内核侧 `iptables-restore --noflush` 每表一次 COMMIT、nft `flush table + 批量 add` 单事务。
- **启动重放**：服务启动即全量同步，替代 iptables-save 等系统持久化。
- **漂移检测**：定期 + 手动，`diffRules` 语义级比较（容忍 nft/iptables 语法差异），报告 missing/extra/modified。
- **防锁死**：危险变更（SSH 端口/管理 IP/全网段）只对「相对 lastGood 的增量」判定 → 60s 延时 → 90s 确认窗口 → 超时回滚；挂起期间拒绝并发变更；撤销时恢复数据库快照。

## 3. 双后端映射

| 语义 | iptables | nftables |
|---|---|---|
| 面板链 | FPANEL_INPUT/FORWARD/DNAT/SNAT 挂内置链首 | table inet firepanel，基础链 priority -10/-110 |
| 应用 | iptables-restore --noflush（stdin 脚本，ip6tables 同步） | nft -f -（stdin 单事务） |
| 快照 | iptables-save 解析 | nft -j JSON（扁平条目流）解析 |
| 限速 | hashlimit + connlimit（两条独立规则，OR 语义） | meter { ip saddr limit rate over X/Y } + meter { ip saddr ct count over N } |
| NAT | MASQUERADE/SNAT -o IFACE | masquerade/snat + oifname |
| 计数 | [pkts:bytes] 解析 | counter 表达式 |

后端选择：`-backend auto`（探测 nft → iptables），切换时重启即重建；同一份 SQLite 期望状态在两种后端间无缝迁移（已实测）。

## 4. 安全

- 命令执行全部 argv 数组 + stdin，绝不拼 shell 字符串（Executor 抽象可注入 fake 测试）
- 输入校验：IP/CIDR/端口段/域名/网卡名白名单正则，非法即 400
- JWT（HS256，密钥首启自动生成入库）+ bcrypt + 登录限速（10/min/IP）
- 审计：谁/何时/对什么/前后快照/来源 IP

## 5. 前端设计系统（浅色呼吸感 SaaS）

- 暖白底 `oklch(0.985 0.002 92)`、白卡片 12px 圆角 + 柔和双层阴影、靛蓝主色、1px 细节线
- 等宽数字（`font-variant-numeric: tabular-nums`）贯穿所有统计与表格
- 图表自绘 SVG：渐变面积图（十字线 tooltip、draw-in 动画）、排行条（宽度动画）、迷你走势
- 深色主题同构（近黑底 + 微辉光），localStorage 持久化 + system 跟随
- 空/错/加载三态全覆盖：定制空态、骨架屏、错误重试卡片
- 动效克制：页面 fade-up 240ms、卡片 hover 浮起 + 底部高亮线、抽屉滑入 220ms、行淡入

## 6. 关键取舍

- **反代 L4 不做双引擎**：TCP/UDP 四层转发由端口转发（DNAT）承担，反代专注 L7 —— 页面互链引导
- **firewalld/ufw 不做深度集成**：共存模式 + 界面指引，避免与发行版工具双重管理
- **GeoIP 预留**：数据结构含 country 字段，UI 入口后续开放（需用户上传 mmdb）
- **证书**：certmagic 集成但内网无法 ACME，手动证书 + 无 TLS 为一等公民路径
