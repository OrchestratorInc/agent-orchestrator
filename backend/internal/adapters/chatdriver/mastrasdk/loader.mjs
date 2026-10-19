import { createRequire } from 'node:module';
import { readFile } from 'node:fs/promises';
import { readFileSync, lstatSync } from 'node:fs';
import { dirname, resolve, sep, join } from 'node:path';

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
    managedInstructions = new Set();
    withoutOwnedContext(plugins) {
      return plugins.map(plugin => {
        if (plugin.id !== 'ao-runtime') return plugin;
        if (plugin.scope !== 'project' || plugin.source !== 'local' || plugin.specifier !== 'agent-orchestrator' ||
            plugin.path !== 'ao-runtime' || plugin.entry !== 'index.ts' || plugin.enabled !== true || plugin.status !== 'active') {
          throw new Error('Ambiguous or disabled AO native instruction plugin');
        }
        const entry = join(options.projectRoot, '.mastracode', 'plugins', 'ao-runtime', 'index.ts');
        if (!lstatSync(entry).isFile() || !readFileSync(entry, 'utf8').startsWith('// agent-orchestrator: managed mastra-code\n')) {
          throw new Error('AO native instruction plugin has no managed ownership witness');
        }
        if (typeof plugin.instructions === 'string') this.managedInstructions.add(plugin.instructions);
        // AO's contribution moves to trusted hostInstructions. Every native
        // user plugin, tool, processor, signal and other contribution remains.
        return { ...plugin, instructions: undefined };
      });
    }
    async reload() { return this.withoutOwnedContext(await super.reload()); }
    getLoadedPlugins() { return this.withoutOwnedContext(super.getLoadedPlugins()); }
    async listPlugins() { return this.withoutOwnedContext(await super.listPlugins()); }
    getPluginInstructions() {
      return this.getLoadedPlugins().flatMap(plugin => plugin.status === 'active' && plugin.instructions ? [plugin.instructions] : []);
    }
    async pollGithubSourcesForUpdates() { return false; }
  })(options);
}
