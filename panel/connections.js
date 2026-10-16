import { $, api, escapeHtml, toast } from './core.js';
import { fmtBytes } from './overview.js';

/* --------------------------------------------------------------- 当前连接 */

let connCache = { connections: [], uploadTotal: 0, downloadTotal: 0 };
let connPage = 1;
let ignoredConnIds = new Set();
let isConnViewCleared = false;

window.clearConnectionRecords = function() {
  const conns = connCache.connections || [];
  conns.forEach(c => { if (c.id) ignoredConnIds.add(c.id); });
  isConnViewCleared = true;
  renderConnections();
  toast('已清空当前连接记录，正在监听新连接…');
};

window.resetConnectionRecords = function() {
  ignoredConnIds.clear();
  isConnViewCleared = false;
  loadConnectionsPage();
};

export function renderConnections() {
  const countEl = $('connPageCount');
  const listEl = $('connsList');
  if (!listEl) return;

  const allConns = connCache.connections || [];
  const protocols = new Set(allConns.map((connection) => connection.metadata?.network).filter(Boolean));
  if ($('connectionSummaryCount')) $('connectionSummaryCount').textContent = String(allConns.length);
  if ($('connectionSummaryProtocols')) $('connectionSummaryProtocols').textContent = protocols.size ? `${protocols.size} 种` : '—';
  if ($('connectionSummaryDownload')) $('connectionSummaryDownload').textContent = fmtBytes(connCache.downloadTotal || 0);
  if ($('connectionSummaryUpload')) $('connectionSummaryUpload').textContent = fmtBytes(connCache.uploadTotal || 0);
  const conns = isConnViewCleared ? allConns.filter(c => !ignoredConnIds.has(c.id)) : allConns;
  const query = ($('connSearch')?.value || '').trim().toLowerCase();

  const filtered = query
    ? conns.filter((c) => {
        const host = (c.metadata?.host || c.metadata?.destinationIP || '').toLowerCase();
        const port = String(c.metadata?.destinationPort || '');
        const src = (c.metadata?.sourceIP || '').toLowerCase();
        const proto = (c.metadata?.network || '').toLowerCase();
        const chain = (c.chains || []).join(' ').toLowerCase();
        const rule = (c.rule || '').toLowerCase();
        return host.includes(query) || port.includes(query) || src.includes(query) ||
               proto.includes(query) || chain.includes(query) || rule.includes(query);
      })
    : conns;

  if (countEl) {
    if (query) {
      countEl.textContent = `匹配 ${filtered.length} / 当前 ${conns.length} 条连接`;
    } else {
      countEl.textContent = `${conns.length} 条连接 · 累计 ↓ ${fmtBytes(connCache.downloadTotal || 0)} / ↑ ${fmtBytes(connCache.uploadTotal || 0)}${isConnViewCleared ? ' (已过滤历史记录)' : ''}`;
    }
  }

  const pageSize = 100;
  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  connPage = Math.min(Math.max(1, connPage), pageCount);
  const pageItems = filtered.slice((connPage - 1) * pageSize, connPage * pageSize);

  if (!filtered.length) {
    if (isConnViewCleared) {
      listEl.innerHTML = `
        <div style="padding:28px 0;text-align:center">
          <p class="note" style="margin-bottom:10px">当前页面记录已清空，正在监听新接入的连接…</p>
          <button class="small" data-action="reset-connections">恢复显示全部连接</button>
        </div>
      `;
    } else {
      listEl.innerHTML = query
        ? '<p class="note" style="padding:16px 0;text-align:center">未找到匹配的连接</p>'
        : '<p class="note" style="padding:16px 0;text-align:center">当前没有活动连接</p>';
    }
    return;
  }

  listEl.innerHTML = `<div class="list">${pageItems.map((c) => {
    const host = escapeHtml(c.metadata?.host || c.metadata?.destinationIP || '—');
    const port = c.metadata?.destinationPort ? `:${c.metadata.destinationPort}` : '';
    const src = escapeHtml(c.metadata?.sourceIP || '');
    const proto = escapeHtml((c.metadata?.network || '').toUpperCase());
    const chain = (c.chains || []).map(escapeHtml).join(' → ');
    const rule = escapeHtml(c.rule || '');
    const id = encodeURIComponent(c.id || '');
    return `
      <div class="conn-item">
        <div class="conn-main">
          <div class="conn-header">
            <span class="conn-target" title="${host}${port}">${host}${port}</span>
            <span class="tag muted conn-proto">${proto}</span>
            ${rule ? `<span class="tag conn-rule" title="${rule}">${rule}</span>` : ''}
          </div>
          <div class="conn-meta-sub">
            <span>源: ${src}</span>
            <span class="conn-sep">·</span>
            <span>链路: ${chain || '直连'}</span>
          </div>
        </div>
        <div class="conn-stats-col">
          <div class="conn-traffic">
            <span class="tf-down">↓ ${fmtBytes(c.download || 0)}</span>
            <span class="tf-sep">/</span>
            <span class="tf-up">↑ ${fmtBytes(c.upload || 0)}</span>
          </div>
          ${c.id ? `<button class="small conn-btn-close" data-action="close-connection" data-id="${encodeURIComponent(c.id)}">断开</button>` : ''}
        </div>
      </div>
    `;
  }).join('')}</div>${pageCount > 1 ? `<div class="connection-pagination" role="navigation" aria-label="连接分页"><button class="small" data-action="connection-page" data-page="${Math.max(1, connPage - 1)}"${connPage <= 1 ? ' disabled' : ''}>上一页</button><span class="note">第 ${connPage} / ${pageCount} 页，共 ${filtered.length} 条</span><button class="small" data-action="connection-page" data-page="${Math.min(pageCount, connPage + 1)}"${connPage >= pageCount ? ' disabled' : ''}>下一页</button></div>` : ''}`;
}

let connAutoPollTimer = null;
let connLoadPromise = null;
let connPollGeneration = 0;

function isConnectionsPageVisible() {
  return !document.hidden && $('page-connections')?.classList.contains('active');
}

export async function loadConnectionsPage() {
  if (connLoadPromise) return connLoadPromise;
  if (!isConnectionsPageVisible()) return;

  const generation = connPollGeneration;
  const listEl = $('connsList');
  const request = (async () => {
    try {
      const data = await api('/connections');
      if (generation !== connPollGeneration || !isConnectionsPageVisible()) return;
      connCache = {
        connections: data.connections || [],
        uploadTotal: data.uploadTotal || 0,
        downloadTotal: data.downloadTotal || 0,
      };
      renderConnections();
    } catch (err) {
      if (generation !== connPollGeneration || !isConnectionsPageVisible()) return;
      if (listEl) listEl.innerHTML = `<p class="err-text" style="padding:16px 0">${escapeHtml(err.message)}</p>`;
    }
  })();
  connLoadPromise = request;
  try {
    await request;
  } finally {
    if (connLoadPromise === request) connLoadPromise = null;
  }
}

export function startConnAutoPoll() {
  stopConnAutoPoll();
  loadConnectionsPage();
  connAutoPollTimer = setInterval(loadConnectionsPage, 1500);
}

export function stopConnAutoPoll() {
  connPollGeneration += 1;
  if (connAutoPollTimer) {
    clearInterval(connAutoPollTimer);
    connAutoPollTimer = null;
  }
}

window.closeSingleConn = async function(id) {
  try {
    await api(`/connections/${id}`, { method: 'DELETE' });
    toast('已断开该连接');
    await loadConnectionsPage();
  } catch (err) {
    toast(`断开失败：${err.message}`);
  }
};

window.closeAllConnections = async function() {
  if (!confirm('确定清空并断开所有当前活动连接吗？（客户端会自动重连）')) return;
  try {
    await api('/connections', { method: 'DELETE' });
    toast('已清空所有连接');
    await loadConnectionsPage();
  } catch (err) {
    toast(`清空失败：${err.message}`);
  }
};


export function setConnectionPage(page) {
  connPage = Math.max(1, Number(page) || 1);
  renderConnections();
}
