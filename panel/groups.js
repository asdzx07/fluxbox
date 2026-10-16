/* MyBox 节点分组规则配置模块 */
import { $, api, escapeHtml, toast, state } from './core.js';

export async function loadGroups() {
  const box = $('groupList');
  try {
    const data = await api('/groups');
    state.groups = data.groups || [];
    renderGroups();
  } catch (err) {
    if (box) box.innerHTML = `<p class="err-text">${escapeHtml(err.message)}</p>`;
  }
}

export function renderGroups() {
  const box = $('groupList');
  if (!box) return;
  if (!state.groups.length) {
    box.innerHTML = '<p class="note">还没有分组规则配置，点击上方「＋ 新建分组」添加。</p>';
    return;
  }
  box.innerHTML = state.groups.map((g, i) => {
    const isBuiltin = g.id === 'all-auto' || g.id === 'all-manual';
    const typeKey = String(g.type || '').toLowerCase();
    const typeText = typeKey === 'urltest' ? '自动择优' : '手动选择';
    return `
    <div class="card group-config-card" style="background:var(--surface-2);margin-bottom:10px">
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

export function removeGroup(index) {
  const g = state.groups[index];
  if (!g) return;
  if (!confirm(`确定删除节点分组「${g.name || '未命名'}」吗？`)) return;
  state.groups.splice(index, 1);
  renderGroups();
  toast('已删除分组，记得保存并部署生效');
}

export async function commitGroupsFromDOM() {
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
}
