/* LAN client page state, rendering and actions. */
import { $, api, escapeHtml, toast, state } from './core.js';

let clientPage = 1;
const CLIENT_PAGE_SIZE = 100;

export async function loadClients() {
  const box = $('clientList');
  try {
    const res = await api('/clients');
    state.clients = res.clients || [];
    renderClients();
  } catch (err) {
    if (box) box.innerHTML = `<p class="err-text" style="padding:12px 0">加载内网设备失败：${escapeHtml(err.message)}</p>`;
  }
}

export function renderClients() {
  const box = $('clientList');
  const clients = state.clients || [];
  const online = clients.filter((client) => client.online).length;
  const proxy = clients.filter((client) => client.mode === 'proxy').length;
  const direct = clients.filter((client) => client.mode === 'direct').length;
  if ($('clientSummaryTotal')) $('clientSummaryTotal').textContent = String(clients.length);
  if ($('clientSummaryOnline')) $('clientSummaryOnline').textContent = String(online);
  if ($('clientSummaryProxy')) $('clientSummaryProxy').textContent = String(proxy);
  if ($('clientSummaryDirect')) $('clientSummaryDirect').textContent = String(direct);
  if (!box) return;
  if (!clients.length) {
    box.innerHTML = '<p class="note" style="padding:16px 0;text-align:center">暂无内网设备。请点击上方「刷新设备列表」自动扫描在线设备，或点击「＋ 添加设备」手动添加。</p>';
    return;
  }
  const query = ($('clientSearch')?.value || '').trim().toLocaleLowerCase();
  const filtered = clients.filter((client) => !query || [client.name, client.ip, client.mac].some((value) => String(value || '').toLocaleLowerCase().includes(query)));
  if (!filtered.length) {
    box.innerHTML = '<p class="note" style="padding:16px 0;text-align:center">没有符合搜索条件的设备。</p>';
    return;
  }
  const pageCount = Math.max(1, Math.ceil(filtered.length / CLIENT_PAGE_SIZE));
  clientPage = Math.min(Math.max(1, clientPage), pageCount);
  const start = (clientPage - 1) * CLIENT_PAGE_SIZE;
  box.innerHTML = `${filtered.slice(start, start + CLIENT_PAGE_SIZE).map((c) => {
    const i = clients.indexOf(c);
    return `
    <div class="client-item">
      <div style="flex:1;min-width:0">
        <div style="font-weight:600;font-size:14px;display:flex;align-items:center;gap:8px;flex-wrap:wrap">
          <span>${escapeHtml(c.name || '未命名设备')}</span>
          <span class="tag muted" style="font-family:monospace;font-size:12px">${escapeHtml(c.ip)}</span>
          ${c.mac ? `<span class="tag muted" style="font-size:11px;opacity:0.8">${escapeHtml(c.mac)}</span>` : ''}
          ${c.online ? `<span class="tag" style="color:var(--ok);font-size:11px;font-weight:600">● 在线</span>` : ''}
        </div>
        <div class="note" style="margin-top:4px">${escapeHtml(c.note || (c.online ? '局域网在线设备' : '已配置设备'))}</div>
      </div>
      <div class="inline" style="gap:8px;align-items:center;flex-shrink:0">
        <select data-action="client-mode" data-id="${escapeHtml(c.id || c.ip)}" style="width:auto;font-size:12px;padding:4px 8px">
          <option value="rule"${c.mode === 'rule' ? ' selected' : ''}>规则分流 (推荐)</option>
          <option value="proxy"${c.mode === 'proxy' ? ' selected' : ''}>全局代理</option>
          <option value="direct"${c.mode === 'direct' ? ' selected' : ''}>完全直连 (绕过代理)</option>
        </select>
        <button class="small" data-action="edit-client" data-index="${i}" style="padding:4px 9px">编辑</button>
        <button class="small danger" data-action="remove-client" data-id="${escapeHtml(c.id || c.ip)}" style="padding:4px 9px">删除</button>
      </div>
    </div>
  `;
  }).join('')}${pageCount > 1 ? `
    <div class="client-pagination" role="navigation" aria-label="设备分页">
      <button class="small" data-action="client-page" data-page="${Math.max(1, clientPage - 1)}"${clientPage <= 1 ? ' disabled' : ''}>上一页</button>
      <span class="note">第 ${clientPage} / ${pageCount} 页 · 显示 ${start + 1}–${Math.min(start + CLIENT_PAGE_SIZE, filtered.length)} / ${filtered.length} 台</span>
      <button class="small" data-action="client-page" data-page="${Math.min(pageCount, clientPage + 1)}"${clientPage >= pageCount ? ' disabled' : ''}>下一页</button>
    </div>` : ''}`;
}

export function setClientPage(page) {
  clientPage = Math.max(1, Number(page) || 1);
  renderClients();
}

export function openClientModal(index = -1) {
  const modal = $('clientModal');
  if (!modal) return;
  $('modalClientIndex').value = index;
  if (index >= 0 && state.clients[index]) {
    const c = state.clients[index];
    $('modalTitle').textContent = '编辑内网设备';
    $('modalClientName').value = c.name || '';
    $('modalClientIp').value = c.ip || '';
    $('modalClientMode').value = c.mode || 'rule';
  } else {
    $('modalTitle').textContent = '添加内网设备';
    $('modalClientName').value = '';
    $('modalClientIp').value = '';
    $('modalClientMode').value = 'rule';
  }
  modal.classList.remove('hidden');
}

export function closeClientModal() {
  const modal = $('clientModal');
  if (modal) modal.classList.add('hidden');
}

export async function saveClientModal() {
  const idx = Number($('modalClientIndex').value);
  const name = $('modalClientName').value.trim();
  const ip = $('modalClientIp').value.trim();
  const mode = $('modalClientMode').value;
  if (!ip) {
    toast('请输入设备 IP 地址');
    return;
  }
  if (!/^192\.168\.\d+\.\d+|^10\.\d+\.\d+\.\d+|^172\.(1[6-9]|2\d|3[01])\.\d+\.\d+/.test(ip)) {
    toast('请输入正确的局域网内网 IP 地址');
    return;
  }
  if (idx >= 0 && state.clients[idx]) {
    state.clients[idx].name = name || '未命名设备';
    state.clients[idx].ip = ip;
    state.clients[idx].mode = mode;
  } else {
    state.clients.push({
      id: `client-${Date.now()}`,
      name: name || `设备 ${ip}`,
      ip,
      mode,
      note: '手动添加',
      online: true,
    });
  }
  try {
    await api('/clients', { method: 'POST', body: { clients: state.clients } });
    renderClients();
    closeClientModal();
    toast(`已保存设备「${name || ip}」`);
  } catch (err) {
    toast(`保存失败：${err.message}`);
  }
}

export async function updateClientMode(id, mode) {
  const client = state.clients.find((c) => c.id === id || c.ip === id);
  if (!client) return;
  client.mode = mode;
  try {
    await api('/clients', { method: 'POST', body: { clients: state.clients } });
    toast(`已更新设备「${client.name || client.ip}」策略为：${mode === 'direct' ? '完全直连' : (mode === 'proxy' ? '全局代理' : '规则分流')}`);
  } catch (err) {
    toast(`更新策略失败：${err.message}`);
  }
}

export async function removeClient(id) {
  const client = state.clients.find((c) => c.id === id || c.ip === id);
  const name = client?.name || client?.ip || '该设备';
  if (!confirm(`确认移除「${name}」的独立分流规则？`)) return;
  try {
    await api(`/clients/${encodeURIComponent(id)}`, { method: 'DELETE' });
    state.clients = state.clients.filter((c) => c.id !== id && c.ip !== id);
    renderClients();
    toast('已移除设备规则');
  } catch (err) {
    toast(`移除失败：${err.message}`);
  }
}

window.openClientModal = openClientModal;
window.closeClientModal = closeClientModal;
window.updateClientMode = updateClientMode;
window.removeClient = removeClient;
