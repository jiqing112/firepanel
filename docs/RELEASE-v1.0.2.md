# FirePanel v1.0.2 发布说明（2026-10-28）

> 更新：**v1.0.3 已发布**（2026-10-28），与 v1.0.2 功能一致，仅修正产物未注入版本号的问题。`releases/latest` 现已指向 v1.0.3，下载即 v1.0.3。

本轮后端审计与安全加固的修复已随 `v1.0.2` 发布。逐条问题清单见 [`FIXES.md`](./FIXES.md)（编号 #1–#55），本文是面向升级者的发布口径。

## 发布信息

| 项 | 值 |
|---|---|
| 标签 | `v1.0.2`（注释标签）→ `7f8376a` |
| 发布日期 | 2026-10-28 |
| 对比基线 | `v1.0.1`（`16bd4cf`）：13 个提交 / 99 文件，+5688 −1163 |
| CI | run #14（`7f8376a`）通过，1m57s |
| Release 构建 | run #3 通过，2m14s |
| 产物 | `firepanel-linux-amd64.tar.gz` 7.6 MB、`firepanel-linux-arm64.tar.gz` 6.9 MB |
| 包内容 | `firepanel` + `install.sh` + `config.example.yaml` + `firepanel.service` |

下载（始终指向最新版）：

```bash
curl -fsSL -o /tmp/fp.tar.gz https://github.com/jiqing112/firepanel/releases/latest/download/firepanel-linux-amd64.tar.gz
```

## 本版包含的变更

`v1.0.1` 之后 main 上有 8 个提交从未发过版，本次一并带出；加上本轮审计的 5 个提交，合计 13 个、99 个文件。

### 一、首次进入 Release 的既有提交（8 个）

- 全部 42 个写端点挂 `requireAdmin`，viewer 彻底只读
- HTTP → HTTPS 自动跳转（307，ACME 路径除外）；登录后 `/me` 校验防竞态清态；WS 鉴权失败停止无限重连
- 设置页新增面板端口自定义（HTTP/HTTPS 试绑校验 + 持久化 + 一键重启生效）
- 引擎切换不再自动停 Caddy（其可能是 80/443 的合法占用者），改为运行检测 + 事件提示；补充寄生前置共存说明
- README 安装部署章节扩写、方式二改为 root 直跑、排版修正

### 二、本轮审计修复（`FIXES.md` #1–#55，按主题）

- **可交付性与防锁死**（#1 #2 #4）：干净检出可构建；高危变更挂起态持久化，重启不再绕过二次确认直接生效
- **防火墙内核正确性**（#5 #6 #7 #8 #10 #13 #29 #30 #31）：nft 无端口限速不再导致整批下发失败；`iptables-save -c` 恢复命中计数；无 IPv6 时只管理 IPv4；读库失败不再下发空规则；nft 改为整表重建以清除残留 meter；IPv6 DNAT 渲染 `[addr]:port`；Caddyfile 内容未变时跳过备份与重载
- **数据安全**（#9 #11）：回滚快照读取失败即中止变更；恢复后重载反代/定时任务/告警/防爆破
- **认证与接口边界**（#3 #17 #18 #23 #27 #43 #55）：WS 令牌移出查询串；路径校验前置到 API 边界；删除并存的旧实现；Basic Auth 凭据移入数据目录；端口试绑校验收紧；数据库错误不再伪装成 404
- **存储 / 调度 / 告警**（#44–#54）：IP 名单规范化与封禁去重；流量按实际时间差积分；Reload 先读成功再替换、调度动作改走 `Mutate`；UPSERT 回查真实 ID；告警配置统一校验与日志脱敏；过期时间时区归一；NAT 仅接受 IPv4
- **前端交互**（#32–#42）：401 不再无限跳转；任务表单 `target_id` 类型修正；恢复备份二次确认；下载走带鉴权通道；备注与启停状态不再被静默覆盖；密码长度前后端对齐
- **部署与工程化**（#14 #15 #16 #24 #25 #26 #28）：compose 改 host 网络；开机重放放行规则；监听失败非零退出；全量 gofmt 并加 CI 门禁；ignore 与换行符；多架构构建；Caddy 服务启动结果校验

## 从 v1.0.1 升级

```bash
# 1. 取新包
curl -fsSL -o /tmp/fp.tar.gz https://github.com/jiqing112/firepanel/releases/latest/download/firepanel-linux-amd64.tar.gz
mkdir -p /tmp/fp && tar -xzf /tmp/fp.tar.gz -C /tmp/fp

# 2. 只换二进制，数据目录 data/ 不动
install -m 755 /tmp/fp/firepanel /opt/firepanel/firepanel

# 3. 重启（启动时自动把 SQLite 期望态重放到内核）
systemctl restart firepanel
systemctl is-active firepanel
curl -s http://127.0.0.1:18088/api/v1/bootstrap
```

- 数据库与证书都在 `data/`，升级不触碰；重启会自动重放期望状态，无需手工重下规则
- 配置键兼容：`firewall.ssh_ports` 优先，旧 `server.ssh_ports` 仅兜底，冲突时启动告警
- 高危变更若处于挂起状态，升级前建议先在面板里确认或撤销

## 已知问题

- **Release 产物版本号注入 —— 已在 v1.0.3 修正**：`.github/workflows/release.yml` 现按 tag 名解析并注入 `-X main.version`，`deploy/Dockerfile` 增加 `ARG VERSION`。因此 `v1.0.2` 及更早下载的包 `./firepanel -version` 仍输出 `firepanel dev`，v1.0.3 起输出 `firepanel v1.0.3`（本地 `scripts/build.sh` 构建一直正常）
- JSON 备份不含用户与设置（JWT 密钥、Basic Auth、DNS-01 凭据）；反代手动证书会连同私钥写入备份，需按密钥保管
- nft-only 且 INPUT 策略为 drop 的主机：安装脚本的放行规则可能被宿主自己的 base chain 覆盖，需在宿主防火墙配置中显式放行面板端口
