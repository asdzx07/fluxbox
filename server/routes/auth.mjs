const LOGIN_WINDOW_MS = 15 * 60 * 1000;
const LOGIN_MAX_FAILURES = 5;
const LOGIN_MAX_CLIENTS = 4096;

export function registerPublicAuthRoutes(app, deps) {
  const {
    isPasswordSet, isAuthed, setPassword, verifyPassword,
    issueToken, clearSessionCookie, setSessionCookie, log,
  } = deps;
  const loginFailures = new Map();

  app.get('/api/health', (_req, res) => {
    res.json({ ok: true });
  });

  app.get('/api/auth/status', (req, res) => {
    res.json({ enabled: true, authenticated: isAuthed(req), passwordSet: isPasswordSet() });
  });

  app.post('/api/auth/setup', (req, res) => {
    if (isPasswordSet()) return res.status(409).json({ error: '面板密码已设置' });
    try {
      setPassword(req.body?.password);
    } catch (err) {
      return res.status(400).json({ error: err.message });
    }
    setSessionCookie(res, issueToken(), req);
    res.json({ ok: true });
  });

  app.post('/api/auth/login', (req, res) => {
    if (!isPasswordSet()) return res.status(409).json({ error: '还没有设置面板密码' });

    const now = Date.now();
    const clientId = req.ip || req.socket?.remoteAddress || 'unknown';
    let attempt = loginFailures.get(clientId);
    if (attempt && now - attempt.startedAt >= LOGIN_WINDOW_MS) {
      loginFailures.delete(clientId);
      attempt = null;
    }
    if (attempt?.failures >= LOGIN_MAX_FAILURES) {
      const retryAfter = Math.max(1, Math.ceil((attempt.startedAt + LOGIN_WINDOW_MS - now) / 1000));
      res.setHeader('Retry-After', String(retryAfter));
      return res.status(429).json({ error: '登录尝试过于频繁，请稍后再试' });
    }

    if (!verifyPassword(req.body?.password)) {
      if (!attempt) attempt = { startedAt: now, failures: 0 };
      attempt.failures += 1;
      if (!loginFailures.has(clientId) && loginFailures.size >= LOGIN_MAX_CLIENTS) {
        loginFailures.delete(loginFailures.keys().next().value);
      }
      loginFailures.set(clientId, attempt);
      log.warn('登录失败，来自 %s', clientId);
      return res.status(401).json({ error: '密码错误' });
    }

    loginFailures.delete(clientId);
    setSessionCookie(res, issueToken(), req);
    res.json({ ok: true });
  });

  app.post('/api/auth/logout', (req, res) => {
    clearSessionCookie(res, req);
    res.json({ ok: true });
  });
}

export function registerProtectedAuthRoutes(app, deps) {
  const {
    verifyPassword, setPassword, rotateSessionSecret, issueToken, setSessionCookie,
  } = deps;

  app.get('/api/auth/me', (req, res) => res.json({ authenticated: true }));

  app.post('/api/auth/change-password', (req, res) => {
    if (!verifyPassword(req.body?.current)) return res.status(401).json({ error: '当前密码错误' });
    try {
      setPassword(req.body?.next);
      if (typeof rotateSessionSecret === 'function') rotateSessionSecret();
      if (typeof setSessionCookie === 'function' && typeof issueToken === 'function') {
        setSessionCookie(res, issueToken(), req);
      }
    } catch (err) {
      return res.status(400).json({ error: err.message });
    }
    res.json({ ok: true });
  });
}
