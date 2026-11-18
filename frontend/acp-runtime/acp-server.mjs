// One Node process for many Claude chats: each socket client gets its own ACP
// connection, as if it had started the adapter itself.
// Usage: node acp-server.mjs <socket path>
import { resolveSettings } from "@anthropic-ai/claude-agent-sdk";
import { nodeToWebReadable, nodeToWebWritable, runAcp } from "@agentclientprotocol/claude-agent-acp";
import { rmSync } from "node:fs";
import { createServer } from "node:net";

const socketPath = process.argv[2];
// Exit this long after the last connection closes.
const idleExitMs = 30_000;

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

rmSync(socketPath, { force: true });
const server = createServer((socket) => {
	open++;
	clearTimeout(idleTimer);
	const { connection, agent } = runAcp(undefined, nodeToWebWritable(socket), nodeToWebReadable(socket));
	socket.on("error", () => socket.destroy());
	socket.on("close", () => {
		agent.dispose().catch((err) => console.error("Error during cleanup:", err));
		if (--open === 0) exitWhenIdle();
	});
	connection.closed.then(() => socket.destroy());
});
server.listen(socketPath, exitWhenIdle);
