import { readFile, realpath } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import { dirname, join } from 'node:path';
import { Readable, Writable } from 'node:stream';
import { createAgent } from './bridge.mjs';
import { resolveInstalledSDK, frozenPluginManager } from './loader.mjs';

// Resolve only the SDK already owned by the independently installed CLI.
// No package download, vendor patch, mutable source profile, or CLI fallback.
if (Number(process.versions.node.split('.')[0]) < 24) throw new Error('Mastra SDK candidate requires Node 24 or newer');
const { sdkPath, mapperPath, acpPath, pluginManagerPath, projectPath } = await resolveInstalledSDK(process.argv[2]);
const { createMastraCode } = await import(pathToFileURL(sdkPath));
const { PluginManager } = await import(pathToFileURL(pluginManagerPath));
const { detectProject } = await import(pathToFileURL(projectPath));
const { handleAgentControllerEvent, setAutoApprove } = await import(pathToFileURL(mapperPath));
const { AgentSideConnection, ndJsonStream } = await import(pathToFileURL(acpPath));
const configBytes = await readFile(process.argv[3]);
if (configBytes.length > 1024 * 1024) throw new Error('Mastra host configuration is too large');
const config = JSON.parse(configBytes);
const workspace = await realpath(config.workspace);
if (!config.owner || typeof config.hostInstructions !== 'string') throw new Error('Invalid owned host configuration');
setAutoApprove(false);
console.log = (...args) => process.stderr.write(args.map(String).join(' ') + '\n');
let agent;
let shutdown;
const dispose = () => agent?.dispose();
const signal = () => {
  shutdown ??= Promise.race([
    Promise.resolve().then(dispose),
    new Promise((_, reject) => setTimeout(() => reject(new Error('SDK cleanup timed out')), 10000).unref()),
  ]).then(() => process.exit(0), () => process.exit(1));
};
process.on('SIGINT', signal);
process.on('SIGTERM', signal);
try {
  const connection = new AgentSideConnection(connection => {
    const ownedRoot = dirname(process.argv[3]);
    agent = createAgent({ connection, workspace, owner: config.owner, hostInstructions: config.hostInstructions,
      storage: { backend: 'libsql', url: pathToFileURL(join(ownedRoot, 'history.db')).href,
        vectorUrl: pathToFileURL(join(ownedRoot, 'vector.db')).href, isRemote: false },
      mapEvent: handleAgentControllerEvent,
      createRuntime: async options => {
        const pluginManager = frozenPluginManager(PluginManager, { projectRoot: detectProject(workspace).rootPath });
        const runtime = await createMastraCode({ ...options, pluginManager });
        let cleanup;
        return { ...runtime, cleanup: () => cleanup ??= (async () => {
          const errors = [];
          const stop = async fn => { try { await fn(); } catch (error) { errors.push(error); } };
          await stop(() => runtime.session.abort());
          await stop(() => runtime.session.thread.detachFromCurrent());
          await stop(() => runtime.stopPluginSignalProviders());
          await stop(() => runtime.githubSignals?.stopAllPolling());
          await stop(() => runtime.threadScheduler.stop());
          await stop(() => runtime.stopNotificationDispatch());
          await stop(() => runtime.session.thread.clearAndReleaseLock());
          await stop(() => runtime.mcpManager?.disconnect());
          await stop(() => runtime.controller.getMastra()?.stopWorkers());
          await stop(() => runtime.controller.stopIntervals());
          await stop(() => runtime.signalsPubSub?.close?.());
          await stop(() => runtime.storage.close());
          if (errors.length) throw new AggregateError(errors, 'Native SDK cleanup failed');
        })() };
      },
    });
    return agent;
  }, ndJsonStream(Writable.toWeb(process.stdout), Readable.toWeb(process.stdin)));
  await connection.closed;
} finally {
  await dispose();
  process.off('SIGINT', signal);
  process.off('SIGTERM', signal);
}
