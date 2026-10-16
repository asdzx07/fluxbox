/* Ruleset subscription and update status controls. */
import { $, api, escapeHtml, toast } from './core.js';

export async function loadRulesetStatus() {
  try {
    const data = await api('/rulesets/status');
    const dateStr = data.formattedDate || '未更新';
    const policyDate = $('rulesetDateText');
    if (policyDate) policyDate.textContent = dateStr;
    const adblockDate = $('adblockRulesetDateText');
    if (adblockDate) adblockDate.textContent = dateStr;
  } catch {}
}

export async function refreshSingleRulesetSub(tag) {
  try {
    toast(`正在检查规则集「${tag}」更新...`);
    const result = await api('/rulesets/refresh', { method: 'POST', body: { tag } });
    if (!result.ok) {
      toast(`更新失败：${result.error || '未知错误'}`);
      return;
    }
    if (result.hasUpdate) {
      toast(`规则集「${tag}」发现新版本并已更新（${result.formattedDate || '刚刚'}）`);
    } else {
      toast(`规则集「${tag}」已是最新版本，无可用更新（上次更新: ${result.formattedDate || '刚刚'}）`);
    }
    await loadRulesetStatus();
    await loadRulesetSubs();
  } catch (err) {
    toast(`更新规则集失败：${err.message}`);
  }
}

export async function loadRulesetSubs() {
  const box = $('rulesetSubList');
  try {
    const data = await api('/ruleset-subs');
    const list = data.items || data.subscriptions || [];
    if (!box) return;
    if (!list.length) {
      box.innerHTML = '<p class="note" style="padding:10px 0">暂无自定义规则集订阅。点击右上角「添加」可引入远程规则集。</p>';
      return;
    }
    box.innerHTML = list.map((ruleset) => `
      <div class="item">
        <label class="switch">
          <input type="checkbox" data-action="toggle-ruleset" data-id="${escapeHtml(ruleset.id)}" ${ruleset.enabled ? 'checked' : ''}>
          <span></span>
        </label>
        <div class="grow">
          <div class="title" style="font-weight:600">
            ${escapeHtml(ruleset.tag)}
            <span class="tag muted" style="font-size:11px;margin-left:6px">${ruleset.format === 'source' ? 'Source' : 'Binary'}</span>
          </div>
          <div class="sub" style="font-size:12px;color:var(--text-2);word-break:break-all">${escapeHtml(ruleset.url)}</div>
          <div class="sub" style="font-size:11px;color:var(--text-muted);margin-top:2px">
            <span>更新时间：${escapeHtml(ruleset.formattedDate || '未更新')}</span>
          </div>
        </div>
        <div class="inline">
          <button class="small" data-action="refresh-ruleset" data-tag="${escapeHtml(ruleset.tag)}">刷新</button>
          <button class="small danger" data-action="remove-ruleset" data-id="${escapeHtml(ruleset.id)}">删除</button>
        </div>
      </div>
    `).join('');
  } catch (err) {
    if (box) box.innerHTML = `<p class="err-text">加载规则集失败：${escapeHtml(err.message)}</p>`;
  }
}

export function openRulesetModal() {
  const modal = $('rulesetModal');
  if (!modal) return;
  $('modalRsTag').value = '';
  $('modalRsUrl').value = '';
  $('modalRsFormat').value = 'binary';
  modal.classList.remove('hidden');
}

export function closeRulesetModal() {
  const modal = $('rulesetModal');
  if (modal) modal.classList.add('hidden');
}

export async function saveRulesetModal() {
  const tag = $('modalRsTag')?.value.trim();
  const url = $('modalRsUrl')?.value.trim();
  const format = $('modalRsFormat')?.value || 'binary';
  if (!tag) return toast('请输入规则集标识 (Tag)');
  if (!url) return toast('请输入规则集订阅 URL');
  try {
    await api('/ruleset-subs', { method: 'POST', body: { tag, url, format } });
    closeRulesetModal();
    await loadRulesetSubs();
    toast(`已添加规则集「${tag}」，改动已保存`);
  } catch (err) {
    toast(`添加失败：${err.message}`);
  }
}

export async function toggleRulesetSub(id, enabled) {
  try {
    await api(`/ruleset-subs/${encodeURIComponent(id)}`, { method: 'PUT', body: { enabled } });
    toast(enabled ? '已启用该规则集订阅' : '已停用该规则集订阅');
  } catch (err) {
    toast(`更新状态失败：${err.message}`);
    loadRulesetSubs();
  }
}

export async function removeRulesetSub(id) {
  if (!confirm('确认删除该规则集订阅？')) return;
  try {
    await api(`/ruleset-subs/${encodeURIComponent(id)}`, { method: 'DELETE' });
    toast('已删除规则集订阅');
    await loadRulesetSubs();
  } catch (err) {
    toast(`删除失败：${err.message}`);
  }
}

window.refreshSingleRulesetSub = refreshSingleRulesetSub;
window.openRulesetModal = openRulesetModal;
window.closeRulesetModal = closeRulesetModal;
window.toggleRulesetSub = toggleRulesetSub;
window.removeRulesetSub = removeRulesetSub;
