/* MyBox 面板顶层入口：路由调度、全局状态与事件编排 */
import { $, api, busy, escapeHtml, toast, state } from './core.js';
import { configureOverview, loadTraffic, startTrafficPoll, loadOverview, loadOverviewGroups } from './overview.js';
import {
  startConnAutoPoll, stopConnAutoPoll, setConnectionPage,
  clearConnectionRecords, resetConnectionRecords, closeSingleConn, closeAllConnections,
  setConnectionProtocolFilter, setConnectionSortMode,
} from './connections.js';
import { GROUP_LABEL, configureNodes, loadNodes, renderNodeGroups, renderNodeList, initNodeColsSwitcher, testLatency, testAll, switchNode } from './nodes.js';
import { loadClients, renderClients, setClientPage, openClientModal, closeClientModal, saveClientModal, updateClientMode, removeClient } from './clients.js';
import { loadSubscriptions } from './subscriptions.js';
import { loadGroups, renderGroups, removeGroup, commitGroupsFromDOM } from './groups.js';
import { loadPolicies, renderPolicies, movePolicy, removePolicy, updatePolicyTarget, commitPoliciesFromDOM, initPolicyDragSort, togglePolicyExpand } from './policies.js';
import { bindSettingsEvents, loadSettings, loadKernelLog, scheduleLogAuto } from './settings.js';
import { loadRulesetStatus, loadRulesetSubs, refreshSingleRulesetSub, openRulesetModal, closeRulesetModal, saveRulesetModal, toggleRulesetSub, removeRulesetSub } from './rulesets.js';

/* --------------------------------------------------------------- 辅助工具 */

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

/* --------------------------------------------------------------- 加载入口与部署 */

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
    toast(`配置部署完成${warns}`);
    await loadOverview();
  });
}

/* --------------------------------------------------------------- 路由切换 */

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

/* --------------------------------------------------------------- 事件委托与绑定 */

function bindEvents() {
  // 单节点测速
  document.addEventListener('click', (e) => {
    const latencyAction = e.target.closest('[data-action="test-latency"]');
    if (latencyAction) {
      e.stopPropagation();
      testLatency(latencyAction.dataset.name);
      return;
    }
    if (e.target.closest('[data-stop-propagation]')) e.stopPropagation();
  }, true);

  // 全局 data-action 事件委托
  document.addEventListener('click', (e) => {
    const action = e.target.closest('[data-action]');
    if (!action) return;
    const { action: name, id, index, tag, group, direction } = action.dataset;

    if (name === 'switch-exit') {
      const g = action.dataset.group;
      const n = action.dataset.name;
      if (g && n) {
        switchNode(g, n).then(() => loadOverviewGroups());
      }
    }
    else if (name === 'reset-connections') resetConnectionRecords();
    else if (name === 'connection-page') setConnectionPage(action.dataset.page);
    else if (name === 'close-connection') closeSingleConn(id);
    else if (name === 'client-page') setClientPage(action.dataset.page);
    else if (name === 'edit-client') openClientModal(Number(index));
    else if (name === 'remove-client') removeClient(id);
    else if (name === 'remove-group') removeGroup(Number(index));
    else if (name === 'remove-policy') removePolicy(Number(index), e);
    else if (name === 'move-policy') movePolicy(Number(index), Number(direction));
    else if (name === 'toggle-policy-expand') togglePolicyExpand(Number(index));
    else if (name === 'refresh-ruleset') refreshSingleRulesetSub(tag);
    else if (name === 'remove-ruleset') removeRulesetSub(id);
  });

  // 全局 change 事件委托
  document.addEventListener('change', (e) => {
    const action = e.target.closest('[data-action]');
    if (!action) return;
    if (action.dataset.action === 'client-mode') updateClientMode(action.dataset.id, action.value);
    else if (action.dataset.action === 'toggle-ruleset') toggleRulesetSub(action.dataset.id, action.checked);
  });

  // 节点分页切换
  $('nodeList')?.addEventListener('click', (e) => {
    const pageButton = e.target.closest('[data-node-page]');
    if (!pageButton || pageButton.disabled) return;
    state.nodePage = Number(pageButton.dataset.nodePage) || 1;
    renderNodeList();
  });

  // 认证按键
  $('gateSubmit')?.addEventListener('click', submitGate);
  $('gatePassword')?.addEventListener('keydown', (e) => { if (e.key === 'Enter') submitGate(); });
  $('gateConfirm')?.addEventListener('keydown', (e) => { if (e.key === 'Enter') submitGate(); });

  // 侧边栏导航切换
  document.querySelectorAll('aside.sidebar nav.sb-nav button, .sb-foot button[data-tab]').forEach((btn) => {
    btn.addEventListener('click', () => switchTab(btn.dataset.tab));
  });

  // 卡片快捷跳转
  document.addEventListener('click', (e) => {
    const navEl = e.target.closest('[data-nav-tab]');
    if (navEl) {
      const tab = navEl.dataset.navTab;
      if (tab) switchTab(tab);
    }
  });

  // 全局常驻部署按钮
  $('btnGlobalDeploy')?.addEventListener('click', (e) => doDeploy(e.currentTarget));
  $('btnDeployOverview')?.addEventListener('click', (e) => doDeploy(e.currentTarget));
  $('btnDeployFromNodes')?.addEventListener('click', (e) => doDeploy(e.currentTarget));
  $('btnDeploySubs')?.addEventListener('click', (e) => doDeploy(e.currentTarget));
  $('btnDeployClients')?.addEventListener('click', (e) => doDeploy(e.currentTarget));
  $('btnDeploy')?.addEventListener('click', (e) => doDeploy(e.currentTarget));

  // 流量重置
  $('btnTrafficReset')?.addEventListener('click', async () => {
    await api('/traffic/reset', { method: 'POST' });
    toast('流量统计已清零');
    loadTraffic();
  });
  startTrafficPoll();

  // 概览页内核操作
  $('btnRestart')?.addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
    await api('/kernel/restart', { method: 'POST' });
    toast('内核已重启');
    await loadOverview();
  }));
  $('btnStop')?.addEventListener('click', (e) => {
    if (!confirm('确定停止 sing-box 内核吗？代理流量会立即中断，直到重新启动内核。')) return;
    return withBusy(e.currentTarget, async () => {
      await api('/kernel/stop', { method: 'POST' });
      toast('内核已停止');
      await loadOverview();
    });
  });

  // 节点页
  $('btnReloadNodes')?.addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
    await loadNodes();
    toast('已刷新节点');
  }));
  $('btnTestAll')?.addEventListener('click', (e) => withBusy(e.currentTarget, testAll));
  $('btnCollapseAll')?.addEventListener('click', () => {
    const anyExpanded = state.nodeGroups.some((g) => state.collapsed[g.name] === false);
    for (const g of state.nodeGroups) state.collapsed[g.name] = anyExpanded;
    renderNodeGroups();
    toast(anyExpanded ? '已全部折叠' : '已全部展开');
  });

  $('nodeGroups')?.addEventListener('click', async (e) => {
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
    toast('已添加新分组，请填写名称和关键词后点击「保存并部署」');
  });

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

  // 内网设备
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
  $('btnAddClient')?.addEventListener('click', () => openClientModal(-1));
  $('btnCancelClientModal')?.addEventListener('click', closeClientModal);
  $('btnSaveClientModal')?.addEventListener('click', saveClientModal);
  $('clientModal')?.addEventListener('click', (e) => {
    if (e.target.id === 'clientModal') closeClientModal();
  });

  // 当前连接页
  $('btnConnsRefresh')?.addEventListener('click', (e) => withBusy(e.currentTarget, resetConnectionRecords));
  $('btnCloseAllConns')?.addEventListener('click', closeAllConnections);
  const renderConnectionsFromFirstPage = () => setConnectionPage(1);
  $('connSearch')?.addEventListener('input', renderConnectionsFromFirstPage);
  $('connSearch')?.addEventListener('search', renderConnectionsFromFirstPage);
  $('connProtocolFilter')?.addEventListener('change', (e) => setConnectionProtocolFilter(e.target.value));
  $('connSortMode')?.addEventListener('change', (e) => setConnectionSortMode(e.target.value));
  $('btnClearConnRecords')?.addEventListener('click', clearConnectionRecords);

  // 订阅管理页
  $('btnAddSub')?.addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
    const url = $('subUrl').value.trim();
    if (!url) throw new Error('请填写订阅地址');
    const result = await api('/subscriptions', { method: 'POST', body: { name: $('subName').value.trim(), url } });
    $('subUrl').value = '';
    $('subName').value = '';
    toast(`已解析 ${result.nodeCount ?? 0} 个节点`);
    await Promise.all([loadSubscriptions(), loadOverview()]);
  }));

  $('btnRefreshSubs')?.addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
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

  $('subList')?.addEventListener('change', async (e) => {
    const id = e.target.dataset.subToggle;
    if (!id) return;
    const enabled = e.target.checked;
    try {
      await api(`/subscriptions/${encodeURIComponent(id)}`, { method: 'PUT', body: { enabled } });
      toast(enabled ? '已启用，记得部署配置' : '已停用，记得部署配置');
      await loadSubscriptions();
    } catch (err) {
      e.target.checked = !enabled;
      toast(err.message);
    }
  });

  $('subList')?.addEventListener('click', async (e) => {
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
      toast('已删除订阅');
      await Promise.all([loadSubscriptions(), loadOverview()]);
    }
  });

  // 目标分流策略页
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

  $('btnAddPolicy')?.addEventListener('click', () => {
    const id = 'custom-' + Date.now();
    // 插入到末尾兜底规则之前
    const insertIdx = Math.max(0, state.policies.length - 1);
    state.policies.splice(insertIdx, 0, { id, name: '新分流策略', target: 'builtin-direct', rulesets: [], domainSuffix: [], enabled: true });
    renderPolicies();
  });

  $('btnCollapseAllPolicies')?.addEventListener('click', () => {
    if (!state.policyCollapsed) state.policyCollapsed = {};
    const anyExpanded = state.policies.some((_, i) => !state.policyCollapsed[i]);
    state.policies.forEach((_, i) => { state.policyCollapsed[i] = anyExpanded; });
    renderPolicies();
    toast(anyExpanded ? '已全部折叠分流策略' : '已全部展开分流策略');
  });

  $('btnResetPolicies')?.addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
    if (!confirm('恢复默认策略将覆盖当前自定义策略，确定？')) return;
    await api('/policies/reset', { method: 'POST' });
    toast('已恢复默认策略');
    await loadPolicies();
  }));

  $('btnRefreshRulesets')?.addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
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

  $('policyList')?.addEventListener('click', (e) => {
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

  $('policyList')?.addEventListener('change', async (e) => {
    const t = e.target;
    if (t.dataset.pTarget !== undefined) {
      updatePolicyTarget(Number(t.dataset.pTarget), t.value);
      return;
    }
    if (t.dataset.pEnabled === undefined) return;
    const i = Number(t.dataset.pEnabled);
    const policy = state.policies[i];
    policy.enabled = t.checked;
    const status = document.querySelector(`[data-p-status="${i}"]`);
    try {
      await api(`/policies/${encodeURIComponent(policy.id)}/toggle`, { method: 'POST', body: { enabled: t.checked } });
      if (status) {
        status.textContent = t.checked ? '已启用' : '已关闭';
        status.className = `tag ${t.checked ? 'good' : 'muted'}`;
      }
      toast(t.checked ? `「${policy.name}」已启用（0断流热切换）` : `「${policy.name}」已关闭（0断流热切换）`);
    } catch (err) {
      t.checked = !t.checked;
      policy.enabled = !t.checked;
      toast(err.message);
    }
  });

  // 自定义规则集弹窗
  $('btnAddRulesetSub')?.addEventListener('click', openRulesetModal);
  $('btnCancelRulesetModal')?.addEventListener('click', closeRulesetModal);
  $('btnSaveRulesetModal')?.addEventListener('click', saveRulesetModal);
  $('rulesetModal')?.addEventListener('click', (e) => {
    if (e.target.id === 'rulesetModal') closeRulesetModal();
  });

  bindSettingsEvents({ withBusy, loadOverview });
}

/* --------------------------------------------------------------- 启动初始化 */

configureNodes({ extractFlag, extractProto });
configureOverview({ groupLabel: GROUP_LABEL, extractFlag });
bindEvents();
checkAuth();
