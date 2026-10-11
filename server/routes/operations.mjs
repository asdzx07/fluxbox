export function registerOperationRoutes(app, deps) {
  const { fs, path, spawn, ROOT, DATA_DIR, loadSettings, getTraffic, resetTrafficTotals, kernel, deploy, log } = deps;

  app.get('/api/traffic', (req, res) => {
    res.json({ ok: true, ...getTraffic() });
  });

  app.post('/api/traffic/reset', (req, res) => {
    resetTrafficTotals();
    res.json({ ok: true });
  });

  app.post('/api/adblock/refresh', async (req, res) => {
    try {
      // 删掉缓存的广告规则集 SRS，重启内核强制重新下载
      let deleted = 0;
      for (const dir of [DATA_DIR, '/tmp', process.cwd()]) {
        try {
          for (const file of fs.readdirSync(dir)) {
            if (file === 'adblock.srs' || file.startsWith('adblock.')) {
              fs.unlinkSync(path.join(dir, file));
              deleted++;
            }
          }
        } catch {}
      }
      await kernel.restart();
      res.json({ ok: true, deleted });
    } catch (err) {
      res.status(500).json({ ok: false, error: err.message });
    }
  });

  app.post('/api/rulesets/refresh', async (req, res) => {
    try {
      // 删掉所有缓存的远程规则集 SRS（策略用的 geosite/geoip），重启内核强制重新下载
      let deleted = 0;
      for (const dir of [DATA_DIR, '/tmp', process.cwd()]) {
        try {
          for (const file of fs.readdirSync(dir)) {
            if (file.endsWith('.srs')) {
              fs.unlinkSync(path.join(dir, file));
              deleted++;
            }
          }
        } catch {}
      }
      await kernel.restart();
      res.json({ ok: true, deleted });
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
      let semver = 'v1.20';
      let buildNumber = 92;
      let commitSha = null;

      // 1. 优先尝试从本地 git 获取提交数与短 hash
      try {
        const { execSync } = await import('node:child_process');
        const count = execSync('git rev-list --count HEAD', { cwd: ROOT, encoding: 'utf8', timeout: 1500 }).trim();
        if (/^\d+$/.test(count)) buildNumber = Number.parseInt(count, 10);
        const sha = execSync('git rev-parse --short HEAD', { cwd: ROOT, encoding: 'utf8', timeout: 1500 }).trim();
        if (sha) commitSha = sha;
      } catch {}

      // 2. 尝试从 VERSION 文件读取（支持纯源码部署或无 git 环境）
      try {
        const versionFile = path.join(ROOT, 'VERSION');
        if (fs.existsSync(versionFile)) {
          const versionText = fs.readFileSync(versionFile, 'utf8').trim();
          const bMatch = versionText.match(/B(\d+)/i) || versionText.match(/\d+\.\d+\.(\d+)/);
          if (bMatch) buildNumber = Number.parseInt(bMatch[1], 10);
          const vMatch = versionText.match(/v?(\d+\.\d+)/);
          if (vMatch) semver = `v${vMatch[1]}`;
        }
      } catch {}

      // 3. 尝试从 commit.sha 文件读取 hash
      if (!commitSha) {
        try {
          const shaFile = path.join(ROOT, 'data', 'commit.sha');
          if (fs.existsSync(shaFile)) commitSha = fs.readFileSync(shaFile, 'utf8').trim().slice(0, 7);
        } catch {}
      }
      const effectiveSha = commitSha || 'f4e9f8d';
      const shortVersion = `${semver} (B${buildNumber})`;
      const current = `MyBox: ${shortVersion}`;

      // 4. 从远程 GitHub 检测是否有最新版本与构建
      let remoteCommitSha = null;
      let remoteSemver = semver;
      let remoteBuild = buildNumber;
      let hasUpdate = false;

      // 优先从 raw 读取最新 VERSION 文件
      try {
        const rawRes = await fetch('https://raw.githubusercontent.com/asdzx07/mybox/main/VERSION', {
          headers: { 'User-Agent': 'mybox' },
          signal: AbortSignal.timeout(3000),
        });
        if (rawRes.ok) {
          const rawText = (await rawRes.text()).trim();
          const rBMatch = rawText.match(/B(\d+)/i) || rawText.match(/\d+\.\d+\.(\d+)/);
          if (rBMatch) remoteBuild = Number.parseInt(rBMatch[1], 10);
          const rVMatch = rawText.match(/v?(\d+\.\d+)/);
          if (rVMatch) remoteSemver = `v${rVMatch[1]}`;
        }
      } catch {}

      // 通过 API 检查最新 commit sha
      try {
        const response = await fetch('https://api.github.com/repos/asdzx07/MyBox/commits/main', {
          headers: { 'User-Agent': 'mybox' },
          signal: AbortSignal.timeout(3000),
        });
        if (response.ok) {
          const json = await response.json();
          remoteCommitSha = json.sha?.slice(0, 7) || null;
        }
      } catch {}

      if (remoteCommitSha && commitSha && remoteCommitSha !== commitSha) {
        hasUpdate = true;
        if (remoteBuild <= buildNumber) remoteBuild = buildNumber + 1;
      } else if (remoteBuild > buildNumber) {
        hasUpdate = true;
      }

      const latest = hasUpdate ? `MyBox: ${remoteSemver} (B${remoteBuild})` : current;

      res.json({
        ok: true,
        current,
        shortVersion,
        semver,
        buildNumber,
        commitSha: effectiveSha,
        latest,
        hasUpdate,
      });
    } catch (err) {
      res.status(500).json({ ok: false, error: err.message });
    }
  });

  app.post('/api/system/update', async (req, res) => {
    try {
      const updateScript = path.join(ROOT, 'scripts', 'update.sh');
      const logFile = path.join(ROOT, 'data', 'update.log');
      let command = 'sh';
      let args = [];
      if (fs.existsSync(updateScript)) {
        args = [updateScript, '--mirror'];
      } else {
        args = ['-c', 'curl -fsSL https://ghfast.top/https://raw.githubusercontent.com/asdzx07/mybox/main/scripts/update.sh | sh -s -- --mirror'];
      }

      try {
        const output = fs.openSync(logFile, 'w');
        const child = spawn(command, args, { detached: true, stdio: ['ignore', output, output] });
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
