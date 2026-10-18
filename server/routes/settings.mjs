import fs from 'node:fs';
import os from 'node:os';

function sanitize(settings) {
  const { panel, ...rest } = settings;
  return { ...rest, panel: { passwordSet: Boolean(panel.passwordHash) } };
}

// /proc/stat 双采样算 CPU 使用率
function cpuSample() {
  try {
    const line = fs.readFileSync('/proc/stat', 'utf8').split('\n')[0];
    const p = line.split(/\s+/).slice(1).map(Number);
    const idle = (p[3] || 0) + (p[4] || 0);
    const total = p.reduce((a, b) => a + (b || 0), 0);
    return { idle, total };
  } catch { return null; }
}

export function registerSettingsRoutes(app, deps) {
  const { loadSettings, saveSettings, normalizeGroupInput, normalizePolicyInput, kernel, netstack, platform } = deps;

  app.get('/api/overview', async (req, res) => {
    const settings = loadSettings();
    const k = await kernel.status();
    const policiesEnabled = settings.policies.filter((policy) => policy.enabled).length;
    res.json({
      kernel: { ...k, versionOutput: await kernel.versionOutput() },
      dnsmasq: await netstack.dnsmasqStatus(),
      platform: platform.describe(),
      dnsPort: settings.dns?.hijackPort || 36053,
      counts: {
        subscriptions: settings.subscriptions.length,
        nodes: settings.nodes.length,
        groups: settings.groups.filter((group) => group.enabled).length,
        policies: settings.policies.length,
        policiesEnabled,
      },
      nodes: {
        total: settings.nodes.length,
      },
      policies: {
        total: settings.policies.length,
        enabled: policiesEnabled,
      },
      meta: settings.meta,
      node: process.version,
    });
  });

  app.get('/api/system/stats', async (req, res) => {
    // 双采样算 CPU 使用率（间隔 150ms，首次调用即有值）
    const first = cpuSample();
    await new Promise((r) => setTimeout(r, 150));
    const second = cpuSample();
    let cpuPct = null;
    if (first && second) {
      const dIdle = second.idle - first.idle;
      const dTotal = second.total - first.total;
      if (dTotal > 0) cpuPct = Math.max(0, Math.min(100, Math.round((1 - dIdle / dTotal) * 100)));
    }
    const totalMem = os.totalmem();
    const usedMem = totalMem - os.freemem();
    res.json({
      cpuPct,
      loadAvg: os.loadavg()[0],
      mem: {
        total: totalMem,
        used: usedMem,
        pct: totalMem ? Math.round((usedMem / totalMem) * 100) : null,
      },
      uptime: Math.floor(os.uptime()),
    });
  });

  app.get('/api/settings', async (req, res) => {
    const data = sanitize(loadSettings({ force: true }));
    // 版本以磁盘上的实际内核为准，不看 settings 里那份（可能是安装脚本装的、没记进来）
    data.kernel = { ...data.kernel, version: await kernel.installedVersion(), installed: kernel.installed() };
    res.json(data);
  });

  app.put('/api/settings', (req, res) => {
    const incoming = req.body || {};
    const current = loadSettings({ force: true });
    // 接受已知的顶层区块，防止把任意字段写进配置
    for (const key of ['kernel', 'network', 'dns', 'meta']) {
      if (incoming[key] && typeof incoming[key] === 'object') {
        if (key === 'network' && incoming.network?.tun && typeof incoming.network.tun === 'object') {
          current.network = {
            ...current.network,
            ...incoming.network,
            tun: { ...(current.network?.tun || {}), ...incoming.network.tun },
          };
        } else {
          current[key] = { ...current[key], ...incoming[key] };
        }
      }
    }
    // 兼容直接通过 settings 接口保存 groups / policies
    if (Array.isArray(incoming.groups)) current.groups = incoming.groups.map(normalizeGroupInput);
    if (Array.isArray(incoming.policies)) current.policies = incoming.policies.map(normalizePolicyInput);
    saveSettings(current);
    res.json(sanitize(current));
  });
}
