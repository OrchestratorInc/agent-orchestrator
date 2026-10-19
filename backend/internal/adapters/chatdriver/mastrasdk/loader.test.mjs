import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { resolveInstalledSDK, frozenPluginManager } from './loader.mjs';

test('loader follows public import-only SDK exports from the native CLI installation', async t => {
  const root = await mkdtemp(join(tmpdir(), 'mastra-sdk-loader-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const sdk = join(root, 'node_modules', '@mastra', 'code-sdk');
  const acp = join(root, 'node_modules', '@agentclientprotocol', 'sdk');
  await mkdir(sdk, { recursive: true });
  await mkdir(acp, { recursive: true });
  await writeFile(join(sdk, 'package.json'), JSON.stringify({ name: '@mastra/code-sdk', version: '1.11.0', type: 'module', exports: {
    '.': { import: { default: './dist/index.js' } },
    './*': { import: { default: './dist/*.js' } },
    './package.json': './package.json',
  } }));
  await writeFile(join(acp, 'package.json'), JSON.stringify({ name: '@agentclientprotocol/sdk', main: './index.js', type: 'module' }));
  await writeFile(join(acp, 'index.js'), 'export const testOnly = true;');
  const resolved = await resolveInstalledSDK(join(root, 'cli.js'));
  assert.equal(resolved.sdkPath, join(sdk, 'dist', 'index.js'));
  assert.equal(resolved.mapperPath, join(sdk, 'dist', 'acp', 'event-mapper.js'));
  assert.equal(resolved.acpPath, join(acp, 'index.js'));
  await writeFile(join(sdk, 'package.json'), JSON.stringify({ name: '@mastra/code-sdk', version: '9.0.0', exports: {} }));
  await assert.rejects(resolveInstalledSDK(join(root, 'cli.js')), /reviewed 1.11.0/);
});

test('public plugin manager override preserves instructions and prevents source auto-update', async () => {
  const options = { projectRoot: '/project' };
  class NativePluginManager {
    constructor(received) { this.options = received; }
    getPluginInstructions() { return ['independent user rules']; }
    async pollGithubSourcesForUpdates() { throw new Error('would fetch/reset source plugin'); }
  }
  const manager = frozenPluginManager(NativePluginManager, options);
  assert.deepEqual(manager.getPluginInstructions(), ['independent user rules']);
  assert.equal(manager.options, options);
  assert.equal(await manager.pollGithubSourcesForUpdates(), false);
});
