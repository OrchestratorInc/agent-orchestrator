import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const go = readFileSync(new URL('../.github/workflows/go.yml', import.meta.url), 'utf8');
const frontend = readFileSync(new URL('../.github/workflows/frontend.yml', import.meta.url), 'utf8');

function job(workflow, id) {
  const block = workflow.split(`\n  ${id}:\n`)[1];
  assert.ok(block, `missing job ${id}`);
  return block.split(/\n  [\w-]+:\n/)[0];
}

function runStep(block, name) {
  const step = block.split(`      - name: ${name}\n`)[1];
  assert.ok(step, `missing step ${name}`);
  const run = step.match(/^        run: (.*)$/m);
  assert.ok(run, `missing command for ${name}`);
  if (run[1] !== '|') return run[1];
  return step.slice(run.index + run[0].length + 1)
    .split('\n').map(line => line.replace(/^          /, '')).join('\n').trim();
}

function execute(t, script, inventory) {
  const dir = mkdtempSync(join(tmpdir(), 'ao-ci-shards-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const capture = join(dir, 'calls.jsonl');
  writeFileSync(capture, '');
  const fakeGo = join(dir, 'go');
  writeFileSync(fakeGo, `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
if (args[0] === 'list' || args.includes('-list')) {
  process.stdout.write(process.env.INVENTORY);
} else {
  fs.appendFileSync(process.env.CAPTURE, JSON.stringify(args) + '\\n');
}
`);
  chmodSync(fakeGo, 0o755);
  const result = spawnSync('bash', ['-e', '-o', 'pipefail', '-c', script], {
    encoding: 'utf8',
    env: { ...process.env, PATH: `${dir}:${process.env.PATH}`, CAPTURE: capture, INVENTORY: inventory },
  });
  const calls = readFileSync(capture, 'utf8').trim().split('\n').filter(Boolean).map(line => JSON.parse(line));
  return { result, calls };
}

function assertRace(args) {
  assert.ok(args.includes('-race'));
  assert.ok(args.includes('-count=1'));
  assert.ok(args.includes('-timeout=10m'));
}

const DEDICATED = ['example/backend/internal/storage/sqlite', 'example/backend/internal/service/chat', 'example/backend/internal/session_manager', 'example/backend/internal/storage/sqlite/store'];

test('three core shards execute every non-dedicated package exactly once', t => {
  const block = job(go, 'backend-core');
  assert.match(block, /shard: \[0, 1, 2\]/);
  const core = Array.from({ length: 7 }, (_, i) => `example/backend/internal/pkg${i}`);
  const ran = [];
  for (const shard of [0, 1, 2]) {
    const script = runStep(block, 'Test').replaceAll('${{ matrix.shard }}', String(shard));
    const { result, calls } = execute(t, script, [...core, ...DEDICATED].join('\n') + '\n');
    assert.equal(result.status, 0, result.stderr);
    assert.equal(calls.length, 1);
    assertRace(calls[0]);
    ran.push(...calls[0].filter(arg => arg.startsWith('example/')));
  }
  assert.deepEqual([...ran].sort(), [...core].sort());
});

test('an empty core selection fails rather than falsely passes', t => {
  const script = runStep(job(go, 'backend-core'), 'Test').replaceAll('${{ matrix.shard }}', '0');
  const { result, calls } = execute(t, script, '');
  assert.notEqual(result.status, 0);
  assert.equal(calls.length, 0);
});

test('storage-race runs exactly the packages backend-core leaves out', () => {
  const excluded = [...runStep(job(go, 'backend-core'), 'Test').matchAll(/-e '\/(internal\/[\w\/]+)\$'/g)].map(match => `./${match[1]}`);
  const storage = runStep(job(go, 'storage-race'), 'Test SQLite, chat, session manager and SQLite store').split(/\s+/).filter(arg => arg.startsWith('./'));
  assert.equal(excluded.length, 4);
  assert.deepEqual([...storage].sort(), [...excluded].sort());
});

test('go-test-parallel.sh runs every test of every package exactly once', t => {
  const dir = mkdtempSync(join(tmpdir(), 'ao-go-parallel-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const capture = join(dir, 'runs.jsonl');
  writeFileSync(capture, '');
  const tests = { sqlite: [...Array.from({ length: 9 }, (_, i) => `TestCase${i}`), 'ExampleStore', 'FuzzDecode'], chat: ['TestOnly'] };
  const fakeTest = join(dir, 'fake-test');
  writeFileSync(fakeTest, `#!/usr/bin/env node
const fs = require('node:fs');
const path = require('node:path');
const name = path.basename(process.argv[1], '.test');
const args = process.argv.slice(2);
if (args[0] === '-test.list') {
  process.stdout.write([...JSON.parse(process.env.TESTS)[name], 'BenchmarkIgnored'].join('\\n') + '\\n');
} else {
  fs.appendFileSync(process.env.CAPTURE, JSON.stringify({ name, run: args[args.indexOf('-test.run') + 1] }) + '\\n');
}
`);
  writeFileSync(join(dir, 'go'), `#!/usr/bin/env node
const fs = require('node:fs');
const path = require('node:path');
const args = process.argv.slice(2);
if (args[0] === 'list') {
  process.stdout.write(process.env.PKGDIR + '\\n');
} else {
  if (!args.includes('-race')) process.exit(2);
  const out = args[args.indexOf('-o') + 1];
  for (const pkg of args.filter(arg => arg.startsWith('./'))) {
    const bin = path.join(out, path.basename(pkg) + '.test');
    fs.copyFileSync(${JSON.stringify(fakeTest)}, bin);
    fs.chmodSync(bin, 0o755);
  }
}
`);
  chmodSync(join(dir, 'go'), 0o755);
  const script = fileURLToPath(new URL('../.github/scripts/go-test-parallel.sh', import.meta.url));
  const result = spawnSync('bash', [script, '3', './internal/storage/sqlite', './internal/service/chat'], {
    encoding: 'utf8',
    env: { ...process.env, PATH: `${dir}:${process.env.PATH}`, CAPTURE: capture, PKGDIR: dir, TESTS: JSON.stringify(tests) },
  });
  assert.equal(result.status, 0, result.stderr);
  const runs = readFileSync(capture, 'utf8').trim().split('\n').map(line => JSON.parse(line));
  for (const [name, names] of Object.entries(tests)) {
    const patterns = runs.filter(run => run.name === name).map(run => new RegExp(run.run));
    for (const test of [...names, 'BenchmarkIgnored']) {
      assert.equal(patterns.filter(pattern => pattern.test(test)).length, test.startsWith('Benchmark') ? 0 : 1, `${name} ${test}`);
    }
  }
});

test('vitest matrix and command agree on two complete shards', () => {
  const block = job(frontend, 'vitest');
  assert.match(block, /shard: \[1, 2\]/);
  const script = runStep(block, 'Run vitest shard');
  assert.match(script, /^ *npx vitest run --shard=\$\{\{ matrix\.shard \}\}\/2$/m);
  assert.match(script, /^ *npx vitest related --run --passWithNoTests --shard=\$\{\{ matrix\.shard \}\}\/2 /m);
  assert.match(block, /timeout-minutes: 30/);
  assert.match(job(go, 'backend-core'), /timeout-minutes: 20/);
  assert.match(job(go, 'storage-race'), /timeout-minutes: 30/);
});
