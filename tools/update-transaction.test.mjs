import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test, { after } from 'node:test';
import { applyUpdate, finalizeUpdate, rollbackUpdate } from '../scripts/update-transaction.mjs';

const toolsDir = path.dirname(fileURLToPath(import.meta.url));
const projectRoot = path.resolve(toolsDir, '..');
const testArea = fs.mkdtempSync(path.join(projectRoot, 'runtime-tests-'));
after(() => fs.rmSync(testArea, { recursive: true, force: true }));

function write(file, content) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, content);
}

test('update transaction swaps staged code, keeps data untouched, and finalizes backups', () => {
  const root = path.join(testArea, 'success-root');
  const stage = path.join(root, '.update-stage');
  const backup = path.join(root, '.backup');
  write(path.join(root, 'server', 'index.mjs'), 'old-server');
  write(path.join(root, 'data', 'settings.json'), 'user-data');
  write(path.join(stage, 'server', 'index.mjs'), 'new-server');
  write(path.join(stage, 'package.json'), '{"version":"2"}');

  applyUpdate({ root, stage, backup, items: ['server', 'package.json'] });
  assert.equal(fs.readFileSync(path.join(root, 'server', 'index.mjs'), 'utf8'), 'new-server');
  assert.equal(fs.readFileSync(path.join(root, 'package.json'), 'utf8'), '{"version":"2"}');
  assert.equal(fs.readFileSync(path.join(root, 'data', 'settings.json'), 'utf8'), 'user-data');
  assert.equal(fs.readFileSync(path.join(backup, 'server', 'index.mjs'), 'utf8'), 'old-server');

  finalizeUpdate({ backup });
  assert.equal(fs.existsSync(path.join(backup, '.update-changed')), false);
  assert.equal(rollbackUpdate({ root, backup }), false);
});

test('update transaction rolls back partially moved code after a replacement failure', () => {
  const root = path.join(testArea, 'rollback-root');
  const stage = path.join(root, '.update-stage');
  const backup = path.join(root, '.backup');
  write(path.join(root, 'server', 'index.mjs'), 'old-server');
  write(path.join(root, 'panel', 'app.js'), 'old-panel');
  write(path.join(root, 'data', 'settings.json'), 'keep-settings');
  write(path.join(stage, 'server', 'index.mjs'), 'new-server');
  write(path.join(stage, 'panel', 'app.js'), 'new-panel');

  let renameCalls = 0;
  assert.throws(() => applyUpdate({
    root,
    stage,
    backup,
    items: ['server', 'panel'],
    renamePath(from, to) {
      renameCalls += 1;
      if (renameCalls === 4) throw new Error('simulated rename failure');
      fs.renameSync(from, to);
    },
  }), /simulated rename failure/);

  assert.equal(fs.readFileSync(path.join(root, 'server', 'index.mjs'), 'utf8'), 'old-server');
  assert.equal(fs.readFileSync(path.join(root, 'panel', 'app.js'), 'utf8'), 'old-panel');
  assert.equal(fs.readFileSync(path.join(root, 'data', 'settings.json'), 'utf8'), 'keep-settings');
  assert.equal(fs.existsSync(path.join(backup, '.update-changed')), false);
});

test('update transaction removes newly introduced paths when rolling back', () => {
  const root = path.join(testArea, 'new-path-root');
  const stage = path.join(root, '.update-stage');
  const backup = path.join(root, '.backup');
  write(path.join(stage, 'tools', 'new-tool.mjs'), 'new-tool');

  applyUpdate({ root, stage, backup, items: ['tools'] });
  assert.equal(fs.readFileSync(path.join(root, 'tools', 'new-tool.mjs'), 'utf8'), 'new-tool');
  assert.equal(rollbackUpdate({ root, backup }), true);
  assert.equal(fs.existsSync(path.join(root, 'tools')), false);
});

function gitObjectHash(type, body) {
  return crypto.createHash('sha1').update(`${type} ${body.length}\0`).update(body).digest('hex');
}

function createTreeFixture(files) {
  const entries = [];
  const directories = new Set(['']);
  const blobs = new Map();
  for (const [name, content] of Object.entries(files)) {
    const parts = name.split('/');
    for (let i = 1; i < parts.length; i += 1) directories.add(parts.slice(0, i).join('/'));
    const body = Buffer.from(content);
    blobs.set(name, { mode: '100644', type: 'blob', sha: gitObjectHash('blob', body) });
  }

  function buildTree(directory) {
    const children = [];
    for (const child of directories) {
      if (!child || path.posix.dirname(child) !== (directory || '.')) continue;
      const name = path.posix.basename(child);
      children.push({ name, mode: '40000', type: 'tree', sha: buildTree(child) });
    }
    for (const [file, entry] of blobs) {
      if (path.posix.dirname(file) === (directory || '.')) children.push({ name: path.posix.basename(file), ...entry });
    }
    children.sort((a, b) => Buffer.compare(
      Buffer.from(a.name + (a.type === 'tree' ? '/' : '\0')),
      Buffer.from(b.name + (b.type === 'tree' ? '/' : '\0')),
    ));
    const body = Buffer.concat(children.flatMap((entry) => [
      Buffer.from(`${entry.mode} ${entry.name}\0`),
      Buffer.from(entry.sha, 'hex'),
    ]));
    const sha = gitObjectHash('tree', body);
    for (const child of children) {
      const fullPath = directory ? `${directory}/${child.name}` : child.name;
      entries.push({ path: fullPath, mode: child.type === 'tree' ? '040000' : child.mode, type: child.type, sha: child.sha });
    }
    return sha;
  }

  const sha = buildTree('');
  return { sha, tree: entries };
}

test('update archive verifier accepts exact Git trees and rejects changed or extra files', () => {
  const updater = fs.readFileSync(path.join(projectRoot, 'scripts', 'update.sh'), 'utf8');
  const marker = `  "$NODE_BIN" - "$SRCDIR" "$TREE_JSON" "$TREE_SHA" <<'NODE'\n`;
  const start = updater.indexOf(marker);
  assert.notEqual(start, -1, 'expected inline Git tree verifier in update.sh');
  const bodyStart = start + marker.length;
  const end = updater.indexOf('\nNODE\n', bodyStart);
  assert.notEqual(end, -1, 'expected end marker for inline Git tree verifier');
  const verifier = updater.slice(bodyStart, end);
  const fixtureDir = path.join(testArea, 'verified-source');
  const fixtureFiles = { 'VERSION': 'v2.0.0 (B200)\n', 'panel/index.html': '<main>safe</main>\n' };
  const tree = createTreeFixture(fixtureFiles);
  for (const [name, content] of Object.entries(fixtureFiles)) {
    const file = path.join(fixtureDir, ...name.split('/'));
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, content);
  }
  const treeFile = path.join(testArea, 'verified-tree.json');
  fs.writeFileSync(treeFile, JSON.stringify({ sha: tree.sha, truncated: false, tree: tree.tree }));
  const runVerifier = () => spawnSync(process.execPath, ['-', fixtureDir, treeFile, tree.sha], { input: verifier, encoding: 'utf8' });

  const valid = runVerifier();
  assert.equal(valid.status, 0, valid.stderr);

  fs.writeFileSync(path.join(fixtureDir, 'VERSION'), 'tampered\n');
  const changed = runVerifier();
  assert.notEqual(changed.status, 0);
  assert.match(changed.stderr, /文件校验和不匹配/);

  fs.writeFileSync(path.join(fixtureDir, 'VERSION'), fixtureFiles.VERSION);
  fs.writeFileSync(path.join(fixtureDir, 'unexpected.txt'), 'unexpected');
  const extra = runVerifier();
  assert.notEqual(extra.status, 0);
  assert.match(extra.stderr, /文件数量|未列入 Git 树/);
});
