import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test, { after } from 'node:test';

const toolsDir = path.dirname(fileURLToPath(import.meta.url));
const projectRoot = path.resolve(toolsDir, '..');
const testArea = fs.mkdtempSync(path.join(projectRoot, 'runtime-tests-'));

// 临时目录清理失败（Windows 文件占用、删除钩子/沙箱拦截）不该算测试失败。
after(() => { try { fs.rmSync(testArea, { recursive: true, force: true }); } catch {} });

const reporter = path.join(toolsDir, 'ci-report-failures.mjs');
const runReporter = (file) => spawnSync(process.execPath, [reporter, file], { encoding: 'utf8' });

test('CI failure reporter turns TAP failures into readable annotations', () => {
  const failing = path.join(testArea, 'failing.tap');
  fs.writeFileSync(failing, [
    'TAP version 13',
    '# Subtest: guarded case',
    'not ok 1 - guarded case',
    '  ---',
    '  duration_ms: 1.2',
    "  type: 'test'",
    '  error: |-',
    '    Expected values to be strictly equal:',
    '    + actual - expected',
    '    + 1',
    '    - 2',
    "  code: 'ERR_ASSERTION'",
    '  stack: |-',
    '    at someInternalFrame (node:internal/test_runner:1:1)',
    '  ...',
    '1..1',
    '# tests 1',
    '# fail 1',
    '',
  ].join('\n'));

  const result = runReporter(failing);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /^::error title=回归测试失败::guarded case — Expected values to be strictly equal:/m);
  assert.doesNotMatch(result.stdout, /someInternalFrame/);
});

test('CI failure reporter stays quiet for a clean run and a missing log', () => {
  const passing = path.join(testArea, 'passing.tap');
  fs.writeFileSync(passing, 'TAP version 13\nok 1 - fine\n1..1\n# tests 1\n# pass 1\n');

  const clean = runReporter(passing);
  assert.equal(clean.status, 0, clean.stderr);
  assert.match(clean.stdout, /::notice::没有从 TAP 输出里解析到失败用例/);

  const missing = runReporter(path.join(testArea, 'missing.tap'));
  assert.equal(missing.status, 0, missing.stderr);
  assert.match(missing.stdout, /::warning::读不到测试输出文件/);
});
