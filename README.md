# FirePanel — Linux 防火墙可视化管理面板

统一管理 **iptables / nftables** 的 Web 面板。单二进制部署（Go + 内嵌 Svelte 前端 + 纯 Go SQLite），零依赖。

```text
+----------------------------------------------------------------+
|                                                                |
|                         [ FIREPANEL ]                          |
|                                                                |
| 单二进制   Go + Svelte 内嵌     零依赖部署                     |
| 双后端     nftables / iptables  自动探测                       |
| 监听器     多端口 HTTPS/HTTP    证书自动签发                   |
| 站点类型   代理 重定向 静态 文本 断开                          |
| 拓扑布局   7 种                 实时流量图                     |
| 自愈       漂移检测             周期体检修复                   |
|                                                                |
+----------------------------------------------------------------+
```

> 本 README 的图帧遵循 [mdxcn](https://www.mdxcn.dev/docs) 的 markdown-friendly 约定——纯文本框图，复制到任何地方都不会散架。

## 能力总览

```text
+----------------------------------------------------------------------+
|                                                                      |
|                             [ MODULES ]                              |
|                                                                      |
| 端口转发   TCP/UDP/双栈 · 端口段 · 域名目标（DDNS 自动更新 DNAT）    |
| 端口台账   监听端口实时扫描 · 进程/PID/Docker 映射识别               |
| 流量监控   网卡速率趋势（nload 式）· TCP 连接排行（iftop 式）        |
| IP 名单    黑白名单 · CIDR 批量 · 过期自动解封 · 导入导出            |
| Web 服务   监听器 + 站点引擎（见下）· 反代/重定向/静态/文本/断开     |
| 网络拓扑   实时流量图 · 7 种布局 · 访客 IP 链路追踪                  |
| 规则视图   内核实时规则 · 命中计数 · 漂移检测一键重同步              |
| 防火墙自愈 外部工具破坏面板链后周期体检自动重建                      |
| 限速防护   按 IP/总量限速 · 单 IP 并发限制 · 防 CC 与扫描            |
| NAT 管理   SNAT / MASQUERADE 多网卡出口 · ip_forward 一键开启        |
| 定时任务   cron 计划启停转发 · 清理过期名单 · 周期同步               |
| 告警通知   Telegram / Webhook / SMTP · 按事件订阅                    |
| 备份迁移   JSON 全量备份 + iptables-save / nft ruleset 导出          |
| 多用户     admin / viewer 角色 · bcrypt + JWT · 操作审计             |
| 访问防护   整站 Basic Auth · 扫描器只见 401                          |
| 安全设计   SSH/管理 IP 变更加二次确认 + 延时生效 + 超时回滚          |
|                                                                      |
+----------------------------------------------------------------------+
```

## Web 服务

监听器 + 站点引擎（纯 Go 单进程，UI 1:1 复刻 [gately](https://github.com/wangwanjie/gately)）：

```text
+--------------------------------------------------------------------+
|                                                                    |
|                          [ WEB SERVICE ]                           |
|                                                                    |
| 互联网 → 监听器(:443 TLS/:80 HTTP) → 站点(按域名路由) → 后端       |
|                                                                    |
| 监听器   TLS 端口嗅探明文：跳转 HTTPS / 直接提供                   |
|          额外 HTTP 端口与主端口共用全部站点                        |
| 站点     精确域名 · *.泛域名 · IP · 默认站点兜底                   |
| 类型     代理 → 重定向 → 静态 → 文本 → 断开(444)                   |
| 代理     多后端 weight/backup · 轮询/随机/ip_hash/最少连接         |
|          主动+被动健康检查 · 失败自动换后端重试                    |
|          分路径分流(去前缀) · 头改写 · 限流 · gzip                 |
| 证书     Let's Encrypt 自动签发(HTTP-01/ALPN/DNS-01)               |
|          IP 短期证书自动续期 · 手动证书 · 一键自签                 |
| 热重载   配置生效不断开已有连接 · 统计跨重载沿用                   |
|                                                                    |
+--------------------------------------------------------------------+
```

内置**网络拓扑**视图：

```text
+--------------------------------------------------------------------+
|                                                                    |
|                            [ TOPOLOGY ]                            |
|                                                                    |
| 互联网 → 引擎 → 监听 → 站点/动作 → 后端/去向 的实时流量图          |
|                                                                    |
| 布局     树形 · 竖向树 · 径向 · 桑基图 · 旭日图 · 矩形树图 · 气泡  |
| 链路     按访客 IP 筛选，高亮整条走过的链路                        |
| 面板     速率指标 · 请求/字节曲线 · 状态码分布 · 活动 IP           |
| 画布     拖拽平移 · Ctrl+滚轮缩放 · 高度拖拽 · 窗口内全屏          |
| 打码     星号遮蔽或代号替换，截图分享不泄露地址                    |
|                                                                    |
+--------------------------------------------------------------------+
```

## 架构

```text
+----------------------------------------------------------------+
|                                                                |
|                          [ PIPELINE ]                          |
|                                                                |
| SQLite(期望状态) → reconciler(翻译) → FPANEL_* 链(原子应用)    |
|                                                                |
| 漂移检测对比期望与内核，面板永不触碰 Docker / firewalld /      |
| incus 等已有规则；但会周期体检并自愈它们对面板自身链与规则     |
| 的破坏（Docker 重设 FORWARD 策略导致转发失效是典型根因）。     |
|                                                                |
+----------------------------------------------------------------+
```

## 安装

环境要求（脚本会自动补装防火墙工具）：

```text
+--------------------------------------------------------------+
|                                                              |
|                      [ PREREQUISITES ]                       |
|                                                              |
| 系统       Linux x86_64 / arm64        uname -m              |
| 内核       >= 4.9（nft 或 iptables 任一）                    |
| 防火墙     nftables / iptables（脚本自动补装）               |
| 端口       面板 18088 · Web 服务 80/443（均可改）            |
| 权限       root（防火墙操作与 systemd 注册需要）             |
|                                                              |
+--------------------------------------------------------------+
```

### 一键安装（推荐）

```text
+--------------------------------------------------------------+
|                                                              |
|                         [ INSTALL ]                          |
|                                                              |
| 1  下载 Release 包                                           |
|    amd64 或 arm64，解压得到 firepanel + install.sh。         |
| │                                                            |
| 2  运行 install.sh                                           |
|    bash install.sh [/opt/firepanel] [18088]。                |
| │                                                            |
| 3  浏览器打开 http://<服务器IP>:18088                        |
|    向导创建管理员账号，首个账号即 admin。                    |
| │                                                            |
| 4  安全加固                                                  |
|    开启整站 Basic Auth，或配置面板自身 HTTPS。               |
|                                                              |
+--------------------------------------------------------------+
```

```text
+----------------------------------------------------------------------------+
|                                                                            |
|                                 [ SHELL ]                                  |
|                                                                            |
| $ curl -fsSL -o /tmp/fp.tar.gz github.com/jiqing112/                       |
|     firepanel/releases/latest/download/...amd64.tar.gz                     |
| $ mkdir -p /tmp/fp && tar -xzf /tmp/fp.tar.gz -C /tmp/fp                   |
| $ cd /tmp/fp && bash install.sh                                            |
| ==> 识别发行版 … 安装防火墙工具 … 部署二进制                               |
| ==> 写入 systemd 服务（开机自启）                                          |
| ==> 备份现有规则后放行面板端口                                             |
| ==> 安装 firepanel-bootstrap.service（开机重放放行）                       |
| ==> FirePanel 已启动  http://<IP>:18088                                    |
| $ systemctl is-active firepanel                                            |
| active                                                                     |
|                                                                            |
+----------------------------------------------------------------------------+
```

### 手动部署（不用脚本）

<details>
<summary>systemd 手动注册</summary>

```bash
# 放置二进制与数据目录
mkdir -p /opt/firepanel/data
install -m 755 /tmp/fp/firepanel /opt/firepanel/firepanel

# 注册 systemd 服务
tee /etc/systemd/system/firepanel.service >/dev/null <<'UNIT'
[Unit]
Description=FirePanel firewall web panel
After=network-online.target
Wants=network-online.target
[Service]
ExecStart=/opt/firepanel/firepanel -listen :18088 -data-dir /opt/firepanel/data
Restart=on-failure
RestartSec=3
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now firepanel

# 临时放行面板端口（重启后会丢失；生产环境用一键脚本，
# 它会额外安装 firepanel-bootstrap.service 在每次开机重放放行）
iptables -I INPUT 1 -p tcp --dport 18088 -j ACCEPT
```

验证：`systemctl is-active firepanel` 为 active，且 `curl http://127.0.0.1:18088/api/v1/bootstrap` 返回 JSON。

</details>

### Docker

前置：Docker 24+ 且带 buildx 插件；80/443/18088 需空闲。

```text
+--------------------------------------------------------------------+
|                                                                    |
|                             [ DOCKER ]                             |
|                                                                    |
| $ git clone https://github.com/jiqing112/firepanel.git             |
| $ cd firepanel/deploy && docker compose up -d --build              |
| $ curl http://127.0.0.1:18088/api/v1/bootstrap                     |
| {"needs_setup":true,...}                                           |
|                                                                    |
| # 必须使用 host 网络 + NET_ADMIN/NET_RAW（compose 已配置）：       |
| # 面板要管理宿主 netfilter 并监听宿主 80/443。                     |
| # 数据（SQLite + 证书）落在 deploy/data/。                         |
|                                                                    |
+--------------------------------------------------------------------+
```

```text
+--------------------------------------------------------------+
|                                                              |
|                         [ WARNING ]                          |
|                                                              |
| !  云服务器务必在厂商控制台的安全组放行面板端口（18088）     |
|    与 Web 服务端口（80/443）——「本机能开、外网打不开」       |
|    的最常见原因就是安全组没放行。                            |
|                                                              |
+--------------------------------------------------------------+
```

### 面板自身 HTTPS（强烈建议）

```text
+------------------------------------------------------------+
|                                                            |
|                          [ TIP ]                           |
|                                                            |
| +  面板自身 HTTPS：systemctl edit firepanel 追加一行       |
|    ExecStart=... -panel-https <公网IP> :8443 端口自动签发  |
|    （IP 走 6.7 天短期证书，全自动续期。要求公网可访问      |
|    80 或 443 完成校验）。                                  |
|                                                            |
+------------------------------------------------------------+
```

要求该 IP/域名可从公网访问 80 或 443（ACME 校验）；签发失败（如内网 IP）自动回退仅 HTTP。内网环境改用手动证书：`panel_tls_cert` / `panel_tls_key` 指向 PEM 文件（面板内可一键生成自签证书）。

### 整站 Basic Auth

```text
+----------------------------------------------------------------+
|                                                                |
|                         [ BASIC AUTH ]                         |
|                                                                |
| 设置页     系统设置 → 整站 Basic Auth，保存即生效              |
| 凭据文件   数据目录 basicauth.yaml（0600），约 2 秒热生效      |
| 启动参数   -basic-auth "user:password"，重启生效               |
|                                                                |
| 覆盖全部页面、API 与 WebSocket；忘记密码回设置页重设。         |
|                                                                |
+----------------------------------------------------------------+
```

## 配置

`config.yaml` 放在二进制同目录（或 `-config` 指定路径），命令行参数优先级更高。

```text
+--------------------------------------------------------------+
|                                                              |
|                       [ CONFIG.YAML ]                        |
|                                                              |
| server.listen            :18088     面板 HTTP 监听           |
| server.data_dir          /var/lib/firepanel    SQLite 与证书 |
| server.proxy_http(s)     :80 / :443            Web 服务监听  |
| server.panel_https       空         面板 HTTPS（自动签发）   |
| server.basic_auth_user   空         整站 Basic Auth          |
| firewall.ssh_ports       [22]       触发高危确认的端口       |
| firewall.backend         auto       auto/iptables/nftables   |
|                                                              |
+--------------------------------------------------------------+
```

```text
+--------------------------------------------------------------+
|                                                              |
|                          [ FLAGS ]                           |
|                                                              |
| -config         YAML 配置文件路径（自动探测）                |
| -listen         :18088              面板 HTTP 监听           |
| -data-dir       /var/lib/firepanel  SQLite 与证书目录        |
| -backend        auto / iptables / nftables                   |
| -proxy-http     :80   Web 服务 HTTP 监听（占用时改 :8180）   |
| -proxy-https    :443  Web 服务 HTTPS 监听                    |
| -panel-https    公网 IP/域名，自动签发面板证书               |
| -panel-https-port :8443             面板 HTTPS 监听          |
| -basic-auth     user:password       整站 Basic Auth          |
| 环境变量         FIREPANEL_LISTEN / FIREPANEL_DATA_DIR       |
|                                                              |
+--------------------------------------------------------------+
```

完整字段见 [deploy/config.example.yaml](deploy/config.example.yaml)。

## 与已有 80/443 服务共存（模式 2）

```text
+------------------------------------------------------------+
|                                                            |
|                        [ COEXIST ]                         |
|                                                            |
| +  80/443 被其他程序占用（模式 2）：面板改用非标端口，     |
|    让占用者把 /.well-known/acme-challenge/ 转发到面板挑战  |
|    端口即可正常签发——「环境探测」对话框一键生成            |
|    Caddy / Nginx 寄生前置片段。                            |
|                                                            |
+------------------------------------------------------------+
```

```text
+------------------------------------------------------------+
|                                                            |
|                     [ COEXIST IMPACT ]                     |
|                                                            |
| ●  Caddy(自动 HTTPS)  不影响——挑战应答优先于站点路由       |
| ●  手动/商业证书      无签发行为，零影响                   |
| ×  Nginx + certbot    有冲突风险——certbot 的 location ^~   |
|                       优先级更高，需合并两条 location，    |
|                       或面板改用 DNS-01                    |
|                                                            |
+------------------------------------------------------------+
```

443 的 TLS 终结与证书加载完全归占用者，寄生前置只转发 80 上的一个路径；面板自身 HTTPS（8443/自定义）在寄生前置下同样可签发/续期。

## 安全设计

```text
+------------------------------------------------------------+
|                                                            |
|                      [ DANGER GUARD ]                      |
|                                                            |
| ●  提交涉及 SSH 端口 / 管理 IP 的变更                      |
| ●  二次确认 + 延时生效（默认 60s）                         |
| ●  确认窗口内未确认 → 超时自动回滚                         |
| ○  面板重启时未确认的变更自动回滚到变更前状态              |
|                                                            |
+------------------------------------------------------------+
```

## 升级与备份

```text
+------------------------------------------------------------------+
|                                                                  |
|                           [ UPGRADE ]                            |
|                                                                  |
| $ curl -fsSL -o /tmp/fp.tar.gz github.com/jiqing112/             |
|     firepanel/releases/latest/download/...amd64.tar.gz           |
| $ tar -xzf /tmp/fp.tar.gz -C /tmp/fp firepanel                   |
| $ systemctl stop firepanel                                       |
| $ install -m 755 /tmp/fp/firepanel /opt/firepanel/firepanel      |
| $ systemctl start firepanel                                      |
| ✓ 配置、数据库、证书都在 data/，升级不动                         |
|                                                                  |
+------------------------------------------------------------------+
```

```text
+----------------------------------------------------------------+
|                                                                |
|                           [ BACKUP ]                           |
|                                                                |
| [x]  面板内导出 JSON（全部结构化配置，不含账号与设置）         |
| [x]  iptables-save / nft ruleset 导出（内核侧）                |
| [x]  文件级备份 data/（firepanel.db + certs/，全量迁移）       |
| [ ]  手动证书随备份写出私钥 —— 按密钥级别保管                  |
|                                                                |
+----------------------------------------------------------------+
```

## 排查

| 现象 | 原因与处理 |
|---|---|
| 外网打不开面板，本机 curl 正常 | 云安全组未放行 18088 |
| `curl` 返回 401 | 整站 Basic Auth 开着，带上账号密码 |
| 反代/证书签发失败 | 80/443 被占用或不可达：环境探测查看占用者；域名走 DNS-01 或寄生前置 |
| `install.sh` 报端口放行失败 | 防火墙工具异常，手动放行后再跑一次脚本 |
| Alpine 下连接列表为空 | 装 iproute2：`apk add iproute2`（其余功能不受影响） |

## 卸载

```text
+----------------------------------------------------------------------------+
|                                                                            |
|                               [ UNINSTALL ]                                |
|                                                                            |
| $ systemctl disable --now firepanel                                        |
| $ systemctl disable --now firepanel-bootstrap.service 2>/dev/null || true  |
| $ rm -f /etc/systemd/system/firepanel-bootstrap.service \                  |
|     /usr/local/sbin/firepanel-bootstrap-firewall                           |
| $ rm -rf /opt/firepanel /etc/systemd/system/firepanel.service              |
| $ systemctl daemon-reload                                                  |
| # 面板自有链（FPANEL_* / table inet firepanel）可在面板                    |
| # 「防火墙规则」页清空，或随宿主持久化策略下次重载自然消失                 |
|                                                                            |
+----------------------------------------------------------------------------+
```

## 发行版兼容

| 发行版 | 默认防火墙 | 面板行为 |
|---|---|---|
| Debian 11/12+ | nftables（iptables-nft） | ✅ 实测（Debian 13，双后端切换验证） |
| Ubuntu 20/22/24 | ufw + nftables | 探测 ufw 启用时界面提示共存 |
| CentOS 7 | iptables legacy | iptables 后端（hashlimit/connlimit 均支持） |
| Rocky / Alma 8-9 / Fedora / openSUSE | firewalld | 共存模式：面板自有 nft 表与 firewalld 不冲突 |
| Arch / Manjaro | 无默认 | 自动探测可用工具链 |
| Alpine | 无默认 | 纯 Go 静态链接；连接明细需要 ss（`apk add iproute2`），缺失时回退 busybox netstat |

## 开发

```text
+----------------------------------------------------------+
|                                                          |
|                       [ DEVELOP ]                        |
|                                                          |
| $ cd frontend && npm install && npm run dev              |
| ✓ 前端热更新（代理到 :8080）                             |
| $ cd backend && go test ./...                            |
| ✓ 后端单测（任意平台可跑）                               |
| $ bash scripts/build.sh linux dist arm64                 |
| ✓ dist/firepanel  ~17MB 单二进制                         |
|                                                          |
+----------------------------------------------------------+
```

> 后端 `//go:embed all:dist` 依赖 `backend/internal/web/dist/` 非空：仓库内已放 `.gitkeep` 占位，干净检出可直接 `go build`（未构建前端时页面为 404）。要得到可用二进制请走 `scripts/build.sh` 或 CI 流程。

## 文档

- [API 文档](docs/API.md)
- [设计说明与原型](docs/DESIGN.md)
- [常见问题](docs/FAQ.md)

## 已知限制

```text
+----------------------------------------------------------------+
|                                                                |
|                        [ KNOWN LIMITS ]                        |
|                                                                |
| !  局域网内无法完成 Let's Encrypt 真实签发（需公网 80/443）    |
|    可达；内网用 DNS-01 / 手动证书 / 无 TLS                     |
| !  GeoIP 国家过滤需要 mmdb 数据文件（结构已预留）              |
| !  临时封禁 / 定时任务依赖面板进程存活，停机期间 cron 不触发   |
| !  nft-only 且 INPUT 策略为 drop 的主机：安装脚本的放行规则    |
|    可能被宿主 base chain 覆盖，需在宿主防火墙配置中显式放行    |
|                                                                |
+----------------------------------------------------------------+
```
