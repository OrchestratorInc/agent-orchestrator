import { describe, expect, it, vi } from "vitest";
import type { BrowserViewState } from "../browserTransport";
import { CloudBrowserTransport, cloudBrowserURL, parseViewerFrame } from "./cloudBrowserTransport";

class FakeSocket {
	static OPEN = 1;
	readyState = 0;
	binaryType = "";
	sent: unknown[] = [];
	closed = false;
	onopen: (() => void) | null = null;
	onclose: (() => void) | null = null;
	onerror: (() => void) | null = null;
	onmessage: ((event: { data: unknown }) => void) | null = null;
	constructor(readonly url: string, readonly headers: Record<string, string>) {}
	send(data: string) { this.sent.push(JSON.parse(data)); }
	close() { this.closed = true; this.readyState = 3; this.onclose?.(); }
	open() { this.readyState = 1; this.onopen?.(); }
	message(m: object) { this.onmessage?.({ data: JSON.stringify(m) }); }
}

function viewerFrame(header: object, jpeg = [0xff, 0xd8, 0xff]): ArrayBuffer {
	const head = new TextEncoder().encode(JSON.stringify(header));
	const buffer = new ArrayBuffer(4 + head.length + jpeg.length);
	new DataView(buffer).setUint32(0, head.length);
	new Uint8Array(buffer, 4).set(head);
	new Uint8Array(buffer, 4 + head.length).set(jpeg);
	return buffer;
}

async function connected() {
	let socket!: FakeSocket;
	const transport = new CloudBrowserTransport({
		baseUrl: "https://api.example.dev",
		orgId: "org 1",
		sessionId: "s/1",
		getToken: async () => "tok",
		createSocket: (url, headers) => (socket = new FakeSocket(url, headers)),
	});
	const handlers = { onStatus: vi.fn(), onState: vi.fn(), onFrame: vi.fn(), onDialog: vi.fn() };
	transport.connect(handlers);
	await vi.waitFor(() => expect(socket).toBeDefined());
	socket.open();
	return { transport, socket, handlers, lastState: () => handlers.onState.mock.lastCall?.[0] as BrowserViewState };
}

describe("cloud browser transport", () => {
	it("dials the relay with the bearer in a header, never on the URL", async () => {
		const { socket } = await connected();
		expect(socket.url).toBe("wss://api.example.dev/api/cloud/v1/orgs/org%201/sessions/s%2F1/agent-browser/ws/cdp");
		expect(socket.headers).toEqual({ Authorization: "Bearer tok" });
		expect(cloudBrowserURL("http://localhost:8080", "o", "s")).toBe("ws://localhost:8080/api/cloud/v1/orgs/o/sessions/s/agent-browser/ws/cdp");
	});

	it("says hello first, at the host's default desktop size", async () => {
		const { socket, handlers } = await connected();
		expect(socket.sent[0]).toMatchObject({ t: "hello", w: 1280, h: 800, dpr: 1 });
		expect(handlers.onStatus).toHaveBeenLastCalledWith("open");
	});

	it("parses viewer frame header", () => {
		const frame = parseViewerFrame(viewerFrame({ seq: 7, ts: 1700, inputSeq: 2, q: 60, meta: { deviceWidth: 1280, deviceHeight: 720 } }, [1, 2, 3, 4]));
		expect(frame.seq).toBe(7);
		expect(frame.frame).toMatchObject({ sequence: 7n, capturedAtMs: 1700n, width: 1280, height: 720 });
		expect([...new Uint8Array(frame.frame.jpeg)]).toEqual([1, 2, 3, 4]);
		expect(() => parseViewerFrame(new ArrayBuffer(2))).toThrow();
		expect(() => parseViewerFrame(viewerFrame({ seq: 1 }).slice(0, 6))).toThrow();
	});

	it("acks every frame", async () => {
		const { socket, handlers } = await connected();
		socket.onmessage?.({ data: viewerFrame({ seq: 1, ts: 1, meta: { deviceWidth: 1280, deviceHeight: 800 } }) });
		socket.onmessage?.({ data: viewerFrame({ seq: 2, ts: 2, meta: { deviceWidth: 1280, deviceHeight: 800 } }) });
		await vi.waitFor(() => expect(handlers.onFrame).toHaveBeenCalledTimes(2));
		expect(socket.sent.filter((m) => (m as { t: string }).t === "ack")).toEqual([{ t: "ack", seq: 1 }, { t: "ack", seq: 2 }]);
	});

	it("folds the host's messages into one view state", async () => {
		const { socket, lastState, handlers } = await connected();
		socket.message({ t: "tabs", tabs: [{ id: "a", url: "https://example.com/", title: "Example" }, { id: "b", url: "about:blank", title: "" }] });
		socket.message({ t: "active", id: "a" });
		socket.message({ t: "url", url: "https://example.com/x" });
		socket.message({ t: "loading", on: true });
		socket.message({ t: "history", back: true, fwd: false });
		socket.message({ t: "viewport", w: 1024, h: 768, mine: false });
		socket.message({ t: "control", owner: "agent" });
		socket.message({ t: "focus", kind: "password" });
		expect(lastState()).toEqual({
			url: "https://example.com/x", title: "Example", loading: true, canGoBack: true, canGoForward: false,
			activeTabId: "a",
			tabs: [{ id: "a", url: "https://example.com/", title: "Example", active: true }, { id: "b", url: "about:blank", title: "", active: false }],
			width: 1024, height: 768, control: "agent", focusedEditable: { kind: "password" },
		});
		socket.message({ t: "focus", kind: "" });
		expect(lastState().focusedEditable).toBeNull();
		socket.message({ t: "dialog", type: "confirm", message: "Leave?" });
		expect(handlers.onDialog).toHaveBeenCalledWith({ kind: "confirm", message: "Leave?" });
	});

	it("maps commands", async () => {
		const { transport, socket } = await connected();
		socket.message({ t: "viewport", w: 1000, h: 500 });
		socket.sent.length = 0;
		transport.send({ type: "input", payload: { kind: "pointer", phase: "down", x: 0.5, y: 0.2, button: "left" } });
		transport.send({ type: "input", payload: { kind: "pointer", phase: "up", x: 0.5, y: 0.2, button: "left" } });
		transport.send({ type: "input", payload: { kind: "wheel", deltaX: 0, deltaY: 30 } });
		transport.send({ type: "input", payload: { kind: "key", phase: "down", key: "Enter", code: "Enter" } });
		transport.send({ type: "input", payload: { kind: "key", phase: "up", key: "Enter", code: "Enter" } });
		transport.send({ type: "input", payload: { kind: "text", value: "hé🙂" } });
		transport.send({ type: "navigate", payload: { action: "open", url: "example.com" } });
		transport.send({ type: "navigate", payload: { action: "back" } });
		transport.send({ type: "navigate", payload: { action: "reload" } });
		transport.send({ type: "navigate", payload: { action: "stop" } });
		transport.send({ type: "tab", payload: { action: "new" } });
		transport.send({ type: "tab", payload: { action: "select", tabId: "b" } });
		transport.send({ type: "tab", payload: { action: "close", tabId: "b" } });
		transport.send({ type: "viewport", payload: { width: 390, height: 700, dpr: 3, mobile: true } });
		transport.send({ type: "quality", payload: { quality: 50, maxFps: 15 } });
		transport.send({ type: "control", payload: { action: "release" } });
		transport.send({ type: "dialog", payload: { accept: true, promptText: "x" } });
		const strip = (m: object) => { const { seq: _seq, ...rest } = m as { seq?: number }; return rest; };
		expect(socket.sent.map((m) => strip(m as object))).toEqual([
			{ t: "mouse", type: "mousePressed", x: 500, y: 100, button: "left", buttons: 1, clickCount: 1, mods: 0 },
			{ t: "mouse", type: "mouseReleased", x: 500, y: 100, button: "left", buttons: 0, clickCount: 1, mods: 0 },
			// Electron's wheel sign is a drag's; CDP's is a scroll's.
			{ t: "mouse", type: "mouseWheel", x: 500, y: 250, dx: 0, dy: -30, button: "none", buttons: 0, mods: 0 },
			{ t: "key", type: "rawKeyDown", key: "Enter", code: "Enter", keyCode: 13, mods: 0 },
			{ t: "key", type: "keyUp", key: "Enter", code: "Enter", keyCode: 13, mods: 0 },
			{ t: "text", text: "hé🙂" },
			{ t: "nav", url: "example.com" },
			{ t: "back" },
			{ t: "reload" },
			{ t: "stop" },
			{ t: "newtab" },
			{ t: "tab", id: "b" },
			{ t: "closetab", id: "b" },
			{ t: "resize", w: 390, h: 700, dpr: 3 },
			{ t: "quality", quality: 50 },
			{ t: "release" },
			{ t: "dialog", accept: true, promptText: "x" },
		]);
		// Input carries an increasing sequence the host echoes in frames.
		const seqs = socket.sent.slice(0, 6).map((m) => (m as { seq: number }).seq);
		expect(seqs).toEqual([...seqs].sort((a, b) => a - b));
		expect(new Set(seqs).size).toBe(6);
	});

	it("closes on background", async () => {
		const appState = { listener: null as ((s: string) => void) | null, removed: false };
		let socket!: FakeSocket;
		const transport = new CloudBrowserTransport({
			baseUrl: "https://api.example.dev", orgId: "o", sessionId: "s", getToken: async () => "tok",
			createSocket: (url, headers) => (socket = new FakeSocket(url, headers)),
			subscribeAppState: (listener) => { appState.listener = listener; return () => { appState.removed = true; }; },
		});
		const onStatus = vi.fn();
		transport.connect({ onStatus, onState: vi.fn(), onFrame: vi.fn() });
		await vi.waitFor(() => expect(socket).toBeDefined());
		socket.open();
		appState.listener!("background");
		expect(socket.closed).toBe(true);
		expect(onStatus).toHaveBeenLastCalledWith("error", "The browser stream paused while the app was in the background.");
		transport.close();
		expect(appState.removed).toBe(true);
	});

	it("reports a missing sign-in without dialing", async () => {
		const createSocket = vi.fn();
		const transport = new CloudBrowserTransport({ baseUrl: "https://api.example.dev", orgId: "o", sessionId: "s", getToken: async () => null, createSocket });
		const onStatus = vi.fn();
		transport.connect({ onStatus, onState: vi.fn(), onFrame: vi.fn() });
		await vi.waitFor(() => expect(onStatus).toHaveBeenLastCalledWith("error", "Sign in to Orchestrator Cloud first."));
		expect(createSocket).not.toHaveBeenCalled();
	});
});
