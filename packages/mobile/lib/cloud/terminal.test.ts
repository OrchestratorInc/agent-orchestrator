import { afterEach, describe, expect, it, vi } from "vitest";
import { CloudApiError } from "@aoagents/cloud-client";
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

function fixture() {
	const createTerminalTicket = vi.fn(async () => ({ ticket: `ticket-${createTerminalTicket.mock.calls.length}`, expiresIn: 300, scopes: ["terminal:operate"] }));
	const terminalUrl = vi.fn((ticket: string) => `wss://cloud.example/api/cloud/v1/terminal?ticket=${ticket}&after=0&kind=agent`);
	const client = { createTerminalTicket, terminalUrl };
	const statuses: string[] = [];
	const output: Uint8Array[] = [];
	const errors: string[] = [];
	const sizes: Array<[number, number]> = [];
	const resets: number[] = [];
	const terminal = createCloudTerminal({
		client: client as never, orgId: "org-1", sessionId: "session-1",
		WebSocketImpl: FakeSocket as never,
		onStatus: (status) => statuses.push(status),
		onOutput: (bytes) => output.push(bytes),
		onError: (message) => errors.push(message),
		onSize: (columns, rows) => sizes.push([columns, rows]),
		onReset: () => resets.push(1),
	});
	return { terminal, client, statuses, output, errors, sizes, resets };
}

afterEach(() => { FakeSocket.instances = []; vi.useRealTimers(); });

describe("Cloud agent terminal", () => {
	it("mints an agent ticket and becomes ready only after the server's ready frame", async () => {
		const { terminal, client, statuses } = fixture();
		await terminal.connect();
		expect(client.createTerminalTicket).toHaveBeenCalledWith("org-1", "session-1", "agent");
		const socket = FakeSocket.instances[0];
		expect(socket.url).toBe("wss://cloud.example/api/cloud/v1/terminal?ticket=ticket-1&after=0&kind=agent&protocol=3");
		socket.open();
		expect(JSON.parse(socket.sent[0])).toEqual({ type: "viewer", role: "secondary", visible: true, columns: 0, rows: 0 });
		expect(terminal.sendInput("x")).toBe(false);
		socket.message({ type: "ready", sequence: 0 });
		expect(statuses.at(-1)).toBe("ready");
		expect(terminal.sendInput("x")).toBe(true);
		expect(socket.sent.at(-1)).toBe('{"type":"input","data":"x"}');
		terminal.disconnect();
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
		expect(socket.sent.slice(1)).toEqual([
			'{"type":"input","data":"\\u001b[200~fix the failing test\\u001b[201~"}',
		]);
		await vi.runAllTimersAsync();
		expect(await submission).toBe(true);
		expect(socket.sent.slice(1)).toEqual([
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

	it("mints a fresh single-use ticket after an unexpected close", async () => {
		vi.useFakeTimers();
		const { terminal, client } = fixture();
		await terminal.connect();
		FakeSocket.instances[0].close();
		await vi.advanceTimersByTimeAsync(1000);
		expect(client.createTerminalTicket).toHaveBeenCalledTimes(2);
		expect(FakeSocket.instances[1].url).toContain("ticket=ticket-2");
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

	it("does not reconnect an exited agent terminal", async () => {
		vi.useFakeTimers();
		const { terminal, client, statuses, errors } = fixture();
		client.createTerminalTicket.mockRejectedValueOnce(new CloudApiError(410, {
			error: "gone", code: "TERMINAL_SESSION_EXITED", message: "Agent exited", requestId: "r2",
		}));
		await terminal.connect();
		await vi.advanceTimersByTimeAsync(5000);
		expect(statuses.at(-1)).toBe("exited");
		expect(errors.at(-1)).toMatch(/exited/i);
		expect(client.createTerminalTicket).toHaveBeenCalledTimes(1);
		terminal.disconnect();
	});

	it("proposes a secondary fit but renders only the authoritative grid", async () => {
		const { terminal, sizes } = fixture();
		terminal.resize(0, 0);
		terminal.resize(55, 39);
		await terminal.connect();
		const socket = FakeSocket.instances[0];
		socket.open();
		expect(JSON.parse(socket.sent[0])).toEqual({ type: "viewer", role: "secondary", visible: true, columns: 55, rows: 39 });
		socket.message({ type: "size", columns: 120, rows: 40 });
		expect(sizes).toEqual([[120, 40]]);
		terminal.resize(54, 38);
		expect(JSON.parse(socket.sent.at(-1)!)).toEqual({ type: "viewer", role: "secondary", visible: true, columns: 54, rows: 38 });
		expect(sizes).toEqual([[120, 40]]);
		terminal.setVisible(false);
		expect(JSON.parse(socket.sent.at(-1)!)).toEqual({ type: "viewer", role: "secondary", visible: false, columns: 54, rows: 38 });
		terminal.disconnect();
	});

	it("resends the latest secondary fit after reconnect and rejects invalid server sizes", async () => {
		vi.useFakeTimers();
		const { terminal, sizes, resets } = fixture();
		terminal.resize(55, 39);
		await terminal.connect();
		const first = FakeSocket.instances[0];
		first.open();
		first.message({ type: "size", columns: 120, rows: 40 });
		first.message({ type: "size", columns: 0, rows: 39 });
		expect(sizes).toEqual([[120, 40]]);
		first.close();
		terminal.resize(54, 38);
		await vi.advanceTimersByTimeAsync(1000);
		const second = FakeSocket.instances[1];
		second.open();
		expect(JSON.parse(second.sent[0])).toEqual({ type: "viewer", role: "secondary", visible: true, columns: 54, rows: 38 });
		second.message({ type: "reset" });
		expect(resets.length).toBeGreaterThanOrEqual(2);
		terminal.disconnect();
	});

	it("surfaces a terminal policy close instead of retrying forever", async () => {
		vi.useFakeTimers();
		const { terminal, client, statuses, errors } = fixture();
		await terminal.connect();
		FakeSocket.instances[0].close(1008);
		await vi.advanceTimersByTimeAsync(5000);
		expect(statuses.at(-1)).toBe("error");
		expect(errors.at(-1)).toMatch(/unavailable|allowed/i);
		expect(client.createTerminalTicket).toHaveBeenCalledTimes(1);
		terminal.disconnect();
	});
});
