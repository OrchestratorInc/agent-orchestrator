import { describe, expect, it, vi } from "vitest";
import { CloudError } from "./errors";
import { connectCloudTerminal, decodeBase64, type TerminalDeps } from "./terminal";

class FakeSocket {
	readyState = 0;
	sent: string[] = [];
	onopen: ((event: unknown) => void) | null = null;
	onmessage: ((event: { data: unknown }) => void) | null = null;
	onclose: ((event: { code: number; reason: string }) => void) | null = null;
	onerror: ((event: unknown) => void) | null = null;
	constructor(readonly url: string) {}
	send(data: string) { this.sent.push(data); }
	close() { this.readyState = 3; }
	open() { this.readyState = 1; this.onopen?.({}); }
	frame(value: Record<string, unknown>) { this.onmessage?.({ data: JSON.stringify(value) }); }
	drop(code: number, reason = "") { this.readyState = 3; this.onclose?.({ code, reason }); }
}

function setup(ticket: () => Promise<{ ticket: string; expiresIn: number; scopes: string[] }>) {
	const sockets: FakeSocket[] = [];
	const timers: Array<() => void> = [];
	const cleared = new Set<number>();
	let nextTimer = 0;
	const deps: TerminalDeps = {
		createSocket: (url) => { const socket = new FakeSocket(url); sockets.push(socket); return socket as never; },
		setTimeout: (callback) => { const id = ++nextTimer; timers.push(() => { if (!cleared.has(id)) callback(); }); return id; },
		clearTimeout: (id) => { cleared.add(id as number); },
	};
	const handlers = { onStatus: vi.fn(), onOutput: vi.fn(), onReset: vi.fn(), onRefused: vi.fn(), onAccess: vi.fn() };
	const client = { createTerminalTicket: vi.fn(ticket), terminalUrl: (value: string) => `wss://x/terminal?ticket=${value}` };
	const connection = connectCloudTerminal({ client, orgId: "o", sessionId: "s", kind: "agent", handlers, deps });
	const flush = () => new Promise((resolve) => setTimeout(resolve, 0));
	return { sockets, timers, handlers, client, connection, flush };
}

describe("cloud terminal", () => {
	it("decodes output, resets, and sends input and a sized grid as JSON frames", async () => {
		const t = setup(async () => ({ ticket: "t1", expiresIn: 300, scopes: ["terminal:read", "terminal:operate"] }));
		await t.flush();
		const socket = t.sockets[0];
		expect(socket.url).toBe("wss://x/terminal?ticket=t1");
		expect(t.handlers.onAccess).toHaveBeenCalledWith(true);
		t.connection.resize(80.6, 2);
		socket.open();
		socket.frame({ type: "reset" });
		socket.frame({ type: "output", data: btoa("hi"), sequence: 1 });
		socket.frame({ type: "replay_complete" });
		t.connection.send("ls\r");
		expect(t.handlers.onReset).toHaveBeenCalledOnce();
		expect(Array.from(t.handlers.onOutput.mock.calls[0][0] as Uint8Array)).toEqual([104, 105]);
		expect(t.handlers.onStatus).toHaveBeenLastCalledWith("open");
		expect(socket.sent.map((frame) => JSON.parse(frame))).toEqual([
			{ type: "resize", columns: 80, rows: 4 },
			{ type: "input", data: "ls\r" },
		]);
	});

	it("asks for a new ticket while the sandbox is still waking", async () => {
		let calls = 0;
		const t = setup(async () => {
			if (++calls === 1) throw new CloudError("not yet", { status: 409, code: "WORKER_UNAVAILABLE" });
			return { ticket: "t2", expiresIn: 300, scopes: ["terminal:read"] };
		});
		await t.flush();
		expect(t.sockets).toHaveLength(0);
		expect(t.handlers.onStatus).toHaveBeenLastCalledWith("connecting", expect.stringMatching(/sandbox/));
		t.timers.shift()!();
		await t.flush();
		expect(t.sockets[0].url).toContain("t2");
		expect(t.handlers.onAccess).toHaveBeenLastCalledWith(false);
	});

	it("reconnects with a fresh ticket after a drop but not after the process exits", async () => {
		let n = 0;
		const t = setup(async () => ({ ticket: `t${++n}`, expiresIn: 300, scopes: [] }));
		await t.flush();
		t.sockets[0].open();
		t.sockets[0].drop(1013, "restarting");
		expect(t.handlers.onStatus).toHaveBeenLastCalledWith("connecting", "Reconnecting…");
		t.timers.shift()!();
		await t.flush();
		expect(t.sockets[1].url).toContain("t2");
		t.sockets[1].drop(1000);
		expect(t.handlers.onStatus).toHaveBeenLastCalledWith("exited", expect.any(String));
		expect(t.timers).toHaveLength(0);
	});

	it("stops cleanly when closed", async () => {
		const t = setup(async () => ({ ticket: "t", expiresIn: 300, scopes: [] }));
		await t.flush();
		t.connection.close();
		t.sockets[0].drop(1006);
		expect(t.handlers.onStatus).toHaveBeenLastCalledWith("closed");
		expect(t.timers).toHaveLength(0);
	});

	// A replacement terminal view reconnects by hand while the drop's retry is still waiting.
	it("cancels the waiting retry when reconnected by hand, so closing reaches the only socket", async () => {
		let n = 0;
		const t = setup(async () => ({ ticket: `t${++n}`, expiresIn: 300, scopes: [] }));
		await t.flush();
		t.sockets[0].open();
		t.sockets[0].drop(1011, "interrupted");
		t.connection.reconnect();
		await t.flush();
		expect(t.sockets).toHaveLength(2);
		for (const fire of t.timers.splice(0)) fire();
		await t.flush();
		expect(t.sockets).toHaveLength(2);
		t.sockets[1].open();
		t.connection.close();
		expect(t.sockets[1].readyState).toBe(3);
	});

	it("decodes base64 to bytes", () => {
		expect(Array.from(decodeBase64(btoa("ÿ\u0001")))).toEqual([255, 1]);
	});
});
