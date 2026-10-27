#!/usr/bin/env bash
# FirePanel 一键安装脚本
# 支持: Debian 10+ / Ubuntu 20.04+ / CentOS 7 / Rocky / Alma / Fedora / openSUSE / Arch
# 用法: sudo bash install.sh [安装目录] [面板端口]
set -euo pipefail

INSTALL_DIR="${1:-/opt/firepanel}"
PANEL_PORT="${2:-18088}"
BIN_URL="${FIREPANEL_BIN_URL:-}"   # 覆盖下载地址（离线安装时可预先放置 firepanel 到同级目录）
SERVICE=firepanel

[ "$(id -u)" -eq 0 ] || { echo "请用 root 运行（sudo bash install.sh）"; exit 1; }

echo "==> FirePanel 安装"
echo "    目录: $INSTALL_DIR  端口: $PANEL_PORT"

# ---------- 1. 识别发行版 ----------
. /etc/os-release 2>/dev/null || true
ID="${ID:-unknown}"
echo "==> 发行版: ${PRETTY_NAME:-$ID}"

PKG=""
case "$ID" in
  debian|ubuntu|linuxmint) PKG="apt" ;;
  centos|rhel|rocky|almalinux|fedora|ol) PKG="dnf-or-yum" ;;
  opensuse*|sles) PKG="zypper" ;;
  arch|manjaro) PKG="pacman" ;;
  *) PKG="none" ;;
esac

# ---------- 2. 安装防火墙工具（不存在时） ----------
need_tools=""
command -v nft >/dev/null 2>&1 || command -v iptables >/dev/null 2>&1 || need_tools=1
if [ -n "$need_tools" ]; then
  echo "==> 安装防火墙工具"
  case "$PKG" in
    apt)       apt-get update && apt-get install -y nftables iptables ;;
    dnf-or-yum)
      if command -v dnf >/dev/null 2>&1; then dnf install -y nftables iptables-nft
      else yum install -y nftables iptables-services; fi ;;
    zypper)    zypper --non-interactive install nftables iptables ;;
    pacman)    pacman -Sy --noconfirm nftables iptables ;;
    *) echo "!! 未识别的包管理器，请手动安装 nftables 或 iptables"; exit 1 ;;
  esac
fi

# ---------- 3. 放置二进制 ----------
mkdir -p "$INSTALL_DIR/data"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64|aarch64|arm64) ;;
  *) echo "!! 仅支持 x86_64 / aarch64（当前 $ARCH）"; exit 1 ;;
esac

if [ -f "$(dirname "$0")/firepanel" ]; then
  echo "==> 使用本地二进制"
  cp "$(dirname "$0")/firepanel" "$INSTALL_DIR/firepanel"
elif [ -n "$BIN_URL" ]; then
  echo "==> 下载 $BIN_URL"
  curl -fsSL "$BIN_URL" -o "$INSTALL_DIR/firepanel"
else
  echo "!! 未找到 firepanel 二进制：将文件放到脚本同目录，或用 FIREPANEL_BIN_URL 指定下载地址"
  exit 1
fi
chmod +x "$INSTALL_DIR/firepanel"

# ---------- 4. systemd 服务 ----------
cat > /etc/systemd/system/${SERVICE}.service <<UNIT
[Unit]
Description=FirePanel firewall web panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$INSTALL_DIR/firepanel -listen :$PANEL_PORT -data-dir $INSTALL_DIR/data
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
UNIT

# ---------- 5. 防火墙放行面板端口（备份 + 持久化） ----------
echo "==> 备份并放行面板端口 $PANEL_PORT"
BACKUP="/tmp/firepanel-ruleset-backup-$(date +%s).txt"
if command -v nft >/dev/null 2>&1; then
  nft list ruleset > "$BACKUP" 2>/dev/null && echo "    nftables 快照: $BACKUP"
elif command -v iptables >/dev/null 2>&1; then
  iptables-save > "$BACKUP" 2>/dev/null && echo "    iptables 快照: $BACKUP"
fi

# 立即放行 + 开机自动重放：写成独立脚本并由 systemd 单元在面板启动前执行。
# 只插一条 iptables 规则是不够的——重启后规则会被宿主防火墙的持久化配置覆盖，
# 面板端口就再次被挡在外面（云主机上表现为「安装当天能用，重启后失联」）。
BOOTSTRAP_SCRIPT=/usr/local/sbin/firepanel-bootstrap-firewall
cat > "$BOOTSTRAP_SCRIPT" <<BOOTSTRAP
#!/bin/sh
# 由 FirePanel install.sh 生成：放行面板端口（幂等，可重复执行）。
PORT="$PANEL_PORT"
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q 'Status: active'; then
  ufw allow "\$PORT/tcp" >/dev/null 2>&1 && exit 0
fi
if command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state 2>/dev/null | grep -q running; then
  firewall-cmd --permanent --add-port="\$PORT/tcp" >/dev/null 2>&1 || true
  firewall-cmd --reload >/dev/null 2>&1 || true
  exit 0
fi
if command -v iptables >/dev/null 2>&1; then
  iptables -C INPUT -p tcp --dport "\$PORT" -m comment --comment "fp:bootstrap" -j ACCEPT 2>/dev/null \
    || iptables -I INPUT 1 -p tcp --dport "\$PORT" -m comment --comment "fp:bootstrap" -j ACCEPT 2>/dev/null \
    || true
  # 可用时顺带做一次发行版持久化（bootstrap 单元本身也会在每次开机重放）
  if command -v netfilter-persistent >/dev/null 2>&1; then
    netfilter-persistent save >/dev/null 2>&1 || true
  elif [ -d /etc/iptables ]; then
    iptables-save > /etc/iptables/rules.v4 2>/dev/null || true
  elif [ -f /etc/sysconfig/iptables ]; then
    iptables-save > /etc/sysconfig/iptables 2>/dev/null || true
  fi
  exit 0
fi
if command -v nft >/dev/null 2>&1; then
  nft add table inet firepanel_bootstrap 2>/dev/null || true
  nft add chain inet firepanel_bootstrap input '{ type filter hook input priority -200; policy accept; }' 2>/dev/null || true
  nft add rule inet firepanel_bootstrap input tcp dport "\$PORT" counter accept 2>/dev/null || true
fi
exit 0
BOOTSTRAP
chmod 755 "$BOOTSTRAP_SCRIPT"

cat > /etc/systemd/system/firepanel-bootstrap.service <<'UNIT'
[Unit]
Description=FirePanel bootstrap firewall allow (panel port)
After=network-online.target firewalld.service nftables.service netfilter-persistent.service
Wants=network-online.target
Before=firepanel.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/sbin/firepanel-bootstrap-firewall

[Install]
WantedBy=multi-user.target
UNIT

if "$BOOTSTRAP_SCRIPT"; then
  echo "    已放行 tcp/$PANEL_PORT（开机由 firepanel-bootstrap.service 自动重放）"
else
  echo "    !! 自动放行失败，请手动放行 tcp/$PANEL_PORT（云服务器还需在安全组放行）"
fi
echo "    说明：nft-only 主机若 INPUT 链策略是 drop，还需要在宿主自身的防火墙配置里放行该端口"
# ---------- 6. 启动 ----------
systemctl daemon-reload
systemctl enable --now firepanel-bootstrap.service ${SERVICE}
sleep 2
systemctl is-active ${SERVICE} >/dev/null && echo "==> FirePanel 已启动" || { echo "!! 启动失败"; journalctl -u ${SERVICE} -n 20 --no-pager; exit 1; }

IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
echo
echo "=============================================="
echo "  安装完成！浏览器访问: http://${IP:-<本机IP>}:${PANEL_PORT}"
echo "  首次访问将引导创建管理员账号"
echo "  服务管理: systemctl {status|restart} ${SERVICE}"
echo "  数据目录: $INSTALL_DIR/data（SQLite，可直接备份）"
echo "=============================================="
