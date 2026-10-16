import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { RULESET_DIR, DATA_DIR } from './paths.mjs';
import { ruleSetUrl, ADBLOCK_URL } from './configgen.mjs';

const MIRRORS = ['', 'https://ghfast.top/', 'https://gh-proxy.com/'];
const RULESET_META_FILE = path.join(DATA_DIR, 'ruleset-meta.json');

/**
 * 格式化时间戳为规范的年月日时分秒。
 */
export function formatDateTime(msOrDate) {
  if (!msOrDate) return '未更新';
  const d = new Date(msOrDate);
  if (Number.isNaN(d.getTime())) return '未更新';
  const pad = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/**
 * 读取规则集元数据缓存文件。
 */
export function loadRulesetMeta(dataDir = DATA_DIR) {
  const metaPath = path.join(dataDir, 'ruleset-meta.json');
  try {
    if (fs.existsSync(metaPath)) {
      const data = JSON.parse(fs.readFileSync(metaPath, 'utf8'));
      if (data && typeof data === 'object') {
        return {
          updatedAt: data.updatedAt || null,
          formattedDate: formatDateTime(data.updatedAt),
          hasUpdate: Boolean(data.hasUpdate),
          items: data.items || {},
        };
      }
    }
  } catch {}

  // 若元数据文件不存在，尝试以规则集目录下最新文件修改时间作为基准
  let fallbackMtime = null;
  try {
    const rsDir = path.join(dataDir, 'rulesets');
    if (fs.existsSync(rsDir)) {
      for (const f of fs.readdirSync(rsDir)) {
        if (f.endsWith('.srs') || f.endsWith('.json')) {
          const stat = fs.statSync(path.join(rsDir, f));
          if (!fallbackMtime || stat.mtimeMs > fallbackMtime) {
            fallbackMtime = stat.mtimeMs;
          }
        }
      }
    }
  } catch {}

  return {
    updatedAt: fallbackMtime,
    formattedDate: formatDateTime(fallbackMtime),
    hasUpdate: false,
    items: {},
  };
}

/**
 * 保存规则集元数据到持久化文件。
 */
export function saveRulesetMeta(meta, dataDir = DATA_DIR) {
  const metaPath = path.join(dataDir, 'ruleset-meta.json');
  try {
    const payload = {
      updatedAt: meta.updatedAt || null,
      formattedDate: formatDateTime(meta.updatedAt),
      hasUpdate: Boolean(meta.hasUpdate),
      items: meta.items || {},
    };
    fs.writeFileSync(metaPath, `${JSON.stringify(payload, null, 2)}\n`, 'utf8');
  } catch {}
}

/**
 * 汇总当前配置中所有已启用的远程规则集项。
 */
export function collectRemoteRulesets(settings) {
  const seen = new Set();
  const list = [];

  // 1. 策略中引用的规则集
  for (const policy of settings.policies || []) {
    if (!policy.enabled) continue;
    for (const tag of policy.rulesets || []) {
      if (seen.has(tag)) continue;
      seen.add(tag);
      const url = ruleSetUrl(tag);
      if (url) {
        list.push({ tag, url, type: 'policy', format: 'binary' });
      }
    }
  }

  // 2. 自定义规则集订阅
  for (const sub of settings.rulesetSubs || []) {
    if (!sub.enabled || !sub.tag || !sub.url) continue;
    if (seen.has(sub.tag)) continue;
    seen.add(sub.tag);
    list.push({ tag: sub.tag, url: sub.url, type: 'custom', format: sub.format || 'binary' });
  }

  // 3. 广告拦截规则集
  if (settings.dns?.adblock) {
    if (!seen.has('adblock')) {
      seen.add('adblock');
      list.push({ tag: 'adblock', url: ADBLOCK_URL, type: 'adblock', format: 'binary' });
    }
  }

  return list;
}

/**
 * 计算文件或 Buffer 的 SHA-256 哈希。
 */
function sha256Buffer(buf) {
  return createHash('sha256').update(buf).digest('hex');
}

/**
 * 拉取远程规则集内容并带镜像重试机制。
 */
async function fetchRulesetBuffer(rawUrl, { timeoutMs = 12000 } = {}) {
  const candidates = [];
  if (rawUrl.startsWith('https://raw.githubusercontent.com/')) {
    const rawPath = rawUrl.replace(/^https:\/\/raw\.githubusercontent\.com\//, '');
    for (const mirror of MIRRORS) {
      candidates.push(mirror ? `${mirror.replace(/\/+$/, '')}/${rawUrl}` : rawUrl);
    }
  } else {
    candidates.push(rawUrl);
  }

  let lastErr = null;
  for (const targetUrl of candidates) {
    try {
      const res = await fetch(targetUrl, {
        signal: AbortSignal.timeout(timeoutMs),
        headers: { 'User-Agent': 'mybox/2.0.0' },
      });
      if (res.ok) {
        const arrBuf = await res.arrayBuffer();
        return Buffer.from(arrBuf);
      }
      lastErr = new Error(`HTTP ${res.status}`);
    } catch (err) {
      lastErr = err;
    }
  }
  throw lastErr || new Error('下载规则集失败');
}

/**
 * 执行规则集更新并检测变动。
 */
export async function refreshRulesets({
  settings,
  dataDir = DATA_DIR,
  kernel,
  mutateSettings,
  log,
  targetTag = null,
} = {}) {
  const rsDir = path.join(dataDir, 'rulesets');
  if (!fs.existsSync(rsDir)) {
    fs.mkdirSync(rsDir, { recursive: true });
  }

  const allRulesets = collectRemoteRulesets(settings);
  const targets = targetTag ? allRulesets.filter((item) => item.tag === targetTag) : allRulesets;

  const meta = loadRulesetMeta(dataDir);
  let updatedCount = 0;
  let totalCount = targets.length;
  const now = Date.now();

  for (const item of targets) {
    const ext = item.format === 'source' ? 'json' : 'srs';
    const filePath = path.join(rsDir, `${item.tag}.${ext}`);
    let existingHash = null;

    if (fs.existsSync(filePath)) {
      try {
        existingHash = sha256Buffer(fs.readFileSync(filePath));
      } catch {}
    }

    try {
      const remoteBuf = await fetchRulesetBuffer(item.url);
      const remoteHash = sha256Buffer(remoteBuf);

      if (!existingHash || existingHash !== remoteHash) {
        fs.writeFileSync(filePath, remoteBuf);
        updatedCount += 1;
        meta.items[item.tag] = {
          tag: item.tag,
          url: item.url,
          format: item.format,
          updatedAt: now,
          formattedDate: formatDateTime(now),
          size: remoteBuf.length,
          hash: remoteHash,
          hasUpdate: true,
        };
      } else {
        // 无变化
        meta.items[item.tag] = {
          tag: item.tag,
          url: item.url,
          format: item.format,
          updatedAt: meta.items[item.tag]?.updatedAt || now,
          formattedDate: formatDateTime(meta.items[item.tag]?.updatedAt || now),
          size: remoteBuf.length,
          hash: remoteHash,
          hasUpdate: false,
        };
      }
    } catch (err) {
      if (log?.warn) {
        log.warn('拉取规则集 %s 失败：%s', item.tag, err.message);
      }
      if (existingHash && !meta.items[item.tag]) {
        meta.items[item.tag] = {
          tag: item.tag,
          url: item.url,
          format: item.format,
          updatedAt: now,
          formattedDate: formatDateTime(now),
          hasUpdate: false,
        };
      }
    }
  }

  const hasAnyUpdate = updatedCount > 0;
  if (hasAnyUpdate || !meta.updatedAt) {
    meta.updatedAt = now;
    meta.formattedDate = formatDateTime(now);
  }
  meta.hasUpdate = hasAnyUpdate;

  saveRulesetMeta(meta, dataDir);

  // 同步更新 settings.rulesetSubs 中的时间戳
  if (typeof mutateSettings === 'function') {
    mutateSettings((s) => {
      for (const sub of s.rulesetSubs || []) {
        if (meta.items[sub.tag]) {
          sub.updatedAt = meta.items[sub.tag].updatedAt;
        }
      }
    });
  }

  // 仅在有规则集更新时重启内核以应用新规则
  let restarted = false;
  if (hasAnyUpdate && kernel?.restart) {
    try {
      await kernel.restart();
      restarted = true;
    } catch (err) {
      if (log?.warn) log.warn('规则集更新后重启内核失败：%s', err.message);
    }
  }

  return {
    ok: true,
    hasUpdate: hasAnyUpdate,
    updatedCount,
    totalCount,
    updatedAt: meta.updatedAt,
    formattedDate: meta.formattedDate,
    restarted,
    items: meta.items,
  };
}
