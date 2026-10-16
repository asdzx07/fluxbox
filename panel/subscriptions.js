/* Subscription page loading and rendering. */
import { $, api, escapeHtml, redactSensitiveUrl } from './core.js';

export async function loadSubscriptions() {
  try {
    const data = await api('/subscriptions');
    renderSubList(data.subscriptions || []);
  } catch (err) {
    $('subList').innerHTML = `<p class="err-text">${escapeHtml(err.message)}</p>`;
  }
}

export function renderSubList(subs) {
  const box = $('subList');
  const list = subs || [];
  const enabled = list.filter((subscription) => subscription.enabled).length;
  const nodeCount = list.reduce((total, subscription) => total + (Number(subscription.nodeCount) || subscription.nodes?.length || 0), 0);
  if ($('subSummaryTotal')) $('subSummaryTotal').textContent = String(list.length);
  if ($('subSummaryEnabled')) $('subSummaryEnabled').textContent = String(enabled);
  if ($('subSummaryNodes')) $('subSummaryNodes').textContent = String(nodeCount);
  if (!box) return;
  if (!list.length) {
    box.innerHTML = '<p class="note">还没有订阅，请在上方添加。</p>';
    return;
  }
  box.innerHTML = list.map((s) => `
    <div class="item">
      <label class="switch">
        <input type="checkbox" data-sub-toggle="${escapeHtml(s.id)}"${s.enabled ? ' checked' : ''}>
        <span></span>
      </label>
      <div class="grow">
        <div class="title">${escapeHtml(s.name || s.id)}</div>
        <div class="sub" title="订阅凭证已隐藏">${escapeHtml(redactSensitiveUrl(s.url || ''))} · 节点: ${Number(s.nodeCount) || s.nodes?.length || 0} 个</div>
      </div>
      <div class="inline">
        <button class="small" data-refresh="${escapeHtml(s.id)}">刷新</button>
        <button class="small danger" data-remove="${escapeHtml(s.id)}">删除</button>
      </div>
    </div>
  `).join('');
}
