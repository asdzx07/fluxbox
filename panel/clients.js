/* MyBox 内网设备管理模块：设备扫描、精细策略分配与列表呈现 */
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
    if (box) box.innerHTML = `<p class="err-text" style="padding:16px 0">加载内网设备失败：${escapeHtml(err.message)}</p>`;
  }
}

export function renderClients() {
  const box = $('clientList');
  const clients = state.clients || [];
  const online = clients.filter((c) => c.online).length;
  const proxy = clients.filter((c) => c.mode === 'proxy').length;
  const direct = clients.filter((c) => c.mode === 'direct').length;
  const rule = clients.length - proxy - direct;

  // 3 个 KPI 卡片更新
  if ($('clientSummaryTotal')) $('clientSummaryTotal').textContent = `${clients.length} 台`;
  if ($('clientSummaryOnline')) $('clientSummaryOnline').textContent = `${online} 台`;
  if ($('clientSummaryModes')) {
    $('clientSummaryModes').innerHTML = `规则 ${rule} <span class="sep">·</span> 代理 ${proxy} <span class="sep">·</span> 直连 ${direct}`;
  }

  if (!box) return;
  if (!clients.length) {
    box.innerHTML = '<p class="note" style="padding:28px 0;text-align:center">暂无内网设备。点击上方「刷新设备列表」扫描局域网，或手动添加设备规则。</p>';
    return;
  }

  const query = ($('clientSearch')?.value || '').trim().toLocaleLowerCase();
  const filtered = clients.filter((c) => !query || [c.name, c.ip, c.mac].some((val) => String(val || '').toLocaleLowerCase().includes(query)));

  if (!filtered.length) {
    box.innerHTML = '<p class="note" style="padding:20px 0;text-align:center">没有符合搜索条件的局域网设备。</p>';
    return;
  }

  const pageCount = Math.max(1, Math.ceil(filtered.length / CLIENT_PAGE_SIZE));
  clientPage = Math.min(Math.max(1, clientPage), pageCount);
  const start = (clientPage - 1) * CLIENT_PAGE_SIZE;
  const visible = filtered.slice(start, start + CLIENT_PAGE_SIZE);

  box.innerHTML = `
    <div class="client-table">
      <div class="client-table-head" aria-hidden="true">
        <span style="flex:2">设备</span>
        <span style="flex:1.5">网络地址</span>
        <span style="flex:1.5">当前分流策略</span>
        <span style="flex:1">状态</span>
        <span style="width:110px;text-align:right">操作</span>
      </div>
      ${visible.map((c) => {
        const i = clients.indexOf(c);
        const name = c.name || '局域网设备';
        const typeNote = c.note || (c.online ? '在线活跃设备' : '已配置设备');

        return `
          <div class="client-row-item">
            <div class="client-col-device" style="flex:2">
              <span class="client-device-avatar ${c.online ? 'is-online' : 'is-offline'}"></span>
              <div class="client-device-names">
                <strong class="title">${escapeHtml(name)}</strong>
                <span class="sub">${escapeHtml(typeNote)}</span>
              </div>
            </div>
            <div class="client-col-address" style="flex:1.5">
              <span class="client-ip-text">${escapeHtml(c.ip || '—')}</span>
              <span class="client-mac-text">${escapeHtml(c.mac || 'MAC 未记录')}</span>
            </div>
            <div class="client-col-mode" style="flex:1.5">
              <select class="client-mode-select" data-action="client-mode" data-id="${escapeHtml(c.id || c.ip)}">
                <option value="rule"${c.mode === 'rule' || !c.mode ? ' selected' : ''}>规则分流</option>
                <option value="proxy"${c.mode === 'proxy' ? ' selected' : ''}>全局代理</option>
                <option value="direct"${c.mode === 'direct' ? ' selected' : ''}>完全直连</option>
              </select>
            </div>
            <div class="client-col-status" style="flex:1">
              <span class="client-status-indicator ${c.online ? 'good' : 'muted'}">
                <span class="status-dot"></span>${c.online ? '在线' : '离线'}
              </span>
            </div>
            <div class="client-col-actions" style="width:110px;text-align:right">
              <button class="small ghost" data-action="edit-client" data-index="${i}">编辑</button>
              <button class="small danger ghost" data-action="remove-client" data-id="${escapeHtml(c.id || c.ip)}">删除</button>
            </div>
          </div>
        `;
      }).join('')}
    </div>
    ${pageCount > 1 ? `
      <div class="client-pagination" role="navigation" aria-label="设备分页">
        <button class="small" data-action="client-page" data-page="${Math.max(1, clientPage - 1)}"${clientPage <= 1 ? ' disabled' : ''}>上一页</button>
        <span class="note">第 ${clientPage} / ${pageCount} 页 · 共 ${filtered.length} 台</span>
        <button class="small" data-action="client-page" data-page="${Math.min(pageCount, clientPage + 1)}"${clientPage >= pageCount ? ' disabled' : ''}>下一页</button>
      </div>` : ''}
  `;
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
    toast('请输入合法的内网 IPv4 地址（如 192.168.1.100）');
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
    toast(`已保存设备「${name || ip}」分流规则`);
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
    const modeName = mode === 'direct' ? '完全直连' : (mode === 'proxy' ? '全局代理' : '规则分流');
    toast(`已更新设备「${client.name || client.ip}」为：${modeName}`);
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
