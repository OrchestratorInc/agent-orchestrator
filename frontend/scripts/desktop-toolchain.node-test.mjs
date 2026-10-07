import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { verifyToolchain } from './verify-desktop-toolchain.mjs';

const read = (file) => readFileSync(new URL(`../../${file}`, import.meta.url), 'utf8');
test('PR checks and every unsigned desktop build use the shared exact toolchain', () => {
  for (const file of ['.node-version', '.npm-version']) assert.match(read(file).trim(), /^\d+\.\d+\.\d+$/);
  for (const workflow of ['frontend.yml', 'build-artifacts.yml']) {
    const contents = read(`.github/workflows/${workflow}`);
    assert.doesNotMatch(contents, /uses: actions\/setup-node|node-version:/);
    assert.equal((contents.match(/uses: \.\/\.github\/actions\/setup-desktop-node/g) ?? []).length, workflow === 'frontend.yml' ? 3 : 1);
  }
  const action = read('.github/actions/setup-desktop-node/action.yml');
  assert.match(action, /node-version-file: \.node-version/);
  assert.match(action, /\.npm-version/);
  assert.match(action, /verify-desktop-toolchain/);
});
test('the toolchain verifier rejects a different Node or npm instead of silently continuing', () => {
  const nodeVersion = read('.node-version').trim(), npmVersion = read('.npm-version').trim();
  assert.match(verifyToolchain(nodeVersion, npmVersion), /Desktop toolchain: Node/);
  assert.throws(() => verifyToolchain('24.0.0', npmVersion), /Desktop toolchain mismatch/);
  assert.throws(() => verifyToolchain(nodeVersion, '11.0.0'), /Desktop toolchain mismatch/);
});
