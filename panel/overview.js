import { $, api, escapeHtml, toast } from './core.js';

const overviewState = { overviewSubs: [], overviewSelectedSubId: 'all', overviewGroupsData: null };
let groupLabel = {};
let extractFlagFn = () => '🌐';

export function configureOverview({ groupLabel: labels, extractFlag: flagExtractor }) {
  groupLabel = labels || {};
  extractFlagFn = flagExtractor || extractFlagFn;
}

/* --------------------------------------------------------------- 概览与实时流量 */

export function fmtBytes(b) {
  if (b < 1024) return b + ' B';
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB';
  if (b < 1024 * 1024 * 1024) return (b / 1024 / 1024).toFixed(1) + ' MB';
  return (b / 1024 / 1024 / 1024).toFixed(2) + ' GB';
}
function fmtSpeed(bps) {
  return fmtBytes(bps) + '/s';
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

    $('statUp').textContent = fmtSpeed(up);
    $('statDown').textContent = fmtSpeed(down);
    $('statTotalUp').textContent = fmtBytes(totalUp);
    $('statTotalDown').textContent = fmtBytes(totalDown);

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
  const w = cv.clientWidth, h = 120;
  cv.width = w * dpr; cv.height = h * dpr;
  const ctx = cv.getContext('2d');
  ctx.scale(dpr, dpr);
  ctx.clearRect(0, 0, w, h);

  // 动态根据历史最大值缩放，最低 10KB/s 防止零流量浮动，有数据时自适应跳动
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
  try {
    const data = await api('/overview');
    const running = Boolean(data.kernel?.running);
    $('heroDot').className = 'dot ' + (running ? 'on' : 'off');

    if (!running) {
      $('statKernel').textContent = '未运行';
    } else {
      const timeStr = data.kernel?.uptime ? ` 运行 ${data.kernel.uptime}` : '';
      $('statKernel').textContent = `运行中 (内核${timeStr})`;
    }

    let note = '';
    if (data.kernel?.reloadedAt) {
      note = `上次热重载：${new Date(data.kernel.reloadedAt).toLocaleTimeString()}`;
    }
    const noteEl = $('overviewNote');
    if (noteEl) {
      noteEl.textContent = note;
      if (note) {
        noteEl.classList.remove('hidden');
      } else {
        noteEl.classList.add('hidden');
      }
    }

    if ($('statVersion')) $('statVersion').textContent = data.kernel?.version || '—';
    const counts = data.counts || {};
    if ($('statNodes')) $('statNodes').textContent = `${counts.nodes || 0} 个`;
    if ($('statPolicies')) $('statPolicies').textContent = `${counts.policiesEnabled || 0} / ${counts.policies || 0}`;
    if ($('quickNodes')) $('quickNodes').textContent = String(counts.nodes || 0);
    if ($('quickPolicies')) $('quickPolicies').textContent = `${counts.policiesEnabled || 0} / ${counts.policies || 0}`;
    if ($('quickGroups')) $('quickGroups').textContent = String(counts.groups || 0);
    if ($('quickSubscriptions')) $('quickSubscriptions').textContent = String(counts.subscriptions || 0);

    const hint = $('overviewHint');
    if (hint) {
      if (data.kernel?.error) {
        hint.textContent = data.kernel.error;
        hint.classList.remove('hidden');
      } else {
        hint.classList.add('hidden');
      }
    }
  } catch (err) {
    if ($('statKernel')) $('statKernel').textContent = '读取失败';
    const hint = $('overviewHint');
    if (hint) {
      hint.textContent = err.message;
      hint.classList.remove('hidden');
    }
  }

  await Promise.all([loadOverviewSubs(), loadOverviewGroups()]);
}

export async function loadOverviewSubs() {
  const box = $('overviewSubsList');
  if (!box) return;
  try {
    const data = await api('/subscriptions');
    overviewState.overviewSubs = data.subscriptions || [];
    if (overviewState.overviewSelectedSubId !== 'all' && !overviewState.overviewSubs.some((s) => s.id === overviewState.overviewSelectedSubId)) {
      overviewState.overviewSelectedSubId = 'all';
    }
    renderOverviewSubs();
  } catch (err) {
    box.innerHTML = `<div class="mini-empty err-text">${escapeHtml(err.message)}</div>`;
  }
}

function renderOverviewSubs() {
  const box = $('overviewSubsList');
  if (!box) return;
  const subs = overviewState.overviewSubs || [];
  const totalNodes = subs.reduce((acc, s) => acc + (s.nodeCount || 0), 0);
  const allActive = overviewState.overviewSelectedSubId === 'all';
  const allEnabled = subs.length > 0 && subs.every((s) => s.enabled);

  let html = `
    <div class="ov-sub-item${allActive ? ' active' : ''}" data-ov-sub-id="all">
      <div class="ov-sub-radio"><span class="ov-sub-radio-dot"></span></div>
      <div class="ov-sub-info">
        <div class="ov-sub-title">全部订阅 · 所有节点</div>
        <div class="ov-sub-meta">全部 ${subs.length} 个订阅 · 共 ${totalNodes} 个节点</div>
      </div>
      <div class="ov-sub-actions" data-stop-propagation>
        <label class="switch" title="${allEnabled ? '一键停用全部订阅' : '一键启用全部订阅'}">
          <input type="checkbox" data-ov-sub-toggle="all"${allEnabled ? ' checked' : ''}${subs.length === 0 ? ' disabled' : ''}>
          <span></span>
        </label>
      </div>
    </div>
  `;

  if (!subs.length) {
    html += '<p class="note" style="padding:10px 0;text-align:center">暂无订阅，点击上方「订阅管理」前往添加</p>';
  } else {
    html += subs.map((s) => {
      const active = overviewState.overviewSelectedSubId === s.id;
      return `
        <div class="ov-sub-item${active ? ' active' : ''}" data-ov-sub-id="${escapeHtml(s.id)}">
          <div class="ov-sub-radio"><span class="ov-sub-radio-dot"></span></div>
          <div class="ov-sub-info">
            <div class="ov-sub-title">${escapeHtml(s.name || s.id)}</div>
            <div class="ov-sub-meta">${s.nodeCount || 0} 个节点</div>
          </div>
          <div class="ov-sub-actions" data-stop-propagation>
            <label class="switch" title="${s.enabled ? '停用此订阅' : '启用此订阅'}">
              <input type="checkbox" data-ov-sub-toggle="${escapeHtml(s.id)}"${s.enabled ? ' checked' : ''}>
              <span></span>
            </label>
          </div>
        </div>
      `;
    }).join('');
  }

  box.innerHTML = html;
}

export async function loadOverviewGroups() {
  const box = $('overviewGroups');
  if (!box) return;
  try {
    const data = await api('/nodes/status');
    overviewState.overviewGroupsData = data.groups || [];
    const currentExit = overviewState.overviewGroupsData.find((group) => group.now);
    const exitEl = $('overviewCurrentExit');
    if (exitEl) exitEl.textContent = currentExit ? `${currentExit.now} · ${groupLabel[currentExit.type] || currentExit.type}` : '暂无可用出口';
    renderOverviewGroups();
  } catch (err) {
    box.innerHTML = `<div class="mini-empty">${escapeHtml(err.message)}</div>`;
  }
}

function renderOverviewGroups() {
  const box = $('overviewGroups');
  if (!box) return;
  const groups = overviewState.overviewGroupsData || [];
  if (!groups.length) {
    box.innerHTML = '<div class="mini-empty">内核未运行或无节点组</div>';
    return;
  }

  const selectedSubId = overviewState.overviewSelectedSubId;
  const currSub = selectedSubId !== 'all' ? (overviewState.overviewSubs || []).find((s) => s.id === selectedSubId) : null;
  const subPill = $('subFilterPill');

  if (currSub) {
    if (subPill) {
      subPill.textContent = `筛选：${currSub.name || currSub.id}`;
      subPill.classList.remove('hidden');
    }
  } else {
    if (subPill) {
      subPill.textContent = '全部节点';
      subPill.classList.add('hidden');
    }
  }

  const subNodeSet = currSub?.nodeTags ? new Set(currSub.nodeTags) : null;

  box.innerHTML = `<div class="mini">${groups.map((g) => {
    let members = g.members || [];
    if (subNodeSet) {
      members = members.filter((m) => subNodeSet.has(m) || m === g.now);
    }
    const hasFilteredOut = subNodeSet && members.length === 0;

    return `
      <div class="mini-row">
        <div class="grow">
          <div class="name" style="font-weight:600">${escapeHtml(g.name)}</div>
          <div class="sub">${groupLabel[g.type] || g.type} · ${members.length} 个可选${currSub ? ` (源于「${escapeHtml(currSub.name)}」)` : ''}</div>
        </div>
        <select data-ov-group="${escapeHtml(g.name)}"${hasFilteredOut ? ' disabled' : ''}>
          ${hasFilteredOut
            ? `<option value="">当前订阅无此组节点</option>`
            : members.map((m) => {
                const isCurrent = m === g.now;
                const notInSub = subNodeSet && !subNodeSet.has(m) && isCurrent;
                return `<option value="${escapeHtml(m)}"${isCurrent ? ' selected' : ''}>${extractFlagFn(m)} ${escapeHtml(m)}${notInSub ? ' (当前非该订阅)' : ''}</option>`;
              }).join('')
          }
        </select>
      </div>
    `;
  }).join('')}</div>`;
}


export function selectOverviewSubscription(id) {
  overviewState.overviewSelectedSubId = id;
  renderOverviewSubs();
  renderOverviewGroups();
}
