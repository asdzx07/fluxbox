/* MyBox 目标分流策略配置模块 */
import { $, api, escapeHtml, toast, state } from './core.js';
import { loadRulesetSubs, loadRulesetStatus } from './rulesets.js';

export function policyIcon(name = '') {
  if (name.includes('AI') || name.includes('OpenAI')) return '🤖 ';
  if (name.includes('YouTube') || name.includes('流媒体') || name.includes('Netflix')) return '📺 ';
  if (name.includes('直连') || name.includes('国内') || name.includes('CN')) return '🇨🇳 ';
  if (name.includes('广告') || name.includes('拦截')) return '🛡️ ';
  if (name.includes('兜底') || name.includes('Final') || name.includes('默认')) return '⚓ ';
  return '🎯 ';
}

export function formatTargetLabel(val) {
  if (!val) return '—';
  if (val === 'builtin-direct' || val === 'direct') return '直连';
  if (val === 'builtin-block' || val === 'block') return '拒绝';
  if (val === 'all-auto') return '所有-自动';
  if (val === 'all-manual') return '所有-手动';
  const found = (state.groups || []).find((g) => g.id === val || g.name === val);
  if (found) return found.name;
  return val;
}

export function getTargetOptionsHtml(currentTarget = '') {
  let matched = false;
  let html = '';

  // 1. 基础目标
  const builtins = [
    { val: 'all-auto', label: '所有-自动 (自动择优)' },
    { val: 'all-manual', label: '所有-手动 (手动切换)' },
    { val: 'builtin-direct', label: '直连 (direct - 绕过代理)' },
    { val: 'builtin-block', label: '拒绝 (block - 阻止连接)' },
  ];

  html += '<optgroup label="基础目标">';
  for (const b of builtins) {
    const isSel = (!matched) && (
      b.val === currentTarget ||
      (b.val === 'all-auto' && currentTarget === '所有-自动') ||
      (b.val === 'all-manual' && currentTarget === '所有-手动') ||
      (b.val === 'builtin-direct' && (currentTarget === 'direct' || currentTarget === 'builtin-direct')) ||
      (b.val === 'builtin-block' && (currentTarget === 'block' || currentTarget === 'builtin-block'))
    );
    if (isSel) matched = true;
    html += `<option value="${escapeHtml(b.val)}"${isSel ? ' selected' : ''}>${escapeHtml(b.label)}</option>`;
  }
  html += '</optgroup>';

  // 2. 出站分组
  const excludeIds = new Set(['all-auto', 'all-manual', 'direct', 'block', 'builtin-direct', 'builtin-block']);
  const groupItems = (state.groups || []).filter((g) => g && !excludeIds.has(g.id) && !excludeIds.has(g.name));

  if (groupItems.length) {
    html += '<optgroup label="出站分组">';
    for (const g of groupItems) {
      const gid = g.id || g.name;
      const isSel = (!matched) && (currentTarget === g.id || currentTarget === g.name);
      if (isSel) matched = true;
      const typeLabel = (g.type === 'urltest' || g.type === 'URLTest') ? '自动' : '手动';
      html += `<option value="${escapeHtml(gid)}"${isSel ? ' selected' : ''}>${escapeHtml(g.name)} (${typeLabel})</option>`;
    }
    html += '</optgroup>';
  }

  // 3. 单个节点
  const nodes = (state.nodeList || []).map((n) => n.name).filter(Boolean);
  if (nodes.length) {
    html += '<optgroup label="指定单个节点">';
    for (const n of nodes) {
      const isSel = (!matched) && (n === currentTarget);
      if (isSel) matched = true;
      html += `<option value="${escapeHtml(n)}"${isSel ? ' selected' : ''}>${escapeHtml(n)}</option>`;
    }
    html += '</optgroup>';
  }

  // 4. 防御性兜底
  if (!matched && currentTarget) {
    html = `<optgroup label="当前配置"><option value="${escapeHtml(currentTarget)}" selected>${escapeHtml(formatTargetLabel(currentTarget))}</option></optgroup>` + html;
  }

  return html;
}

export async function loadPolicies() {
  const box = $('policyList');
  try {
    const data = await api('/policies');
    state.policies = data.policies || [];
    renderPolicies();
    loadRulesetSubs();
    loadRulesetStatus();
  } catch (err) {
    if (box) box.innerHTML = `<p class="err-text">${escapeHtml(err.message)}</p>`;
  }
}

export function renderPolicies() {
  const box = $('policyList');
  if (!box) return;
  const policies = state.policies || [];
  const enabledCount = policies.filter((policy) => policy.enabled).length;
  if ($('policyEnabledSummary')) $('policyEnabledSummary').textContent = `已启用 ${enabledCount} / ${policies.length} 条`;
  if (!policies.length) {
    box.innerHTML = '<p class="note" style="grid-column:1/-1;text-align:center;padding:16px 0">还没有策略，点击右上角「＋ 新增策略」。</p>';
    return;
  }
  const isCol = (idx) => state.policyCollapsed?.[idx] !== false;

  box.innerHTML = policies.map((p, i) => {
    const collapsed = isCol(i);
    const targetVal = p.target || 'all-auto';
    const isFallback = i === policies.length - 1 && (p.name.includes('兜底') || p.id === 'final');
    const badgeNum = String(i + 1).padStart(2, '0');

    return `
    <div class="card policy-card ${isFallback ? 'is-fallback' : ''}" draggable="${isFallback ? 'false' : 'true'}" data-policy-index="${i}">
      <div class="card-head group-head" data-policy-collapse="${i}" style="cursor:pointer;margin-bottom:${collapsed ? '0' : '12px'}">
        ${isFallback ? '<span class="drag-grip fallback-fixed" title="兜底规则固定末位，防漏路由">⚓</span>' : `
          <span class="drag-grip" title="长按或拖拽调整策略优先级" data-stop-propagation>
            <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2.5"><circle cx="9" cy="6" r="1.5"/><circle cx="15" cy="6" r="1.5"/><circle cx="9" cy="12" r="1.5"/><circle cx="15" cy="12" r="1.5"/><circle cx="9" cy="18" r="1.5"/><circle cx="15" cy="18" r="1.5"/></svg>
          </span>
          <button type="button" class="small policy-move" data-action="move-policy" data-index="${i}" data-direction="-1" aria-label="将${escapeHtml(p.name)}上移"${i === 0 ? ' disabled' : ''}>↑</button>
          <button type="button" class="small policy-move" data-action="move-policy" data-index="${i}" data-direction="1" aria-label="将${escapeHtml(p.name)}下移"${i >= policies.length - 2 ? ' disabled' : ''}>↓</button>
        `}
        <span class="policy-idx-badge ${isFallback ? 'fallback-badge' : ''}">${badgeNum}</span>
        <span class="chevron">${collapsed ? '▸' : '▾'}</span>
        <h3 style="display:flex;align-items:center;gap:6px">${policyIcon(p.name)}${escapeHtml(p.name)}</h3>
        <span class="tag ${p.enabled ? 'good' : 'muted'}" data-p-status="${i}">${p.enabled ? '已启用' : '已关闭'}</span>
        <span class="note policy-target-note" style="font-size:12px;margin-left:4px">出口：<strong style="color:var(--accent)" id="policyTargetLabel-${i}">${escapeHtml(formatTargetLabel(targetVal))}</strong></span>
        <div class="spacer"></div>
        <label class="switch" data-stop-propagation>
          <input type="checkbox" data-p-enabled="${i}"${p.enabled ? ' checked' : ''}${isFallback ? ' disabled' : ''}>
          <span></span>
        </label>
        ${isFallback ? '<span class="tag muted" style="font-size:11px">固定末位</span>' : `<button class="small danger" data-action="remove-policy" data-index="${i}">删除</button>`}
      </div>
      <div data-p-body="${i}" class="${collapsed ? 'hidden' : ''}">
        <div class="row">
          <label class="field" style="flex:1 1 180px">
            <span>策略名称</span>
            <input data-p-name="${i}" value="${escapeHtml(p.name || '')}" ${isFallback ? 'readonly' : ''}>
          </label>
          <label class="field" style="flex:1 1 200px">
            <span>出口目标（直连 / 拒绝 / 分组 / 单节点）</span>
            <select data-p-target="${i}">
              ${getTargetOptionsHtml(targetVal)}
            </select>
          </label>
        </div>
        <div class="row">
          <label class="field"><span>规则集 (rulesets)</span><input data-p-rulesets="${i}" value="${escapeHtml(p.rulesets?.join(', ') || '')}" placeholder="例如：geosite-openai, geosite-netflix"></label>
          <label class="field"><span>域名后缀 (domain_suffix)</span><input data-p-suffix="${i}" value="${escapeHtml(p.domainSuffix?.join(', ') || '')}" placeholder="例如：openai.com, google.com"></label>
        </div>
        ${isFallback ? '<p class="note" style="margin-top:6px;font-size:11px">作为最终出口，所有未被前序规则命中的流量都将由此出口处理，避免未匹配流量无路由。</p>' : ''}
      </div>
    </div>
  `;
  }).join('');
}

export function movePolicy(index, direction) {
  syncPoliciesFromDOM();
  const targetIndex = index + direction;
  // 保护兜底规则始终在末位
  const lastIndex = state.policies.length - 1;
  if (targetIndex < 0 || targetIndex >= lastIndex || index >= lastIndex) return;
  const [moved] = state.policies.splice(index, 1);
  state.policies.splice(targetIndex, 0, moved);
  renderPolicies();
  toast(`已将「${moved.name || '策略'}」调整至第 ${targetIndex + 1} 优先级`);
}

export function updatePolicyTarget(index, val) {
  if (state.policies[index]) {
    state.policies[index].target = val;
    const labelEl = $(`policyTargetLabel-${index}`);
    if (labelEl) labelEl.textContent = formatTargetLabel(val);
  }
}

export function removePolicy(index, event) {
  if (event) {
    event.stopPropagation();
    event.preventDefault();
  }
  const lastIndex = state.policies.length - 1;
  if (index >= lastIndex && state.policies[index]?.name.includes('兜底')) {
    toast('兜底策略为系统最终出口，不可删除');
    return;
  }
  const p = state.policies[index];
  if (!p) return;
  if (!confirm(`确定删除分流策略「${p.name || '未命名策略'}」吗？`)) return;
  state.policies.splice(index, 1);
  renderPolicies();
  toast('已删除策略，记得保存并部署生效');
}

export function syncPoliciesFromDOM() {
  document.querySelectorAll('#policyList input[data-p-name]').forEach((input) => {
    const idx = Number(input.dataset.pName);
    if (state.policies[idx]) state.policies[idx].name = input.value.trim();
  });
  document.querySelectorAll('#policyList input[data-p-rulesets]').forEach((input) => {
    const idx = Number(input.dataset.pRulesets);
    if (state.policies[idx]) state.policies[idx].rulesets = input.value.split(',').map((s) => s.trim()).filter(Boolean);
  });
  document.querySelectorAll('#policyList input[data-p-suffix]').forEach((input) => {
    const idx = Number(input.dataset.pSuffix);
    if (state.policies[idx]) state.policies[idx].domainSuffix = input.value.split(',').map((s) => s.trim()).filter(Boolean);
  });
}

export async function commitPoliciesFromDOM() {
  document.querySelectorAll('#policyList select[data-p-target]').forEach((sel) => {
    const idx = Number(sel.dataset.pTarget);
    if (state.policies[idx]) state.policies[idx].target = sel.value;
  });
  syncPoliciesFromDOM();
  const res = await api('/policies', { method: 'PUT', body: { policies: state.policies } });
  if (res?.policies) state.policies = res.policies;
  renderPolicies();
}

export function initPolicyDragSort() {
  const listEl = $('policyList');
  if (!listEl || listEl._dragInited) return;
  listEl._dragInited = true;

  let dragSrcIdx = null;

  listEl.addEventListener('dragstart', (e) => {
    const card = e.target.closest('.policy-card');
    if (!card || card.classList.contains('is-fallback')) return;
    syncPoliciesFromDOM();
    dragSrcIdx = Number(card.dataset.policyIndex);
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', String(dragSrcIdx));
    setTimeout(() => card.classList.add('dragging'), 0);
  });

  listEl.addEventListener('dragover', (e) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    const card = e.target.closest('.policy-card');
    if (!card || card.classList.contains('is-fallback')) return;
    const overIdx = Number(card.dataset.policyIndex);
    if (overIdx === dragSrcIdx) return;
    document.querySelectorAll('.policy-card.drag-over').forEach((el) => {
      if (el !== card) el.classList.remove('drag-over');
    });
    card.classList.add('drag-over');
  });

  listEl.addEventListener('dragleave', (e) => {
    const card = e.target.closest('.policy-card');
    if (card && !card.contains(e.relatedTarget)) {
      card.classList.remove('drag-over');
    }
  });

  listEl.addEventListener('drop', (e) => {
    e.preventDefault();
    const card = e.target.closest('.policy-card');
    document.querySelectorAll('.policy-card').forEach((el) => el.classList.remove('dragging', 'drag-over'));
    if (!card || card.classList.contains('is-fallback')) return;
    const dropTargetIdx = Number(card.dataset.policyIndex);
    if (isNaN(dragSrcIdx) || isNaN(dropTargetIdx) || dragSrcIdx === dropTargetIdx) return;

    const [movedPolicy] = state.policies.splice(dragSrcIdx, 1);
    state.policies.splice(dropTargetIdx, 0, movedPolicy);

    renderPolicies();
    toast(`已将「${movedPolicy.name || '策略'}」移至第 ${dropTargetIdx + 1} 优先级`);
    dragSrcIdx = null;
  });

  listEl.addEventListener('dragend', () => {
    document.querySelectorAll('.policy-card').forEach((el) => el.classList.remove('dragging', 'drag-over'));
    dragSrcIdx = null;
  });
}
