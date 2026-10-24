import { CloudApiError } from "@aoagents/cloud-client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createCloudPreviewSource } from "./browser";

class NativeSocket {
	static instances: NativeSocket[] = [];
	readyState = 1; binaryType = ""; sent: object[] = []; closed = false;
	onopen?: () => void; onerror?: () => void; onclose?: () => void; onmessage?: (event: { data: unknown }) => void;
	constructor(readonly url: string, _protocols?: unknown, readonly options?: object) { NativeSocket.instances.push(this); }
	send(text: string) { this.sent.push(JSON.parse(text)); }
	close() { this.closed = true; }
}
function setup() {
	let current = true;
	const client = {
		getSession: vi.fn().mockResolvedValue({ session: { desiredState: "running", runtimeConnected: true, isTerminated: false } }),
		getAgentBrowserStats: vi.fn().mockResolvedValue({ tabs: [] }),
		getAgentBrowserSocketRequest: vi.fn().mockResolvedValue({ url: "wss://cloud.example/api/cloud/v1/orgs/org/sessions/session/agent-browser/ws/cdp", headers: { Authorization: "Bearer secret" } }),
	};
	const source = createCloudPreviewSource({ client, orgId: "org", scopeKey: "deployment/account/org", isCurrent: () => current, WebSocketImpl: NativeSocket as unknown as typeof WebSocket });
	const handlers = { onStatus: vi.fn(), onState: vi.fn(), onFrame: vi.fn() };
	const live = source.createLive("session", handlers);
	return { client, source, handlers, live, invalidate: () => { current = false; } };
}
afterEach(() => { NativeSocket.instances = []; vi.useRealTimers(); vi.unstubAllGlobals(); });

describe("scoped Cloud live browser", () => {
	it("probes lifecycle and stats before opening an authenticated native socket", async () => {
		const { live, client, handlers } = setup(); await live.connect();
		const socket = NativeSocket.instances[0];
		expect(client.getSession).toHaveBeenCalledWith("org", "session", expect.objectContaining({ signal: expect.any(AbortSignal) }));
		expect(client.getAgentBrowserStats).toHaveBeenCalledTimes(1);
		expect(socket.url).not.toContain("secret");
		expect(socket.options).toEqual({ headers: { Authorization: "Bearer secret" } });
		socket.onopen?.(); expect(socket.sent[0]).toMatchObject({ t: "hello", embed: false });
		expect(handlers.onStatus).toHaveBeenLastCalledWith("open");
		live.close(); expect(socket.closed).toBe(true);
	});

	it.each([{ desiredState: "paused" }, { desiredState: "terminated" }, { runtimeConnected: false }, { isTerminated: true }])("never attaches to unavailable lifecycle %j", async (change) => {
		const { live, client, handlers } = setup();
		client.getSession.mockResolvedValue({ session: { desiredState: "running", runtimeConnected: true, isTerminated: false, ...change } });
		await live.connect(); expect(NativeSocket.instances).toHaveLength(0); expect(client.getAgentBrowserStats).not.toHaveBeenCalled();
		expect(handlers.onStatus).toHaveBeenLastCalledWith("error", expect.stringMatching(/resume|reconnect|running/i));
	});

	it("does not start a browser that stats explicitly says is stopped", async () => {
		const { live, client, handlers } = setup(); client.getAgentBrowserStats.mockResolvedValue({ running: false });
		await live.connect(); expect(NativeSocket.instances).toHaveLength(0); expect(client.getAgentBrowserSocketRequest).not.toHaveBeenCalled();
		expect(handlers.onStatus).toHaveBeenLastCalledWith("error", expect.stringMatching(/agent.*open/i));
	});

	it("honors relay Retry-After across explicit attempts without retrying or resuming", async () => {
		vi.useFakeTimers();
		const { live, client, source, handlers } = setup();
		client.getAgentBrowserStats.mockRejectedValue(new CloudApiError(409, { error: "Conflict", code: "AGENT_BROWSER_UNAVAILABLE", message: "Unavailable", requestId: "r" }, 30));
		await live.connect(); await source.createLive("session", handlers).connect();
		expect(client.getAgentBrowserStats).toHaveBeenCalledTimes(1);
		expect(handlers.onStatus).toHaveBeenLastCalledWith("error", expect.stringMatching(/30 seconds/));
		await vi.advanceTimersByTimeAsync(30000); await source.createLive("session", handlers).connect();
		expect(client.getAgentBrowserStats).toHaveBeenCalledTimes(2); expect(NativeSocket.instances).toHaveLength(0);
	});

	it.each([401, 403, 404])("stops honestly on HTTP %s without an automatic reconnect", async (status) => {
		const { live, client, handlers } = setup(); client.getAgentBrowserStats.mockRejectedValue(new CloudApiError(status, { error: "Unavailable", code: "not_found", message: "secret service https://internal.example", requestId: "r" }));
		await live.connect(); expect(NativeSocket.instances).toHaveLength(0);
		expect(handlers.onStatus.mock.lastCall?.[1]).not.toContain("secret");
		expect(client.getAgentBrowserStats).toHaveBeenCalledTimes(1);
	});

	it.each(["getSession", "getAgentBrowserStats", "getAgentBrowserSocketRequest"] as const)("fences account changes during %s", async (method) => {
		const { live, client, invalidate, handlers } = setup(); let resolve!: (value: unknown) => void;
		client[method].mockReturnValue(new Promise((done) => { resolve = done; }));
		const connecting = live.connect(); await Promise.resolve(); await Promise.resolve();
		invalidate(); resolve(method === "getSession" ? { session: { desiredState: "running", runtimeConnected: true } } : method === "getAgentBrowserStats" ? {} : { url: "wss://cloud.example/ws", headers: { Authorization: "Bearer secret" } });
		await connecting; expect(NativeSocket.instances).toHaveLength(0); expect(handlers.onStatus).not.toHaveBeenCalledWith("open");
	});

	it("acknowledges accepted frames and rejects callbacks, inputs and late binary decode after invalidation", async () => {
		const { live, source, handlers } = setup(); await live.connect(); const socket = NativeSocket.instances[0]; socket.onopen?.();
		const jpeg = Uint8Array.from([255,216,255,192,0,11,8,1,104,2,128,1,1,17,0,255,217]);
		const header = new TextEncoder().encode('{"seq":7,"ts":123}'); const frame = new Uint8Array(4 + header.length + jpeg.length);
		new DataView(frame.buffer).setUint32(0, header.length); frame.set(header, 4); frame.set(jpeg, 4 + header.length);
		socket.onmessage?.({ data: frame.buffer }); await Promise.resolve();
		expect(handlers.onFrame).toHaveBeenCalledTimes(1); expect(socket.sent.at(-1)).toEqual({ t: "ack", seq: 7 });
		let resolve!: (value: ArrayBuffer) => void; const late = new Blob(); late.arrayBuffer = () => new Promise((done) => { resolve = done; });
		socket.onmessage?.({ data: late }); source.dispose?.(); resolve(frame.buffer); await Promise.resolve();
		live.send({ type: "navigate", payload: { action: "reload" } }); socket.onmessage?.({ data: '{"t":"url","url":"https://example.com"}' });
		expect(socket.closed).toBe(true); expect(handlers.onFrame).toHaveBeenCalledTimes(1); expect(handlers.onState).not.toHaveBeenCalled(); expect(socket.sent).toHaveLength(2);
	});

	it("rejects invalid source and session identifiers before requests", async () => {
		const { source, handlers, client } = setup(); await source.createLive("../another", handlers).connect();
		expect(client.getSession).not.toHaveBeenCalled(); expect(NativeSocket.instances).toHaveLength(0);
	});
	it("allows a fresh transport after effect cleanup while the same source lease remains current", async () => {
		const { source, live, handlers } = setup(); await live.connect(); source.dispose?.();
		await source.createLive("session", handlers).connect();
		expect(NativeSocket.instances).toHaveLength(2);
		expect(NativeSocket.instances[0].closed).toBe(true);
		expect(NativeSocket.instances[1].closed).toBe(false);
	});
	it("validates a native WebSocket URL without relying on React Native's HTTP-only URL getters", async () => {
		const { live } = setup();
		vi.stubGlobal("URL", class { constructor() { throw new Error("Unsupported native URL getter"); } });
		await live.connect(); expect(NativeSocket.instances).toHaveLength(1);
	});
	it.each([null, [], "running"])("rejects malformed stats %j without starting a browser", async (stats) => {
		const { live, client } = setup(); client.getAgentBrowserStats.mockResolvedValue(stats);
		await live.connect(); expect(NativeSocket.instances).toHaveLength(0);
	});
});
