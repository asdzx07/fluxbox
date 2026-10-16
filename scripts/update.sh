#!/bin/sh
# MyBox 升级脚本：更新面板代码，保留 data/（订阅、设置、开关文件）。
#
#   curl -fsSL https://raw.githubusercontent.com/asdzx07/mybox/main/scripts/update.sh | sudo sh
#
# 内核不在本脚本里升级——在面板「设置 → 内核」里点「安装/更新官方内核」，
# 这样内核版本和面板版本可以各自独立。
set -eu

REPO="asdzx07/mybox"
BRANCH="main"
ROOT=/opt/mybox
USE_MIRROR=0
SRC_DIR=""
TMP=""
STAGE=""
BACKUP=""
NODE_BIN=""
TXN_HELPER=""
UPDATE_APPLIED=0
if [ -f /etc/openwrt_release ]; then PLATFORM=openwrt; else PLATFORM=systemd; fi

TOKEN="${GITHUB_TOKEN:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    --mirror) USE_MIRROR=1; shift ;;
    --src) SRC_DIR="$2"; shift 2 ;;
    --token) TOKEN="$2"; shift 2 ;;
    *) echo "未知参数：$1" >&2; exit 1 ;;
  esac
done

say()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
put()  { cp "$2" "$3" && chmod "$1" "$3"; }
die()  { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

backup_external() {
  target=$1
  key=$2
  mkdir -p "$BACKUP/external"
  if [ -e "$target" ]; then
    cp -p "$target" "$BACKUP/external/$key"
    : > "$BACKUP/external/$key.present"
  else
    : > "$BACKUP/external/$key.missing"
  fi
}

restore_external() {
  target=$1
  key=$2
  if [ -f "$BACKUP/external/$key.present" ]; then
    mkdir -p "$(dirname "$target")"
    cp -p "$BACKUP/external/$key" "$target"
  elif [ -f "$BACKUP/external/$key.missing" ]; then
    rm -f "$target"
  fi
}

restore_external_files() {
  if [ "$PLATFORM" = openwrt ]; then
    restore_external /etc/init.d/mybox-panel openwrt-panel
    restore_external /etc/init.d/mybox-kernel openwrt-kernel
    restore_external /usr/lib/lua/luci/controller/mybox.lua luci-controller
    restore_external /usr/lib/lua/luci/view/mybox/status.htm luci-status
    rm -rf /tmp/luci-indexcache 2>/dev/null || true
  else
    restore_external /etc/systemd/system/mybox-panel.service systemd-panel
    restore_external /etc/systemd/system/mybox-kernel.service systemd-kernel
    systemctl daemon-reload >/dev/null 2>&1 || true
  fi
}

restore_commit_marker() {
  [ -n "$BACKUP" ] || return 0
  if [ -f "$BACKUP/commit.sha.present" ]; then
    cp -p "$BACKUP/commit.sha" "$ROOT/data/commit.sha"
  elif [ -f "$BACKUP/commit.sha.missing" ]; then
    rm -f "$ROOT/data/commit.sha"
  fi
}

cleanup_update() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ "$status" -ne 0 ] && [ "$UPDATE_APPLIED" = 1 ]; then
    say "更新失败，正在恢复上一版代码"
    if [ -n "$NODE_BIN" ] && [ -f "$TXN_HELPER" ]; then
      "$NODE_BIN" "$TXN_HELPER" rollback "$ROOT" "$BACKUP" || say "代码自动回滚未完成，请从 $BACKUP 手动恢复"
    fi
    restore_external_files || say "外部服务文件自动恢复未完成，请从 $BACKUP/external 手动恢复"
    restore_commit_marker || say "commit.sha 自动恢复失败"
    if [ "$PLATFORM" = openwrt ]; then
      /etc/init.d/mybox-panel restart >/dev/null 2>&1 || say "上一版面板未能自动启动，请检查服务日志"
    else
      systemctl restart mybox-panel >/dev/null 2>&1 || say "上一版面板未能自动启动，请检查服务日志"
    fi
  fi
  [ -n "$STAGE" ] && rm -rf "$STAGE"
  [ -n "$TMP" ] && rm -rf "$TMP"
  exit "$status"
}

[ "$(id -u)" = "0" ] || die "请用 root 运行"
[ -d "$ROOT" ] || die "$ROOT 不存在，请先安装"
for c in "$ROOT/node/bin/node" "$(command -v node 2>/dev/null)"; do
  if [ -n "$c" ] && [ -x "$c" ]; then NODE_BIN="$c"; break; fi
done
[ -n "$NODE_BIN" ] || die "找不到 Node，无法验证更新源码"
TMP=$(mktemp -d)
trap cleanup_update EXIT
trap 'exit 1' HUP INT TERM

MIRRORS="https://ghfast.top https://gh-proxy.com"

if [ -n "$SRC_DIR" ]; then
  say "从本地目录升级：$SRC_DIR"
  SRCDIR="$SRC_DIR"
else
  say "验证并下载最新源码"
  TARBALL="$TMP/mybox.tar.gz"
  COMMIT_JSON="$TMP/commit.json"
  TREE_JSON="$TMP/tree.json"
  API_BASE="https://api.github.com/repos/$REPO"

  api_get() {
    if [ -n "$TOKEN" ]; then
      curl -fsSL -H "Authorization: Bearer $TOKEN" -H "Accept: application/vnd.github+json" -H "X-GitHub-Api-Version: 2022-11-28" "$1" -o "$2"
    else
      curl -fsSL -H "Accept: application/vnd.github+json" -H "X-GitHub-Api-Version: 2022-11-28" "$1" -o "$2"
    fi
  }

  api_get "$API_BASE/commits/$BRANCH" "$COMMIT_JSON" || die "无法从 GitHub API 验证更新提交；已停止更新"
  COMMIT_META=$("$NODE_BIN" -e 'const fs=require("fs");const j=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));if(!/^[a-f0-9]{40}$/i.test(j.sha||"")||!/^[a-f0-9]{40}$/i.test(j.commit?.tree?.sha||""))process.exit(2);process.stdout.write(`${j.sha} ${j.commit.tree.sha}`)' "$COMMIT_JSON") || die "GitHub 提交元数据无效"
  set -- $COMMIT_META
  COMMIT_SHA=$1
  TREE_SHA=$2
  api_get "$API_BASE/git/trees/$TREE_SHA?recursive=1" "$TREE_JSON" || die "无法获取提交文件树校验数据；已停止更新"

  if [ -n "$TOKEN" ]; then
    say "检测到 GitHub Token，使用认证 API 下载已固定提交"
    API_URL="$API_BASE/tarball/$COMMIT_SHA"
    if ! curl -fsSL -H "Authorization: Bearer $TOKEN" -H "Accept: application/vnd.github+json" "$API_URL" -o "$TARBALL"; then
      die "下载已验证提交失败"
    fi
  else
    URL="https://github.com/$REPO/archive/$COMMIT_SHA.tar.gz"
    ok=0
    if [ "$USE_MIRROR" = "1" ]; then
      for m in $MIRRORS; do
        if curl -fsSL "$m/$URL" -o "$TARBALL" 2>/dev/null; then ok=1; break; fi
      done
    fi
    if [ "$ok" = "0" ]; then
      curl -fsSL "$URL" -o "$TARBALL" 2>/dev/null && ok=1 || true
    fi
    [ "$ok" = "1" ] || die "下载已验证提交失败"
  fi

  tar -tzf "$TARBALL" > "$TMP/archive.list" || die "源码归档格式无效"
  tar -tvzf "$TARBALL" > "$TMP/archive.details" || die "源码归档格式无效"
  "$NODE_BIN" -e 'const fs=require("fs");const lines=fs.readFileSync(process.argv[1],"utf8").split(/\r?\n/).filter(Boolean);if(!lines.length||lines.some(line=>!/[d-]/.test(line[0]||"")))process.exit(2)' "$TMP/archive.details" || die "源码归档含有不支持的链接或特殊文件"
  ARCHIVE_ROOT=$("$NODE_BIN" -e '
    const fs=require("fs");
    const names=fs.readFileSync(process.argv[1],"utf8").split(/\r?\n/).filter(Boolean);
    let root=null;
    if(!names.length) process.exit(2);
    for(const raw of names){
      const name=raw.endsWith("/")?raw.slice(0,-1):raw;
      const parts=name.split("/");
      if(!name||name.startsWith("/")||parts.some(p=>!p||p==="."||p===".."||p.includes("\\")||/[\x00-\x1f\x7f]/.test(p))) process.exit(2);
      if(!root) root=parts[0];
      if(parts[0]!==root) process.exit(2);
    }
    process.stdout.write(root);
  ' "$TMP/archive.list") || die "源码归档包含不安全路径"
  mkdir -p "$TMP/source"
  tar -xzf "$TARBALL" -C "$TMP/source" || die "源码归档解包失败"
  SRCDIR="$TMP/source/$ARCHIVE_ROOT"

  say "校验归档内容与 Git 提交树"
  "$NODE_BIN" - "$SRCDIR" "$TREE_JSON" "$TREE_SHA" <<'NODE'
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');

const [root, treeFile, expectedRootSha] = process.argv.slice(2);
const fail = (message) => { console.error(`更新归档校验失败：${message}`); process.exit(1); };
const objectHash = (type, body) => crypto.createHash('sha1').update(`${type} ${body.length}\0`).update(body).digest('hex');
// GitHub 归档会按 .gitattributes 做换行转换（例如历史遗留的 text eol=crlf），
// 转换后的字节与 Git 树里的 blob 不同。行尾差异不改变内容语义，因此允许
// 「原始字节」或「CRLF 归一化后」两种匹配；其它任何差异仍判为篡改。
const blobMatches = (content, sha) => {
  if (objectHash('blob', content) === sha) return true;
  if (!content.includes(13)) return false;
  const normalized = Buffer.from(content.toString('binary').replace(/\r\n/g, '\n'), 'binary');
  return objectHash('blob', normalized) === sha;
};
const safePath = (value) => {
  if (typeof value !== 'string' || !value || value.includes('\\') || /[\x00-\x1f\x7f]/.test(value)) return false;
  const parts = value.split('/');
  return parts.every((part) => part && part !== '.' && part !== '..');
};

let document;
try { document = JSON.parse(fs.readFileSync(treeFile, 'utf8')); } catch { fail('Git 树 JSON 无效'); }
if (document.sha !== expectedRootSha || document.truncated || !Array.isArray(document.tree) || document.tree.length > 50000) {
  fail('Git 树摘要无效、被截断或超出验证上限');
}
const entries = new Map();
for (const entry of document.tree) {
  if (!safePath(entry.path) || !/^[a-f0-9]{40}$/i.test(entry.sha || '') || entries.has(entry.path)) fail('Git 树路径或对象摘要无效');
  if (!['blob', 'tree'].includes(entry.type)) fail('更新树包含不支持的子模块或对象');
  if (entry.type === 'blob' && !['100644', '100755', '120000'].includes(entry.mode)) fail('文件权限类型不受支持');
  if (entry.type === 'tree' && !['040000', '40000'].includes(entry.mode)) fail('目录权限类型无效');
  entries.set(entry.path, entry);
}
const directories = new Map([['', []]]);
for (const entry of entries.values()) {
  if (entry.type === 'tree') directories.set(entry.path, []);
}
for (const entry of entries.values()) {
  const slash = entry.path.lastIndexOf('/');
  const parent = slash < 0 ? '' : entry.path.slice(0, slash);
  if (!directories.has(parent)) fail('Git 树存在缺失父目录');
  directories.get(parent).push({ name: entry.path.slice(slash + 1), ...entry });
}

function verifyTree(directory) {
  const children = directories.get(directory);
  const rows = children.map((entry) => {
    const suffix = Buffer.from(entry.name + (entry.type === 'tree' ? '/' : '\0'));
    return { entry, sortKey: suffix };
  }).sort((a, b) => Buffer.compare(a.sortKey, b.sortKey));
  const bodyParts = [];
  for (const { entry } of rows) {
    const mode = entry.type === 'tree' ? '40000' : entry.mode;
    bodyParts.push(Buffer.from(`${mode} ${entry.name}\0`), Buffer.from(entry.sha, 'hex'));
  }
  const actual = objectHash('tree', Buffer.concat(bodyParts));
  const expected = directory ? entries.get(directory)?.sha : expectedRootSha;
  if (actual !== expected) fail(`目录摘要不匹配：${directory || '/'}`);
  for (const { entry } of rows) if (entry.type === 'tree') verifyTree(entry.path);
}
verifyTree('');
let rootStat;
try { rootStat = fs.lstatSync(root); } catch { fail('解包根目录不存在'); }
if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) fail('解包根路径不是普通目录');

const expectedFiles = new Map([...entries].filter(([, entry]) => entry.type === 'blob'));
const actualFiles = new Map();
function walk(directory, relative = '') {
  for (const item of fs.readdirSync(directory, { withFileTypes: true })) {
    const name = relative ? `${relative}/${item.name}` : item.name;
    const full = path.join(directory, item.name);
    const stat = fs.lstatSync(full);
    if (stat.isSymbolicLink()) actualFiles.set(name, { full, type: 'symlink', stat });
    else if (stat.isDirectory()) walk(full, name);
    else if (stat.isFile()) actualFiles.set(name, { full, type: 'file', stat });
    else fail(`存在不支持的文件类型：${name}`);
  }
}
try { walk(root); } catch (error) { fail(error.message); }
if (actualFiles.size !== expectedFiles.size) fail('归档文件数量与已验证 Git 树不一致');
for (const [name, entry] of expectedFiles) {
  const actual = actualFiles.get(name);
  if (!actual) fail(`归档缺少文件：${name}`);
  let content;
  if (entry.mode === '120000') {
    if (actual.type !== 'symlink') fail(`符号链接类型不匹配：${name}`);
    content = fs.readlinkSync(actual.full, { encoding: 'buffer' });
  } else {
    if (actual.type !== 'file') fail(`普通文件类型不匹配：${name}`);
    const executable = Boolean(actual.stat.mode & 0o111);
    if ((entry.mode === '100755') !== executable) fail(`可执行权限不匹配：${name}`);
    content = fs.readFileSync(actual.full);
  }
  if (!blobMatches(content, entry.sha)) fail(`文件校验和不匹配：${name}`);
}
for (const name of actualFiles.keys()) if (!expectedFiles.has(name)) fail(`归档含有未列入 Git 树的文件：${name}`);
NODE
  [ "$?" = "0" ] || die "源码归档完整性校验失败"
fi

[ -d "$SRCDIR/server" ] || die "源码里没有 server/"
[ -d "$SRCDIR/panel" ] || die "源码里没有 panel/"
[ -f "$SRCDIR/package.json" ] || die "源码里没有 package.json"
[ -f "$SRCDIR/scripts/update-transaction.mjs" ] || die "源码里没有更新事务工具"

say "准备暂存目录并备份当前代码"
STAMP=$(date +%Y%m%d%H%M%S)
BACKUP="$ROOT/.backup-$STAMP-$$"
STAGE="$ROOT/.update-stage-$STAMP-$$"
mkdir -p "$BACKUP" "$STAGE"
TXN_HELPER="$TMP/update-transaction.mjs"
cp "$SRCDIR/scripts/update-transaction.mjs" "$TXN_HELPER"

for d in server panel system tools scripts; do
  [ -d "$SRCDIR/$d" ] || continue
  cp -R "$SRCDIR/$d" "$STAGE/$d"
done
cp "$SRCDIR/package.json" "$STAGE/package.json"
if [ -f "$SRCDIR/VERSION" ]; then
  cp "$SRCDIR/VERSION" "$STAGE/VERSION"
else
  echo "v2.0.0 (B200)" > "$STAGE/VERSION"
fi
[ -f "$SRCDIR/commit.sha" ] && cp "$SRCDIR/commit.sha" "$STAGE/commit.sha"
[ -d "$STAGE/server" ] && [ -d "$STAGE/panel" ] && [ -d "$STAGE/tools" ] || die "暂存源码缺少必需目录"

# Node / npm 依赖先在 staging 中准备；失败时正式目录保持不动。
for c in "$ROOT/node/bin/node" "$(command -v node 2>/dev/null)"; do
  if [ -n "$c" ] && [ -x "$c" ]; then NODE_BIN="$c"; break; fi
done
[ -n "$NODE_BIN" ] || die "找不到 Node，无法验证暂存代码"
NPM_BIN=""
for c in "$ROOT/node/bin/npm" "$(command -v npm 2>/dev/null)"; do
  if [ -n "$c" ] && [ -x "$c" ]; then NPM_BIN="$c"; break; fi
done
if [ -n "$NPM_BIN" ]; then
  say "在暂存目录安装依赖"
  PATH="$ROOT/node/bin:$PATH" "$NPM_BIN" --prefix "$STAGE" install --omit=dev --no-audit --no-fund >/dev/null || die "暂存依赖安装失败，正式版本未更改"
elif [ -d "$ROOT/node_modules" ]; then
  say "没找到 npm，保留现有依赖目录"
  cp -R "$ROOT/node_modules" "$STAGE/node_modules"
else
  die "没找到 npm 或现有 node_modules，无法准备更新"
fi

say "验证暂存代码"
"$NODE_BIN" --check "$STAGE/server/index.mjs"
"$NODE_BIN" "$STAGE/tools/check-syntax.mjs"
for script in "$STAGE"/scripts/*.sh; do
  [ -f "$script" ] || continue
  sh -n "$script"
done

# 记录上一版 commit 标记，以便失败回滚时恢复；此文件位于 data/，不参与代码替换。
mkdir -p "$ROOT/data"
if [ -f "$ROOT/data/commit.sha" ]; then
  cp -p "$ROOT/data/commit.sha" "$BACKUP/commit.sha"
  : > "$BACKUP/commit.sha.present"
else
  : > "$BACKUP/commit.sha.missing"
fi

# 备份更新时会触及的系统服务文件，失败时恢复原件。
if [ "$PLATFORM" = openwrt ]; then
  backup_external /etc/init.d/mybox-panel openwrt-panel
  backup_external /etc/init.d/mybox-kernel openwrt-kernel
  backup_external /usr/lib/lua/luci/controller/mybox.lua luci-controller
  backup_external /usr/lib/lua/luci/view/mybox/status.htm luci-status
else
  backup_external /etc/systemd/system/mybox-panel.service systemd-panel
  backup_external /etc/systemd/system/mybox-kernel.service systemd-kernel
fi

# 远程归档固定到已验证的提交，记录相同提交的短 SHA。
COMMIT=$(printf '%s' "${COMMIT_SHA:-}" | cut -c1-7)

say "原子切换代码（data/ 不动）"
UPDATE_APPLIED=1
"$NODE_BIN" "$TXN_HELPER" apply "$ROOT" "$STAGE" "$BACKUP" server panel system tools scripts package.json VERSION node_modules
[ -f "$SRCDIR/commit.sha" ] && cp -f "$SRCDIR/commit.sha" "$ROOT/data/commit.sha" 2>/dev/null || true
[ -z "$COMMIT" ] || printf '%s\n' "$COMMIT" > "$ROOT/data/commit.sha"

if [ "$PLATFORM" = openwrt ]; then
  say "更新服务脚本（procd）"
  for s in mybox-panel mybox-kernel; do
    [ -f "$ROOT/system/openwrt/initd/$s" ] && put 0755 "$ROOT/system/openwrt/initd/$s" "/etc/init.d/$s"
  done

  say "安装 LuCI 兜底页"
  if [ -f "$ROOT/system/openwrt/luci/controller/mybox.lua" ]; then
    mkdir -p /usr/lib/lua/luci/controller /usr/lib/lua/luci/view/mybox
    cp "$ROOT/system/openwrt/luci/controller/mybox.lua" /usr/lib/lua/luci/controller/mybox.lua
    cp "$ROOT/system/openwrt/luci/view/mybox/status.htm" /usr/lib/lua/luci/view/mybox/status.htm
    rm -rf /tmp/luci-indexcache 2>/dev/null || true
  fi

  chmod +x "$ROOT/scripts/"*.sh 2>/dev/null || true
  ln -sf "$ROOT/scripts/update.sh" /usr/bin/mybox-update 2>/dev/null || true

  KERNEL_WAS_RUNNING=0
  /etc/init.d/mybox-kernel status 2>/dev/null | grep -q running && KERNEL_WAS_RUNNING=1

  say "重启面板"
  /etc/init.d/mybox-panel stop 2>/dev/null || true
  killall -9 node 2>/dev/null || true
  sleep 1
  /etc/init.d/mybox-panel start || /etc/init.d/mybox-panel restart || die "面板重启失败，正在回滚并恢复上一版"

  if [ "$KERNEL_WAS_RUNNING" = "1" ]; then
    say "重启内核"
    /etc/init.d/mybox-kernel restart || true
  fi
else
  say "更新 systemd 单元"
  for unit in mybox-panel mybox-kernel; do
    [ -f "$ROOT/system/$unit.service" ] && put 0644 "$ROOT/system/$unit.service" "/etc/systemd/system/$unit.service"
  done
  systemctl daemon-reload

  say "重启面板"
  systemctl restart mybox-panel || die "面板重启失败，正在回滚并恢复上一版"

  # 内核如果本来在跑，重启一次让新配置生效
  if systemctl is-active --quiet mybox-kernel; then
    say "重启内核"
    systemctl restart mybox-kernel || true
  fi
fi

"$NODE_BIN" "$TXN_HELPER" finalize "$BACKUP"
UPDATE_APPLIED=0
say "升级完成（备份：$BACKUP）"
