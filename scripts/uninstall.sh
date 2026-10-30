#!/bin/sh
# FluxBox 2.0 卸载脚本
#
#   curl -fsSL https://raw.githubusercontent.com/asdzx07/fluxbox/main/scripts/uninstall.sh | sudo sh
#
set -eu

ROOT=/opt/fluxbox
PURGE=0
[ "${1:-}" = "--purge" ] && PURGE=1

say() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = "0" ] || die "请用 root 用户运行"

say "停止 FluxBox 2.0 服务..."
killall -15 fluxboxd 2>/dev/null || true

if [ -f /etc/openwrt_release ]; then
  /etc/init.d/fluxbox stop 2>/dev/null || true
  /etc/init.d/fluxbox disable 2>/dev/null || true
  rm -f /etc/init.d/fluxbox
elif [ -d /run/systemd/system ]; then
  systemctl stop fluxbox 2>/dev/null || true
  systemctl disable fluxbox 2>/dev/null || true
  rm -f /etc/systemd/system/fluxbox.service
  systemctl daemon-reload
fi

say "彻底清理残留网络表与策略路由..."
nft delete table inet fluxbox_tproxy 2>/dev/null || true
ip rule del fwmark 0x2023 table 2022 2>/dev/null || true
ip rule del fwmark 0x2024 table main 2>/dev/null || true
ip route flush table 2022 2>/dev/null || true

# 还原 dnsmasq（清理内存 drop-in 配置并兼容还原历史 UCI）
rm -f /tmp/dnsmasq.d/fluxbox.conf
if command -v uci >/dev/null 2>&1; then
  uci -q delete dhcp.@dnsmasq[0].server
  uci set dhcp.@dnsmasq[0].noresolv=0
  uci commit dhcp
fi
if command -v ubus >/dev/null 2>&1; then
  ubus call service reload '{"name":"dnsmasq"}' 2>/dev/null || true
fi
/etc/init.d/dnsmasq restart 2>/dev/null || true
say "已还原 dnsmasq 默认上游配置"

if [ "$PURGE" = "1" ]; then
  say "删除 $ROOT 目录（含配置文件与节点数据）"
  rm -rf "$ROOT"
else
  say "保留数据目录 $ROOT/data，删除主二进制"
  rm -rf "$ROOT/bin"
fi

say "✅ FluxBox 2.0 卸载完成，软路由网络已完全恢复默认原生状态。"
