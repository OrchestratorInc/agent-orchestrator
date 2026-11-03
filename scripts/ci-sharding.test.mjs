import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
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
  assert.ok(args.includes('-timeout=40m'));
}

test('core singleton executes every non-dedicated package once', t => {
  const block = job(go, 'backend-core');
  assert.match(block, /shard: \[0\]/);
  const core = ['example/backend/internal/cli', 'example/backend/internal/service', 'example/backend/internal/storage/sqlite/gen'];
  const dedicated = ['example/backend/internal/storage/sqlite', 'example/backend/internal/service/chat', 'example/backend/internal/session_manager', 'example/backend/internal/storage/sqlite/store'];
  const { result, calls } = execute(t, runStep(block, 'Test'), [...core, ...dedicated].join('\n') + '\n');
  assert.equal(result.status, 0, result.stderr);
  assert.equal(calls.length, 1);
  assertRace(calls[0]);
  assert.deepEqual(calls[0].filter(arg => arg.startsWith('example/')), core);
});

test('chat singleton runs the complete package without a name filter', t => {
  const block = job(go, 'chat-race');
  assert.match(block, /shard: \[0\]/);
  const { result, calls } = execute(t, runStep(block, 'Test chat'), '');
  assert.equal(result.status, 0, result.stderr);
  assert.equal(calls.length, 1);
  assertRace(calls[0]);
  assert.equal(calls[0].at(-1), './internal/service/chat');
  assert.ok(!calls[0].includes('-run'));
});

test('two SQLite shards partition all tests, examples and fuzz seeds exactly once', t => {
  const block = job(go, 'sqlite-race');
  assert.match(block, /shard: \[0, 1\]/);
  const tests = [...Array.from({ length: 17 }, (_, i) => `TestCase${i}`), 'ExampleStore', 'FuzzDecode'];
  const inventory = [...tests, 'BenchmarkIgnored', 'ok example/backend/internal/storage/sqlite 0.01s'].join('\n') + '\n';
  const selected = [];
  for (const shard of [0, 1]) {
    const script = runStep(block, 'Test SQLite shard').replaceAll('${{ matrix.shard }}', String(shard));
    const { result, calls } = execute(t, script, inventory);
    assert.equal(result.status, 0, result.stderr);
    assert.equal(calls.length, 1);
    assertRace(calls[0]);
    const pattern = calls[0][calls[0].indexOf('-run') + 1];
    const matches = tests.filter(name => new RegExp(pattern).test(name));
    assert.ok(matches.length > 0);
    selected.push(...matches);
  }
  assert.equal(selected.length, tests.length);
  assert.deepEqual([...selected].sort(), [...tests].sort());
});

test('empty core and SQLite selections fail rather than falsely pass', t => {
  for (const [id, name] of [['backend-core', 'Test'], ['sqlite-race', 'Test SQLite shard']]) {
    const script = runStep(job(go, id), name).replaceAll('${{ matrix.shard }}', '0');
    const { result, calls } = execute(t, script, '');
    assert.notEqual(result.status, 0);
    assert.equal(calls.length, 0);
  }
});

test('vitest matrix and command agree on two complete shards', () => {
  const block = job(frontend, 'vitest');
  assert.match(block, /shard: \[1, 2\]/);
  assert.equal(runStep(block, 'Run vitest shard'), 'npx vitest run --shard=${{ matrix.shard }}/2');
  assert.match(block, /timeout-minutes: 30/);
  for (const id of ['backend-core', 'chat-race', 'sqlite-race']) {
    assert.match(job(go, id), /timeout-minutes: 50/);
  }
});
