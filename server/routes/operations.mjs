import crypto from 'node:crypto';
import { loadRulesetMeta, refreshRulesets, formatDateTime } from '../lib/rulesets.mjs';

export const UPDATE_SCRIPT_SHA256 = '2046d836791a3f6ce3552a04616cde9da6bf2f694e7c8e589ee6b8d610498b68';
export const TRUSTED_UPDATE_SCRIPT_SHA256 = [
  UPDATE_SCRIPT_SHA256,
  'bed9b1d8c6fc2088a6cbcfa303c0157e98d069a53620fc73daa16d86c609cf5a',
];

export function registerOperationRoutes(app, deps) {
  const { fs, path, spawn, ROOT, DATA_DIR, loadSettings, mutateSettings, getTraffic, resetTrafficTotals, kernel, deploy, netstack, log } = deps;
  const fetchImpl = deps.fetchImpl || globalThis.fetch;

  app.get('/api/traffic', (req, res) => {
    res.json({ ok: true, ...getTraffic() });
  });

  app.post('/api/traffic/reset', (req, res) => {
    resetTrafficTotals();
    res.json({ ok: true });
  });

  app.get('/api/netstack/fastpath', async (req, res) => {
    try {
      const status = await netstack.getDirectFastpathStatus();
      res.json({ ok: true, ...status });
    } catch (err) {
      res.status(500).json({ ok: false, error: err.message });
    }
  });

  app.post('/api/netstack/fastpath', async (req, res) => {
    try {
      const ips = Array.isArray(req.body?.ips) ? req.body.ips : [req.body?.ip].filter(Boolean);
      const timeout = req.body?.timeout ? String(req.body.timeout).trim() : '2h';
      const result = await netstack.addDirectFastpathIps(ips, { timeout });
      res.json({ ok: true, ...result });
    } catch (err) {
      res.status(500).json({ ok: false, error: err.message });
    }
  });

  app.delete('/api/netstack/fastpath', async (req, res) => {
    try {
      await netstack.flushDirectFastpath();
      res.json({ ok: true });
    } catch (err) {
      res.status(500).json({ ok: false, error: err.message });
    }
  });

  app.get('/api/rulesets/status', (req, res) => {
    try {
      const meta = loadRulesetMeta(DATA_DIR);
      res.json({ ok: true, ...meta });
    } catch (err) {
      res.status(500).json({ ok: false, error: err.message });
    }
  });

  app.post('/api/adblock/refresh', async (req, res) => {
    try {
      const settings = loadSettings();
      const result = await refreshRulesets({
        settings,
        dataDir: DATA_DIR,
        kernel,
        mutateSettings,
        log,
        targetTag: 'adblock',
      });
      res.json(result);
    } catch (err) {
      res.status(500).json({ ok: false, error: err.message });
    }
  });

  app.post('/api/rulesets/refresh', async (req, res) => {
    try {
      const settings = loadSettings();
      const targetTag = req.body?.tag ? String(req.body.tag).trim() : null;
      const result = await refreshRulesets({
        settings,
        dataDir: DATA_DIR,
        kernel,
        mutateSettings,
        log,
        targetTag,
      });
      res.json(result);
    } catch (err) {
      res.status(500).json({ ok: false, error: err.message });
    }
  });

  app.post('/api/deploy', async (req, res) => {
    try {
      const report = await deploy.deploy({ restart: req.body?.restart !== false });
      res.json(report);
    } catch (err) {
      res.status(500).json({ ok: false, error: err.message });
    }
  });

  app.post('/api/teardown', async (req, res) => {
    try {
      res.json(await deploy.teardown());
    } catch (err) {
      res.status(500).json({ error: err.message });
    }
  });

  app.get('/api/system/version', async (req, res) => {
    try {
      let semver = null;
      let buildNumber = null;
      let commitSha = null;

      // 1. 优先尝试从 VERSION 文件读取（官方权威版本文件）
      try {
        const versionFile = path.join(ROOT, 'VERSION');
        if (fs.existsSync(versionFile)) {
          const versionText = fs.readFileSync(versionFile, 'utf8').trim();
          const bMatch = versionText.match(/B(\d+)/i) || versionText.match(/\d+\.\d+\.(\d+)/);
          if (bMatch) buildNumber = Number.parseInt(bMatch[1], 10);
          const vMatch = versionText.match(/v?(\d+\.\d+\.\d+)/) || versionText.match(/v?(\d+\.\d+)/);
          if (vMatch) semver = `v${vMatch[1]}`;
        }
      } catch {}

      if (!semver) {
        try {
          const packageInfo = JSON.parse(fs.readFileSync(path.join(ROOT, 'package.json'), 'utf8'));
          if (/^\d+\.\d+\.\d+$/.test(packageInfo.version || '')) semver = `v${packageInfo.version}`;
        } catch {}
      }

      // 2. 本地 git 计数兜底（仅在无 VERSION 文件时）
      if (!fs.existsSync(path.join(ROOT, 'VERSION'))) {
        try {
          const { execSync } = await import('node:child_process');
          const count = execSync('git rev-list --count HEAD', { cwd: ROOT, encoding: 'utf8', timeout: 1500 }).trim();
          if (/^\d+$/.test(count)) buildNumber = Number.parseInt(count, 10);
          const sha = execSync('git rev-parse --short HEAD', { cwd: ROOT, encoding: 'utf8', timeout: 1500 }).trim();
          if (sha) commitSha = sha;
        } catch {}
      }

      // 3. 读取本地 commit.sha（展示用）
      // 3. 读取本地 commit.sha（展示用，优先从 data/ 读取真实安装写入的哈希）
      if (!commitSha) {
        for (const sf of [path.join(ROOT, 'data', 'commit.sha'), path.join(ROOT, 'commit.sha')]) {
          try {
            if (fs.existsSync(sf)) {
              commitSha = fs.readFileSync(sf, 'utf8').trim().slice(0, 7);
              if (commitSha) break;
            }
          } catch {}
        }
      }
      const shortVersion = semver
        ? `${semver}${Number.isFinite(buildNumber) ? ` (B${buildNumber})` : ''}`
        : '版本未知';
      const current = `FluxBox: ${shortVersion}`;

      // 4. 从远程 GitHub 检测是否有最新版本与构建
      let remoteCommitSha = null;
      let remoteSemver = semver;
      let remoteBuild = buildNumber;
      let hasUpdate = false;

      // 优先从 raw 读取最新 VERSION 文件（带防缓存时间戳与国内镜像重试，兼顾 fluxbox 与 mybox 仓库地址）
      const cacheBust = `?t=${Date.now()}`;
      const versionCandidates = [
        `https://ghfast.top/https://raw.githubusercontent.com/asdzx07/fluxbox/main/VERSION${cacheBust}`,
        `https://gh-proxy.com/https://raw.githubusercontent.com/asdzx07/fluxbox/main/VERSION${cacheBust}`,
        `https://raw.githubusercontent.com/asdzx07/fluxbox/main/VERSION${cacheBust}`,
        `https://ghfast.top/https://raw.githubusercontent.com/asdzx07/mybox/main/VERSION${cacheBust}`,
        `https://gh-proxy.com/https://raw.githubusercontent.com/asdzx07/mybox/main/VERSION${cacheBust}`,
        `https://raw.githubusercontent.com/asdzx07/mybox/main/VERSION${cacheBust}`,
      ];

      let fetchSucceeded = false;
      for (const vUrl of versionCandidates) {
        try {
          const rawRes = await fetchImpl(vUrl, {
            headers: { 'User-Agent': 'fluxbox/2.0.0', 'Cache-Control': 'no-cache' },
            signal: AbortSignal.timeout(5000),
          });
          if (rawRes.ok) {
            const rawText = (await rawRes.text()).trim();
            const rBMatch = rawText.match(/B(\d+)/i) || rawText.match(/\d+\.\d+\.(\d+)/);
            if (rBMatch) remoteBuild = Number.parseInt(rBMatch[1], 10);
            const rVMatch = rawText.match(/v?(\d+\.\d+\.\d+)/) || rawText.match(/v?(\d+\.\d+)/);
            if (rVMatch) remoteSemver = `v${rVMatch[1]}`;
            fetchSucceeded = true;
            break;
          }
        } catch {}
      }

      // 仅在本地或远程缺少数字构建号时（例如开发环境源码调试），才退化到检查最新 Git Commit SHA
      if (!Number.isFinite(buildNumber) || !Number.isFinite(remoteBuild)) {
        for (const repoName of ['asdzx07/fluxbox', 'asdzx07/mybox']) {
          try {
            const response = await fetchImpl(`https://api.github.com/repos/${repoName}/commits/main`, {
              headers: { 'User-Agent': 'fluxbox/2.0.0' },
              signal: AbortSignal.timeout(5000),
            });
            if (response.ok) {
              const json = await response.json();
              remoteCommitSha = json.sha?.slice(0, 7) || null;
              if (remoteCommitSha) {
                fetchSucceeded = true;
                break;
              }
            }
          } catch {}
        }
      }

      // 判定更新逻辑（确立唯一权威准绳）：
      // 1. 若本地与远程均存在数字构建号，有且仅有 remoteBuild > buildNumber 时判定为有新构建；
      //    当 remoteBuild <= buildNumber 时，判定为当前已是最新，严禁因 commit SHA 错配引发死循环假更新；
      // 2. 若缺乏构建号时（开发环境源码调试），退化对比 commit SHA
      if (Number.isFinite(buildNumber) && Number.isFinite(remoteBuild)) {
        hasUpdate = remoteBuild > buildNumber;
      } else if (
        remoteCommitSha &&
        commitSha &&
        remoteCommitSha.toLowerCase() !== commitSha.toLowerCase()
      ) {
        hasUpdate = true;
      } else {
        hasUpdate = false;
      }

      // 最新版本文案格式统一规范化：有构建号时严格以构建号展示，绝不展示易引起混淆的 commit sha
      const latest = hasUpdate
        ? (Number.isFinite(remoteBuild)
            ? `FluxBox: ${remoteSemver || semver || 'v1.20'} (B${remoteBuild})`
            : `FluxBox: ${remoteSemver || semver || 'v1.20'} (${remoteCommitSha || '新提交'})`)
        : current;

      res.json({
        ok: true,
        current,
        shortVersion,
        semver,
        buildNumber,
        commitSha,
        latest,
        hasUpdate,
        checkFailed: !fetchSucceeded,
        checkError: !fetchSucceeded ? '无法连接到更新服务器，请检查旁路由网络连接' : null,
      });
    } catch (err) {
      res.status(500).json({ ok: false, error: err.message });
    }
  });

  app.post('/api/system/update', async (req, res) => {
    try {
      const rootDir = path.resolve(ROOT);
      const updateScript = path.resolve(rootDir, 'scripts', 'update.sh');
      const relativeScript = path.relative(rootDir, updateScript);
      if (relativeScript.startsWith('..') || path.isAbsolute(relativeScript)) {
        return res.status(500).json({ ok: false, error: '更新脚本路径无效' });
      }
      if (!fs.existsSync(updateScript)) {
        return res.status(503).json({ ok: false, error: '本地更新脚本不存在，已停止远程脚本执行' });
      }
      const scriptStat = fs.lstatSync(updateScript);
      if (!scriptStat.isFile()) {
        return res.status(503).json({ ok: false, error: '本地更新脚本类型无效' });
      }
      const actualHash = crypto.createHash('sha256').update(fs.readFileSync(updateScript)).digest('hex');
      const actualHashBytes = Buffer.from(actualHash, 'hex');
      const isTrusted = TRUSTED_UPDATE_SCRIPT_SHA256.some((expected) => {
        const expectedBytes = Buffer.from(expected, 'hex');
        return actualHashBytes.length === expectedBytes.length && crypto.timingSafeEqual(actualHashBytes, expectedBytes);
      });
      if (!isTrusted) {
        log.error('更新脚本 SHA-256 校验失败：%s', updateScript);
        return res.status(503).json({ ok: false, error: '更新脚本校验失败，已停止执行' });
      }

      const logFile = path.join(rootDir, 'data', 'update.log');
      try {
        const output = fs.openSync(logFile, 'w');
        const child = spawn('sh', [updateScript], { detached: true, stdio: ['ignore', output, output] });
        child.unref();
      } catch (spawnErr) {
        log.error('触发更新脚本失败：%s', spawnErr.message);
        return res.status(500).json({ ok: false, error: spawnErr.message });
      }

      log.info('系统更新脚本已触发');
      res.json({ ok: true, message: '更新已在后台开始，面板服务即将重启，请稍后刷新页面' });
    } catch (err) {
      res.status(500).json({ ok: false, error: err.message });
    }
  });

  app.get('/api/system/update/log', (req, res) => {
    try {
      const logFile = path.join(ROOT, 'data', 'update.log');
      if (!fs.existsSync(logFile)) return res.json({ ok: true, log: '' });
      res.json({ ok: true, log: fs.readFileSync(logFile, 'utf8') });
    } catch (err) {
      res.status(500).json({ ok: false, error: err.message });
    }
  });

  app.post('/api/kernel/:action', async (req, res) => {
    const { action } = req.params;
    try {
      if (action === 'start') return res.json(await kernel.start());
      if (action === 'stop') return res.json(await kernel.stop());
      if (action === 'restart') return res.json(await kernel.restart());
      if (action === 'install') {
        const version = req.body?.version;
        if (!version) return res.status(400).json({ error: '缺少 version' });
        const wasRunning = (await kernel.status()).running;
        const info = await kernel.installKernel(version, { onProgress: (message) => log.info('%s', message) });
        // 新二进制要重启内核才生效（替换是 rename 做的，老进程还跑着旧的）
        if (wasRunning) {
          try {
            await kernel.restart();
            log.info('内核已用 %s 重启', version);
          } catch (err) {
            log.warn('内核重启失败，请手动检查：%s', err.message);
          }
        }
        return res.json({ ...info, restarted: wasRunning });
      }
      return res.status(400).json({ error: '未知操作' });
    } catch (err) {
      res.status(500).json({ error: err.message });
    }
  });

  app.get('/api/kernel/log', async (req, res) => {
    res.json({ log: await kernel.tailLogAsync(Number(req.query.lines) || 200) });
  });

  app.get('/api/kernel/latest', async (req, res) => {
    try {
      // 默认取 1.15 预发布版（用户指定）
      const includePrerelease = req.query.stable !== '1';
      const curVersion = await kernel.installedVersion();
      const latestInfo = await kernel.fetchLatestVersion({ includePrerelease });

      const cur = curVersion ? (curVersion.startsWith('v') ? curVersion : `v${curVersion}`) : '未安装';
      const latest = latestInfo?.version ? (latestInfo.version.startsWith('v') ? latestInfo.version : `v${latestInfo.version}`) : '—';
      const hasUpdate = Boolean(curVersion && latestInfo?.version && cur !== latest);

      res.json({
        ok: true,
        current: cur,
        latest,
        hasUpdate,
        ...latestInfo,
      });
    } catch (err) {
      res.status(502).json({ error: err.message });
    }
  });
}
