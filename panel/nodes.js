import { $, api, escapeHtml, toast, state } from './core.js';

let extractFlagFn = () => '🌐';
let extractProtoFn = () => 'NODE';

export function configureNodes({ extractFlag, extractProto }) {
  extractFlagFn = extractFlag || extractFlagFn;
  extractProtoFn = extractProto || extractProtoFn;
}

export const GROUP_LABEL = {
  Selector: '手动选择',
  URLTest: '自动择优',
  Fallback: '故障转移',
  LoadBalance: '负载均衡',
  selector: '手动选择',
  urltest: '自动择优',
  fallback: '故障转移',
  loadbalance: '负载均衡',
};

export async function loadNodes() {
  try {
    const data = await api('/nodes/status');
    state.nodeGroups = data.groups || [];
    state.nodeList = data.nodes || [];
    renderNodeGroups();
    renderNodeList();
  } catch (err) {
    $('nodeGroups').innerHTML = `<p class="err-text">${escapeHtml(err.message)}</p>`;
    $('nodeList').innerHTML = '';
  }
}

function delayBadge(name) {
  const entry = state.latency[name];
  if (!entry) return '';
  if (entry.pending) return '<span class="lat na">测速中…</span>';
  if (entry.delay === null || entry.delay === undefined) return '<span class="lat bad">超时</span>';
  const cls = entry.delay < 150 ? 'good' : entry.delay < 350 ? 'mid' : 'bad';
  return `<span class="lat ${cls}">● ${entry.delay} ms</span>`;
}

export function renderNodeGroups() {
  const box = $('nodeGroups');
  if (!state.nodeGroups.length) {
    box.innerHTML = '<p class="note">内核没有返回任何分组。可能内核没在运行，或还没有部署配置。</p>';
    return;
  }
  const collapsed = (name) => state.collapsed[name] !== false;

  box.innerHTML = state.nodeGroups.map((g) => {
    const isCollapsed = collapsed(g.name);
    return `
    <div class="card" style="background:var(--surface-2);margin-bottom:14px">
      <div class="card-head group-head" data-collapse="${escapeHtml(g.name)}" style="cursor:pointer;margin-bottom:${isCollapsed ? '0' : '14px'}">
        <span class="chevron">${isCollapsed ? '▸' : '▾'}</span>
        <h3>${escapeHtml(g.name)}</h3>
        <span class="tag">${GROUP_LABEL[g.type] || g.type}</span>
        <span class="tag muted">${g.members.length} 个节点</span>
        <div class="spacer"></div>
        <span class="note">当前出口：<strong style="color:var(--accent)">${escapeHtml(g.now || '—')}</strong></span>
      </div>
      <div class="node-grid${isCollapsed ? ' hidden' : ''}">
        ${g.members.map((m) => {
          const isActive = (m === g.now);
          const flag = extractFlagFn(m);
          const proto = extractProtoFn(m);
          return `
          <div class="node-card ${isActive ? 'active' : ''}" data-select-group="${escapeHtml(g.name)}" data-select-name="${escapeHtml(m)}">
            <div class="node-card-head">
              <span class="node-flag">${flag}</span>
              <div class="node-title" title="${escapeHtml(m)}">${escapeHtml(m)}</div>
              <span class="node-proto">${proto}</span>
            </div>
            <div class="node-card-foot">
              <div>${delayBadge(m) || '<span class="lat na">未测试</span>'}</div>
              <button class="small" data-latency="${escapeHtml(m)}" data-action="test-latency" data-name="${escapeHtml(m)}" style="padding:2px 8px;font-size:11px">测速</button>
            </div>
          </div>`;
        }).join('')}
      </div>
    </div>`;
  }).join('');
}

export function renderNodeList() {
  const allNodes = state.nodeList || [];
  const measured = allNodes
    .map((node) => ({ name: node.name, delay: state.latency[node.name]?.delay }))
    .filter((node) => Number.isFinite(node.delay));
  const fastest = measured.reduce((best, item) => !best || item.delay < best.delay ? item : best, null);
  if ($('nodeSummaryTotal')) $('nodeSummaryTotal').textContent = String(allNodes.length);
  if ($('nodeSummaryHealthy')) $('nodeSummaryHealthy').textContent = `${measured.length} / ${allNodes.length}`;
  if ($('nodeSummaryFastest')) $('nodeSummaryFastest').textContent = fastest ? `${fastest.delay} ms · ${fastest.name}` : '未测速';
  if ($('nodeSummaryGroups')) $('nodeSummaryGroups').textContent = String((state.nodeGroups || []).length);

  const query = ($('nodeSearch')?.value || '').trim().toLocaleLowerCase();
  const sort = $('nodeSort')?.value || 'default';
  let nodes = allNodes.filter((node) => !query || [node.name, node.type, node.server].some((value) => String(value || '').toLocaleLowerCase().includes(query)));
  if (sort === 'name') nodes = [...nodes].sort((a, b) => String(a.name).localeCompare(String(b.name), 'zh-CN'));
  if (sort === 'latency') nodes = [...nodes].sort((a, b) => {
    const left = state.latency[a.name]?.delay;
    const right = state.latency[b.name]?.delay;
    return (Number.isFinite(left) ? left : Number.POSITIVE_INFINITY) - (Number.isFinite(right) ? right : Number.POSITIVE_INFINITY);
  });

  const count = $('nodeCount');
  if (count) count.textContent = query ? `${nodes.length} / ${allNodes.length} 个` : `${allNodes.length} 个`;
  const box = $('nodeList');
  if (!allNodes.length) {
    box.innerHTML = '<p class="note" style="grid-column:1/-1;text-align:center;padding:16px 0">还没有节点。先在「订阅」页添加订阅并刷新，然后部署配置。</p>';
    return;
  }
  if (!nodes.length) {
    box.innerHTML = '<p class="note" style="grid-column:1/-1;text-align:center;padding:16px 0">没有符合搜索条件的节点。</p>';
    return;
  }
  const pageSize = 100;
  const page = Math.max(1, Number(state.nodePage) || 1);
  const pageCount = Math.max(1, Math.ceil(nodes.length / pageSize));
  state.nodePage = Math.min(page, pageCount);
  const start = (state.nodePage - 1) * pageSize;
  const visibleNodes = nodes.slice(start, start + pageSize);
  box.innerHTML = `${visibleNodes.map((n) => `
    <div class="item node-item">
      <span class="node-flag">${extractFlagFn(n.name)}</span>
      <div class="grow">
        <div class="title" title="${escapeHtml(n.name)}">${escapeHtml(n.name)}</div>
        <div class="sub">${escapeHtml(n.type)} · ${escapeHtml(n.server || '')}</div>
      </div>
      <div class="node-item-actions">
        ${delayBadge(n.name)}
        <button class="small" data-latency="${escapeHtml(n.name)}" data-action="test-latency" data-name="${escapeHtml(n.name)}">测速</button>
      </div>
    </div>
  `).join('')}${pageCount > 1 ? `
    <div class="node-pagination" role="navigation" aria-label="节点分页">
      <button class="small" data-node-page="${Math.max(1, state.nodePage - 1)}"${state.nodePage <= 1 ? ' disabled' : ''}>上一页</button>
      <span class="note">第 ${state.nodePage} / ${pageCount} 页 · 显示 ${start + 1}–${Math.min(start + pageSize, nodes.length)} 个</span>
      <button class="small" data-node-page="${Math.min(pageCount, state.nodePage + 1)}"${state.nodePage >= pageCount ? ' disabled' : ''}>下一页</button>
    </div>` : ''}`;
}

export function initNodeColsSwitcher() {
  const switcher = $('nodeColsSwitcher');
  const box = $('nodeList');
  if (!switcher || !box) return;

  const savedCols = localStorage.getItem('mybox_node_cols') || '2';
  applyNodeCols(savedCols);

  switcher.addEventListener('click', (e) => {
    const btn = e.target.closest('.col-btn');
    if (!btn) return;
    const cols = btn.dataset.cols;
    if (!cols) return;
    applyNodeCols(cols);
    try { localStorage.setItem('mybox_node_cols', cols); } catch (_) {}
  });
}

function applyNodeCols(cols) {
  const box = $('nodeList');
  const switcher = $('nodeColsSwitcher');
  if (!box) return;
  box.classList.remove('cols-1', 'cols-2', 'cols-3', 'cols-4');
  box.classList.add(`cols-${cols}`);
  if (switcher) {
    switcher.querySelectorAll('.col-btn').forEach((btn) => {
      btn.classList.toggle('active', btn.dataset.cols === String(cols));
    });
  }
}

export async function testLatency(name) {
  state.latency[name] = { pending: true };
  renderNodeGroups();
  renderNodeList();
  try {
    const r = await api(`/nodes/latency?name=${encodeURIComponent(name)}`);
    state.latency[name] = { delay: r.delay, error: r.error };
  } catch (err) {
    state.latency[name] = { delay: null, error: err.message };
  }
  renderNodeGroups();
  renderNodeList();
}

export async function testAll() {
  const names = state.nodeList.map((n) => n.name);
  if (!names.length) return;
  names.forEach((n) => { state.latency[n] = { pending: true }; });
  renderNodeGroups();
  renderNodeList();
  const batchSize = 200;
  let completed = 0;
  for (let offset = 0; offset < names.length; offset += batchSize) {
    const batch = names.slice(offset, offset + batchSize);
    try {
      const { results = {} } = await api('/nodes/latency/batch', { method: 'POST', body: { names: batch } });
      for (const name of batch) {
        state.latency[name] = results[name] || { delay: null, error: '测速未返回结果' };
      }
      completed += batch.length;
      renderNodeGroups();
      renderNodeList();
      toast(`测速进度 ${completed} / ${names.length}`);
    } catch (err) {
      for (const name of names.slice(offset)) state.latency[name] = { delay: null, error: err.message };
      renderNodeGroups();
      renderNodeList();
      toast(`测速在 ${completed} / ${names.length} 处中断：${err.message}`);
      return;
    }
  }
  toast(`测速完成：${completed} 个节点`);
}

export async function switchNode(group, name) {
  try {
    await api('/nodes/select', { method: 'PUT', body: { group, name } });
    const g = state.nodeGroups.find((x) => x.name === group);
    if (g) g.now = name;
    renderNodeGroups();
    toast(`已切换出口：${group} → ${name}`);
  } catch (err) {
    toast(err.message);
    await loadNodes();
  }
}
