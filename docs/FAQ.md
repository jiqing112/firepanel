# 常见问题（FAQ）

## 面板会不会和 Docker / firewalld / ufw 冲突？

不会。面板只读写自己的链/表（iptables: `FPANEL_INPUT/FPANEL_FORWARD/FPANEL_DNAT/FPANEL_SNAT`；
nftables: `table inet firepanel`），不触碰其他服务的规则。Docker、firewalld、incus 的规则只读展示
（防火墙规则页），面板对它们的唯一影响是挂接点插在内置链最前，优先级高于默认链策略。

firewalld 启用时面板以 nftables 共存模式工作（firewalld 默认也是 nftables 后端，两者各自独立表）。

## 规则在重启后还在吗？

在。面板每次启动会从 SQLite 重放「期望状态」到内核（日志中的「启动重放完成」），这是主持久化机制，
不依赖 iptables-save / netfilter-persistent。若系统自带持久化工具同时启用，面板设置页提供「接管模式」
说明：建议停用系统工具避免双重管理，或保持共存（面板重放以自有链为界，不会叠加）。

## 我手动改了 iptables/nft，面板发现不了吗？

能。漂移检测默认每 30 秒运行一次，也可在「防火墙规则」页手动触发。漂移报告会精确到条
（缺失 / 多余 / 被改），并可一键「重新同步」以面板数据覆盖内核。

## 什么是危险变更？为什么我的操作要二次确认？

凡是可能导致管理连接中断的变更——转发/放行 SSH 端口、封禁你当前登录的 IP、封禁全网段——
都会进入高危流程：60 秒延时生效 → 90 秒确认窗口 → 超时未确认自动回滚。
这是防锁死机制，宁多一步、不失联。

## 80 / 443 端口被占用，反代起不来？

用 `-proxy-http :8180 -proxy-https :8143` 换监听端口（或 Docker 改映射）。
Let's Encrypt HTTP-01 验证需要公网可达的 80 端口；内网环境请用手动证书或无 TLS 模式。

## 域名目标（DDNS）多久解析一次？

转发规则的目标域名由后台 janitor 周期解析（当前 TTL 策略：每次全量同步前解析），解析结果变化会
自动更新 DNAT。也可配合「定时任务」做低频强制同步。

## 密码忘了怎么办？

```bash
systemctl stop firepanel
rm /opt/firepanel/data/firepanel.db   # 会同时清空规则配置，建议先备份
systemctl start firepanel             # 重新走初始化向导，再从备份恢复规则
```

## 如何完全卸载？

```bash
sudo systemctl disable --now firepanel
sudo rm /etc/systemd/system/firepanel.service
# 清理面板链（二选一，按当前后端）：
sudo nft delete table inet firepanel
# 或 sudo iptables -t nat -F FPANEL_DNAT ... （详见防火墙规则页链名）
sudo rm -rf /opt/firepanel
```

## 面板端口如何修改？

`-listen :9000` 参数或编辑 systemd unit 的 ExecStart；安装脚本第二个参数亦可指定。
