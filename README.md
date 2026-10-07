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

### 0. 环境要求与准备

| 项 | 要求 | 检查命令 |
|---|---|---|
| 系统 | Linux x86_64 / arm64（amd64 为主） | `uname -m` |
| 内核 | ≥ 4.9（nftables 或 iptables 任一） | `uname -r` |
| 防火墙工具 | nftables 或 iptables（脚本会自动补装） | `command -v nft || command -v iptables` |
| 端口 | 面板默认 18088；反代默认 80/443（均可改） | `ss -tlnp \| grep -E ':80 |:443 |:18088'` |
| 权限 | root（防火墙操作与 systemd 注册需要） | `whoami` |

- 面板本身**零依赖**：单二进制已内嵌前端与 SQLite，静态链接（musl/glibc 无关，Alpine 也能跑）
- 如需反代/证书：80/443（或自定义端口）可监听即可；DNS-01 挑战则无端口要求
- **云服务器务必放行安全组**：面板端口（18088）与反代端口（80/443）要在云厂商控制台的安全组/防火墙里放行——这是「安装后面板打不开」的最常见原因

### 1. 获取安装包

三种途径任选：

**a. 下载 GitHub Release 包（推荐，最新版）**

```bash
# amd64（绝大多数云服务器）
curl -fsSL -o /tmp/fp.tar.gz https://github.com/jiqing112/firepanel/releases/latest/download/firepanel-linux-amd64.tar.gz
# arm64
curl -fsSL -o /tmp/fp.tar.gz https://github.com/jiqing112/firepanel/releases/latest/download/firepanel-linux-arm64.tar.gz
mkdir -p /tmp/fp && tar -xzf /tmp/fp.tar.gz -C /tmp/fp && ls /tmp/fp
# 解压得到：firepanel  install.sh  config.example.yaml  firepanel.service
```

**b. 本地源码构建后上传**

```bash
git clone https://github.com/jiqing112/firepanel.git
cd firepanel && bash scripts/build.sh linux    # 产物 dist/firepanel（~17MB）
scp dist/firepanel user@server:/tmp/fp/
```

**c. 从 Releases 页手动下载**：https://github.com/jiqing112/firepanel/releases

### 2. 方式一：一键安装脚本（推荐）

把 `install.sh` 与 `firepanel` 放在一起执行（Release 包解压后天然满足）：

```bash
cd /tmp/fp                 # 或包含这两个文件的任意目录
bash install.sh            # 默认安装到 /opt/firepanel，面板端口 18088
# 自定义目录与端口：
# bash install.sh /opt/firepanel 18088
```

> root 直接运行即可；没有 sudo 的最小化系统同样适用（脚本自己就是 root 语义）。

脚本依次自动完成（每步都有 `==>` 前缀输出，失败会停在对应步骤）：

1. 识别发行版（Debian/Ubuntu/CentOS/Rocky/Alma/Fedora/openSUSE/Arch）
2. 安装 nftables / iptables（仅当系统没有时）
3. 二进制放到 `/opt/firepanel/`
4. 写入并启用 systemd 服务 `firepanel`（开机自启，崩溃自动重启）
5. **先备份现有防火墙规则**到 `/tmp/firepanel-ruleset-backup-<时间戳>.txt`，再放行面板端口
6. 启动并输出访问地址

成功标志：输出 `FirePanel 已启动` + 访问地址。验证：

```bash
systemctl is-active firepanel          # active
curl http://127.0.0.1:18088/api/v1/bootstrap    # 返回 JSON
```

### 3. 方式二：手动部署（不用脚本）

适合想完全掌控每一步的场景。

```bash
# ① 放置二进制与数据目录
mkdir -p /opt/firepanel/data
install -m 755 /tmp/fp/firepanel /opt/firepanel/firepanel

# ② 注册 systemd 服务
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

# ③ 放行面板端口（临时；之后可在面板内管理）
iptables -I INPUT 1 -p tcp --dport 18088 -j ACCEPT
```

验证：`systemctl is-active firepanel` 为 active，且 `curl http://127.0.0.1:18088/api/v1/bootstrap` 返回 JSON。

### 4. 方式三：Docker

前置：Docker 24+ 且带 **buildx** 插件（Debian/Ubuntu 的 `docker.io` 包不带，安装：

```bash
mkdir -p /usr/local/lib/docker/cli-plugins
curl -fsSL -o /usr/local/lib/docker/cli-plugins/docker-buildx https://github.com/docker/buildx/releases/latest/download/buildx-v0.28.0.linux-amd64
chmod +x /usr/local/lib/docker/cli-plugins/docker-buildx
```

）；80/443/18088 需空闲（先停掉占用这些端口的服务，例如之前部署的 Caddy：`systemctl disable --now caddy`）。

```bash
git clone https://github.com/jiqing112/firepanel.git
cd firepanel/deploy && docker compose up -d --build
```

- 容器需要 `NET_ADMIN` 能力操作宿主 netfilter（compose 已配置）
- 面板 18088、反代 80/443 已映射；数据（SQLite + 证书）落在 `deploy/data/`
- 验证：`docker ps` 可见 firepanel；`curl http://127.0.0.1:18088/api/v1/bootstrap` 返回 JSON

### 5. 首次初始化

1. 浏览器打开 `http://<服务器IP>:18088`
2. 按向导创建管理员账号（首个账号即 admin）
3. 进入面板

**第一件事建议做安全加固**（二选一或都做，避免管理端口裸奔公网）：

- 「系统设置 → 整站 Basic Auth」开启（浏览器原生账号框，扫描器只见 401）
- 或按下文配置面板自身 HTTPS

另外检查云厂商**安全组**已放行 18088（常见踩坑：本机能开、外网打不开，就是安全组没放行）。

### 6. 面板自身 HTTPS（强烈建议）

公网服务器上填公网 IP，启动即自动向 Let's Encrypt 签发证书（IP → 6.7 天短期证书；域名 → 90 天），全自动续期：

```bash
systemctl edit firepanel    # 在编辑器中追加以下内容后保存
# [Service]
# ExecStart=
# ExecStart=/opt/firepanel/firepanel -listen :18088 -data-dir /opt/firepanel/data \
#           -panel-https <你的公网IP或域名> -panel-https-port :8443
systemctl restart firepanel
```

- 要求该 IP/域名可从**公网访问 80 或 443**（ACME 校验）；面板与反代共用 80/443 时挑战自动处理
- 签发失败（如内网 IP）会自动回退仅 HTTP 并在日志说明。内网环境改用：
  面板「反向代理 → 手动证书 → 自动生成自签证书」生成 PEM 后存成文件，配置 `panel_tls_cert` / `panel_tls_key`
- 验证：`curl https://<IP>:8443/api/v1/bootstrap`（真证书，无需 -k）

### 7. 面板整站 Basic Auth

| 方式 | 操作 | 生效时机 |
|---|---|---|
| 设置页（推荐） | 「系统设置 → 整站 Basic Auth」开关 + 账号密码 | 保存即生效 |
| 凭据文件 | 程序目录 `basicauth.yaml`（0600），手改 | 约 2 秒自动热生效 |
| 启动参数 | `-basic-auth "user:password"` | 重启生效 |

覆盖全部页面、API 与 WebSocket。浏览器首次访问弹系统级账号框。忘记密码：回设置页重设即可覆盖。

### 8. 配置文件

`config.yaml` 放在二进制同目录（或 `-config` 指定路径），命令行参数优先级更高。关键字段：

| 配置 | 默认 | 说明 |
|---|---|---|
| server.listen | :18088 | 面板 HTTP 监听 |
| server.data_dir | /var/lib/firepanel | SQLite 与证书目录 |
| server.proxy_http / proxy_https | :80 / :443 | 反代监听（三种形态见文件内注释） |
| server.panel_https | 空 | 面板 HTTPS 的 IP/域名（自动签发） |
| server.basic_auth_user / pass | 空 | 整站 Basic Auth |
| firewall.ssh_ports | [22] | 触发高危确认的 SSH 端口 |
| firewall.backend | auto | auto / iptables / nftables |
| proxy.backend | builtin | 反代引擎 builtin / caddy |

完整字段见 [deploy/config.example.yaml](deploy/config.example.yaml)。

### 9. 反向代理与已有 80/443 服务共存（模式 2）

80/443 被其他程序占用时，面板反代改用非标端口（如 8180/8143），并用**寄生前置**解决证书签发：
让占用者把 `/.well-known/acme-challenge/` 路径转发到面板的挑战端口——面板内「环境探测」可一键生成 Caddy/Nginx 片段。

共存机制（谁签发谁应答，token 不同互不碰撞）：

| 占用者 | 寄生前置是否影响其自身证书签发 | 说明 |
|---|---|---|
| Caddy（自动 HTTPS） | ❌ 不影响 | Caddy 的内置 ACME 挑战应答优先于站点路由，签自己的证书不会走到转发块 |
| Nginx + certbot | ⚠ 有冲突风险 | certbot 插入的 `location ^~` 优先级更高，会截走面板的挑战请求；需手动把两条 location 合并共存，或面板改用 DNS-01 |
| 任意程序（手动/商业证书） | ❌ 无签发行为 | 零影响 |

443 的 TLS 终结与证书加载完全归占用者，寄生前置只转发 80 上的一个路径。
面板自身 HTTPS（8443/自定义）在寄生前置下同样可签发/续期（挑战经占用者转发到面板）。
### 9. 常见部署问题

| 现象 | 原因与处理 |
|---|---|
| 外网打不开面板，本机 curl 正常 | 云安全组未放行 18088（见 §0） |
| `curl` 返回 401 | 整站 Basic Auth 开着，带上账号密码 |
| 反代/证书签发失败 | 80/443 被占用或不可达：环境探测对话框查看占用者；域名走 DNS-01 或寄生前置 |
| `install.sh` 报端口放行失败 | 防火墙工具异常，手动放行后再跑一次脚本 |
| Alpine 下连接列表为空 | 装 iproute2：`apk add iproute2`（其余功能不受影响） |

### 10. 升级

```bash
curl -fsSL -o /tmp/fp.tar.gz https://github.com/jiqing112/firepanel/releases/latest/download/firepanel-linux-amd64.tar.gz
tar -xzf /tmp/fp.tar.gz -C /tmp/fp firepanel
systemctl stop firepanel
install -m 755 /tmp/fp/firepanel /opt/firepanel/firepanel
systemctl start firepanel
```

配置、数据库、证书都在 `data/` 目录，升级不动它们。面板启动时自动把 SQLite 期望状态重放到内核——重启服务器/面板后规则自动恢复，无需额外持久化配置。

### 11. 数据备份与迁移

- 面板内：「备份迁移」页导出 JSON（转发/名单）+ iptables-save / nft ruleset
- 文件级：直接备份 `data/` 目录（`firepanel.db` + `certs/`），拷到新机同路径即完成迁移

### 12. 卸载

```bash
systemctl disable --now firepanel
rm -rf /opt/firepanel /etc/systemd/system/firepanel.service
systemctl daemon-reload
# 面板自有链（FPANEL_* / table inet firepanel）可在面板「防火墙规则」页清空，
# 或删除后由系统防火墙持久化策略在下次重载时自然消失
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
