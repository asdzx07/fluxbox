#!/bin/sh
# FluxBox 安全自举升级脚本：用于旧设备脱离更新死锁，通过 SHA-256 校验安全恢复并更新系统。
#
#   sh scripts/bootstrap-update.sh [--mirror]
#
set -eu

REPO="${REPO:-asdzx07/fluxbox}"
BRANCH="${BRANCH:-v1}"
ROOT=/opt/mybox
USE_MIRROR=0
RUN_AFTER=1
SRC_FILE=""
TOKEN="${GITHUB_TOKEN:-}"

# 已固定的可信 update.sh SHA-256（严格校验，防止任意代码执行）
EXPECTED_SHA256="2046d836791a3f6ce3552a04616cde9da6bf2f694e7c8e589ee6b8d610498b68"

while [ $# -gt 0 ]; do
  case "$1" in
    --mirror) USE_MIRROR=1; shift ;;
    --token) TOKEN="$2"; shift 2 ;;
    --no-run) RUN_AFTER=0; shift ;;
    --src) SRC_FILE="$2"; shift 2 ;;
    *) echo "未知参数：$1" >&2; exit 1 ;;
  esac
done

say()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = "0" ] || die "请用 root 运行"
[ -d "$ROOT" ] || die "$ROOT 不存在，请先安装 MyBox"

TARGET="$ROOT/scripts/update.sh"
BACKUP="$ROOT/scripts/update.sh.bak"
TMP_FILE=$(mktemp)

cleanup() {
  status=$?
  rm -f "$TMP_FILE"
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

if [ -n "$SRC_FILE" ]; then
  say "使用本地脚本测试自举：$SRC_FILE"
  cp "$SRC_FILE" "$TMP_FILE"
else
  say "正在安全下载已固定摘要的新版更新脚本..."
  RAW_URL="https://raw.githubusercontent.com/$REPO/$BRANCH/scripts/update.sh"
  MIRRORS="https://ghfast.top https://gh-proxy.com"
  
  got=0
  for current_repo in "$REPO" "asdzx07/mybox"; do
    RAW_URL="https://raw.githubusercontent.com/$current_repo/$BRANCH/scripts/update.sh"
    if [ -n "$TOKEN" ]; then
      if curl -fsSL -H "Authorization: Bearer $TOKEN" "$RAW_URL" -o "$TMP_FILE" 2>/dev/null; then
        got=1
        break
      fi
    else
      if [ "$USE_MIRROR" = "1" ]; then
        for m in $MIRRORS; do
          if curl -fsSL "$m/$RAW_URL" -o "$TMP_FILE" 2>/dev/null; then
            got=1
            break 2
          fi
        done
      fi
      if curl -fsSL "$RAW_URL" -o "$TMP_FILE" 2>/dev/null; then
        got=1
        break
      fi
    fi
  done
  [ "$got" = "1" ] || die "下载新版更新脚本失败，请检查网络连接"
fi

say "正在核验更新脚本 SHA-256 摘要..."
ACTUAL_SHA=""
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL_SHA=$(sha256sum "$TMP_FILE" | awk '{print $1}')
elif [ -x "$ROOT/node/bin/node" ]; then
  ACTUAL_SHA=$("$ROOT/node/bin/node" -e 'const fs=require("fs"),c=require("crypto");process.stdout.write(c.createHash("sha256").update(fs.readFileSync(process.argv[1])).digest("hex"))' "$TMP_FILE")
elif command -v node >/dev/null 2>&1; then
  ACTUAL_SHA=$(node -e 'const fs=require("fs"),c=require("crypto");process.stdout.write(c.createHash("sha256").update(fs.readFileSync(process.argv[1])).digest("hex"))' "$TMP_FILE")
fi

[ -n "$ACTUAL_SHA" ] || die "无法计算更新脚本摘要，未找到可用哈希工具"

if [ "$ACTUAL_SHA" != "$EXPECTED_SHA256" ]; then
  die "更新脚本 SHA-256 摘要不匹配（预期：$EXPECTED_SHA256，实际：$ACTUAL_SHA），已拒绝替换"
fi

say "摘要校验通过，正在安全备份旧更新器并原子替换..."
if [ -f "$TARGET" ]; then
  cp -p "$TARGET" "$BACKUP"
fi

chmod 0755 "$TMP_FILE"
mkdir -p "$ROOT/scripts"
mv "$TMP_FILE" "$TARGET"
say "新版更新脚本已成功就绪：$TARGET"

if [ "$RUN_AFTER" = "1" ]; then
  say "正在执行新版更新器以完成整机系统平滑升级..."
  UPDATE_ARGS=""
  [ "$USE_MIRROR" = "1" ] && UPDATE_ARGS="$UPDATE_ARGS --mirror"
  [ -n "$TOKEN" ] && UPDATE_ARGS="$UPDATE_ARGS --token $TOKEN"
  
  if ! sh "$TARGET" $UPDATE_ARGS; then
    say "系统升级过程未完成，正在恢复旧版脚本备份..."
    if [ -f "$BACKUP" ]; then
      mv "$BACKUP" "$TARGET"
    fi
    die "升级失败，已恢复原版更新器"
  fi
  say "系统自举升级已全部完成！"
fi
