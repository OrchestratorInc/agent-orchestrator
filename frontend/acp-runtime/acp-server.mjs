// One Node process for many Claude chats: each socket client gets its own ACP
// connection, as if it had started the adapter itself.
// Usage: node acp-server.mjs <socket path>
import childProcess from "node:child_process";
import fs from "node:fs";
import { syncBuiltinESMExports } from "node:module";
import { createServer } from "node:net";
import { onUncaughtException, onUnhandledRejection, ownedSpawn, release, runAsChat, track } from "./connection-owner.mjs";

const socketPath = process.argv[2];
// Exit this long after the last connection closes.
const idleExitMs = 30_000;
// After a chat disconnects, its adapter gets this long to stop its sessions;
// then whatever it started is stopped here, as the chat host does with a
// provider process.
const releaseMs = 3_000;

// The SDK's spawn and the adapter's fs.watch are recorded for the chat that
// started them, so they end with that chat as they ended with its own process.
childProcess.spawn = ownedSpawn(childProcess.spawn);
const { watch } = fs;
fs.watch = (...args) => track(watch(...args));
syncBuiltinESMExports();

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
process.on("unhandledRejection", onUnhandledRejection);
process.on("uncaughtException", (err) => onUncaughtException(err));

let open = 0;
let idleTimer;
const exitWhenIdle = () => {
	idleTimer = setTimeout(() => process.exit(0), idleExitMs);
};

fs.rmSync(socketPath, { force: true });
const server = createServer((socket) => {
	open++;
	clearTimeout(idleTimer);
	const chat = { sessionId: "", resources: new Set(), released: false, close: () => socket.destroy() };
	runAsChat(chat, () => {
		const { connection, agent } = runAcp(undefined, nodeToWebWritable(socket), nodeToWebReadable(socket));
		// The chat host terminates these groups when it stops, including when this
		// process has died.
		chat.onSpawn = (child) => agent.client.extNotification("_ao/process_group", { pid: child.pid }).catch(() => {});
		let closed = false;
		const dispose = () => agent.dispose().catch((err) => console.error("Error during cleanup:", err));
		// A session registers only after its query starts, so one still starting at
		// disconnect would outlive dispose(). Dispose again when it finishes.
		for (const method of ["newSession", "loadSession", "resumeSession", "unstable_forkSession"]) {
			const start = agent[method].bind(agent);
			agent[method] = (params) => {
				chat.sessionId ||= params?._meta?.claudeCode?.options?.env?.AO_SESSION_ID ?? "";
				return start(params).finally(() => {
					if (closed) dispose();
				});
			};
		}
		socket.on("error", () => socket.destroy());
		socket.on("close", () => {
			closed = true;
			dispose();
			setTimeout(() => release(chat), releaseMs);
			if (--open === 0) exitWhenIdle();
		});
		connection.closed.then(() => socket.destroy());
	});
});
server.listen(socketPath, exitWhenIdle);
