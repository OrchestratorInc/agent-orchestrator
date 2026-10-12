// Isolated desktop test provider. Never selected by the product or installed on PATH.
const fs = require("node:fs");
const readline = require("node:readline");
const args = process.argv.slice(2);
if (args.includes("--version")) {
	console.log("1.0.0");
	process.exit(0);
}
if (args[0] === "auth") {
	console.log("1 credential (isolated fixture)");
	process.exit(0);
}
if (args[0] === "models") {
	console.log("fixture/live-grid");
	process.exit(0);
}
const session = process.env.AO_SESSION_ID ?? "fixture";
const log = (entry) =>
	fs.appendFileSync(process.env.AO_GRID_PROVIDER_LOG, JSON.stringify({ session, pid: process.pid, ...entry }) + "\n");
log({ event: "launch", mode: args[0] === "acp" ? "chat" : "tui" });
if (args[0] !== "acp") {
	console.log(`Live TUI · ${session} · PID ${process.pid}\r`);
	if (process.stdin.isTTY) process.stdin.setRawMode(true);
	process.stdin.on("data", (data) => {
		log({ event: "input", text: data.toString() });
		process.stdout.write(data);
	});
	let tick = 0;
	setInterval(() => process.stdout.write(`\r\n${session}: live tick ${++tick}\r\n`), 1500);
} else {
	const send = (frame) => process.stdout.write(JSON.stringify({ jsonrpc: "2.0", ...frame }) + "\n");
	const update = (content, sessionUpdate = "agent_message_chunk") =>
		send({
			method: "session/update",
			params: {
				sessionId: `fixture-${process.pid}`,
				update: { sessionUpdate, content: { type: "text", text: content } },
			},
		});
	readline.createInterface({ input: process.stdin }).on("line", (line) => {
		let frame;
		try {
			frame = JSON.parse(line);
		} catch {
			return;
		}
		log({ event: "rpc", method: frame.method });
		if (frame.method === "initialize")
			send({
				id: frame.id,
				result: { protocolVersion: 1, agentCapabilities: { sessionCapabilities: { resume: {} } }, authMethods: [] },
			});
		else if (["session/new", "session/load", "session/resume"].includes(frame.method))
			send({ id: frame.id, result: { sessionId: `fixture-${process.pid}` } });
		else if (frame.method === "session/prompt") {
			const text = (frame.params.prompt ?? []).map((item) => item.text ?? "").join("");
			log({ event: "input", text });
			update(`Thinking in ${session}`, "agent_thought_chunk");
			update(`Live Chat ${session}\n\nReceived: ${text}\n\n`);
			let tick = 0;
			const interval = setInterval(() => {
				update(`stream ${++tick} · `);
				if (tick === 8) {
					clearInterval(interval);
					send({ id: frame.id, result: { stopReason: "end_turn" } });
				}
			}, 500);
		} else if (frame.id != null) send({ id: frame.id, result: {} });
	});
}
