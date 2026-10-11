import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import {
  formatDateTime,
  loadRulesetMeta,
  saveRulesetMeta,
  collectRemoteRulesets,
  refreshRulesets,
} from '../server/lib/rulesets.mjs';

test('ruleset date formatting handles valid timestamps and empty values', () => {
  assert.equal(formatDateTime(null), '未更新');
  assert.equal(formatDateTime(undefined), '未更新');
  assert.equal(formatDateTime('invalid'), '未更新');

  const ts = new Date('2026-10-11T12:30:45Z').getTime();
  const formatted = formatDateTime(ts);
  assert.match(formatted, /^2026-10-11/);
  assert.match(formatted, /\d{2}:\d{2}:\d{2}$/);
});

test('ruleset metadata loads and persists correctly', () => {
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'ruleset-meta-test-'));
  try {
    const initial = loadRulesetMeta(tmpDir);
    assert.equal(initial.updatedAt, null);
    assert.equal(initial.formattedDate, '未更新');

    const sample = {
      updatedAt: 1728648000000,
      hasUpdate: true,
      items: {
        'geosite-google': {
          tag: 'geosite-google',
          url: 'https://example.com/test.srs',
          updatedAt: 1728648000000,
          formattedDate: formatDateTime(1728648000000),
          hasUpdate: true,
        },
      },
    };
    saveRulesetMeta(sample, tmpDir);

    const reloaded = loadRulesetMeta(tmpDir);
    assert.equal(reloaded.updatedAt, 1728648000000);
    assert.equal(reloaded.hasUpdate, true);
    assert.equal(reloaded.items['geosite-google'].tag, 'geosite-google');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
});

test('collectRemoteRulesets aggregates policy, custom and adblock rulesets', () => {
  const settings = {
    policies: [
      { id: 'p1', enabled: true, rulesets: ['geosite-google', 'geosite-youtube'] },
      { id: 'p2', enabled: false, rulesets: ['geosite-ai'] },
    ],
    rulesetSubs: [
      { id: 'sub1', enabled: true, tag: 'custom-rules', url: 'https://example.com/custom.srs', format: 'binary' },
      { id: 'sub2', enabled: false, tag: 'disabled-rules', url: 'https://example.com/off.srs' },
    ],
    dns: {
      adblock: true,
    },
  };

  const collected = collectRemoteRulesets(settings);
  const tags = collected.map((item) => item.tag);

  assert.ok(tags.includes('geosite-google'));
  assert.ok(tags.includes('geosite-youtube'));
  assert.ok(tags.includes('custom-rules'));
  assert.ok(tags.includes('adblock'));
  assert.ok(!tags.includes('geosite-ai'));
  assert.ok(!tags.includes('disabled-rules'));
});
