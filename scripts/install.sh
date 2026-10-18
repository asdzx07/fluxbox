#!/bin/sh
# FluxBox 2.0 (Go + eBPF Edition) 极速安装脚本
#
#   OpenWrt / iStoreOS:
#     curl -fsSL https://raw.githubusercontent.com/asdzx07/fluxbox/main/scripts/install.sh | sh
#   Debian / Ubuntu:
#     curl -fsSL https://raw.githubusercontent.com/asdzx07/fluxbox/main/scripts/install.sh | sudo sh
#
set -eu

REPO="${REPO:-asdzx07/fluxbox}"
BRANCH="main"
ROOT=/opt/fluxbox
PORT="${PORT:-3036}"
USE_MIRROR=0
MIRRORS="https://ghfast.top https://gh-proxy.com"

while [ $# -gt 0 ]; do
  case "$1" in
    --port) PORT="$2"; shift 2 ;;
    --mirror) USE_MIRROR=1; shift ;;
    *) echo "未知参数：$1" >&2; exit 1 ;;
  esac
done

say()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[!]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = "0" ] || die "请用 root 用户运行"

# 1. 检测系统平台
if [ -f /etc/openwrt_release ]; then
  PLATFORM=openwrt
  . /etc/openwrt_release
  say "平台：OpenWrt 系（${DISTRIB_DESCRIPTION:-OpenWrt}）"
elif [ -d /run/systemd/system ]; then
  PLATFORM=systemd
  say "平台：Linux + systemd"
else
  PLATFORM=generic
  warn "未检测到 systemd 或 OpenWrt，将作为普通 Linux 进程安装"
fi

# 2. 检测 CPU 架构
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "当前 2.0 eBPF 版本暂不支持的架构：$(uname -m)（仅支持 x86_64 与 aarch64）" ;;
esac
say "CPU 架构：$ARCH"

# 3. 准备安装目录
say "准备安装目录：$ROOT"
mkdir -p "$ROOT/bin" "$ROOT/data" "$ROOT/data/flip"

# 4. 下载最新纯静态单一二进制 fluxboxd
BIN_NAME="fluxboxd-linux-$ARCH"
DOWNLOAD_URL="https://github.com/$REPO/releases/latest/download/$BIN_NAME"
TARGET_BIN="$ROOT/bin/fluxboxd"

say "正在获取 FluxBox 2.0 单二进制程序..."
got=0
if [ "$USE_MIRROR" = "1" ]; then
  for m in $MIRRORS; do
    if curl -fsSL "$m/$DOWNLOAD_URL" -o "$TARGET_BIN" 2>/dev/null; then got=1; break; fi
  done
fi
if [ "$got" = "0" ]; then
  # 优先尝试从 Release 下载，若失败则尝试加速节点
  curl -fsSL "$DOWNLOAD_URL" -o "$TARGET_BIN" 2>/dev/null || \
  curl -fsSL "https://ghfast.top/$DOWNLOAD_URL" -o "$TARGET_BIN" 2>/dev/null || \
  warn "从远程 Release 获取二进制超时，请确认网络连接"
fi

# 检查二进制有效性
if [ -s "$TARGET_BIN" ]; then
  chmod +x "$TARGET_BIN"
  say "FluxBox 二进制安装成功：$TARGET_BIN"
else
  warn "未能自动下载预编译二进制，保留已有文件或可手动上传至 $TARGET_BIN"
fi

# 5. 检查并准备 sing-box 内核
if [ ! -x "$ROOT/bin/sing-box" ] && ! command -v sing-box >/dev/null 2>&1; then
  say "正在配置配套 sing-box 内核..."
  SB_URL="https://github.com/SagerNet/sing-box/releases/download/v1.11.4/sing-box-1.11.4-linux-$ARCH.tar.gz"
  TMP_SB=$(mktemp -d)
  if curl -fsSL "$SB_URL" -o "$TMP_SB/sb.tar.gz" 2>/dev/null || curl -fsSL "https://ghfast.top/$SB_URL" -o "$TMP_SB/sb.tar.gz" 2>/dev/null; then
    tar -xzf "$TMP_SB/sb.tar.gz" -C "$TMP_SB"
    find "$TMP_SB" -type f -name "sing-box" -exec cp {} "$ROOT/bin/sing-box" \;
    chmod +x "$ROOT/bin/sing-box" 2>/dev/null || true
    say "sing-box 内核安装成功"
  fi
  rm -rf "$TMP_SB"
fi

# 6. 配置开机自启服务
if [ "$PLATFORM" = "openwrt" ]; then
  say "配置 OpenWrt Procd 服务..."
  cat <<'EOF' > /etc/init.d/fluxbox
#!/bin/sh /etc/rc.common
START=95
STOP=10
USE_PROCD=1

PROG=/opt/fluxbox/bin/fluxboxd

start_service() {
    procd_open_instance
    procd_set_param command "$PROG"
    procd_set_param respawn 3600 5 5
    procd_set_param stdout 1
    procd_set_param stderr 1
    procd_set_param limits core="unlimited"
    procd_close_instance
}

stop_service() {
    killall -15 fluxboxd 2>/dev/null || true
}
EOF
  chmod +x /etc/init.d/fluxbox
  /etc/init.d/fluxbox enable 2>/dev/null || true
  /etc/init.d/fluxbox restart 2>/dev/null || true
elif [ "$PLATFORM" = "systemd" ]; then
  say "配置 Systemd 服务..."
  cat <<EOF > /etc/systemd/system/fluxbox.service
[Unit]
Description=FluxBox 2.0 (Go + eBPF Edition)
After=network.target

[Service]
Type=simple
ExecStart=/opt/fluxbox/bin/fluxboxd
Restart=always
RestartSec=3
KillSignal=SIGINT
TimeoutStopSec=5

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable fluxbox 2>/dev/null || true
  systemctl restart fluxbox 2>/dev/null || true
fi

# 7. 打印安装成功提示
LAN_IP="127.0.0.1"
if [ "$PLATFORM" = "openwrt" ]; then
  LAN_IP=$(uci -q get network.lan.ipaddr || echo "192.168.3.2")
fi

say "================================================="
say "🎉 FluxBox 2.0 (Go + eBPF Edition) 安装完成！"
say "控制面板访问地址: http://${LAN_IP}:${PORT}"
say "主程序路径: $ROOT/bin/fluxboxd"
say "数据目录: $ROOT/data"
say "================================================="
