/* MyBox 订阅管理模块：加载、脱敏、状态与列表渲染 */
import { $, api, escapeHtml, redactSensitiveUrl } from './core.js';

function formatSubscriptionTime(ts) {
  if (!ts || isNaN(ts) || ts <= 0) return '尚未刷新';
  const d = new Date(ts);
  const now = new Date();
  const isToday = d.toDateString() === now.toDateString();
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  const isYesterday = d.toDateString() === yesterday.toDateString();

  const timeStr = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  if (isToday) return `今天 ${timeStr}`;
  if (isYesterday) return `昨天 ${timeStr}`;
  return `${d.getMonth() + 1}月${d.getDate()}日 ${timeStr}`;
}

export async function loadSubscriptions() {
  const box = $('subList');
  try {
    const data = await api('/subscriptions');
    renderSubList(data.subscriptions || []);
  } catch (err) {
    if (box) box.innerHTML = `<p class="err-text" style="padding:16px 0">${escapeHtml(err.message)}</p>`;
  }
}

export function renderSubList(subs) {
  const box = $('subList');
  const list = subs || [];
  const enabledCount = list.filter((s) => s.enabled).length;
  const totalNodes = list.reduce((total, s) => total + (Number(s.nodeCount) || s.nodes?.length || 0), 0);

  // 找出最近一次刷新的时间
  let latestRefreshTs = 0;
  let latestHasError = false;
  let latestErrorMsg = '';
  for (const s of list) {
    const t = s.updatedAt || s.addedAt || 0;
    if (t > latestRefreshTs) {
      latestRefreshTs = t;
      latestHasError = Boolean(s.lastError);
      latestErrorMsg = s.lastError || '';
    }
  }

  // 3 个 KPI 卡片更新
  if ($('subSummaryEnabled')) $('subSummaryEnabled').textContent = `${enabledCount} / ${list.length} 条`;
  if ($('subSummaryNodes')) $('subSummaryNodes').textContent = `${totalNodes} 个`;
  if ($('subSummaryLatest')) {
    if (latestRefreshTs > 0) {
      const timeStr = formatSubscriptionTime(latestRefreshTs);
      $('subSummaryLatest').innerHTML = latestHasError
        ? `${timeStr} <span class="tag-err">· 有刷新错误</span>`
        : `${timeStr} <span class="tag-ok">· 更新成功</span>`;
    } else {
      $('subSummaryLatest').textContent = '暂无刷新记录';
    }
  }

  if (!box) return;
  if (!list.length) {
    box.innerHTML = '<p class="note" style="padding:24px 0;text-align:center">还没有订阅，请在上方填写名称与订阅链接添加。</p>';
    return;
  }

  box.innerHTML = `
    <div class="subscription-table">
      <div class="subscription-table-head" aria-hidden="true">
        <span style="width:36px"></span>
        <span style="flex:2">订阅源</span>
        <span style="flex:1">节点 / 更新状态</span>
        <span style="flex:1">最后刷新</span>
        <span style="width:110px;text-align:right">操作</span>
      </div>
      ${list.map((s) => {
        const nodeNum = Number(s.nodeCount) || s.nodes?.length || 0;
        const safeUrl = redactSensitiveUrl(s.url || '');
        const timeText = formatSubscriptionTime(s.updatedAt || s.addedAt);

        // 状态判定：未启用、有错误、正常
        let statusTag = '';
        if (!s.enabled) {
          statusTag = '<span class="sub-status-pill muted">已停用</span>';
        } else if (s.lastError) {
          statusTag = `<span class="sub-status-pill warn" title="${escapeHtml(s.lastError)}">有警告</span>`;
        } else {
          statusTag = '<span class="sub-status-pill good">正常</span>';
        }

        return `
          <div class="subscription-row ${s.enabled ? 'is-enabled' : 'is-disabled'}">
            <div style="width:36px">
              <label class="switch" title="${s.enabled ? '停用订阅' : '启用订阅'}">
                <input type="checkbox" data-sub-toggle="${escapeHtml(s.id)}" ${s.enabled ? 'checked' : ''}>
                <span></span>
              </label>
            </div>
            <div class="subscription-source" style="flex:2">
              <strong class="title">${escapeHtml(s.name || s.id)}</strong>
              <span class="sub-url" title="已脱敏凭证">${escapeHtml(safeUrl)}</span>
            </div>
            <div class="subscription-node-status" style="flex:1">
              <strong class="sub-node-count">${s.enabled ? `${nodeNum} 个节点` : '—'}</strong>
              ${statusTag}
            </div>
            <div class="subscription-time" style="flex:1">
              <span class="time-text">${timeText}</span>
            </div>
            <div class="subscription-actions" style="width:110px;text-align:right">
              <button class="small ghost" data-refresh="${escapeHtml(s.id)}">刷新</button>
              <button class="small danger ghost" data-remove="${escapeHtml(s.id)}">删除</button>
            </div>
          </div>
        `;
      }).join('')}
    </div>
  `;
}
