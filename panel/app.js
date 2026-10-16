/* MyBox 面板入口：页面状态与事件编排。 */
import { $, api, busy, escapeHtml, toast, state } from './core.js';
import { configureOverview, loadTraffic, startTrafficPoll, loadOverview, loadOverviewSubs, loadOverviewGroups, selectOverviewSubscription } from './overview.js';
import { startConnAutoPoll, stopConnAutoPoll, setConnectionPage } from './connections.js';
import { GROUP_LABEL, configureNodes, loadNodes, renderNodeGroups, renderNodeList, initNodeColsSwitcher, testLatency, testAll, switchNode } from './nodes.js';
import { loadClients, renderClients, setClientPage, openClientModal, closeClientModal, saveClientModal } from './clients.js';
import { loadSubscriptions } from './subscriptions.js';
import { bindSettingsEvents, loadSettings, loadKernelLog, scheduleLogAuto } from './settings.js';
import { loadRulesetStatus, loadRulesetSubs, refreshSingleRulesetSub, openRulesetModal, closeRulesetModal, saveRulesetModal, toggleRulesetSub, removeRulesetSub } from './rulesets.js';

/* --------------------------------------------------------------- 页面功能 */

// 自动匹配国家/地区 Emoji 国旗
function extractFlag(name = '') {
  const n = name.toUpperCase();
  if (n.includes('香港') || n.includes('HK') || n.includes('HONG KONG')) return '🇭🇰';
  if (n.includes('日本') || n.includes('JP') || n.includes('JAPAN') || n.includes('东京') || n.includes('大阪')) return '🇯🇵';
  if (n.includes('美国') || n.includes('US') || n.includes('USA') || n.includes('美') || n.includes('波特兰') || n.includes('洛杉矶')) return '🇺🇸';
  if (n.includes('新加坡') || n.includes('SG') || n.includes('SINGAPORE') || n.includes('狮城')) return '🇸🇬';
  if (n.includes('台湾') || n.includes('TW') || n.includes('TAIWAN') || n.includes('台北')) return '🇹🇼';
  if (n.includes('韩国') || n.includes('KR') || n.includes('KOREA') || n.includes('首尔')) return '🇰🇷';
  if (n.includes('英国') || n.includes('UK') || n.includes('GB') || n.includes('伦敦')) return '🇬🇧';
  if (n.includes('德国') || n.includes('DE') || n.includes('GERMANY') || n.includes('法兰克福')) return '🇩🇪';
  if (n.includes('法国') || n.includes('FR') || n.includes('FRANCE') || n.includes('巴黎')) return '🇫🇷';
  if (n.includes('加拿大') || n.includes('CA') || n.includes('CANADA')) return '🇨🇦';
  if (n.includes('澳大利亚') || n.includes('AU') || n.includes('AUSTRALIA') || n.includes('悉尼')) return '🇦🇺';
  if (n.includes('俄罗斯') || n.includes('RU') || n.includes('RUSSIA')) return '🇷🇺';
  if (n.includes('直连') || n.includes('DIRECT')) return '⚡';
  if (n.includes('拒绝') || n.includes('BLOCK') || n.includes('REJECT')) return '🚫';
  return '🌐';
}

// 自动识别协议 Badge
function extractProto(name = '', type = '') {
  if (type) return type.toUpperCase();
  const n = name.toUpperCase();
  if (n.includes('HY2') || n.includes('HYSTERIA')) return 'HY2';
  if (n.includes('VLESS')) return 'VLESS';
  if (n.includes('VMESS')) return 'VMESS';
  if (n.includes('TROJAN')) return 'TROJAN';
  if (n.includes('TUIC')) return 'TUIC';
  if (n.includes('SS') || n.includes('SHADOWSOCKS')) return 'SS';
  return 'NODE';
}

/* --------------------------------------------------------------- 登录认证 */

let setupMode = false;

async function checkAuth() {
  const status = await api('/auth/status');
  if (!status.passwordSet) {
    setupMode = true;
    $('gateHint').textContent = '第一次使用，请设置面板密码';
    $('gateConfirmField').classList.remove('hidden');
    $('gateSubmit').textContent = '设置密码并进入';
  } else if (!status.authenticated) {
    setupMode = false;
    $('gateHint').textContent = '请输入面板密码';
    $('gateConfirmField').classList.add('hidden');
    $('gateSubmit').textContent = '进入';
  } else {
    return showApp();
  }
  $('gate').classList.remove('hidden');
}

async function submitGate() {
  const password = $('gatePassword').value;
  $('gateError').textContent = '';
  if (!password) return;

  if (setupMode) {
    if (password !== $('gateConfirm').value) {
      $('gateError').textContent = '两次输入不一致';
      return;
    }
    try {
      await api('/auth/setup', { method: 'POST', body: { password } });
    } catch (err) {
      $('gateError').textContent = err.message;
      return;
    }
  } else {
    try {
      await api('/auth/login', { method: 'POST', body: { password } });
    } catch (err) {
      $('gateError').textContent = err.message;
      return;
    }
  }
  $('gate').classList.add('hidden');
  await showApp();
}

function showApp() {
  $('gate').classList.add('hidden');
  $('app').classList.remove('hidden');
  return loadAll();
}

/* --------------------------------------------------------------- 节点与分组 */

/* Node groups and latency controls live in nodes.js. */

/* Overview rendering, traffic metrics and polling live in overview.js. */

/* Connection polling and rendering live in connections.js. */

/* --------------------------------------------------------------- 内网分流 */

/* Client loading and management live in clients.js. */

/* Client renderer lives in clients.js. */

/* Client modal handlers live in clients.js. */

/* Client modal handlers live in clients.js. */

/* Client save action lives in clients.js. */

/* Client mode action lives in clients.js. */

/* Client removal action lives in clients.js. */

/* --------------------------------------------------------------- 订阅管理 */

/* Subscription page rendering lives in subscriptions.js. */

/* --------------------------------------------------------------- 节点分组策略 */

async function loadGroups() {
  try {
    const data = await api('/groups');
    state.groups = data.groups || [];
    renderGroups();
  } catch (err) {
    $('groupList').innerHTML = `<p class="err-text">${escapeHtml(err.message)}</p>`;
  }
}

function renderGroups() {
  const box = $('groupList');
  if (!state.groups.length) {
    box.innerHTML = '<p class="note">还没有分组规则配置。</p>';
    return;
  }
  box.innerHTML = state.groups.map((g, i) => {
    const isBuiltin = g.id === 'all-auto' || g.id === 'all-manual';
    const typeKey = String(g.type || '').toLowerCase();
    const typeText = typeKey === 'urltest' ? '自动择优' : '手动选择';
    return `
    <div class="card" style="background:var(--surface-2);margin-bottom:10px">
      <div class="row" style="align-items:flex-end">
        <label class="field" style="flex:1 1 140px">
          <span>分组名称</span>
          <input data-g-name="${i}" value="${escapeHtml(g.name || '')}">
        </label>
        <label class="field" style="flex:1 1 120px">
          <span>类型</span>
          <input value="${escapeHtml(typeText)}" readonly>
        </label>
        <label class="field" style="flex:2 1 200px">
          <span>${g.mode === 'dynamic' ? '匹配关键词（逗号分隔）' : '包含节点（逗号分隔）'}</span>
          <input data-g-members="${i}" value="${escapeHtml((g.mode === 'dynamic' ? g.keywords : g.members)?.join(', ') || '')}">
        </label>
        <div class="fixed" style="padding-bottom:10px;display:flex;align-items:center;gap:10px">
          <label class="inline"><input type="checkbox" data-g-enabled="${i}"${g.enabled !== false ? ' checked' : ''} style="width:auto"> 启用</label>
          ${isBuiltin ? '' : `<button class="small danger" data-action="remove-group" data-index="${i}" style="padding:4px 8px">删除</button>`}
        </div>
      </div>
    </div>
  `;
  }).join('');
}

window.removeGroup = function(index, event) {
  if (event) {
    event.stopPropagation();
    event.preventDefault();
  }
  const g = state.groups[index];
  if (!g) return;
  if (!confirm(`确定删除节点分组「${g.name || '未命名'}」吗？`)) return;
  state.groups.splice(index, 1);
  renderGroups();
  toast('已删除分组，记得点击下方「保存分组」生效');
};

/* --------------------------------------------------------------- 目标分流 */

async function loadPolicies() {
  try {
    const data = await api('/policies');
    state.policies = data.policies || [];
    renderPolicies();
    loadRulesetSubs();
    loadRulesetStatus();
  } catch (err) {
    $('policyList').innerHTML = `<p class="err-text">${escapeHtml(err.message)}</p>`;
  }
}

function policyIcon(name = '') {
  if (name.includes('AI') || name.includes('OpenAI')) return '🤖 ';
  if (name.includes('YouTube') || name.includes('流媒体')) return '📺 ';
  if (name.includes('直连') || name.includes('国内')) return '🇨🇳 ';
  return '🎯 ';
}

function formatTargetLabel(val) {
  if (!val) return '—';
  if (val === 'builtin-direct' || val === 'direct') return '直连';
  if (val === 'builtin-block' || val === 'block') return '拒绝';
  if (val === 'all-auto') return '所有-自动';
  if (val === 'all-manual') return '所有-手动';
  const found = (state.groups || []).find((g) => g.id === val || g.name === val);
  if (found) return found.name;
  return val;
}

/**
 * 组装策略出口目标的下拉选项 HTML：支持内置出口、节点分组、所有单节点、以及绝对防回退兜底
 */
function getTargetOptionsHtml(currentTarget = '') {
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

  // 2. 出站分组 (排除内置的 all-auto 和 all-manual，因基础目标中已包含)
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

  // 4. 防御性兜底：若前序均未匹配，且 currentTarget 非空，强制将其插入作为选中项，彻底杜绝浏览器回退到首项
  if (!matched && currentTarget) {
    html = `<optgroup label="当前配置"><option value="${escapeHtml(currentTarget)}" selected>${escapeHtml(formatTargetLabel(currentTarget))}</option></optgroup>` + html;
  }

  return html;
}

function renderPolicies() {
  const box = $('policyList');
  const enabledCount = state.policies.filter((policy) => policy.enabled).length;
  if ($('policyEnabledSummary')) $('policyEnabledSummary').textContent = `已启用 ${enabledCount} / ${state.policies.length} 条`;
  if (!state.policies.length) {
    box.innerHTML = '<p class="note" style="grid-column:1/-1;text-align:center;padding:16px 0">还没有策略。</p>';
    return;
  }
  const isCol = (idx) => state.policyCollapsed?.[idx] !== false;

  box.innerHTML = state.policies.map((p, i) => {
    const collapsed = isCol(i);
    const targetVal = p.target || 'all-auto';
    return `
    <div class="card policy-card" draggable="true" data-policy-index="${i}">
      <div class="card-head group-head" data-policy-collapse="${i}" style="cursor:pointer;margin-bottom:${collapsed ? '0' : '12px'}">
        <span class="drag-grip" title="长按或拖拽调整策略优先级" data-stop-propagation>
          <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2.5"><circle cx="9" cy="6" r="1.5"/><circle cx="15" cy="6" r="1.5"/><circle cx="9" cy="12" r="1.5"/><circle cx="15" cy="12" r="1.5"/><circle cx="9" cy="18" r="1.5"/><circle cx="15" cy="18" r="1.5"/></svg>
        </span>
        <button type="button" class="small policy-move" data-action="move-policy" data-index="${i}" data-direction="-1" aria-label="将${escapeHtml(p.name)}上移"${i === 0 ? ' disabled' : ''}>↑</button>
        <button type="button" class="small policy-move" data-action="move-policy" data-index="${i}" data-direction="1" aria-label="将${escapeHtml(p.name)}下移"${i === state.policies.length - 1 ? ' disabled' : ''}>↓</button>
        <span class="policy-idx-badge">${i + 1}</span>
        <span class="chevron">${collapsed ? '▸' : '▾'}</span>
        <h3 style="display:flex;align-items:center;gap:6px">${policyIcon(p.name)}${escapeHtml(p.name)}</h3>
        <span class="tag muted" data-p-status="${i}">${p.enabled ? '已启用' : '已关闭'}</span>
        <span class="note policy-target-note" style="font-size:12px;margin-left:4px">出口：<strong style="color:var(--accent)" id="policyTargetLabel-${i}">${escapeHtml(formatTargetLabel(targetVal))}</strong></span>
        <div class="spacer"></div>
        <label class="switch" data-stop-propagation>
          <input type="checkbox" data-p-enabled="${i}"${p.enabled ? ' checked' : ''}>
          <span></span>
        </label>
        <button class="small danger" data-action="remove-policy" data-index="${i}">删除</button>
      </div>
      <div data-p-body="${i}" class="${collapsed ? 'hidden' : ''}">
        <div class="row">
          <label class="field" style="flex:1 1 180px">
            <span>策略名称</span>
            <input data-p-name="${i}" value="${escapeHtml(p.name || '')}">
          </label>
          <label class="field" style="flex:1 1 200px">
            <span>出口目标（直连/拒绝/分组/节点）</span>
            <select data-p-target="${i}">
              ${getTargetOptionsHtml(targetVal)}
            </select>
          </label>
        </div>
        <div class="row">
          <label class="field"><span>规则集 (rulesets)</span><input data-p-rulesets="${i}" value="${escapeHtml(p.rulesets?.join(', ') || '')}"></label>
          <label class="field"><span>域名后缀 (domain_suffix)</span><input data-p-suffix="${i}" value="${escapeHtml(p.domainSuffix?.join(', ') || '')}"></label>
        </div>
      </div>
    </div>
  `;
  }).join('');
}

function movePolicy(index, direction) {
  syncPoliciesFromDOM();
  const targetIndex = index + direction;
  if (targetIndex < 0 || targetIndex >= state.policies.length) return;
  const [moved] = state.policies.splice(index, 1);
  state.policies.splice(targetIndex, 0, moved);
  renderPolicies();
  toast(`已将「${moved.name || '策略'}」移至第 ${targetIndex + 1} 位，请保存部署`);
}

window.updatePolicyTarget = function(index, val) {
  if (state.policies[index]) {
    state.policies[index].target = val;
    const labelEl = $(`policyTargetLabel-${index}`);
    if (labelEl) labelEl.textContent = formatTargetLabel(val);
  }
};

window.removePolicy = function(index, event) {
  if (event) {
    event.stopPropagation();
    event.preventDefault();
  }
  const p = state.policies[index];
  if (!p) return;
  if (!confirm(`确定删除分流策略「${p.name || '未命名策略'}」吗？`)) return;
  state.policies.splice(index, 1);
  renderPolicies();
  toast('已删除策略，记得点击下方「保存策略」生效');
};

function syncPoliciesFromDOM() {
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

function initPolicyDragSort() {
  const listEl = $('policyList');
  if (!listEl || listEl._dragInited) return;
  listEl._dragInited = true;

  let dragSrcIdx = null;

  listEl.addEventListener('dragstart', (e) => {
    const card = e.target.closest('.policy-card');
    if (!card) return;
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
    if (!card) return;
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
    if (!card) return;
    const dropTargetIdx = Number(card.dataset.policyIndex);
    if (isNaN(dragSrcIdx) || isNaN(dropTargetIdx) || dragSrcIdx === dropTargetIdx) return;

    const [movedPolicy] = state.policies.splice(dragSrcIdx, 1);
    state.policies.splice(dropTargetIdx, 0, movedPolicy);

    if (state.policyCollapsed) {
      const newCol = {};
      state.policies.forEach((_, i) => { newCol[i] = true; });
      state.policyCollapsed = newCol;
    }

    renderPolicies();
    toast(`已将「${movedPolicy.name || '策略'}」移至第 ${dropTargetIdx + 1} 位，请保存部署`);
    dragSrcIdx = null;
  });

  listEl.addEventListener('dragend', () => {
    document.querySelectorAll('.policy-card').forEach((el) => el.classList.remove('dragging', 'drag-over'));
    dragSrcIdx = null;
  });

  // 支持移动端/触屏长按拖拽排序
  let touchTimer = null;
  let touchCard = null;
  let touchStartIdx = null;

  listEl.addEventListener('touchstart', (e) => {
    const grip = e.target.closest('.drag-grip');
    if (!grip) return;
    const card = grip.closest('.policy-card');
    if (!card) return;
    touchCard = card;
    touchStartIdx = Number(card.dataset.policyIndex);

    touchTimer = setTimeout(() => {
      syncPoliciesFromDOM();
      card.classList.add('dragging');
      if (navigator.vibrate) navigator.vibrate(35);
    }, 200);
  }, { passive: true });

  listEl.addEventListener('touchmove', (e) => {
    if (!touchCard || !touchCard.classList.contains('dragging')) {
      clearTimeout(touchTimer);
      return;
    }
    const touch = e.touches[0];
    const elem = document.elementFromPoint(touch.clientX, touch.clientY);
    const targetCard = elem?.closest('.policy-card');
    document.querySelectorAll('.policy-card.drag-over').forEach((el) => {
      if (el !== targetCard) el.classList.remove('drag-over');
    });
    if (targetCard && targetCard !== touchCard) {
      targetCard.classList.add('drag-over');
    }
  }, { passive: true });

  listEl.addEventListener('touchend', (e) => {
    clearTimeout(touchTimer);
    if (!touchCard) return;
    const isDragging = touchCard.classList.contains('dragging');
    touchCard.classList.remove('dragging');
    document.querySelectorAll('.policy-card.drag-over').forEach((el) => el.classList.remove('drag-over'));

    if (isDragging) {
      const touch = e.changedTouches[0];
      const elem = document.elementFromPoint(touch.clientX, touch.clientY);
      const targetCard = elem?.closest('.policy-card');
      if (targetCard && targetCard !== touchCard) {
        const dropTargetIdx = Number(targetCard.dataset.policyIndex);
        if (!isNaN(touchStartIdx) && !isNaN(dropTargetIdx) && touchStartIdx !== dropTargetIdx) {
          const [moved] = state.policies.splice(touchStartIdx, 1);
          state.policies.splice(dropTargetIdx, 0, moved);
          renderPolicies();
          toast(`已将「${moved.name || '策略'}」移至第 ${dropTargetIdx + 1} 位，请保存部署`);
        }
      }
    }
    touchCard = null;
    touchStartIdx = null;
  });
}

/* ---------------------------------------------------------- 自定义规则集订阅 */


/* Ruleset status and actions live in rulesets.js. */

/* Ruleset subscription rendering lives in rulesets.js. */

/* Ruleset modal handlers live in rulesets.js. */

/* Ruleset modal saving lives in rulesets.js. */

/* Ruleset subscription mutations live in rulesets.js. */

/* --------------------------------------------------------------- 核心设置与日志 */

/* Settings data loading lives in settings.js. */

/* Kernel log controls live in settings.js. */

/* --------------------------------------------------------------- 加载入口与事件 */

async function loadAll() {
  await Promise.all([
    loadOverview(),
    loadSubscriptions(),
    loadGroups(),
    loadNodes(),
    loadSettings(),
  ]);
  // 必须确保 groups 和 nodes 已经加载完毕，再执行 loadPolicies 渲染下拉选项
  await loadPolicies();
}

async function withBusy(button, fn) {
  busy(button, true);
  try {
    await fn();
  } catch (err) {
    toast(err.message);
  } finally {
    busy(button, false);
  }
}

async function doDeploy(button) {
  await withBusy(button, async () => {
    const report = await api('/deploy', { method: 'POST', body: { restart: true } });
    const warns = report.warnings?.length ? `（${report.warnings.length} 条提示）` : '';
    toast(`部署完成${warns}`);
    await loadOverview();
  });
}

async function switchTab(tab) {
  if (!tab) return;
  if (tab === 'logout') {
    await api('/auth/logout', { method: 'POST' });
    location.reload();
    return;
  }
  const targetBtn = document.querySelector(`aside.sidebar button[data-tab="${tab}"]`);
  document.querySelectorAll('aside.sidebar button[data-tab]').forEach((b) => b.classList.toggle('active', b === targetBtn));
  document.querySelectorAll('section.page').forEach((p) => p.classList.toggle('active', p.id === `page-${tab}`));

  if (tab === 'overview') { await loadOverview(); }
  if (tab === 'nodes') { await loadNodes(); await loadGroups(); }
  if (tab === 'connections') {
    startConnAutoPoll();
  } else {
    stopConnAutoPoll();
  }
  if (tab === 'clients') loadClients();
  if (tab === 'subscriptions') await loadSubscriptions();
  if (tab === 'policies') {
    if (!state.groups?.length) await loadGroups();
    if (!state.nodeList?.length) await loadNodes();
    await loadPolicies();
  }
  if (tab === 'settings') { await loadSettings(); await loadKernelLog(); await loadRulesetStatus(); }
  scheduleLogAuto();
}

function bindEvents() {
  document.addEventListener('click', (e) => {
    const latencyAction = e.target.closest('[data-action="test-latency"]');
    if (latencyAction) {
      e.stopPropagation();
      testLatency(latencyAction.dataset.name);
      return;
    }
    if (e.target.closest('[data-stop-propagation]')) e.stopPropagation();
  }, true);
  document.addEventListener('click', (e) => {
    const action = e.target.closest('[data-action]');
    if (!action) return;
    const { action: name, id, index, tag } = action.dataset;
    if (name === 'reset-connections') window.resetConnectionRecords();
    else if (name === 'connection-page') {
      setConnectionPage(action.dataset.page);
    }
    else if (name === 'close-connection') window.closeSingleConn(id);
    else if (name === 'client-page') setClientPage(action.dataset.page);
    else if (name === 'edit-client') window.openClientModal(Number(index));
    else if (name === 'remove-client') window.removeClient(id);
    else if (name === 'remove-group') window.removeGroup(Number(index), e);
    else if (name === 'remove-policy') window.removePolicy(Number(index), e);
    else if (name === 'move-policy') movePolicy(Number(index), Number(action.dataset.direction));
    else if (name === 'refresh-ruleset') window.refreshSingleRulesetSub(tag);
    else if (name === 'remove-ruleset') window.removeRulesetSub(id);
  });
  document.addEventListener('change', (e) => {
    const action = e.target.closest('[data-action]');
    if (!action) return;
    if (action.dataset.action === 'client-mode') window.updateClientMode(action.dataset.id, action.value);
    else if (action.dataset.action === 'toggle-ruleset') window.toggleRulesetSub(action.dataset.id, action.checked);
  });
  $('nodeList').addEventListener('click', (e) => {
    const pageButton = e.target.closest('[data-node-page]');
    if (!pageButton || pageButton.disabled) return;
    state.nodePage = Number(pageButton.dataset.nodePage) || 1;
    renderNodeList();
  });
  $('gateSubmit').addEventListener('click', submitGate);
  $('gatePassword').addEventListener('keydown', (e) => { if (e.key === 'Enter') submitGate(); });
  $('gateConfirm').addEventListener('keydown', (e) => { if (e.key === 'Enter') submitGate(); });

  // 导航切换
  document.querySelectorAll('aside.sidebar nav.sb-nav button, .sb-foot button[data-tab]').forEach((btn) => {
    btn.addEventListener('click', () => switchTab(btn.dataset.tab));
  });

  // 支持所有带有 data-nav-tab 的卡片标题或元素点击跳转
  document.addEventListener('click', (e) => {
    const navEl = e.target.closest('[data-nav-tab]');
    if (navEl) {
      const tab = navEl.dataset.navTab;
      if (tab) switchTab(tab);
    }
  });

  // 全局常驻与概览部署
  $('btnGlobalDeploy')?.addEventListener('click', (e) => doDeploy(e.currentTarget));
  $('btnDeployFromNodes')?.addEventListener('click', (e) => doDeploy(e.currentTarget));
  $('btnDeploySubs')?.addEventListener('click', (e) => doDeploy(e.currentTarget));
  $('btnDeployClients')?.addEventListener('click', (e) => doDeploy(e.currentTarget));
  $('btnDeploy')?.addEventListener('click', (e) => doDeploy(e.currentTarget));
  $('btnTrafficReset')?.addEventListener('click', async () => {
    await api('/traffic/reset', { method: 'POST' });
    toast('流量统计已清零');
    loadTraffic();
  });
  startTrafficPoll();

  // 概览页订阅管理与节点联动
  $('btnOverviewRefreshSubs')?.addEventListener('click', (e) => withBusy(e.currentTarget, loadOverviewSubs));
  $('overviewSubsList')?.addEventListener('click', (e) => {
    const item = e.target.closest('[data-ov-sub-id]');
    if (!item) return;
    const subId = item.dataset.ovSubId;
    if (subId) {
      selectOverviewSubscription(subId);
    }
  });
  $('overviewSubsList')?.addEventListener('change', async (e) => {
    const toggle = e.target.closest('[data-ov-sub-toggle]');
    if (!toggle) return;
    const subId = toggle.dataset.ovSubToggle;
    const enabled = toggle.checked;
    try {
      await api(`/subscriptions/${encodeURIComponent(subId)}`, { method: 'PUT', body: { enabled } });
      toast(subId === 'all' ? `已${enabled ? '启用全部' : '停用全部'}订阅并部署生效` : `订阅已${enabled ? '启用' : '停用'}并部署生效`);
      await loadOverviewSubs();
      await loadOverviewGroups();
    } catch (err) {
      toast(`切换订阅失败：${err.message}`);
      toggle.checked = !enabled;
    }
  });

  $('btnRestart').addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
    await api('/kernel/restart', { method: 'POST' });
    toast('内核已重启');
    await loadOverview();
  }));
  $('btnStop').addEventListener('click', (e) => {
    if (!confirm('确定停止 sing-box 内核吗？代理流量会立即中断，直到重新启动内核。')) return;
    return withBusy(e.currentTarget, async () => {
    await api('/kernel/stop', { method: 'POST' });
    toast('内核已停止');
    await loadOverview();
    });
  });
  $('btnOverviewNodes').addEventListener('click', (e) => withBusy(e.currentTarget, loadOverviewGroups));
  $('overviewGroups').addEventListener('change', async (e) => {
    const group = e.target.dataset.ovGroup;
    if (!group) return;
    await switchNode(group, e.target.value);
    await loadOverviewGroups();
  });

  // 节点页
  $('btnReloadNodes').addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
    await loadNodes();
    toast('已刷新节点');
  }));
  $('btnTestAll').addEventListener('click', (e) => withBusy(e.currentTarget, testAll));
  $('btnCollapseAll').addEventListener('click', () => {
    const anyExpanded = state.nodeGroups.some((g) => state.collapsed[g.name] === false);
    for (const g of state.nodeGroups) state.collapsed[g.name] = anyExpanded;
    renderNodeGroups();
    toast(anyExpanded ? '已全部折叠' : '已全部展开');
  });

  $('nodeGroups').addEventListener('click', async (e) => {
    const lat = e.target.dataset.latency;
    if (lat) {
      e.stopPropagation();
      await testLatency(lat);
      return;
    }
    const collapseName = e.target.closest('[data-collapse]')?.dataset.collapse;
    if (collapseName !== undefined) {
      const nowCollapsed = state.collapsed[collapseName] !== false;
      state.collapsed[collapseName] = !nowCollapsed;
      renderNodeGroups();
      return;
    }
    const card = e.target.closest('[data-select-group]');
    if (card) {
      const group = card.dataset.selectGroup;
      const name = card.dataset.selectName;
      if (group && name) await switchNode(group, name);
    }
  });

  $('nodeList').addEventListener('click', async (e) => {
    const lat = e.target.dataset.latency;
    if (lat) await testLatency(lat);
  });
  initNodeColsSwitcher();
  $('nodeSearch')?.addEventListener('input', () => {
    state.nodePage = 1;
    renderNodeList();
  });
  $('nodeSort')?.addEventListener('change', () => {
    state.nodePage = 1;
    renderNodeList();
  });

  // 分组管理
  $('btnAddGroup')?.addEventListener('click', () => {
    const newIdx = state.groups.length + 1;
    state.groups.push({
      id: `grp-${Date.now()}`,
      name: `自定义组-${newIdx}`,
      type: 'selector',
      mode: 'dynamic',
      keywords: [],
      members: [],
      enabled: true,
    });
    renderGroups();
    toast('已添加新分组，请填写名称和关键词后点击「保存分组」');
  });

  async function commitGroupsFromDOM() {
    document.querySelectorAll('#groupList input[data-g-name]').forEach((input) => {
      const idx = Number(input.dataset.gName);
      if (state.groups[idx]) {
        state.groups[idx].name = input.value.trim();
      }
    });
    document.querySelectorAll('#groupList input[data-g-members]').forEach((input) => {
      const idx = Number(input.dataset.gMembers);
      if (state.groups[idx]) {
        const arr = input.value.split(',').map((s) => s.trim()).filter(Boolean);
        if (state.groups[idx].mode === 'dynamic') {
          state.groups[idx].keywords = arr;
        } else {
          state.groups[idx].members = arr;
        }
      }
    });
    document.querySelectorAll('#groupList input[data-g-enabled]').forEach((input) => {
      const idx = Number(input.dataset.gEnabled);
      if (state.groups[idx]) {
        state.groups[idx].enabled = input.checked;
      }
    });

    const res = await api('/groups', { method: 'PUT', body: { groups: state.groups } });
    if (res?.groups) state.groups = res.groups;
    renderGroups();
    // 联动刷新策略下拉框中的分组列表
    if (state.policies?.length) renderPolicies();
  }

  $('btnSaveGroups')?.addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
    await commitGroupsFromDOM();
    toast('分组配置已保存（尚未部署生效）');
  }));

  $('btnSaveDeployGroups')?.addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
    await commitGroupsFromDOM();
    const report = await api('/deploy', { method: 'POST', body: { restart: true } });
    const warns = report.warnings?.length ? `（${report.warnings.length} 条提示）` : '';
    toast(`分组已保存并成功部署！${warns}`);
    await loadOverview();
  }));

  $('groupList')?.addEventListener('input', (e) => {
    const t = e.target;
    if (t.dataset.gName !== undefined) {
      const i = Number(t.dataset.gName);
      if (state.groups[i]) state.groups[i].name = t.value.trim();
    } else if (t.dataset.gMembers !== undefined) {
      const i = Number(t.dataset.gMembers);
      if (state.groups[i]) {
        const arr = t.value.split(',').map((s) => s.trim()).filter(Boolean);
        if (state.groups[i].mode === 'dynamic') {
          state.groups[i].keywords = arr;
        } else {
          state.groups[i].members = arr;
        }
      }
    }
  });

  $('groupList')?.addEventListener('change', (e) => {
    const t = e.target;
    if (t.dataset.gEnabled !== undefined) {
      const i = Number(t.dataset.gEnabled);
      if (state.groups[i]) state.groups[i].enabled = t.checked;
    }
  });

  // 内网分流按键与弹窗
  $('clientSearch')?.addEventListener('input', () => setClientPage(1));
  $('btnScanClients')?.addEventListener('click', async (e) => {
    const btn = e.currentTarget;
    btn.disabled = true;
    const oldHtml = btn.innerHTML;
    btn.innerHTML = '<span class="animate-spin">↻</span> 正在扫描局域网设备...';
    try {
      const res = await api('/clients/scan', { method: 'POST' });
      state.clients = res.clients || [];
      renderClients();
      toast(`扫描完成，共发现 ${state.clients.length} 台局域网设备`);
    } catch (err) {
      toast(`扫描失败：${err.message}`);
    } finally {
      btn.disabled = false;
      btn.innerHTML = oldHtml;
    }
  });
  $('btnAddClient')?.addEventListener('click', () => {
    openClientModal(-1);
  });
  $('btnCancelClientModal')?.addEventListener('click', closeClientModal);
  $('btnSaveClientModal')?.addEventListener('click', saveClientModal);
  $('clientModal')?.addEventListener('click', (e) => {
    if (e.target.id === 'clientModal') closeClientModal();
  });

  // 连接页
  $('btnConnsRefresh')?.addEventListener('click', (e) => withBusy(e.currentTarget, window.resetConnectionRecords));
  $('btnCloseAllConns')?.addEventListener('click', window.closeAllConnections);
  const renderConnectionsFromFirstPage = () => setConnectionPage(1);
  $('connSearch')?.addEventListener('input', renderConnectionsFromFirstPage);
  $('connSearch')?.addEventListener('search', renderConnectionsFromFirstPage);
  $('btnClearConnRecords')?.addEventListener('click', window.clearConnectionRecords);

  // 订阅页
  $('btnAddSub').addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
    const url = $('subUrl').value.trim();
    if (!url) throw new Error('请填写订阅地址');
    const result = await api('/subscriptions', { method: 'POST', body: { name: $('subName').value.trim(), url } });
    $('subUrl').value = '';
    $('subName').value = '';
    toast(`已解析 ${result.nodeCount ?? 0} 个节点`);
    await Promise.all([loadSubscriptions(), loadOverview()]);
  }));

  $('btnRefreshSubs').addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
    const { subscriptions } = await api('/subscriptions');
    let total = 0;
    const failures = [];
    for (const [index, s] of subscriptions.entries()) {
      toast(`正在刷新订阅 ${index + 1} / ${subscriptions.length}：${s.name || s.id}`);
      try {
        const r = await api(`/subscriptions/${encodeURIComponent(s.id)}/refresh`, { method: 'POST' });
        total += r.nodeCount || 0;
      } catch (err) {
        failures.push(s.name || s.id);
      }
    }
    toast(`刷新完成：${subscriptions.length - failures.length} / ${subscriptions.length} 条成功，共 ${total} 个节点${failures.length ? `；失败：${failures.join('、')}` : ''}`);
    await Promise.all([loadSubscriptions(), loadOverview()]);
  }));

  $('subList').addEventListener('change', async (e) => {
    const id = e.target.dataset.subToggle;
    if (!id) return;
    const enabled = e.target.checked;
    try {
      await api(`/subscriptions/${encodeURIComponent(id)}`, { method: 'PUT', body: { enabled } });
      toast(enabled ? '已启用，记得「部署配置」' : '已停用，记得「部署配置」');
      await loadSubscriptions();
    } catch (err) {
      e.target.checked = !enabled;
      toast(err.message);
    }
  });

  $('subList').addEventListener('click', async (e) => {
    const refreshId = e.target.dataset.refresh;
    const removeId = e.target.dataset.remove;
    if (refreshId) {
      await withBusy(e.target, async () => {
        const r = await api(`/subscriptions/${refreshId}/refresh`, { method: 'POST' });
        toast(`已解析 ${r.nodeCount} 个节点`);
        await Promise.all([loadSubscriptions(), loadOverview()]);
      });
    }
    if (removeId) {
      if (!confirm('删除这条订阅及其节点？')) return;
      await api(`/subscriptions/${encodeURIComponent(removeId)}`, { method: 'DELETE' });
      toast('已删除');
      await Promise.all([loadSubscriptions(), loadOverview()]);
    }
  });

  // 策略页
  async function commitPoliciesFromDOM() {
    // 强制从 DOM 提取所有策略控件的最新值
    document.querySelectorAll('#policyList select[data-p-target]').forEach((sel) => {
      const idx = Number(sel.dataset.pTarget);
      if (state.policies[idx]) {
        state.policies[idx].target = sel.value;
      }
    });
    document.querySelectorAll('#policyList input[data-p-name]').forEach((input) => {
      const idx = Number(input.dataset.pName);
      if (state.policies[idx]) {
        state.policies[idx].name = input.value.trim();
      }
    });
    document.querySelectorAll('#policyList input[data-p-rulesets]').forEach((input) => {
      const idx = Number(input.dataset.pRulesets);
      if (state.policies[idx]) {
        state.policies[idx].rulesets = input.value.split(',').map((s) => s.trim()).filter(Boolean);
      }
    });
    document.querySelectorAll('#policyList input[data-p-suffix]').forEach((input) => {
      const idx = Number(input.dataset.pSuffix);
      if (state.policies[idx]) {
        state.policies[idx].domainSuffix = input.value.split(',').map((s) => s.trim()).filter(Boolean);
      }
    });

    const res = await api('/policies', { method: 'PUT', body: { policies: state.policies } });
    if (res?.policies) state.policies = res.policies;
    renderPolicies();
  }

  $('btnSavePolicies')?.addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
    await commitPoliciesFromDOM();
    toast('策略配置已保存（尚未部署生效）');
  }));

  $('btnSaveDeployPolicies')?.addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
    await commitPoliciesFromDOM();
    const report = await api('/deploy', { method: 'POST', body: { restart: true } });
    const warns = report.warnings?.length ? `（${report.warnings.length} 条提示）` : '';
    toast(`分流策略已保存并成功部署！${warns}`);
    await loadOverview();
  }));

  $('btnAddPolicy').addEventListener('click', () => {
    const id = 'custom-' + Date.now();
    state.policies.push({ id, name: '新策略', target: 'builtin-direct', rulesets: [], domainSuffix: [], enabled: true });
    renderPolicies();
  });

  $('btnCollapseAllPolicies')?.addEventListener('click', () => {
    if (!state.policyCollapsed) state.policyCollapsed = {};
    const anyExpanded = state.policies.some((_, i) => !state.policyCollapsed[i]);
    state.policies.forEach((_, i) => { state.policyCollapsed[i] = anyExpanded; });
    renderPolicies();
    toast(anyExpanded ? '已全部折叠分流策略' : '已全部展开分流策略');
  });

  $('btnResetPolicies').addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
    if (!confirm('恢复默认策略将覆盖当前自定义策略，确定？')) return;
    await api('/policies/reset', { method: 'POST' });
    toast('已恢复默认策略');
    await loadPolicies();
  }));

  $('btnRefreshRulesets').addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
    try {
      const r = await api('/rulesets/refresh', { method: 'POST' });
      if (!r.ok) {
        toast('更新失败：' + (r.error || '未知错误'));
        return;
      }
      if (r.hasUpdate) {
        toast(`🎉 发现新版本：已更新 ${r.updatedCount || 1} 个规则集（${r.formattedDate || '刚刚'}）`);
      } else {
        toast(`规则集已是最新版本，无可用更新（上次更新: ${r.formattedDate || '刚刚'}）`);
      }
      await loadRulesetStatus();
      await loadRulesetSubs();
    } catch (err) {
      toast(`更新规则集失败：${err.message}`);
    }
  }));

  $('policyList').addEventListener('click', (e) => {
    // 点击卡片头折叠/展开
    const collapseEl = e.target.closest('[data-policy-collapse]');
    if (collapseEl && !e.target.closest('.switch') && !e.target.closest('button') && !e.target.closest('.drag-grip')) {
      const idx = Number(collapseEl.dataset.policyCollapse);
      if (!state.policyCollapsed) state.policyCollapsed = {};
      const curCol = state.policyCollapsed[idx] !== false;
      state.policyCollapsed[idx] = !curCol;
      renderPolicies();
    }
  });
  initPolicyDragSort();

  $('policyList').addEventListener('input', (e) => {
    const t = e.target;
    if (t.dataset.pName !== undefined) {
      const i = Number(t.dataset.pName);
      if (state.policies[i]) state.policies[i].name = t.value;
    } else if (t.dataset.pRulesets !== undefined) {
      const i = Number(t.dataset.pRulesets);
      if (state.policies[i]) {
        state.policies[i].rulesets = t.value.split(',').map((s) => s.trim()).filter(Boolean);
      }
    } else if (t.dataset.pSuffix !== undefined) {
      const i = Number(t.dataset.pSuffix);
      if (state.policies[i]) {
        state.policies[i].domainSuffix = t.value.split(',').map((s) => s.trim()).filter(Boolean);
      }
    }
  });

  $('policyList').addEventListener('change', async (e) => {
    const t = e.target;
    if (t.dataset.pTarget !== undefined) {
      window.updatePolicyTarget(Number(t.dataset.pTarget), t.value);
      return;
    }
    if (t.dataset.pEnabled === undefined) return;
    const i = Number(t.dataset.pEnabled);
    const policy = state.policies[i];
    policy.enabled = t.checked;
    const status = document.querySelector(`[data-p-status="${i}"]`);
    try {
      await api(`/policies/${encodeURIComponent(policy.id)}/toggle`, { method: 'POST', body: { enabled: t.checked } });
      if (status) status.textContent = t.checked ? '已启用' : '已关闭';
      toast(t.checked ? `「${policy.name}」已启用（0断流热切换）` : `「${policy.name}」已关闭（0断流热切换）`);
    } catch (err) {
      t.checked = !t.checked;
      policy.enabled = !t.checked;
      toast(err.message);
    }
  });

  // 自定义规则集订阅弹窗按键
  $('btnAddRulesetSub')?.addEventListener('click', openRulesetModal);
  $('btnCancelRulesetModal')?.addEventListener('click', closeRulesetModal);
  $('btnSaveRulesetModal')?.addEventListener('click', saveRulesetModal);
  $('rulesetModal')?.addEventListener('click', (e) => {
    if (e.target.id === 'rulesetModal') closeRulesetModal();
  });

  bindSettingsEvents({ withBusy, loadOverview });

  // 当前连接页：断开全部连接
  $('btnCloseAllConns')?.addEventListener('click', window.closeAllConnections);
}

/* --------------------------------------------------------------- 启动 */

configureNodes({ extractFlag, extractProto });
configureOverview({ groupLabel: GROUP_LABEL, extractFlag });
bindEvents();
checkAuth();
