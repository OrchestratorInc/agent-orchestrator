import { createRequire } from 'node:module';
import { readFile } from 'node:fs/promises';
import { dirname, resolve, sep } from 'node:path';

// The reviewed Mastra package exposes import-only entrypoints. Resolve its
// public package.json first, then honor its declared import export targets;
// CommonJS require.resolve(root) would reject these supported ESM exports.
export async function resolveInstalledSDK(nativeEntrypoint) {
  const nativeRequire = createRequire(nativeEntrypoint);
  const metadataPath = nativeRequire.resolve('@mastra/code-sdk/package.json');
  const metadata = JSON.parse(await readFile(metadataPath, 'utf8'));
  if (metadata.name !== '@mastra/code-sdk' || metadata.version !== '1.11.0') throw new Error('Mastra SDK candidate requires the reviewed 1.11.0 API');
  const root = dirname(metadataPath);
  const exported = subpath => {
    const entry = metadata.exports[subpath] ?? metadata.exports['./*'];
    const target = entry?.import?.default;
    if (typeof target !== 'string' || !target.startsWith('./')) throw new Error('Native SDK does not expose the reviewed import API');
    const path = resolve(root, target.replace('*', subpath.slice(2)));
    if (!path.startsWith(root + sep)) throw new Error('Native SDK export escapes its package');
    return path;
  };
  const sdkPath = exported('.');
  const mapperPath = exported('./acp/event-mapper');
  return { sdkPath, mapperPath,
    pluginManagerPath: exported('./plugins/manager'), projectPath: exported('./utils/project'),
    acpPath: createRequire(sdkPath).resolve('@agentclientprotocol/sdk') };
}

// Public virtual method only: retain provider plugin discovery/contributions,
// while preventing the native background updater from writing source profiles.
export function frozenPluginManager(PluginManager, options) {
  return new (class extends PluginManager {
    async pollGithubSourcesForUpdates() { return false; }
  })(options);
}
