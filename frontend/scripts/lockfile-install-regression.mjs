// Integration check: reproduce the missing peer entry that passed PR CI but
// failed unsigned builds. Work only on a disposable copy; never repair the lock.
import assert from 'node:assert/strict';
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { verifyToolchain } from './verify-desktop-toolchain.mjs';

const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm';
const run = (args, cwd) => spawnSync(npm, args, { cwd, encoding: 'utf8', shell: process.platform === 'win32' });
verifyToolchain(process.versions.node, run(['--version']).stdout.trim());
const directory = mkdtempSync(path.join(tmpdir(), 'desktop-lock-regression-'));
try {
  const frontend = path.join(directory, 'frontend');
  mkdirSync(frontend);
  mkdirSync(path.join(directory, 'packages/product-ui'), { recursive: true });
  copyFileSync(new URL('../package.json', import.meta.url), path.join(frontend, 'package.json'));
  copyFileSync(new URL('../../packages/product-ui/package.json', import.meta.url), path.join(directory, 'packages/product-ui/package.json'));
  const lock = JSON.parse(readFileSync(new URL('../package-lock.json', import.meta.url), 'utf8'));
  const peer = 'node_modules/@pierre/trees/node_modules/@pierre/theme';
  assert.ok(lock.packages[peer], 'The regression fixture needs the restored nested optional peer entry');
  delete lock.packages[peer];
  writeFileSync(path.join(frontend, 'package-lock.json'), JSON.stringify(lock));
  const result = run(['ci', '--ignore-scripts', '--no-audit', '--no-fund'], frontend);
  assert.notEqual(result.status, 0, 'The release npm must reject the historical incomplete lockfile');
  assert.match(result.stderr, /Missing: @pierre\/theme@[^\s]+ from lock file/);
  console.log('Historical missing-peer lockfile rejected by the pinned release toolchain.');
} finally {
  rmSync(directory, { recursive: true, force: true });
}
