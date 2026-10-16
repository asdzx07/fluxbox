#!/usr/bin/env node
/**
 * 把 node:test 的 TAP 输出压缩成 GitHub Actions 注解。
 *
 * 为什么需要它：Actions 的原始日志下载接口要求仓库管理员权限，
 * 而 check-run 的 annotations 是公开可读的。失败时把「哪个用例失败 + 断言差异」
 * 写成注解，就能在只有仓库读权限时直接定位问题。
 *
 * 用法：node tools/ci-report-failures.mjs <tap-output-file>
 */
import fs from 'node:fs';

const outputFile = process.argv[2] || 'test-output.txt';
let text = '';
try {
  text = fs.readFileSync(outputFile, 'utf8');
} catch {
  console.log(`::warning::读不到测试输出文件：${outputFile}`);
  process.exit(0);
}

/** 取出 TAP 里 `error: |-` 块的内容；没有该块时退回收集较深缩进的行。 */
function readFailureDetails(lines, start) {
  const details = [];
  let inErrorBlock = false;
  for (let i = start; i < lines.length && details.length < 10; i += 1) {
    const line = lines[i];
    if (/^(?:ok|not ok) \d+ - /.test(line) || /^# Subtest: /.test(line)) break;
    if (/^\s*error: \|[-+]?\s*$/.test(line)) {
      inErrorBlock = true;
      continue;
    }
    const indent = /^(\s*)/.exec(line)[1].length;
    if (inErrorBlock) {
      // 错误块结束于缩进回退或 TAP 的省略号行。
      if (line.trim() === '...' || indent < 4) break;
      if (line.trim()) details.push(line.trim());
      continue;
    }
    const deepLine = /^\s{4,}(\S.*)$/.exec(line);
    if (deepLine && deepLine[1].trim() !== '...') details.push(deepLine[1].trim());
  }
  return details;
}

const lines = text.split(/\r?\n/);
const failures = [];
for (let i = 0; i < lines.length; i += 1) {
  const header = /^not ok \d+ - (.+)$/.exec(lines[i]);
  if (!header) continue;
  failures.push({ name: header[1].trim(), details: readFailureDetails(lines, i + 1) });
}

if (!failures.length) {
  console.log('::notice::没有从 TAP 输出里解析到失败用例');
  process.exit(0);
}

for (const failure of failures) {
  const detail = failure.details.join(' | ').slice(0, 900);
  console.log(`::error title=回归测试失败::${failure.name}${detail ? ` — ${detail}` : ''}`);
}
console.log(`::notice::共解析到 ${failures.length} 个失败用例`);
process.exit(0);
