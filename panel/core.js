/* Shared API and DOM utilities for the MyBox panel. */

export const $ = (id) => document.getElementById(id);

export const state = {
  settings: null,
  groups: [],
  policies: [],
  nodeGroups: [],
  nodeList: [],
  latency: {},
  collapsed: {},
  clients: [],
};

export async function api(path, options = {}) {
  const timeoutMs = path === '/deploy' ? 300000 : 30000;
  const res = await fetch(`/api${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
    signal: AbortSignal.timeout(timeoutMs),
    body: options.body ? JSON.stringify(options.body) : undefined,
  });
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = { raw: text }; }
  if (!res.ok) {
    const err = new Error(data?.error || `HTTP ${res.status}`);
    err.status = res.status;
    err.data = data;
    throw err;
  }
  return data;
}

let toastTimer;
export function toast(message) {
  const el = $('toast');
  if (!el) return;
  el.textContent = message;
  el.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove('show'), 2600);
}

export function busy(button, on, label) {
  if (!button) return;
  if (on) {
    button.dataset.label = button.textContent;
    button.textContent = label || '处理中…';
    button.disabled = true;
  } else {
    button.textContent = button.dataset.label || button.textContent;
    button.disabled = false;
  }
}

export const escapeHtml = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

export function redactSensitiveUrl(value) {
  const raw = String(value ?? '').trim();
  if (!raw) return '';
  try {
    const url = new URL(raw);
    url.username = '';
    url.password = '';
    const hasPath = url.pathname && url.pathname !== '/';
    const query = url.search ? '?…' : '';
    return `${url.origin}${hasPath ? '/…' : ''}${query}`;
  } catch {
    return '订阅地址（已隐藏）';
  }
}
