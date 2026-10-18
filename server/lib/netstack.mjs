import { execFile } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { promisify } from 'node:util';
import { DATA_DIR, KERNEL } from './paths.mjs';
import { createLogger } from './log.mjs';
import { readJson, writeJsonAtomic } from './fsx.mjs';

const execFileAsync = promisify(execFile);
const log = createLogger('netstack');

const STATE_FILE = path.join(DATA_DIR, 'netstack-state.json');
const MANAGED_CONF = 'mybox.conf';
const FASTPATH_TABLE = KERNEL.nftTable || 'fluxbox';
const BYPASS_MARK = '0x02000000/0x02000000';
const RULE_PRIO = '8998';

async function run(cmd, args, { allowFail = true } = {}) {
  try {
    const { stdout } = await execFileAsync(cmd, args, { timeout: 20000 });
    return { ok: true, out: stdout.trim() };
  } catch (err) {
    if (!allowFail) throw err;
    return { ok: false, out: (err.stdout || err.stderr || err.message || '').trim() };
  }
}

export function isOpenWrt() {
  return fs.existsSync('/etc/openwrt_release');
}

/* ------------------------------------------------------------ IP 转发 */

async function readSysctl(key) {
  const r = await run('sysctl', ['-n', key]);
  return r.ok ? r.out.trim() : null;
}

/** 作为旁路由/主路由给局域网分流，必须打开 IP 转发。 */
export async function enableForwarding(ipv6 = false) {
  const key = 'net.ipv4.ip_forward';
  const before = await readSysctl(key);
  if (before !== '1') {
    await run('sysctl', ['-w', `${key}=1`]);
    log.info('已打开 IPv4 转发（原值 %s）', before ?? '未知');
  }

  let v6Before = null;
  if (ipv6) {
    v6Before = await readSysctl('net.ipv6.conf.all.forwarding');
    if (v6Before !== '1') {
      await run('sysctl', ['-w', 'net.ipv6.conf.all.forwarding=1']);
      await run('sysctl', ['-w', 'net.ipv6.conf.default.forwarding=1']);
      log.info('已打开 IPv6 转发（原值 %s）', v6Before ?? '未知');
    }
  }

  return { key, before, v6Before };
}

export async function restoreForwarding(forwardingState) {
  const v4Before = typeof forwardingState === 'object' ? forwardingState?.before : forwardingState;
  if (v4Before === '0') await run('sysctl', ['-w', 'net.ipv4.ip_forward=0']);

  const v6Before = typeof forwardingState === 'object' ? forwardingState?.v6Before : null;
  if (v6Before === '0') {
    await run('sysctl', ['-w', 'net.ipv6.conf.all.forwarding=0']);
    await run('sysctl', ['-w', 'net.ipv6.conf.default.forwarding=0']);
  }
}

/* -------------------------------------------------------- dnsmasq 接管 */

/**
 * 找出 dnsmasq 读的 conf-dir。
 * OpenWrt 上优先用 uci 的 confdir，没有就用 init 脚本的默认 /tmp/dnsmasq.<段名>.d。
 */
async function dnsmasqConfDir() {
  if (isOpenWrt()) {
    const uci = await run('uci', ['-q', 'get', 'dhcp.@dnsmasq[0].confdir']);
    if (uci.ok && uci.out) {
      const first = uci.out.split(',')[0].trim();
      if (first.startsWith('/')) return first;
    }
    const show = await run('uci', ['-q', 'show', 'dhcp.@dnsmasq[0]']);
    const m = show.out.match(/^dhcp\.([^.=]+)=dnsmasq/m);
    return `/tmp/dnsmasq${m ? `.${m[1]}` : ''}.d`;
  }
  // Debian / Ubuntu：dnsmasq 的 conf-dir 常见于 /etc/dnsmasq.d
  return '/etc/dnsmasq.d';
}

async function dnsmasqRestart() {
  if (isOpenWrt()) return run('/etc/init.d/dnsmasq', ['restart']);
  if (fs.existsSync('/run/systemd/system')) return run('systemctl', ['restart', 'dnsmasq']);
  return run('service', ['dnsmasq', 'restart']);
}

/**
 * 检查是否已经有别处设了 noresolv / log-facility。
 *
 * 这是踩过的坑：dnsmasq 2.9x 遇到重复的关键字会直接拒绝启动（illegal repeated
 * keyword），DNS 和 DHCP 一起停。所以动 dnsmasq 前必须先看别人设过没有。
 */
async function globalOptionAlreadySet(option, confDir) {
  const files = [];
  if (isOpenWrt()) {
    files.push('/etc/dnsmasq.conf');
    for (const f of fs.existsSync('/var/etc') ? fs.readdirSync('/var/etc') : []) {
      if (f.startsWith('dnsmasq.conf')) files.push(path.join('/var/etc', f));
    }
  } else {
    files.push('/etc/dnsmasq.conf');
  }
  if (confDir && fs.existsSync(confDir)) {
    for (const f of fs.readdirSync(confDir)) {
      if (f === MANAGED_CONF) continue;
      files.push(path.join(confDir, f));
    }
  }

  const re = new RegExp(`^\\s*${option}\\s*[=]`, 'm');
  for (const file of files) {
    try {
      if (re.test(fs.readFileSync(file, 'utf8'))) return file;
    } catch {
      /* 读不了就跳过 */
    }
  }
  // OpenWrt 还可能在 uci 里设了
  if (isOpenWrt()) {
    const uciKey = option === 'noresolv' ? 'noresolv' : 'logfacility';
    const r = await run('uci', ['-q', 'get', `dhcp.@dnsmasq[0].${uciKey}`]);
    if (r.ok && r.out) return `uci dhcp.@dnsmasq[0].${uciKey}`;
  }
  return null;
}

/**
 * 找出真实的上游 DNS，用作接管失败时的兜底。
 *
 * 为什么不直接用备份值：如果之前已经装过别的代理插件，
 * dnsmasq 的 server 本来就指向那个插件的 DNS 端口。这种情况下「还原备份」
 * 等于还原成一个已经没人监听的地址，全 LAN 还是解析不了。
 */
async function discoverUpstreamDns() {
  for (const file of ['/tmp/resolv.conf.d/resolv.conf.auto', '/tmp/resolv.conf.auto', '/etc/resolv.conf']) {
    try {
      const lines = fs.readFileSync(file, 'utf8').split('\n');
      const found = lines
        .filter((l) => /^\s*nameserver\s+/.test(l))
        .map((l) => l.trim().split(/\s+/)[1])
        .filter((ip) => ip && !ip.startsWith('127.') && ip !== '::1');
      if (found.length) return found.slice(0, 2);
    } catch {
      /* 下一个 */
    }
  }
  return ['223.5.5.5', '119.29.29.29'];
}

/** 备份里指向内核自己 DNS 端口的条目要剔除，否则还原后全 LAN 无解析。 */
function sanitizeDnsBackup(servers, ownPort) {
  const kept = (servers ?? []).filter((s) => !s.includes(`#${ownPort}`) && !s.includes(`:${ownPort}`));
  return kept.length ? kept : null;
}

async function uciGet(option) {
  const r = await run('uci', ['-q', 'get', option]);
  if (!r.ok || !r.out) return null;
  // uci 列表可能用换行分隔，也可能被拼成一行空格分隔，两种都要拆开
  return r.out.split(/[\s\n]+/).map((s) => s.trim()).filter(Boolean);
}

async function uciCommit() {
  return run('uci', ['-q', 'commit', 'dhcp']);
}

/**
 * OpenWrt：直接改 uci 的 dnsmasq server 选项。
 *
 * 为什么不能只在 conf-dir 里塞一行 server=：uci 里原有的 server（比如别人装的
 * mosdns 占着 5335）不会消失，dnsmasq 会同时问两个上游，分流就漏了。
 * 必须把 server 列表整体接管过来，并且把原值备份下来以便还原。
 */
async function applyDnsmasqOpenWrt({ dnsPort, listen, directBypass = false }) {
  const section = 'dhcp.@dnsmasq[0]';
  const currentServers = await uciGet(`${section}.server`);

  // 剔除指向内核自己端口的条目：装了别的代理面板时原值可能就是那个端口，
  // 留着它会让「还原」变成一个死地址。
  const cleanServers = sanitizeDnsBackup(currentServers, dnsPort);
  const before = {
    server: cleanServers ?? await discoverUpstreamDns(),
    noresolv: await uciGet(`${section}.noresolv`),
    nftset: await uciGet(`${section}.nftset`),
    replacedSelfReferential: Boolean(currentServers && !cleanServers),
  };
  if (before.replacedSelfReferential) {
    log.warn('原 dnsmasq 上游指向 %s#%d（已失效的代理面板），还原时将改用真实上游 DNS', listen, dnsPort);
  }

  await run('uci', ['-q', 'delete', `${section}.server`]);
  await run('uci', ['-q', 'add_list', `${section}.server=${listen}#${dnsPort}`]);
  await run('uci', ['-q', 'set', `${section}.noresolv=1`]);

  if (directBypass) {
    await run('uci', ['-q', 'delete', `${section}.nftset`]);
    await run('uci', ['-q', 'add_list', `${section}.nftset=/cn/4#inet#${FASTPATH_TABLE}#direct_fastpath4,6#inet#${FASTPATH_TABLE}#direct_fastpath6`]);
  }

  await uciCommit();

  const restart = await dnsmasqRestart();
  if (!restart.ok) {
    // 起不来就回滚，否则整网 DNS 断掉
    await uciRollbackDnsmasq(before);
    throw new Error(`dnsmasq 重启失败，已回滚：${restart.out}`);
  }

  writeJsonAtomic(STATE_FILE, { ...readJson(STATE_FILE, {}), dnsmasq: { mode: 'uci', section, before } });
  log.info('dnsmasq 已接管（uci）→ %s#%d', listen, dnsPort);
  return { confDir: 'uci', noresolv: true, nftset: Boolean(directBypass) };
}

async function uciRollbackDnsmasq(before) {
  const section = 'dhcp.@dnsmasq[0]';
  // 备份为空（或原本就没设过）时用真实上游 DNS，不能留空——
  // 留空 + noresolv 被删掉的话还能靠 resolv.conf 兜住，但显式给更稳。
  const servers = before?.server?.length ? before.server : await discoverUpstreamDns();

  await run('uci', ['-q', 'delete', `${section}.server`]);
  for (const v of servers) {
    await run('uci', ['-q', 'add_list', `${section}.server=${v}`]);
  }
  if (before?.noresolv?.length) {
    await run('uci', ['-q', 'set', `${section}.noresolv=${before.noresolv[0]}`]);
  } else {
    await run('uci', ['-q', 'delete', `${section}.noresolv`]);
  }
  await run('uci', ['-q', 'delete', `${section}.nftset`]);
  if (before?.nftset?.length) {
    for (const item of before.nftset) {
      await run('uci', ['-q', 'add_list', `${section}.nftset=${item}`]);
    }
  }
  await uciCommit();
}

/**
 * 接管 dnsmasq：把它的上游指向内核的 DNS 入站。
 * 失败时抛错并回滚——调用方负责记录。
 */
export async function applyDnsmasq({ dnsPort = KERNEL.dnsPort, listen = '127.0.0.1', directBypass = false } = {}) {
  if (isOpenWrt()) return applyDnsmasqOpenWrt({ dnsPort, listen, directBypass });

  const confDir = await dnsmasqConfDir();
  fs.mkdirSync(confDir, { recursive: true });

  const conflict = await globalOptionAlreadySet('noresolv', confDir);
  const lines = [
    '# 由 FluxBox 生成，请勿手工修改。',
    '# 上游指向内核的 DNS 入站，由内核按分流规则决定用哪个上游 DNS。',
    `server=${listen}#${dnsPort}`,
  ];
  if (!conflict) lines.push('noresolv=1');
  else log.warn('noresolv 已被 %s 设置，跳过（避免 dnsmasq 重复关键字启动失败）', conflict);

  if (directBypass) {
    lines.push(`nftset=/cn/4#inet#${FASTPATH_TABLE}#direct_fastpath4,6#inet#${FASTPATH_TABLE}#direct_fastpath6`);
  }

  const target = path.join(confDir, MANAGED_CONF);
  const before = fs.existsSync(target) ? fs.readFileSync(target, 'utf8') : null;

  fs.writeFileSync(target, `${lines.join('\n')}\n`);

  const restart = await dnsmasqRestart();
  if (!restart.ok) {
    if (before === null) fs.rmSync(target, { force: true });
    else fs.writeFileSync(target, before);
    await dnsmasqRestart();
    throw new Error(`dnsmasq 重启失败，已回滚：${restart.out}`);
  }

  writeJsonAtomic(STATE_FILE, { ...readJson(STATE_FILE, {}), dnsmasq: { mode: 'confdir', confDir, file: target, before } });
  log.info('dnsmasq 已接管 → %s#%d', listen, dnsPort);
  return { confDir, file: target, noresolv: !conflict };
}

/** 还原 dnsmasq。内核起不来或卸载时必须调这个，否则整网无解析。 */
export async function restoreDnsmasq() {
  const state = readJson(STATE_FILE, {})?.dnsmasq;
  if (!state) return { restored: false };

  try {
    if (state.mode === 'uci') {
      await uciRollbackDnsmasq(state.before);
      await dnsmasqRestart();
    } else if (state.file) {
      if (state.before === null || state.before === undefined) fs.rmSync(state.file, { force: true });
      else fs.writeFileSync(state.file, state.before);
      await dnsmasqRestart();
    }
  } catch (err) {
    log.error('还原 dnsmasq 失败：%s', err.message);
  }

  const next = readJson(STATE_FILE, {});
  delete next.dnsmasq;
  writeJsonAtomic(STATE_FILE, next);
  log.info('dnsmasq 已还原');
  return { restored: true };
}

/* -------------------------------------------------------- TUN 网卡性能调优 */

export async function tuneTunDevice(settings = {}) {
  const tunName = KERNEL.tunName || 'mybox-tun';
  if (!fs.existsSync(`/sys/class/net/${tunName}`)) {
    return { ok: false, reason: `网卡 ${tunName} 尚未创建` };
  }

  const isAggressive = Boolean(settings.network?.tun?.aggressive);
  const userMtu = Number(settings.network?.tun?.mtu);
  const targetMtu = (Number.isFinite(userMtu) && userMtu > 0)
    ? userMtu
    : (isAggressive ? 65535 : 1500);

  // 1. 同步 TUN 网卡 MTU
  await run('ip', ['link', 'set', 'dev', tunName, 'mtu', String(targetMtu)]);

  // 2. 优化队列长度，防止高速并发时在网卡队列上溢出丢包
  await run('ip', ['link', 'set', 'dev', tunName, 'txqueuelen', '1000']);

  // 3. 激进模式：尝试开启硬件分段卸载特性（GSO/GRO/TSO）
  if (isAggressive || settings.network?.tun?.gso) {
    const ethtool = await run('ethtool', ['-K', tunName, 'rx', 'on', 'tx', 'on', 'tso', 'on', 'gso', 'on', 'gro', 'on']);
    log.info('TUN 网卡 %s 已优化为激进吞吐模式 (MTU=%d, GSO/GRO=on)%s', tunName, targetMtu, ethtool.ok ? '' : ` [ethtool: ${ethtool.out}]`);
  } else {
    log.info('TUN 网卡 %s 已应用标准安全模式 (MTU=%d)', tunName, targetMtu);
  }

  return { ok: true, dev: tunName, mtu: targetMtu, aggressive: isAggressive };
}

/* ---------------------------------------------------- 直连动态快速通道 (Fastpath) */

export async function setupDirectFastpath(settings = {}) {
  // 1. 确保 nftables 表与集合存在
  const nftInit = `
table inet ${FASTPATH_TABLE} {
  set direct_fastpath4 {
    type ipv4_addr
    flags timeout
  }
  set direct_fastpath6 {
    type ipv6_addr
    flags timeout
  }
  chain bypass_in {
    type filter hook prerouting priority mangle - 10; policy accept;
    ip daddr @direct_fastpath4 meta l4proto { tcp, udp } th dport != 53 mark |= 0x02000000
    ip daddr @direct_fastpath4 meta l4proto != { tcp, udp } mark |= 0x02000000
    ip6 daddr @direct_fastpath6 meta l4proto { tcp, udp } th dport != 53 mark |= 0x02000000
    ip6 daddr @direct_fastpath6 meta l4proto != { tcp, udp } mark |= 0x02000000
  }
}
`.trim();

  const tmpNft = path.join(DATA_DIR, 'tmp', 'fastpath-init.nft');
  try {
    fs.mkdirSync(path.dirname(tmpNft), { recursive: true });
    fs.writeFileSync(tmpNft, `${nftInit}\n`);
    const r = await run('nft', ['-f', tmpNft]);
    if (!r.ok) {
      log.warn('nftables 快速通道规则载入警告：%s', r.out);
    }
  } finally {
    fs.rmSync(tmpNft, { force: true });
  }

  // 2. 确保 ip rule 路由放行规则存在（priority 8998 在 TUN 表之前直接走 main 表直连，绕过内核）
  const rulesV4 = await run('ip', ['rule', 'show']);
  if (rulesV4.ok && !rulesV4.out.includes('0x2000000/0x2000000')) {
    await run('ip', ['rule', 'add', 'fwmark', BYPASS_MARK, 'lookup', 'main', 'priority', RULE_PRIO]);
    log.info('已配置 IPv4 动态直连快速通道 ip rule (priority %s -> main)', RULE_PRIO);
  }

  if (settings.network?.ipv6) {
    const rulesV6 = await run('ip', ['-6', 'rule', 'show']);
    if (rulesV6.ok && !rulesV6.out.includes('0x2000000/0x2000000')) {
      await run('ip', ['-6', 'rule', 'add', 'fwmark', BYPASS_MARK, 'lookup', 'main', 'priority', RULE_PRIO]);
      log.info('已配置 IPv6 动态直连快速通道 ip rule (priority %s -> main)', RULE_PRIO);
    }
  }

  writeJsonAtomic(STATE_FILE, {
    ...readJson(STATE_FILE, {}),
    fastpath: { table: FASTPATH_TABLE, mark: BYPASS_MARK, priority: RULE_PRIO },
  });

  return { ok: true, table: FASTPATH_TABLE, mark: '0x02000000', priority: RULE_PRIO };
}

export async function teardownDirectFastpath() {
  const rulesV4 = await run('ip', ['rule', 'show']);
  if (rulesV4.ok && rulesV4.out.includes('0x2000000/0x2000000')) {
    await run('ip', ['rule', 'del', 'fwmark', BYPASS_MARK, 'lookup', 'main', 'priority', RULE_PRIO]);
  }
  const rulesV6 = await run('ip', ['-6', 'rule', 'show']);
  if (rulesV6.ok && rulesV6.out.includes('0x2000000/0x2000000')) {
    await run('ip', ['-6', 'rule', 'del', 'fwmark', BYPASS_MARK, 'lookup', 'main', 'priority', RULE_PRIO]);
  }

  await run('nft', ['delete', 'table', 'inet', FASTPATH_TABLE]);

  const state = readJson(STATE_FILE, {});
  delete state.fastpath;
  writeJsonAtomic(STATE_FILE, state);
  log.info('动态直连快速通道已清理');
}

/** 动态向直连快速通道集合塞入 IP（带超时 TTL，如 '2h'） */
export async function addDirectFastpathIps(ips = [], { timeout = '2h' } = {}) {
  if (!fs.existsSync('/proc/sys/net/ipv4')) return { ok: false, count: 0, reason: '非 Linux 系统' };
  const v4 = [];
  const v6 = [];
  for (const raw of ips) {
    const ip = String(raw).trim();
    if (!ip) continue;
    if (ip.includes(':')) v6.push(ip);
    else if (/^\d+\.\d+\.\d+\.\d+$/.test(ip)) v4.push(ip);
  }

  let added = 0;
  if (v4.length) {
    const elements = v4.map((ip) => `${ip} timeout ${timeout}`).join(', ');
    const r = await run('nft', ['add', 'element', 'inet', FASTPATH_TABLE, 'direct_fastpath4', `{ ${elements} }`]);
    if (r.ok) added += v4.length;
  }
  if (v6.length) {
    const elements = v6.map((ip) => `${ip} timeout ${timeout}`).join(', ');
    const r = await run('nft', ['add', 'element', 'inet', FASTPATH_TABLE, 'direct_fastpath6', `{ ${elements} }`]);
    if (r.ok) added += v6.length;
  }

  return { ok: true, added, totalRequested: ips.length };
}

/** 查询当前直连快速通道的条目数与状态 */
export async function getDirectFastpathStatus() {
  if (!fs.existsSync('/proc/sys/net/ipv4')) {
    return { enabled: false, supported: false, count4: 0, count6: 0, total: 0 };
  }
  const r4 = await run('nft', ['list', 'set', 'inet', FASTPATH_TABLE, 'direct_fastpath4']);
  const r6 = await run('nft', ['list', 'set', 'inet', FASTPATH_TABLE, 'direct_fastpath6']);
  const countElements = (out = '') => {
    const m = out.match(/elements\s*=\s*\{([^}]*)\}/s);
    if (!m || !m[1]) return 0;
    return m[1].split(',').map((s) => s.trim()).filter(Boolean).length;
  };
  const count4 = r4.ok ? countElements(r4.out) : 0;
  const count6 = r6.ok ? countElements(r6.out) : 0;
  return {
    enabled: r4.ok,
    table: FASTPATH_TABLE,
    count4,
    count6,
    total: count4 + count6,
  };
}

/** 一键清空直连快速通道集合 */
export async function flushDirectFastpath() {
  await run('nft', ['flush', 'set', 'inet', FASTPATH_TABLE, 'direct_fastpath4']);
  await run('nft', ['flush', 'set', 'inet', FASTPATH_TABLE, 'direct_fastpath6']);
  return { ok: true };
}

/* -------------------------------------------------------------- 入口 */

export async function apply(settings) {
  const result = { forwarding: null, dnsmasq: null, tun: null, fastpath: null, warnings: [] };

  if (!fs.existsSync('/proc/sys/net/ipv4')) {
    result.warnings.push('非 Linux 环境：跳过系统网络配置（仅生成配置、启停内核）。');
    return result;
  }

  result.forwarding = await enableForwarding(Boolean(settings.network?.ipv6));
  if (result.forwarding) {
    writeJsonAtomic(STATE_FILE, { ...readJson(STATE_FILE, {}), forwarding: result.forwarding });
  }

  // 1. TUN 网卡性能调优（MTU / GSO 硬件卸载 / 发送队列）
  try {
    result.tun = await tuneTunDevice(settings);
  } catch (err) {
    result.warnings.push(`TUN 网卡调优提示：${err.message}`);
  }

  // 2. 直连快速通道（nftables 动态集合与 0x02000000 硬件级直连放行规则）
  if (settings.network?.directBypass) {
    try {
      result.fastpath = await setupDirectFastpath(settings);
    } catch (err) {
      result.warnings.push(`动态直连快速通道建立失败：${err.message}`);
    }
  } else {
    try {
      await teardownDirectFastpath();
    } catch {}
  }

  // 3. dnsmasq 接管与直连域名 nftset 联动
  if (settings.dns.mode === 'dnsmasq') {
    try {
      result.dnsmasq = await applyDnsmasq({
        dnsPort: settings.dns.hijackPort || KERNEL.dnsPort,
        directBypass: Boolean(settings.network?.directBypass),
      });
    } catch (err) {
      result.warnings.push(`dnsmasq 接管失败：${err.message}`);
    }
  } else {
    await restoreDnsmasq();
  }

  return result;
}

export async function cleanup() {
  const state = readJson(STATE_FILE, {});
  await restoreDnsmasq();
  await teardownDirectFastpath();
  if (state.forwarding) await restoreForwarding(state.forwarding);
  fs.rmSync(STATE_FILE, { force: true });
}

export async function dnsmasqStatus(targetPort = null) {
  const state = readJson(STATE_FILE, {})?.dnsmasq;
  if (!state) return { takenOver: false };
  const portToCheck = targetPort || KERNEL.dnsPort;
  if (state.mode === 'uci') {
    const servers = await uciGet('dhcp.@dnsmasq[0].server');
    return {
      takenOver: Boolean(servers?.some((s) => s.includes(`#${portToCheck}`))),
      confDir: 'uci',
      servers: servers ?? [],
    };
  }
  return { takenOver: Boolean(state.file && fs.existsSync(state.file)), confDir: state.confDir, file: state.file };
}

