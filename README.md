# FirePanel — Linux 防火墙可视化管理面板

统一管理 **iptables / nftables** 的 Web 面板，单二进制部署（Go + 内嵌 Svelte 前端 + 纯 Go SQLite）。


## 核心特性

- **端口转发**：TCP/UDP/双栈，单端口或端口段，域名目标（DDNS 周期解析自动更新 DNAT）
- **端口台账**：本机监听端口实时扫描（进程/PID/监听地址/Docker 映射识别），端口用途记录与监听状态联动
- **流量监控**：网卡实时收发速率与 5 分钟趋势图（nload 式），TCP 连接级流量排行（iftop 式，socket 计数差分）
- **IP 黑白名单**：单 IP / CIDR 批量管理，标签分组、过期自动解封、txt/csv/json 导入导出
- **反向代理**：HTTP/HTTPS 域名 + 路径路由，WebSocket 透传，上游健康检查，Let's Encrypt 自动证书（certmagic）与手动证书；**双引擎面板内切换**（内置零依赖引擎 / **Caddy 完全接管**：面板生成完整 Caddyfile → validate → 写 /etc/caddy/Caddyfile → 优雅重载，重启 Caddy 不丢配置；Caddyfile 可预览/导出/手改）+ 环境探测推荐（80/443 占用检测 → HTTP-01 / TLS-ALPN / DNS-01 挑战矩阵），DNS-01 内置 Cloudflare / 阿里云 / DNSPod 凭据，支持泛域名与寄生前置配置生成；站点级压缩/安全头/访问日志开关；IP 短期证书（shortlived，内置引擎）
- **防火墙规则视图**：内核实时规则、命中计数、链挂接状态、**漂移检测**与一键重新同步
- **限速防护**：按 IP / 总量限速（hashlimit / nft meter）、单 IP 并发连接限制（connlimit / ct count），防 CC 与扫描
- **NAT 管理**：SNAT / MASQUERADE 多网卡出口策略（ip_forward 未开启时一键开启并持久化）
- **定时任务**：cron 计划启停转发规则、清理过期名单、周期同步
- **告警通知**：Telegram / Webhook / SMTP，可订阅事件（应用失败、漂移、高危待确认、上游不可达、自动回滚）
- **备份迁移**：JSON 全量备份/恢复 + iptables-save / nft ruleset 导出，跨机迁移
- **多用户**：admin / viewer 角色，bcrypt + JWT，操作审计
- **访问防护**：设置页可开启整站 Basic Auth（运行时生效），凭据写入程序目录 `basicauth.yaml`（0600，可手动编辑），端口扫描器只看到 401
- **安全设计**：涉及 SSH 端口 / 管理 IP 的变更加危险确认流程（二次确认 + 延时生效 + 超时自动回滚）

## 架构一句话

SQLite 存「期望状态」→ reconciler 翻译为面板自有链（`FPANEL_*` / `table inet firepanel`）→ 后端原子应用；
漂移检测对比期望与内核；面板永不触碰 Docker / firewalld / incus 等已有规则。

## 安装部署

### 环境要求

- Linux x86_64（amd64）/ arm64，内核 ≥ 4.9（nftables 或 iptables 任一）
- 面板本身零依赖：单二进制已内嵌前端与 SQLite；仅防火墙功能需要系统装有 nftables 或 iptables（安装脚本会自动补装）
- 如需反代/证书功能：80/443（或自定义端口）可监听；DNS-01 挑战则无端口要求

### 方式一：一键安装脚本（推荐）

```bash
# 1. 获取二进制：直接下载 Release 包（推荐），或本地构建后上传
#    https://github.com/jiqing112/firepanel/releases/latest
curl -fsSL -o /tmp/fp.tar.gz https://github.com/jiqing112/firepanel/releases/latest/download/firepanel-linux-amd64.tar.gz

# 2. 解压后与 install.sh 同目录执行
mkdir -p /tmp/deploy && tar -xzf /tmp/fp.tar.gz -C /tmp/deploy
cd /tmp/deploy
bash install.sh                        # 默认 /opt/firepanel + 端口 18088
# 自定义：bash install.sh /opt/firepanel 18088

# （从仓库 checkout 运行则是：cd /tmp/deploy && bash deploy/install.sh）
```

> 用 root 直接运行即可（无需 sudo）；无 sudo 的最小化系统同样适用。
> Release 包为平铺结构（firepanel 与 install.sh 同级）。

脚本自动完成：识别发行版 → 安装 nftables/iptables（缺失时）→ 备份现有防火墙规则（/tmp 快照）→
放行面板端口 → 注册并启动 systemd 服务（开机自启）。完成后浏览器访问 `http://<服务器IP>:18088`，
首次访问按向导创建管理员账号。

> GitHub Actions 会在每次打 tag（`v*`）时自动构建 amd64/arm64 二进制并发布到 Releases，
> 可直接下载 `firepanel-linux-amd64.tar.gz` 解压后执行上面的第 2 步。

### 方式二：手动部署（不用脚本）

```bash
# 目标机上：下载并解压（Release 包，root 执行）
curl -fsSL -o /tmp/fp.tar.gz https://github.com/jiqing112/firepanel/releases/latest/download/firepanel-linux-amd64.tar.gz
mkdir -p /tmp/fp && tar -xzf /tmp/fp.tar.gz -C /tmp/fp
mkdir -p /opt/firepanel/data
install -m 755 /tmp/fp/firepanel /opt/firepanel/firepanel

# systemd 服务（模板见 deploy/firepanel.service）
sudo tee /etc/systemd/system/firepanel.service >/dev/null <<'UNIT'
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
sudo systemctl daemon-reload && sudo systemctl enable --now firepanel

# 别忘了放行面板端口（或在面板「防火墙规则 / IP 名单」里管理）
```

### 方式三：Docker

前置：Docker 24+ 且带 **buildx** 插件（Debian/Ubuntu 的 docker.io 不自带，安装：
`curl -fsSL -o /usr/local/lib/docker/cli-plugins/docker-buildx https://github.com/docker/buildx/releases/latest/download/buildx-v0.28.0.linux-amd64 && chmod +x /usr/local/lib/docker/cli-plugins/docker-buildx`）；
80/443/18088 需空闲（先停掉占用这些端口的服务，如旧实例的 Caddy）。

```bash
git clone https://github.com/jiqing112/firepanel.git
cd firepanel/deploy && docker compose up -d --build
```

容器需要 `NET_ADMIN` 能力操作宿主 netfilter；面板 18088、反代 80/443 已在 compose 中映射。
数据（SQLite + 证书）落在 `deploy/data/`。

### 首次初始化

浏览器打开 `http://<IP>:18088` → 按向导创建管理员账号 → 进入面板。
建议第一件事：到「系统设置」开启整站 Basic Auth，或配置面板 HTTPS（见下），避免管理端口裸奔公网。

### 面板自身 HTTPS（强烈建议）

```bash
# 云服务器：填公网 IP，启动即自动签发 Let's Encrypt IP 证书（6.7 天短期证书，全自动续期）
sudo systemctl edit firepanel   # 追加：
# [Service]
# ExecStart=
# ExecStart=/opt/firepanel/firepanel -listen :18088 -data-dir /opt/firepanel/data -panel-https <你的公网IP> -panel-https-port :8443
```

- 填**域名**则签发常规 90 天证书；填**内网 IP** 会签发失败并自动回退仅 HTTP
  （内网改用「反向代理 → 手动证书 → 自动生成自签证书」，把 PEM 存成文件后配置
  `panel_tls_cert` / `panel_tls_key`）
- 也可直接写 `config.yaml`（见 [deploy/config.example.yaml](deploy/config.example.yaml) 全字段注释）

### 面板整站 Basic Auth

- **设置页**：「系统设置 → 整站 Basic Auth」，开关 + 账号密码，保存即生效
- **凭据文件**：程序目录 `basicauth.yaml`（0600），手动编辑后约 2 秒自动热生效
- **启动参数**：`-basic-auth "user:password"`（应急）

### 配置文件

`config.yaml` 放在二进制同目录（或 `-config` 指定），命令行参数优先级更高。
关键字段速查：

| 配置 | 默认 | 说明 |
|---|---|---|
| server.listen | :18088 | 面板 HTTP 监听 |
| server.data_dir | /var/lib/firepanel | SQLite 与证书目录 |
| server.proxy_http / proxy_https | :80 / :443 | 反代监听（三种形态见文件内注释） |
| server.panel_https | 空 | 面板 HTTPS 的 IP/域名（自动签发） |
| server.basic_auth_user / pass | 空 | 整站 Basic Auth |
| firewall.ssh_ports | [22] | 触发高危确认的 SSH 端口 |
| proxy.backend | builtin | 反代引擎 builtin / caddy |

### 升级

```bash
# 替换二进制并重启即可：配置、数据库、证书都在 data 目录，升级不动它们
sudo systemctl stop firepanel
sudo cp firepanel /opt/firepanel/firepanel && sudo systemctl start firepanel
```

面板启动时会自动把 SQLite 期望状态重放到内核——重启服务器/面板后规则自动恢复，无需额外持久化配置。

### 数据备份与迁移

- 面板内：备份迁移页导出 JSON（转发/名单）+ iptables-save / nft ruleset
- 文件级：直接备份 `data/` 目录（firepanel.db + certs/），拷到新机同路径即完成迁移

### 卸载

```bash
sudo systemctl disable --now firepanel
sudo rm -rf /opt/firepanel /etc/systemd/system/firepanel.service
sudo systemctl daemon-reload
# 面板自有链（FPANEL_* / table inet firepanel）可由防火墙规则页清空，或重启后按系统持久化策略处理
```

## 命令行参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `-config` | 自动探测 | YAML 配置文件路径（默认尝试程序目录 / 工作目录的 `config.yaml`） |
| `-listen` | `:18088` | 面板 HTTP 监听 |
| `-data-dir` | `/var/lib/firepanel` | SQLite 与证书目录 |
| `-backend` | `auto` | `auto` / `iptables` / `nftables` |
| `-proxy-http` | `:80` | 反代 HTTP 监听（80 被占用时改，如 `:8180`） |
| `-proxy-https` | `:443` | 反代 HTTPS 监听 |
| `-panel-https` | 空 | 面板自身 HTTPS 的 IP/域名：**云服务器上填公网 IP，启动即自动签发 Let's Encrypt IP 证书（6.7 天短期证书，自动续期）并应用到面板 Web 端口** |
| `-panel-https-port` | `:8443` | 面板 HTTPS 监听 |
| `-basic-auth` | 空 | 面板整站 Basic Auth，格式 `user:password` |

环境变量：`FIREPANEL_LISTEN`、`FIREPANEL_DATA_DIR`。

完整配置示例见 [deploy/config.example.yaml](deploy/config.example.yaml)（含面板 HTTPS 手动证书、Basic Auth、SSH 保护端口等全部字段）。

### 云服务器启用面板 HTTPS（IP 证书自动签发）

```yaml
server:
  listen: ":8088"
  panel_https: "203.0.113.9"     # 你的公网 IP
  panel_https_port: ":8443"
```

要求该 IP 可从公网访问 80/443（ACME 校验；面板与反代共用 80/443 时挑战自动处理）。
签发失败（如内网 IP）面板自动回退仅 HTTP 并在日志说明；内网环境可在面板
「反向代理 → 添加域名 → 手动证书 → 自动生成自签证书」，将 PEM 保存为文件后配置
`panel_tls_cert` / `panel_tls_key` 走手动证书模式。

### 面板整站 Basic Auth

```yaml
server:
  basic_auth_user: "ops"
  basic_auth_pass: "YourStrongPass"
```

覆盖全部页面、API 与 WebSocket，是应用层登录之外的额外防护层（浏览器原生账号框）。
配置文件建议 `chmod 600`。也可用 `-basic-auth "user:password"` 参数临时启用。

## 发行版兼容

| 发行版 | 默认防火墙 | 面板行为 |
|---|---|---|
| Debian 11/12+ | nftables（iptables-nft） | ✅ 实测（Debian 13，双后端切换验证） |
| Ubuntu 20/22/24 | ufw + nftables | 探测 ufw 启用时面板界面提示共存 |
| CentOS 7 | iptables legacy | iptables 后端（hashlimit/connlimit 均支持） |
| Rocky / Alma 8-9 / Fedora / openSUSE | firewalld | 共存模式：面板自有 nft 表与 firewalld 不冲突 |
| Arch / Manjaro | 无默认 | 自动探测可用工具链（iproute2 属 base 必装） |
| Alpine | 无默认 | 兼容：二进制为纯 Go 静态链接（musl 无关）。端口扫描优先用 ss（`apk add iproute2`），无 ss 时自动回退 busybox netstat；流量监控的连接级明细需要 ss，缺失时页面给出安装提示（网卡速率图表不受影响）。`sysctl -w` 与 /etc/sysctl.conf 由 busybox/OpenRC 支持 |

## 目录结构

```
backend/            Go 后端
  cmd/firepanel/    入口
  internal/
    fw/             防火墙抽象（iptables / nftables / reconciler / 漂移 / 防锁死）
    store/          SQLite 存储层（内嵌迁移）
    proxy/          L7 反向代理
    scheduler/      cron 定时任务
    notify/         告警通知
    api/            REST + WebSocket
    app/            运行时装配
    web/            go:embed 前端产物
frontend/           Svelte 5 + shadcn-svelte + Tailwind 4
deploy/             install.sh / Dockerfile / docker-compose / systemd unit
docs/               API.md / 设计文档 / 原型截图
scripts/            build.sh / sshx（开发期 SSH 工具）
```

## 开发

```bash
cd frontend && npm install && npm run dev     # 前端热更新（代理到 :8080）
cd backend && go test ./...                   # 后端单测（可在任意平台跑）
bash scripts/build.sh linux                   # 构建单二进制
```

## 文档

- [API 文档](docs/API.md)
- [设计说明与原型](docs/DESIGN.md)
- [常见问题](docs/FAQ.md)

## 已知限制

- 局域网内无法完成 Let's Encrypt 真实签发（需公网 80/443 可达）；内网用手动证书或无 TLS
- GeoIP 国家过滤需要 mmdb 数据文件（数据结构已预留，页面入口后续开放）
- 临时封禁 / 定时任务依赖面板进程存活；面板停机期间 cron 不触发（恢复后按计划继续）
