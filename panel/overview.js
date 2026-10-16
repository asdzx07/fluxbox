import { $, api, escapeHtml, toast, state } from './core.js';

const overviewState = {
  overviewSubs: [],
  overviewSelectedSubId: 'all',
  overviewGroupsData: [],
  onlineClients: [],
};
let groupLabel = {};
let extractFlagFn = () => '🌐';

export function configureOverview({ groupLabel: labels, extractFlag: flagExtractor }) {
  groupLabel = labels || {};
  extractFlagFn = flagExtractor || extractFlagFn;
}

/* --------------------------------------------------------------- 概览与实时流量 */

export function fmtBytes(b) {
  if (!b || isNaN(b) || b <= 0) return '0 B';
  if (b < 1024) return Math.round(b) + ' B';
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB';
  if (b < 1024 * 1024 * 1024) return (b / 1024 / 1024).toFixed(1) + ' MB';
  return (b / 1024 / 1024 / 1024).toFixed(2) + ' GB';
}

function fmtSpeed(bps) {
  return fmtBytes(bps) + '/s';
}

function formatUptime(seconds) {
  if (!seconds || isNaN(seconds) || seconds <= 0) return '刚启动';
  const s = Math.floor(seconds);
  const days = Math.floor(s / 86400);
  const hours = Math.floor((s % 86400) / 3600);
  const mins = Math.floor((s % 3600) / 60);
  if (days > 0) return `${days} 天 ${String(hours).padStart(2, '0')}:${String(mins).padStart(2, '0')}`;
  if (hours > 0) return `${hours} 小时 ${String(mins).padStart(2, '0')} 分`;
  return `${mins} 分钟`;
}

function formatTimeOnly(ts) {
  if (!ts) return '—';
  try {
    const d = new Date(ts);
    return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  } catch {
    return '—';
  }
}

let trafficTimer = null;
const trafficHist = { up: [], down: [] };
const HIST_LEN = 45;

export async function loadTraffic() {
  try {
    const t = await api('/traffic');
    if (!t) return;
    const up = Number(t.up) || 0;
    const down = Number(t.down) || 0;
    const totalUp = Number(t.totalUp) || 0;
    const totalDown = Number(t.totalDown) || 0;
    const connsCount = Number(t.connectionsCount) || 0;

    if ($('statUp')) $('statUp').textContent = fmtSpeed(up);
    if ($('statDown')) $('statDown').textContent = fmtSpeed(down);
    if ($('statTotalUp')) $('statTotalUp').textContent = fmtBytes(totalUp);
    if ($('statTotalDown')) $('statTotalDown').textContent = fmtBytes(totalDown);
    if ($('ovKpiConns')) $('ovKpiConns').textContent = `${connsCount} 条`;

    // 写入历史
    trafficHist.up.push(up);
    trafficHist.down.push(down);
    if (trafficHist.up.length > HIST_LEN) trafficHist.up.shift();
    if (trafficHist.down.length > HIST_LEN) trafficHist.down.shift();
    drawTrafficChart();
  } catch {}
}

function drawTrafficChart() {
  const cv = $('trafficChart');
  if (!cv) return;
  const dpr = window.devicePixelRatio || 1;
  const w = cv.clientWidth, h = 130;
  cv.width = w * dpr; cv.height = h * dpr;
  const ctx = cv.getContext('2d');
  ctx.scale(dpr, dpr);
  ctx.clearRect(0, 0, w, h);

  // 动态根据历史最大值缩放，最低 10KB/s 防止零流量浮动
  const max = Math.max(1024 * 10, ...trafficHist.up, ...trafficHist.down);

  // 绘制水平辅助虚线
  ctx.strokeStyle = 'rgba(148, 163, 184, 0.12)';
  ctx.lineWidth = 1;
  for (let i = 1; i <= 3; i++) {
    const y = (h / 4) * i;
    ctx.beginPath();
    ctx.moveTo(0, y);
    ctx.lineTo(w, y);
    ctx.stroke();
  }

  const drawSmooth = (data, strokeColor, fillColor) => {
    if (!data.length) return;
    ctx.beginPath();
    const step = w / (HIST_LEN - 1);
    data.forEach((v, i) => {
      const x = i * step;
      const y = h - 6 - (v / max) * (h - 22);
      if (i === 0) ctx.moveTo(x, y);
      else {
        const prevX = (i - 1) * step;
        const prevY = h - 6 - (data[i - 1] / max) * (h - 22);
        const cpX = (prevX + x) / 2;
        ctx.bezierCurveTo(cpX, prevY, cpX, y, x, y);
      }
    });

    ctx.strokeStyle = strokeColor;
    ctx.lineWidth = 2.2;
    ctx.stroke();

    if (fillColor) {
      ctx.lineTo(w, h);
      ctx.lineTo(0, h);
      ctx.closePath();
      const grad = ctx.createLinearGradient(0, 0, 0, h);
      grad.addColorStop(0, fillColor);
      grad.addColorStop(1, 'transparent');
      ctx.fillStyle = grad;
      ctx.fill();
    }
  };

  // 下行 (绿色)
  drawSmooth(trafficHist.down, '#22c55e', 'rgba(34, 197, 94, 0.16)');
  // 上行 (蓝色)
  drawSmooth(trafficHist.up, '#3b82f6', 'rgba(59, 130, 246, 0.16)');
}

export function startTrafficPoll() {
  if (trafficTimer) return;
  loadTraffic();
  trafficTimer = setInterval(() => {
    if ($('page-overview').classList.contains('active')) loadTraffic();
  }, 1500);
}

export async function loadOverview() {
  let overviewData = null;
  try {
    overviewData = await api('/overview');
    const running = Boolean(overviewData.kernel?.running);

    // 1. 顶部 Header 运行状态指示徽章
    const topBadge = $('overviewTopBadge');
    if (topBadge) {
      topBadge.innerHTML = `<span class="badge-dot ${running ? 'on' : 'off'}"></span><span>${running ? '核心运行正常' : '核心未运行'}</span>`;
    }

    // 2. Hero 卡片主状态
    if ($('heroDot')) $('heroDot').className = 'dot ' + (running ? 'on' : 'off');
    if ($('heroStatusTitle')) $('heroStatusTitle').textContent = running ? '网络核心运行中' : '网络核心未运行';
    if ($('heroStatusBadge')) {
      $('heroStatusBadge').textContent = running ? '稳定' : '已停止';
      $('heroStatusBadge').className = `hero-badge ${running ? 'good' : 'warn'}`;
    }

    // 运行时间与接管说明
    const uptimeStr = overviewData.kernel?.uptime ? formatUptime(overviewData.kernel.uptime) : '刚启动';
    if ($('heroStatusDesc')) {
      $('heroStatusDesc').textContent = running
        ? `sing-box 已接管流量，DNS 分流正常 · 运行时间 ${uptimeStr}`
        : 'sing-box 尚未启动，局域网流量未处于代理接管状态';
    }

    // 状态标签药丸：mybox-tun, DNS 接管, nftables
    const dnsTaken = Boolean(overviewData.dnsmasq?.takenOver);
    if ($('pillTun')) {
      $('pillTun').className = `hero-pill ${running ? 'good' : 'muted'}`;
      $('pillTun').innerHTML = `<span class="pill-dot"></span>mybox-tun`;
    }
    if ($('pillDns')) {
      $('pillDns').className = `hero-pill ${dnsTaken ? 'good' : 'warn'}`;
      $('pillDns').innerHTML = `<span class="pill-dot"></span>DNS 接管`;
    }
    if ($('pillNft')) {
      $('pillNft').className = `hero-pill ${running ? 'good' : 'muted'}`;
      $('pillNft').innerHTML = `<span class="pill-dot"></span>nftables`;
    }

    // 3. 服务状态卡片详情
    renderServiceStatus(overviewData);

    // 4. 节点与策略健康指标
    const counts = overviewData.counts || {};
    if ($('quickNodes')) $('quickNodes').textContent = String(counts.nodes || 0);
    if ($('quickPolicies')) $('quickPolicies').textContent = `${counts.policiesEnabled || 0} / ${counts.policies || 0}`;
    if ($('quickGroups')) $('quickGroups').textContent = String(counts.groups || 0);
    if ($('quickSubscriptions')) $('quickSubscriptions').textContent = String(counts.subscriptions || 0);

  } catch (err) {
    if ($('heroStatusTitle')) $('heroStatusTitle').textContent = '状态读取失败';
    if ($('heroStatusDesc')) $('heroStatusDesc').textContent = err.message;
  }

  // 并行加载出口节点和局域网设备概览
  await Promise.all([
    loadOverviewGroups(),
    loadOverviewClients(),
  ]);
}

function renderServiceStatus(data) {
  if (!data) return;
  const running = Boolean(data.kernel?.running);
  const k = data.kernel || {};
  const ver = k.version || 'v1.15.0';
  const pid = k.pid ? `PID ${k.pid}` : '未分配';
  const dnsState = data.dnsmasq?.takenOver ? '正常' : '未接管';
  const deployTime = data.meta?.lastDeployAt ? formatTimeOnly(data.meta.lastDeployAt) : '最近已部署';

  if ($('srvKernelMeta')) $('srvKernelMeta').textContent = `${ver} · ${pid}`;
  if ($('srvKernelBadge')) {
    $('srvKernelBadge').textContent = running ? '运行中' : '已停止';
    $('srvKernelBadge').className = `status-pill ${running ? 'good' : 'bad'}`;
  }

  if ($('srvTunMeta')) $('srvTunMeta').textContent = running ? 'mybox-tun · auto-route' : 'tun 接口未就绪';
  if ($('srvTunBadge')) {
    $('srvTunBadge').textContent = running ? '正常' : '异常';
    $('srvTunBadge').className = `status-pill ${running ? 'good' : 'bad'}`;
  }

  if ($('srvDnsMeta')) $('srvDnsMeta').textContent = data.dnsmasq?.takenOver ? 'dnsmasq 接管 · 分流开启' : '未检测到接管规则';
  if ($('srvDnsBadge')) {
    $('srvDnsBadge').textContent = data.dnsmasq?.takenOver ? '正常' : '未接管';
    $('srvDnsBadge').className = `status-pill ${data.dnsmasq?.takenOver ? 'good' : 'warn'}`;
  }

  if ($('srvNftMeta')) $('srvNftMeta').textContent = `nftables · 最近部署 ${deployTime}`;
  if ($('srvNftBadge')) {
    $('srvNftBadge').textContent = running ? '正常' : '未就绪';
    $('srvNftBadge').className = `status-pill ${running ? 'good' : 'muted'}`;
  }
}

export async function loadOverviewGroups() {
  const box = $('overviewExitList');
  try {
    const data = await api('/nodes/status');
    overviewState.overviewGroupsData = data.groups || [];
    const nodes = data.nodes || [];

    // 计算测通的健康在线节点数
    const measuredCount = nodes.filter((n) => {
      const lat = state.latency[n.name]?.delay;
      return Number.isFinite(lat) && lat > 0;
    }).length;

    if ($('ovKpiNodes')) {
      $('ovKpiNodes').innerHTML = nodes.length > 0
        ? `${measuredCount > 0 ? measuredCount : '—'} <span class="unit">/ ${nodes.length}</span>`
        : '0 <span class="unit">/ 0</span>';
    }

    const currentExitGroup = overviewState.overviewGroupsData.find((group) => group.now) || overviewState.overviewGroupsData[0];
    const currentExitNodeName = currentExitGroup?.now || '';

    // 更新 Hero 卡片右侧当前出口
    if ($('overviewHeroExitName')) {
      $('overviewHeroExitName').textContent = currentExitNodeName ? `${extractFlagFn(currentExitNodeName)} ${currentExitNodeName}` : '暂无可用出口';
    }
    if ($('overviewHeroExitMeta')) {
      const lat = state.latency[currentExitNodeName]?.delay;
      const latStr = Number.isFinite(lat) ? `延迟 ${lat} ms` : '未测速';
      const typeStr = currentExitGroup ? (groupLabel[currentExitGroup.type] || currentExitGroup.type) : '直连';
      $('overviewHeroExitMeta').textContent = `${typeStr} · ${latStr}`;
    }

    renderOverviewExitNodes();
  } catch (err) {
    if (box) box.innerHTML = `<div class="mini-empty err-text">${escapeHtml(err.message)}</div>`;
  }
}

function renderOverviewExitNodes() {
  const box = $('overviewExitList');
  if (!box) return;
  const groups = overviewState.overviewGroupsData || [];
  if (!groups.length) {
    box.innerHTML = '<div class="mini-empty">内核未运行或无可用出口节点</div>';
    return;
  }

  // 优先选取首个 Selector 手动选择组或当前生效组
  const group = groups.find((g) => g.type === 'Selector' || g.type === 'selector') || groups[0];
  const members = group.members || [];
  const currentNow = group.now || '';

  if (!members.length) {
    box.innerHTML = '<div class="mini-empty">当前节点组没有可用节点，请在订阅页添加节点</div>';
    return;
  }

  // 展示前 5 个节点供快速切换
  const displayNodes = members.slice(0, 6);

  box.innerHTML = displayNodes.map((m) => {
    const isCurrent = (m === currentNow);
    const flag = extractFlagFn(m);
    const lat = state.latency[m]?.delay;
    const latBadge = Number.isFinite(lat) ? `<span class="exit-lat good">${lat} ms</span>` : '<span class="exit-lat na">—</span>';

    return `
      <div class="overview-exit-item ${isCurrent ? 'is-active' : ''}" data-action="switch-exit" data-group="${escapeHtml(group.name)}" data-name="${escapeHtml(m)}">
        <div class="exit-radio-circle"><span class="exit-radio-inner"></span></div>
        <div class="exit-node-info">
          <div class="exit-node-name">${flag} ${escapeHtml(m)}</div>
          <div class="exit-node-type">${groupLabel[group.type] || group.type}</div>
        </div>
        <div class="exit-node-actions">
          ${latBadge}
          ${isCurrent ? '<span class="exit-current-tag">当前出口</span>' : '<button class="small ghost exit-switch-btn">选择</button>'}
        </div>
      </div>
    `;
  }).join('');
}

export async function loadOverviewClients() {
  const box = $('overviewClientsList');
  if (!box) return;
  try {
    const res = await api('/clients');
    const clients = res.clients || [];
    overviewState.onlineClients = clients.filter((c) => c.online);

    if ($('ovClientsCount')) {
      $('ovClientsCount').textContent = `${overviewState.onlineClients.length} 台在线 · 按设备设置分流`;
    }

    if (!overviewState.onlineClients.length) {
      box.innerHTML = '<div class="mini-empty">当前暂无在线局域网设备</div>';
      return;
    }

    // 展示前 3~4 台在线设备
    const preview = overviewState.onlineClients.slice(0, 4);
    box.innerHTML = preview.map((c) => {
      const modeText = c.mode === 'proxy' ? '全局代理' : c.mode === 'direct' ? '完全直连' : '规则分流';
      const modeTagCls = c.mode === 'proxy' ? 'tag-proxy' : c.mode === 'direct' ? 'tag-direct' : 'tag-rule';
      return `
        <div class="overview-client-row">
          <div class="client-avatar"><span class="client-avatar-dot"></span></div>
          <div class="client-detail">
            <div class="client-name">${escapeHtml(c.name || '局域网设备')}</div>
            <div class="client-ip">${escapeHtml(c.ip || '—')}</div>
          </div>
          <span class="client-mode-badge ${modeTagCls}">${modeText}</span>
        </div>
      `;
    }).join('');
  } catch (err) {
    box.innerHTML = `<div class="mini-empty">${escapeHtml(err.message)}</div>`;
  }
}
