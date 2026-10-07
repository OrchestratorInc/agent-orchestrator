import { readFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import path from 'node:path';

const expectedNode = readFileSync(new URL('../../.node-version', import.meta.url), 'utf8').trim();
const expectedNpm = readFileSync(new URL('../../.npm-version', import.meta.url), 'utf8').trim();
export function verifyToolchain(nodeVersion, npmVersion) {
  if (nodeVersion !== expectedNode || npmVersion !== expectedNpm) {
    throw new Error(`Desktop toolchain mismatch: Node ${nodeVersion}, npm ${npmVersion}; expected Node ${expectedNode}, npm ${expectedNpm}`);
  }
  return `Desktop toolchain: Node ${expectedNode}, npm ${expectedNpm}`;
}
if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  const npmVersion = execFileSync(process.platform === 'win32' ? 'npm.cmd' : 'npm', ['--version'], {
    encoding: 'utf8', shell: process.platform === 'win32',
  }).trim();
  console.log(verifyToolchain(process.versions.node, npmVersion));
}
