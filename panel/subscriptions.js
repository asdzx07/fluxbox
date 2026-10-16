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
  box.innerHTML = `<div class="subscription-table">
    <div class="subscription-table-head" aria-hidden="true"><span></span><span>订阅来源</span><span>节点 / 状态</span><span>操作</span></div>
    ${list.map((s) => `
      <div class="item subscription-row ${s.enabled ? 'is-enabled' : 'is-disabled'}">
        <label class="switch subscription-toggle" title="${s.enabled ? '停用订阅' : '启用订阅'}">
          <input type="checkbox" data-sub-toggle="${escapeHtml(s.id)}" aria-label="${escapeHtml(s.name || s.id)}：${s.enabled ? '已启用' : '已停用'}"${s.enabled ? ' checked' : ''}>
          <span></span>
        </label>
        <div class="subscription-source">
          <strong class="title">${escapeHtml(s.name || s.id)}</strong>
          <span class="sub" title="订阅凭证已隐藏">${escapeHtml(redactSensitiveUrl(s.url || ''))}</span>
        </div>
        <div class="subscription-state">
          <strong>${Number(s.nodeCount) || s.nodes?.length || 0} 个节点</strong>
          <span class="subscription-status ${s.enabled ? 'is-enabled' : 'is-disabled'}"><i aria-hidden="true"></i>${s.enabled ? '已启用' : '已停用'}</span>
        </div>
        <div class="inline subscription-actions">
          <button class="small" data-refresh="${escapeHtml(s.id)}">刷新</button>
          <button class="small danger" data-remove="${escapeHtml(s.id)}">删除</button>
        </div>
      </div>
    `).join('')}
  </div>`;
}
