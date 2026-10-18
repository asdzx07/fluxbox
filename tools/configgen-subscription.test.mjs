import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test, { after } from 'node:test';

const toolsDir = path.dirname(fileURLToPath(import.meta.url));
const projectRoot = path.resolve(toolsDir, '..');
const testArea = fs.mkdtempSync(path.join(projectRoot, 'runtime-tests-'));
process.env.MYBOX_ROOT = path.join(testArea, 'runtime');

// 临时目录清理失败（Windows 文件占用、删除钩子/沙箱拦截）不该算测试失败。
after(() => { try { fs.rmSync(testArea, { recursive: true, force: true }); } catch {} });

const { defaultSettings } = await import('../server/lib/settings.mjs');
const { generateConfig } = await import('../server/lib/configgen.mjs');
const { parseShareLink, parseSubscription } = await import('../server/lib/subscription.mjs');

test('config generation keeps node cleaning and dynamic group matching output stable', () => {
  const settings = defaultSettings();
  settings.subscriptions = [
    { id: 'sub-enabled', enabled: true },
    { id: 'sub-disabled', enabled: false },
  ];
  settings.nodes = [
    {
      type: 'vless',
      tag: 'Alpha-NY',
      server: 'alpha.example.net',
      server_port: 443,
      uuid: 'alpha-uuid',
      __subscriptionId: 'sub-enabled',
      __privateNote: 'drop-me',
      emptyList: [],
      emptyObject: {},
      nested: { keep: true, __hidden: 'drop-me', emptyList: [], emptyObject: {} },
    },
    {
      type: 'trojan',
      tag: 'Beta-EU',
      server: 'beta.example.net',
      server_port: 443,
      password: 'beta-password',
      __subscriptionId: 'sub-enabled',
    },
    {
      type: 'shadowsocks',
      tag: 'Disabled-Node',
      server: 'disabled.example.net',
      server_port: 8388,
      method: 'aes-128-gcm',
      password: 'unused',
      __subscriptionId: 'sub-disabled',
    },
  ];
  settings.groups.find((group) => group.id === 'all-manual').keywords = ['ny', 'EU', 'missing'];

  const { config } = generateConfig(settings);
  const alpha = config.outbounds.find((outbound) => outbound.tag === 'Alpha-NY');
  assert.deepEqual(alpha, {
    type: 'vless',
    tag: 'Alpha-NY',
    server: 'alpha.example.net',
    server_port: 443,
    uuid: 'alpha-uuid',
    nested: { keep: true },
  });
  assert.equal(config.outbounds.some((outbound) => outbound.tag === 'Disabled-Node'), false);
  assert.deepEqual(
    config.outbounds.find((outbound) => outbound.tag === '所有-自动').outbounds,
    ['Alpha-NY', 'Beta-EU'],
  );
  assert.deepEqual(
    config.outbounds.find((outbound) => outbound.tag === '所有-手动').outbounds,
    ['Alpha-NY', 'Beta-EU'],
  );
  assert.equal(JSON.stringify(config).includes('__subscriptionId'), false);
  assert.equal(JSON.stringify(config).includes('__privateNote'), false);
});

test('VLESS and VMess parsing preserves transport and TLS output', () => {
  const vlessLink = 'vless://11111111-2222-3333-4444-555555555555@vless.example.net:443?type=ws&path=%2Fws&host=cdn.example.net&security=tls&sni=front.example.net&alpn=h2%2Chttp%2F1.1&fp=chrome#vless-ws';
  const vmessPayload = Buffer.from(JSON.stringify({
    ps: 'vmess-grpc',
    add: 'vmess.example.net',
    port: '8443',
    id: 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee',
    scy: 'auto',
    aid: '0',
    net: 'grpc',
    path: 'agent.Service',
    tls: 'tls',
    sni: 'front.vmess.example.net',
  })).toString('base64');
  const vmessLink = `vmess://${vmessPayload}`;

  const expectedVless = {
    type: 'vless',
    tag: 'vless-ws',
    server: 'vless.example.net',
    server_port: 443,
    uuid: '11111111-2222-3333-4444-555555555555',
    transport: { type: 'ws', path: '/ws', headers: { Host: 'cdn.example.net' } },
    tls: {
      enabled: true,
      server_name: 'front.example.net',
      alpn: ['h2', 'http/1.1'],
      utls: { enabled: true, fingerprint: 'chrome' },
    },
  };
  const expectedVmess = {
    type: 'vmess',
    tag: 'vmess-grpc',
    server: 'vmess.example.net',
    server_port: 8443,
    uuid: 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee',
    security: 'auto',
    alter_id: 0,
    transport: { type: 'grpc', service_name: 'agent.Service' },
    tls: { enabled: true, server_name: 'front.vmess.example.net' },
  };

  assert.deepEqual(parseShareLink(vlessLink), expectedVless);
  assert.deepEqual(parseShareLink(vmessLink), expectedVmess);
  assert.deepEqual(parseSubscription(`${vlessLink}\n${vmessLink}`), {
    format: 'links',
    nodes: [expectedVless, expectedVmess],
  });
});

test('config generation supports IPv6 dual-stack and prevents FakeIP route exclusion collision', () => {
  const settings = defaultSettings();
  settings.network.ipv6 = true;
  const { config } = generateConfig(settings);

  const tun = config.inbounds.find((i) => i.tag === 'tun-in');
  assert.ok(tun.address.includes('fdfe:dcba:9876::1/126'));
  assert.ok(tun.dns_address.includes('fdfe:dcba:9876::2'));
  assert.ok(tun.route_exclude_address.includes('fe80::/10'));
  assert.ok(tun.route_exclude_address.includes('ff00::/8'));
  // 核心断言：绝对不能包含 fc00::/7，否则与 FakeIP 的 fc00::/18 冲突导致断网
  assert.equal(tun.route_exclude_address.includes('fc00::/7'), false);

  const direct = config.outbounds.find((o) => o.tag === '直连');
  assert.equal(direct.inet4_bind_address, '0.0.0.0');
  assert.equal(direct.inet6_bind_address, '::');

  const dnsIn = config.inbounds.find((i) => i.tag === 'dns-in');
  assert.equal(dnsIn.listen, '::');

  assert.equal(config.dns.strategy, 'prefer_ipv4');
});

test('config generation applies aggressive throughput MTU 65535 and GSO', () => {
  const settings = defaultSettings();
  settings.network.tun.aggressive = true;
  const { config } = generateConfig(settings);
  const tun = config.inbounds.find((i) => i.tag === 'tun-in');
  assert.equal(tun.mtu, 65535);
  assert.equal(tun.gso, true);

  const safeSettings = defaultSettings();
  safeSettings.network.tun.aggressive = false;
  const { config: safeConfig } = generateConfig(safeSettings);
  const safeTun = safeConfig.inbounds.find((i) => i.tag === 'tun-in');
  assert.equal(safeTun.mtu, 1500);
  assert.equal(safeTun.gso, undefined);
});
