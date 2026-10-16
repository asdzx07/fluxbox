/* Settings and kernel-log data loading for the settings page. */
import { $, api, state, toast } from './core.js';
import { loadRulesetStatus } from './rulesets.js';

export async function loadSettings() {
  try {
    const s = await api('/settings');
    state.settings = s;
    if ($('setIpv6')) $('setIpv6').checked = Boolean(s.network?.ipv6);
    if ($('setRejectQuic')) $('setRejectQuic').checked = Boolean(s.network?.rejectQuic);
    if ($('setAutoRedirect')) $('setAutoRedirect').checked = s.network?.tun?.autoRedirect !== false;
    if ($('setDirectBypass')) $('setDirectBypass').checked = s.network?.directBypass !== false;
    if ($('setDirectForNodes')) $('setDirectForNodes').checked = s.network?.directForNodes !== false;
    if ($('setDnsMode')) $('setDnsMode').value = s.dns?.mode || 'dnsmasq';
    if ($('setDnsDirect')) $('setDnsDirect').value = s.dns?.direct === 'wan' ? '' : (s.dns?.directAddress || '');
    if ($('setDnsProxy')) $('setDnsProxy').value = s.dns?.proxy || '';
    if ($('setDnsStrategy')) $('setDnsStrategy').value = s.dns?.strategy || 'prefer_ipv4';
    if ($('setDnsProxyProtocol')) $('setDnsProxyProtocol').value = s.dns?.proxyProtocol || 'tcp';
    if ($('setDnsSplit')) $('setDnsSplit').checked = s.dns?.split !== false;
    if ($('setFakeIp')) $('setFakeIp').checked = Boolean(s.dns?.fakeIp);
    if ($('setAdblock')) $('setAdblock').checked = Boolean(s.dns?.adblock);
    if ($('setAdblockAllow')) $('setAdblockAllow').value = (s.dns?.adblockAllow || []).join('\n');
    if ($('setAdblockCustom')) $('setAdblockCustom').value = (s.dns?.adblockCustom || []).join('\n');
    if ($('setLogLevel')) $('setLogLevel').value = s.kernel?.logLevel || 'warn';

    const kVer = s.kernel?.installed
      ? (s.kernel.version ? (s.kernel.version.startsWith('v') ? s.kernel.version : `v${s.kernel.version}`) : 'v1.15.0-alpha.12')
      : '未安装';
    if ($('setKernelVersion')) $('setKernelVersion').textContent = kVer;
  } catch {}

  try {
    const v = await api('/system/version');
    const cur = v.current || 'MyBox: 版本未知';
    const shortVer = v.shortVersion || cur.replace(/^MyBox:\s*/, '');
    if ($('sysVersion')) $('sysVersion').textContent = cur;
    if ($('settingCurrentVerTag')) $('settingCurrentVerTag').textContent = shortVer;
    if ($('brandVersion')) $('brandVersion').textContent = shortVer;
    if ($('latestSysVersion')) $('latestSysVersion').textContent = v.latest || cur;
    if ($('panelCheckTime')) $('panelCheckTime').textContent = '已读取本机版本';
    if ($('panelCommitSha')) $('panelCommitSha').textContent = v.commitSha ? `运行构建：${v.commitSha}` : '运行构建：未知';

    const pBadge = $('panelUpdateBadge');
    const applyBtn = $('btnSysUpdate');
    if (v.checkFailed) {
      if (pBadge) {
        pBadge.textContent = '检查失败';
        pBadge.style.color = 'var(--err)';
      }
      if (applyBtn) applyBtn.classList.add('hidden');
    } else if (v.hasUpdate) {
      if (pBadge) {
        pBadge.textContent = `发现新版本 ${v.latest}`;
        pBadge.style.color = 'var(--warn)';
      }
      if (applyBtn) {
        applyBtn.classList.remove('hidden');
        if ($('sysUpdateBtnText')) $('sysUpdateBtnText').textContent = `升级至 ${v.latest}`;
      }
    } else {
      if (pBadge) {
        pBadge.textContent = '当前已是最新';
        pBadge.style.color = '';
      }
      if (applyBtn) applyBtn.classList.add('hidden');
    }
  } catch {
    if ($('sysVersion')) $('sysVersion').textContent = 'MyBox: 版本不可用';
    if ($('settingCurrentVerTag')) $('settingCurrentVerTag').textContent = '版本不可用';
    if ($('brandVersion')) $('brandVersion').textContent = '版本不可用';
    if ($('latestSysVersion')) $('latestSysVersion').textContent = '—';
    if ($('panelCheckTime')) $('panelCheckTime').textContent = '无法读取本机版本';
    if ($('panelCommitSha')) $('panelCommitSha').textContent = '运行构建：未知';
    if ($('panelUpdateBadge')) {
      $('panelUpdateBadge').textContent = '无法确认版本';
      $('panelUpdateBadge').classList.add('version-unavailable');
    }
  }

  try {
    const kLatest = await api('/kernel/latest');
    if (kLatest.current && $('setKernelVersion')) $('setKernelVersion').textContent = kLatest.current;
    const latestKVer = kLatest.latest || kLatest.version || 'v1.15.0-alpha.12';
    if ($('latestKernelVer')) $('latestKernelVer').textContent = latestKVer;

    const kBadge = $('kernelStatusBadge');
    if (kBadge) {
      if (kLatest.hasUpdate) {
        kBadge.textContent = `发现新内核 ${latestKVer}`;
        kBadge.style.color = 'var(--warn)';
      } else {
        kBadge.textContent = '当前已是最新';
        kBadge.style.color = '';
      }
    }
  } catch {}
}

export async function loadKernelLog() {
  try {
    const data = await api('/kernel/log');
    $('kernelLog').textContent = data.log || '暂无日志';
  } catch (err) {
    $('kernelLog').textContent = `读取日志失败：${err.message}`;
  }
}

let logAutoTimer = null;
export function scheduleLogAuto() {
  clearInterval(logAutoTimer);
  if ($('logAuto')?.checked) logAutoTimer = setInterval(loadKernelLog, 3000);
}

export function filterKernelLog() {
  const kw = $('logSearch').value.toLowerCase();
  const text = $('kernelLog').textContent;
  if (!kw) return;
  const lines = text.split('\n').filter((line) => line.toLowerCase().includes(kw));
  $('kernelLog').textContent = lines.join('\n') || '无匹配内容';
}

export function bindSettingsEvents({ withBusy, loadOverview }) {
  // 设置页右上角核心操作 1: 保存并部署 (带完整成功变绿动效)
  $('btnSaveSettings')?.addEventListener('click', async (e) => {
    const btn = e.currentTarget;
    const txt = $('saveSettingsBtnText');
    const prevText = txt ? txt.textContent : '保存并部署';
    btn.disabled = true;
    if (txt) txt.innerHTML = '<span class="animate-spin">↻</span> 正在部署...';
    try {
      const patch = {
        network: {
          ipv6: $('setIpv6').checked,
          rejectQuic: $('setRejectQuic').checked,
          directBypass: $('setDirectBypass').checked,
          directForNodes: $('setDirectForNodes').checked,
          tun: { autoRedirect: $('setAutoRedirect').checked },
        },
        dns: {
          mode: $('setDnsMode').value,
          direct: $('setDnsDirect').value.trim() ? 'custom' : 'wan',
          directAddress: $('setDnsDirect').value.trim(),
          proxy: $('setDnsProxy').value.trim() || '1.1.1.1',
          strategy: $('setDnsStrategy') ? $('setDnsStrategy').value : 'prefer_ipv4',
          proxyProtocol: $('setDnsProxyProtocol') ? $('setDnsProxyProtocol').value : 'tcp',
          split: $('setDnsSplit') ? $('setDnsSplit').checked : true,
          fakeIp: $('setFakeIp').checked,
          adblock: $('setAdblock').checked,
          adblockAllow: $('setAdblockAllow').value.split('\n').map((s) => s.trim()).filter(Boolean),
          adblockCustom: $('setAdblockCustom').value.split('\n').map((s) => s.trim()).filter(Boolean),
        },
        kernel: { logLevel: $('setLogLevel') ? $('setLogLevel').value : 'warn' },
      };
      await api('/settings', { method: 'PUT', body: patch });
      await api('/deploy', { method: 'POST', body: { restart: true } });

      // 成功变绿并显示对勾动效
      btn.classList.add('btn-success');
      if (txt) txt.textContent = '✓ 保存部署成功';
      toast('✓ 配置已保存，0断流热重载生效！');

      setTimeout(() => {
        btn.classList.remove('btn-success');
        if (txt) txt.textContent = '保存并部署';
        btn.disabled = false;
      }, 2500);

      await loadOverview();
    } catch (err) {
      btn.disabled = false;
      if (txt) txt.textContent = prevText;
      toast(`保存失败：${err.message}`);
    }
  });

  // 设置页右上角核心操作 2: 检查更新
  $('btnCheckSysUpdate')?.addEventListener('click', async (e) => {
    const btn = e.currentTarget;
    const txt = $('checkUpdateBtnText');
    btn.disabled = true;
    if (txt) txt.innerHTML = '<span class="animate-spin">↻</span> 检查中...';
    try {
      const v = await api('/system/version');
      const cur = v.current || 'MyBox: 版本未知';
      const shortVer = v.shortVersion || cur.replace(/^MyBox:\s*/, '');
      if ($('sysVersion')) $('sysVersion').textContent = cur;
      if ($('settingCurrentVerTag')) $('settingCurrentVerTag').textContent = shortVer;
      if ($('brandVersion')) $('brandVersion').textContent = shortVer;
      if ($('latestSysVersion')) $('latestSysVersion').textContent = v.latest || cur;
      if ($('panelCheckTime')) $('panelCheckTime').textContent = '已读取本机版本';
      if ($('panelCommitSha')) $('panelCommitSha').textContent = v.commitSha ? `运行构建：${v.commitSha}` : '运行构建：未知';
      $('panelUpdateBadge')?.classList.remove('version-unavailable');

      const pBadge = $('panelUpdateBadge');
      const applyBtn = $('btnSysUpdate');
      if (v.checkFailed) {
        if (pBadge) {
          pBadge.textContent = '检查失败';
          pBadge.style.color = 'var(--err)';
        }
        if (applyBtn) applyBtn.classList.add('hidden');
        if (txt) txt.textContent = '检查更新';
        toast(`检查更新失败：${v.checkError || '网络连接超时'}`);
        return;
      }
      if (v.hasUpdate) {
        if (pBadge) {
          pBadge.textContent = `发现新版本 ${v.latest}`;
          pBadge.style.color = 'var(--warn)';
        }
        if (applyBtn) {
          applyBtn.classList.remove('hidden');
          if ($('sysUpdateBtnText')) $('sysUpdateBtnText').textContent = `升级至 ${v.latest}`;
        }
        if (txt) txt.textContent = '有新版本';
        toast(`发现新版本 ${v.latest} 可更新！`);
      } else {
        if (pBadge) {
          pBadge.textContent = '当前已是最新';
          pBadge.style.color = '';
        }
        if (applyBtn) applyBtn.classList.add('hidden');
        if (txt) txt.textContent = '已是最新';
        toast('当前版本已是最新');
      }
    } catch (err) {
      toast(`检查失败：${err.message}`);
      if (txt) txt.textContent = '检查更新';
    } finally {
      setTimeout(() => {
        btn.disabled = false;
        if (txt) txt.textContent = '检查更新';
      }, 2200);
    }
  });

  // 更新广告规则集
  $('btnAdblockRefresh')?.addEventListener('click', (e) => withBusy(e.currentTarget, async () => {
    const txt = $('btnAdblockRefreshText');
    const prev = txt ? txt.textContent : '';
    if (txt) txt.textContent = '检查并更新...';
    try {
      const r = await api('/adblock/refresh', { method: 'POST' });
      if (!r.ok) {
        toast('更新失败：' + (r.error || '未知错误'));
        return;
      }
      if (r.hasUpdate) {
        toast(`🎉 广告规则集发现新版本并已更新（${r.formattedDate || '刚刚'}）`);
      } else {
        toast(`广告规则集已是最新版本，无可用更新（上次更新: ${r.formattedDate || '刚刚'}）`);
      }
      await loadRulesetStatus();
    } catch (err) {
      toast(`更新失败：${err.message}`);
    } finally {
      if (txt) txt.textContent = prev;
    }
  }));

  // 修改面板管理密码
  $('btnUpdatePassword')?.addEventListener('click', async (e) => {
    const btn = e.currentTarget;
    const curr = $('setCurrPassword')?.value || '';
    const next = $('setNewPassword')?.value || '';
    const confirm = $('setConfirmPassword')?.value || '';

    if (!curr) {
      toast('请输入当前管理密码');
      $('setCurrPassword')?.focus();
      return;
    }
    if (!next || next.length < 6) {
      toast('新密码长度不能少于 6 位');
      $('setNewPassword')?.focus();
      return;
    }
    if (next !== confirm) {
      toast('两次输入的新密码不一致，请重新确认');
      $('setConfirmPassword')?.focus();
      return;
    }

    btn.disabled = true;
    const oldHtml = btn.innerHTML;
    btn.innerHTML = '<span class="animate-spin">↻</span> 正在修改...';

    try {
      await api('/auth/change-password', {
        method: 'POST',
        body: { current: curr, next },
      });
      toast('✓ 管理密码修改成功！历史会话已全部注销');
      if ($('setCurrPassword')) $('setCurrPassword').value = '';
      if ($('setNewPassword')) $('setNewPassword').value = '';
      if ($('setConfirmPassword')) $('setConfirmPassword').value = '';
    } catch (err) {
      toast(`密码修改失败：${err.message}`);
    } finally {
      btn.disabled = false;
      btn.innerHTML = oldHtml;
    }
  });

  // 立即升级面板
  $('btnSysUpdate')?.addEventListener('click', async (e) => {
    const btn = e.currentTarget;
    const txt = $('sysUpdateBtnText');
    if (!confirm('确认拉取线上最新版本并重启控制面板？')) return;
    btn.disabled = true;
    if (txt) txt.innerHTML = '<span class="animate-spin">↻</span> 正在更新...';
    try {
      await api('/system/update', { method: 'POST' });
      toast('更新已在后台开始，面板服务即将重启...');
      if (txt) txt.textContent = '✓ 更新指令已下发';
      setTimeout(() => location.reload(), 3000);
    } catch (err) {
      btn.disabled = false;
      if (txt) txt.textContent = '立即更新';
      toast(`更新失败：${err.message}`);
    }
  });

  // 设置页右上角核心操作 3: 内核更新 (带完整成功变绿动效)
  $('btnInstallKernel')?.addEventListener('click', async (e) => {
    const btn = e.currentTarget;
    const txt = $('kernelBtnText');
    btn.disabled = true;
    if (txt) txt.innerHTML = '<span class="animate-spin">↻</span> 查询最新...';
    try {
      const latest = await api('/kernel/latest');
      const targetVersion = latest.latest || latest.version;
      if ($('latestKernelVer')) $('latestKernelVer').textContent = targetVersion;
      if (latest.current && $('setKernelVersion')) $('setKernelVersion').textContent = latest.current;

      const cur = (latest.current || state.settings?.kernel?.version || '').replace(/^v/, '');
      const tar = (targetVersion || '').replace(/^v/, '');
      if (cur && tar && cur === tar) {
        toast(`当前内核已是最新 (${targetVersion})`);
        if (txt) txt.textContent = '内核已是最新';
        setTimeout(() => {
          btn.disabled = false;
          if (txt) txt.textContent = '内核更新';
        }, 2000);
        return;
      }
      if (!confirm(`下载并安装官方 sing-box ${targetVersion}？（安装后自动重启内核）`)) {
        btn.disabled = false;
        if (txt) txt.textContent = '内核更新';
        return;
      }
      if (txt) txt.innerHTML = '<span class="animate-spin">↻</span> 正在下载内核...';
      const info = await api('/kernel/install', { method: 'POST', body: { version: targetVersion } });
      btn.classList.add('btn-success');
      if (txt) txt.textContent = '✓ 内核更新成功';
      toast(`✓ 官方 sing-box ${info.version} 安装完成${info.restarted ? '，内核已热重启！' : ''}`);
      if ($('setKernelVersion')) $('setKernelVersion').textContent = info.version;
      setTimeout(() => {
        btn.classList.remove('btn-success');
        if (txt) txt.textContent = '内核更新';
        btn.disabled = false;
      }, 3000);
      await Promise.all([loadSettings(), loadOverview()]);
    } catch (err) {
      btn.disabled = false;
      if (txt) txt.textContent = '内核更新';
      toast(`内核更新失败：${err.message}`);
    }
  });

  // 日志相关
  $('btnLog')?.addEventListener('click', loadKernelLog);
  $('logAuto')?.addEventListener('change', scheduleLogAuto);
  $('logSearch')?.addEventListener('input', filterKernelLog);
}
