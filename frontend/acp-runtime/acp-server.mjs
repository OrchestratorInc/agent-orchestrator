// One Node process for many Claude chats: each socket client gets its own ACP
// connection, as if it had started the adapter itself.
// Usage: node acp-server.mjs <socket path>
import { AsyncLocalStorage } from "node:async_hooks";
import childProcess from "node:child_process";
import fs from "node:fs";
import { syncBuiltinESMExports } from "node:module";
import { createServer } from "node:net";

const socketPath = process.argv[2];
// Exit this long after the last connection closes.
const idleExitMs = 30_000;
// After a chat disconnects, its adapter gets this long to stop its sessions;
// then whatever it started is stopped here, as the chat host does with a
// provider process.
const releaseMs = 3_000;

// Each connection owns the processes and file watchers started while handling
// it, so they end with the connection as they ended with a per-chat process.
const owner = new AsyncLocalStorage();
const track = (resource) => {
	const resources = owner.getStore();
	if (resources) {
		resources.add(resource);
		resource.once(typeof resource.kill === "function" ? "exit" : "close", () => resources.delete(resource));
	}
	return resource;
};
const { spawn } = childProcess;
childProcess.spawn = (...args) => track(spawn(...args));
const { watch } = fs;
fs.watch = (...args) => track(watch(...args));
syncBuiltinESMExports();
const release = (resources) => {
	for (const resource of resources) {
		if (typeof resource.kill !== "function") {
			resource.close();
			continue;
		}
		resource.kill("SIGTERM");
		setTimeout(() => resource.exitCode === null && resource.signalCode === null && resource.kill("SIGKILL"), 2_000);
	}
};

const { resolveSettings } = await import("@anthropic-ai/claude-agent-sdk");
const { nodeToWebReadable, nodeToWebWritable, runAcp } = await import("@agentclientprotocol/claude-agent-acp");

// As the adapter's own entry point: managed-policy env first, stdout kept quiet.
const policy = await resolveSettings({ settingSources: [] });
for (const [key, value] of Object.entries(policy.effective.env ?? {})) {
	process.env[key] = value;
}
console.log = console.error;
console.info = console.error;
console.warn = console.error;
console.debug = console.error;
process.on("unhandledRejection", (reason) => console.error("Unhandled Rejection:", reason));

let open = 0;
let idleTimer;
const exitWhenIdle = () => {
	idleTimer = setTimeout(() => process.exit(0), idleExitMs);
};

fs.rmSync(socketPath, { force: true });
const server = createServer((socket) => {
	open++;
	clearTimeout(idleTimer);
	const resources = new Set();
	owner.run(resources, () => {
		const { connection, agent } = runAcp(undefined, nodeToWebWritable(socket), nodeToWebReadable(socket));
		let closed = false;
		const dispose = () => agent.dispose().catch((err) => console.error("Error during cleanup:", err));
		// A session registers only after its query starts, so one still starting at
		// disconnect would outlive dispose(). Dispose again when it finishes.
		for (const method of ["newSession", "loadSession", "resumeSession", "unstable_forkSession"]) {
			const start = agent[method].bind(agent);
			agent[method] = (params) =>
				start(params).finally(() => {
					if (closed) dispose();
				});
		}
		socket.on("error", () => socket.destroy());
		socket.on("close", () => {
			closed = true;
			dispose();
			setTimeout(() => release(resources), releaseMs);
			if (--open === 0) exitWhenIdle();
		});
		connection.closed.then(() => socket.destroy());
	});
});
server.listen(socketPath, exitWhenIdle);
