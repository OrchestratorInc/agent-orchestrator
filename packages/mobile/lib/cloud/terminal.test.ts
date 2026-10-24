import { afterEach, describe, expect, it, vi } from "vitest";
import { CloudApiError, CloudClient } from "@aoagents/cloud-client";
import { createCloudTerminal } from "./terminal";

class FakeSocket {
	static instances: FakeSocket[] = [];
	static readonly OPEN = 1;
	readonly sent: string[] = [];
	readyState = 0;
	onopen: (() => void) | null = null;
	onmessage: ((event: { data: string }) => void) | null = null;
	onclose: ((event: { code: number }) => void) | null = null;
	onerror: (() => void) | null = null;

	constructor(readonly url: string) { FakeSocket.instances.push(this); }
	open() { this.readyState = FakeSocket.OPEN; this.onopen?.(); }
	message(value: unknown) { this.onmessage?.({ data: JSON.stringify(value) }); }
	close(code = 1006) { this.readyState = 3; this.onclose?.({ code }); }
	send(value: string) { this.sent.push(value); }
}

function fixture(kind?: "agent" | "workspace", terminalId?: string) {
	const createTerminalTicket = vi.fn(async () => ({ ticket: `ticket-${createTerminalTicket.mock.calls.length}`, expiresIn: 300, scopes: ["terminal:operate"] }));
	const urlClient = new CloudClient({ baseUrl: "https://cloud.example", getAccessToken: () => null });
	const terminalUrl = vi.fn(urlClient.terminalUrl.bind(urlClient));
	const client = { createTerminalTicket, terminalUrl };
	const statuses: string[] = [];
	const output: Uint8Array[] = [];
	const errors: string[] = [];
	const terminal = createCloudTerminal({
		client: client as never, orgId: "org-1", sessionId: "session-1", kind, terminalId,
		WebSocketImpl: FakeSocket as never,
		onStatus: (status) => statuses.push(status),
		onOutput: (bytes) => output.push(bytes),
		onError: (message) => errors.push(message),
	});
	return { terminal, client, statuses, output, errors };
}

afterEach(() => { FakeSocket.instances = []; vi.useRealTimers(); });

describe("Cloud terminal", () => {
	it("attaches only the requested reviewer terminal in both the ticket and socket without auto-resume", async () => {
		const terminalId = "00000000-0000-0000-0000-000000000091";
		const { terminal, client } = fixture("agent", terminalId);
		await terminal.connect();
		expect(client.createTerminalTicket).toHaveBeenCalledExactlyOnceWith("org-1", "session-1", "agent", { signal: expect.any(AbortSignal), terminalId });
		const url = new URL(FakeSocket.instances[0].url);
		expect(url.searchParams.get("terminalId")).toBe(terminalId);
		expect(url.searchParams.get("kind")).toBe("agent");
		expect(url.searchParams.get("protocol")).toBe("2");
		expect(url.searchParams.has("autoResume")).toBe(false);
		terminal.disconnect();
	});

	it.each(["", " ", "worker-default", "../worker", "00000000-0000-0000-0000-000000000091?token=secret"])("does not fall back to the worker terminal for invalid reviewer ID %j", async (terminalId) => {
		const { terminal, client, statuses, errors } = fixture("agent", terminalId);
		await terminal.connect();
		expect(client.createTerminalTicket).not.toHaveBeenCalled();
		expect(FakeSocket.instances).toHaveLength(0);
		expect(statuses.at(-1)).toBe("error");
		expect(errors.at(-1)).toMatch(/reviewer terminal.*unavailable/i);
		terminal.disconnect();
	});

	it("does not reopen an exited reviewer as the worker terminal", async () => {
		vi.useFakeTimers();
		const terminalId = "00000000-0000-0000-0000-000000000091";
		const { terminal, client, statuses, errors } = fixture("agent", terminalId);
		client.createTerminalTicket.mockRejectedValueOnce(new CloudApiError(410, { error: "gone", code: "TERMINAL_SESSION_EXITED", message: "hidden", requestId: "hidden" }));
		await terminal.connect(); await vi.advanceTimersByTimeAsync(5000);
		expect(statuses.at(-1)).toBe("exited");
		expect(errors.at(-1)).toMatch(/reviewer terminal has exited/i);
		expect(client.createTerminalTicket).toHaveBeenCalledOnce();
		expect(FakeSocket.instances).toHaveLength(0);
		terminal.disconnect();
	});

	it.each(["agent", "workspace"] as const)("aborts a pending %s ticket when disconnected and never opens a stale socket", async (kind) => {
		const { terminal, client, statuses, errors } = fixture(kind);
		let resolve!: (value: unknown) => void;
		client.createTerminalTicket.mockImplementationOnce(() => new Promise((done) => { resolve = done as never; }));
		const connecting = terminal.connect();
		const options = (client.createTerminalTicket.mock.calls[0] as unknown as [string, string, string, { signal: AbortSignal }])[3];
		terminal.disconnect(); expect(options.signal.aborted).toBe(true);
		resolve({ ticket: "late" }); await connecting; expect(FakeSocket.instances).toHaveLength(0);
		expect(client.createTerminalTicket).toHaveBeenCalledWith("org-1", "session-1", kind, { signal: options.signal });
		expect(statuses).toEqual(["connecting"]);
		expect(errors).toEqual([]);
	});
	it("defaults to an agent ticket and becomes ready only after the server's ready frame", async () => {
		const { terminal, client, statuses } = fixture();
		await terminal.connect();
		expect(client.createTerminalTicket).toHaveBeenCalledWith("org-1", "session-1", "agent", { signal: expect.any(AbortSignal) });
		expect(statuses).toEqual(["connecting", "attaching"]);
		const socket = FakeSocket.instances[0];
		expect(socket.url).toBe("wss://cloud.example/api/cloud/v1/terminal?ticket=ticket-1&after=0&kind=agent&protocol=2");
		socket.open();
		expect(terminal.sendInput("x")).toBe(false);
		socket.message({ type: "ready", sequence: 0 });
		expect(statuses.at(-1)).toBe("ready");
		expect(terminal.sendInput("x")).toBe(true);
		expect(socket.sent.at(-1)).toBe('{"type":"input","data":"x"}');
		terminal.disconnect();
	});

	it("uses the workspace kind for both its ticket and protocol-v2 socket", async () => {
		const { terminal, client } = fixture("workspace");
		await terminal.connect();
		const socket = FakeSocket.instances[0];
		terminal.disconnect();
		expect.soft(client.createTerminalTicket).toHaveBeenCalledWith("org-1", "session-1", "workspace", { signal: expect.any(AbortSignal) });
		expect(socket.url).toBe("wss://cloud.example/api/cloud/v1/terminal?ticket=ticket-1&after=0&kind=workspace&protocol=2");
	});

	it("submits a composed prompt as bracketed text followed by a separate Enter event", async () => {
		vi.useFakeTimers();
		const { terminal } = fixture();
		await terminal.connect();
		const socket = FakeSocket.instances[0];
		socket.open();
		socket.message({ type: "ready", sequence: 0 });
		// The mode marker may be split across terminal output frames.
		socket.message({ type: "output", data: "G1s/MjAw" });
		socket.message({ type: "output", data: "NGg=" });

		const submission = terminal.sendPrompt("fix the failing test");
		expect(socket.sent).toEqual([
			'{"type":"input","data":"\\u001b[200~fix the failing test\\u001b[201~"}',
		]);
		await vi.runAllTimersAsync();
		expect(await submission).toBe(true);
		expect(socket.sent).toEqual([
			'{"type":"input","data":"\\u001b[200~fix the failing test\\u001b[201~"}',
			'{"type":"input","data":"\\r"}',
		]);
		terminal.disconnect();
	});

	it("renders decoded output and clears stale output when replay resets", async () => {
		const { terminal, output } = fixture();
		await terminal.connect();
		const socket = FakeSocket.instances[0];
		socket.open();
		socket.message({ type: "reset" });
		socket.message({ type: "output", data: "aGVsbG8=", sequence: 7 });
		expect(new TextDecoder().decode(output[0])).toBe("\u001b[3J\u001b[H\u001b[2J");
		expect(new TextDecoder().decode(output[1])).toBe("hello");
		terminal.disconnect();
	});

	it.each(["agent", "workspace"] as const)("mints a fresh single-use %s ticket after an unexpected close", async (kind) => {
		vi.useFakeTimers();
		const { terminal, client } = fixture(kind);
		await terminal.connect();
		FakeSocket.instances[0].close();
		await vi.advanceTimersByTimeAsync(1000);
		expect(client.createTerminalTicket).toHaveBeenCalledTimes(2);
		expect(client.createTerminalTicket).toHaveBeenLastCalledWith("org-1", "session-1", kind, { signal: expect.any(AbortSignal) });
		expect(FakeSocket.instances[1].url).toContain("ticket=ticket-2");
		expect(new URL(FakeSocket.instances[1].url).searchParams.get("kind")).toBe(kind);
		terminal.disconnect();
		FakeSocket.instances[1].close();
		await vi.advanceTimersByTimeAsync(5000);
		expect(client.createTerminalTicket).toHaveBeenCalledTimes(2);
	});

	it("waits for a provisioning worker and retries its ticket without using a Local connection", async () => {
		vi.useFakeTimers();
		const { terminal, client, statuses } = fixture();
		client.createTerminalTicket.mockRejectedValueOnce(new CloudApiError(409, {
			error: "worker unavailable", code: "WORKER_UNAVAILABLE", message: "Worker is starting", requestId: "r1",
		}));
		await terminal.connect();
		expect(statuses.at(-1)).toBe("waiting");
		expect(FakeSocket.instances).toHaveLength(0);
		await vi.advanceTimersByTimeAsync(1000);
		expect(FakeSocket.instances).toHaveLength(1);
		terminal.disconnect();
	});

	it.each([
		{ kind: "agent", subject: /coding-agent terminal/i },
		{ kind: "workspace", subject: /workspace terminal/i },
	] as const)("does not reconnect an exited $kind terminal", async ({ kind, subject }) => {
		vi.useFakeTimers();
		const { terminal, client, statuses, errors } = fixture(kind);
		client.createTerminalTicket.mockRejectedValueOnce(new CloudApiError(410, {
			error: "gone", code: "TERMINAL_SESSION_EXITED", message: "Agent exited", requestId: "r2",
		}));
		await terminal.connect();
		await vi.advanceTimersByTimeAsync(5000);
		expect(statuses.at(-1)).toBe("exited");
		expect(errors.at(-1)).toMatch(/exited/i);
		expect(errors.at(-1)).toMatch(subject);
		expect(client.createTerminalTicket).toHaveBeenCalledTimes(1);
		terminal.disconnect();
	});

	it("sends a measured grid and never sends an invalid zero-size resize", async () => {
		const { terminal } = fixture();
		terminal.resize(0, 0);
		terminal.resize(42, 23);
		await terminal.connect();
		const socket = FakeSocket.instances[0];
		socket.open();
		expect(socket.sent).toEqual(['{"type":"resize","columns":42,"rows":23}']);
		terminal.disconnect();
	});
	it.each([
		{ columns: 2, rows: 1 },
		{ columns: 19, rows: 4 },
		{ columns: 20, rows: 3 },
	])("ignores a transient $columns by $rows fit before the first connection", async ({ columns, rows }) => {
		const { terminal } = fixture();
		terminal.resize(columns, rows);
		await terminal.connect();
		const socket = FakeSocket.instances[0];
		socket.open();
		expect(socket.sent).toEqual([]);
		terminal.resize(20, 4);
		expect(socket.sent).toEqual(['{"type":"resize","columns":20,"rows":4}']);
		terminal.disconnect();
	});
	it("keeps the last settled grid across tiny fits and a socket reconnect", async () => {
		vi.useFakeTimers();
		const { terminal } = fixture();
		terminal.resize(42, 23);
		await terminal.connect();
		const first = FakeSocket.instances[0];
		first.open();
		terminal.resize(2, 1);
		terminal.resize(19, 4);
		terminal.resize(20, 3);
		expect(first.sent).toEqual(['{"type":"resize","columns":42,"rows":23}']);
		first.close();
		await vi.advanceTimersByTimeAsync(1000);
		const second = FakeSocket.instances[1];
		second.open();
		expect(second.sent).toEqual(['{"type":"resize","columns":42,"rows":23}']);
		terminal.resize(80, 24);
		expect(second.sent.at(-1)).toBe('{"type":"resize","columns":80,"rows":24}');
		terminal.disconnect();
	});

	it.each([
		{ kind: "agent", subject: /agent terminal/i },
		{ kind: "workspace", subject: /workspace terminal/i },
	] as const)("surfaces a $kind terminal policy close instead of retrying forever", async ({ kind, subject }) => {
		vi.useFakeTimers();
		const { terminal, client, statuses, errors } = fixture(kind);
		await terminal.connect();
		FakeSocket.instances[0].close(1008);
		await vi.advanceTimersByTimeAsync(5000);
		expect(statuses.at(-1)).toBe("error");
		expect(errors.at(-1)).toMatch(/unavailable|allowed/i);
		expect(errors.at(-1)).toMatch(subject);
		expect(client.createTerminalTicket).toHaveBeenCalledTimes(1);
		terminal.disconnect();
	});
});
