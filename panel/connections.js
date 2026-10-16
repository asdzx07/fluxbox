/* MyBox 当前连接监控模块：实时活动连接、搜索过滤、流量监控与单条/批量断开 */
import { $, api, escapeHtml, toast } from './core.js';
import { fmtBytes } from './overview.js';

let connCache = { connections: [], uploadTotal: 0, downloadTotal: 0 };
let connPage = 1;
let ignoredConnIds = new Set();
let isConnViewCleared = false;
let connProtocolFilter = 'all';
let connSortMode = 'traffic';

export function clearConnectionRecords() {
  const conns = connCache.connections || [];
  conns.forEach((c) => { if (c.id) ignoredConnIds.add(c.id); });
  isConnViewCleared = true;
  renderConnections();
  toast('已清空当前连接显示记录，正在监听新连接…');
}

export function resetConnectionRecords() {
  ignoredConnIds.clear();
  isConnViewCleared = false;
  loadConnectionsPage();
}

export function setConnectionProtocolFilter(proto) {
  connProtocolFilter = proto || 'all';
  connPage = 1;
  renderConnections();
}

export function setConnectionSortMode(mode) {
  connSortMode = mode || 'traffic';
  connPage = 1;
  renderConnections();
}

export function renderConnections() {
  const countEl = $('connPageCount');
  const listEl = $('connsList');
  if (!listEl) return;

  const allConns = connCache.connections || [];
  const protocols = new Set(allConns.map((c) => c.metadata?.network).filter(Boolean));

  // 4 个 KPI 卡片更新
  if ($('connSummaryCount')) $('connSummaryCount').textContent = `${allConns.length} 条`;
  if ($('connSummaryDownload')) $('connSummaryDownload').textContent = fmtBytes(connCache.downloadTotal || 0);
  if ($('connSummaryUpload')) $('connSummaryUpload').textContent = fmtBytes(connCache.uploadTotal || 0);
  if ($('connSummaryTotalTraffic')) {
    $('connSummaryTotalTraffic').textContent = `↓ ${fmtBytes(connCache.downloadTotal || 0)} / ↑ ${fmtBytes(connCache.uploadTotal || 0)}`;
  }

  // 过滤已清空的连接
  let conns = isConnViewCleared ? allConns.filter((c) => !ignoredConnIds.has(c.id)) : allConns;

  // 协议筛选
  if (connProtocolFilter !== 'all') {
    conns = conns.filter((c) => (c.metadata?.network || '').toLowerCase() === connProtocolFilter.toLowerCase());
  }

  // 搜索关键字
  const query = ($('connSearch')?.value || '').trim().toLowerCase();
  let filtered = query
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

  // 排序
  if (connSortMode === 'traffic') {
    filtered = [...filtered].sort((a, b) => ((b.download || 0) + (b.upload || 0)) - ((a.download || 0) + (a.upload || 0)));
  } else if (connSortMode === 'time') {
    filtered = [...filtered].sort((a, b) => new Date(b.start || 0) - new Date(a.start || 0));
  }

  if (countEl) {
    if (query || connProtocolFilter !== 'all') {
      countEl.textContent = `匹配 ${filtered.length} / 共 ${allConns.length} 条连接`;
    } else {
      countEl.textContent = `${allConns.length} 条连接 · 累计 ↓ ${fmtBytes(connCache.downloadTotal || 0)} / ↑ ${fmtBytes(connCache.uploadTotal || 0)}${isConnViewCleared ? ' (已隐藏历史)' : ''}`;
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
        ? '<p class="note" style="padding:24px 0;text-align:center">未找到匹配的连接</p>'
        : '<p class="note" style="padding:24px 0;text-align:center">当前没有活动连接</p>';
    }
    return;
  }

  listEl.innerHTML = `
    <div class="connections-table">
      <div class="conn-table-head" aria-hidden="true">
        <span style="flex:2">目标地址</span>
        <span style="flex:1.2">来源 / 协议</span>
        <span style="flex:1.5">规则 / 出口链路</span>
        <span style="flex:1.2">↓ 下载 / ↑ 上传</span>
        <span style="width:72px;text-align:right">操作</span>
      </div>
      ${pageItems.map((c) => {
        const host = escapeHtml(c.metadata?.host || c.metadata?.destinationIP || '—');
        const port = c.metadata?.destinationPort ? `:${c.metadata.destinationPort}` : '';
        const src = escapeHtml(c.metadata?.sourceIP || '');
        const network = (c.metadata?.network || '').toUpperCase();
        const chain = (c.chains || []).map(escapeHtml).join(' → ') || '直连';
        const rule = escapeHtml(c.rule || '默认路由');
        const downBytes = fmtBytes(c.download || 0);
        const upBytes = fmtBytes(c.upload || 0);

        // 协议标签
        let protoBadge = network || 'TCP';
        if (c.metadata?.destinationPort === 443 || c.metadata?.host?.includes('tls')) protoBadge = 'TLS';
        else if (c.metadata?.destinationPort === 80) protoBadge = 'HTTP';

        return `
          <div class="conn-row-item">
            <div class="conn-col-target" style="flex:2">
              <span class="conn-host-text" title="${host}${port}">${host}${port}</span>
              <span class="conn-proto-tag">${protoBadge}</span>
            </div>
            <div class="conn-col-source" style="flex:1.2">
              <span class="conn-src-ip">${src}</span>
              <span class="conn-sub-text">${network} · 局域网设备</span>
            </div>
            <div class="conn-col-route" style="flex:1.5">
              <span class="conn-rule-name">${rule}</span>
              <span class="conn-chain-name" title="${chain}">${chain}</span>
            </div>
            <div class="conn-col-traffic" style="flex:1.2">
              <span class="tf-down">↓ ${downBytes}</span>
              <span class="tf-up">↑ ${upBytes}</span>
            </div>
            <div class="conn-col-action" style="width:72px;text-align:right">
              ${c.id ? `<button class="small ghost danger" data-action="close-connection" data-id="${encodeURIComponent(c.id)}">断开</button>` : ''}
            </div>
          </div>
        `;
      }).join('')}
    </div>
    ${pageCount > 1 ? `
      <div class="connection-pagination" role="navigation" aria-label="连接分页">
        <button class="small" data-action="connection-page" data-page="${Math.max(1, connPage - 1)}"${connPage <= 1 ? ' disabled' : ''}>上一页</button>
        <span class="note">第 ${connPage} / ${pageCount} 页 · 共 ${filtered.length} 条</span>
        <button class="small" data-action="connection-page" data-page="${Math.min(pageCount, connPage + 1)}"${connPage >= pageCount ? ' disabled' : ''}>下一页</button>
      </div>` : ''}
  `;
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

export async function closeSingleConn(id) {
  try {
    await api(`/connections/${id}`, { method: 'DELETE' });
    toast('已断开该连接');
    await loadConnectionsPage();
  } catch (err) {
    toast(`断开失败：${err.message}`);
  }
}

export async function closeAllConnections() {
  if (!confirm('确定清空并断开所有当前活动连接吗？（客户端会自动重连）')) return;
  try {
    await api('/connections', { method: 'DELETE' });
    toast('已清空并断开所有连接');
    await loadConnectionsPage();
  } catch (err) {
    toast(`清空失败：${err.message}`);
  }
}

export function setConnectionPage(page) {
  connPage = Math.max(1, Number(page) || 1);
  renderConnections();
}
