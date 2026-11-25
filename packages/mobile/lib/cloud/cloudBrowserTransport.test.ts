import { describe, expect, it, vi } from "vitest";
import type { BrowserViewState } from "../browserTransport";
import { CloudBrowserTransport, cloudBrowserURL, jpegSize, parseViewerFrame } from "./cloudBrowserTransport";

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

async function connected(
	upload = vi.fn(async (_url: string, _headers: Record<string, string>, _file: { uri: string; name: string; type?: string }) => undefined),
	randomBytes: (count: number) => Uint8Array = (count) => new Uint8Array(count).map((_, i) => i * 17),
) {
	let socket!: FakeSocket;
	const transport = new CloudBrowserTransport({
		baseUrl: "https://api.example.dev",
		orgId: "org 1",
		sessionId: "s/1",
		getToken: async () => "tok",
		createSocket: (url, headers) => (socket = new FakeSocket(url, headers)),
		upload,
		randomBytes,
	});
	const handlers = { onStatus: vi.fn(), onState: vi.fn(), onFrame: vi.fn(), onDialog: vi.fn(), onDialogClosed: vi.fn(), onDownload: vi.fn() };
	transport.connect(handlers);
	await vi.waitFor(() => expect(socket).toBeDefined());
	socket.open();
	return { transport, socket, handlers, upload, lastState: () => handlers.onState.mock.lastCall?.[0] as BrowserViewState };
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

	it("pings the host while open, so a still page doesn't look idle to proxies", async () => {
		vi.useFakeTimers();
		try {
			const { transport, socket } = await connected();
			const pings = () => socket.sent.filter((m) => (m as { t: string }).t === "ping").length;
			expect(pings()).toBe(0);
			vi.advanceTimersByTime(5_000);
			expect(pings()).toBe(1);
			vi.advanceTimersByTime(10_000);
			expect(pings()).toBe(3);
			transport.close();
			vi.advanceTimersByTime(20_000);
			expect(pings()).toBe(3);
		} finally {
			vi.useRealTimers();
		}
	});

	it("keeps the connection error when the failed socket then closes", async () => {
		const { socket, handlers } = await connected();
		socket.onerror?.();
		socket.onclose?.();
		expect(handlers.onStatus).toHaveBeenLastCalledWith("error", "Couldn't reach this session's browser. It starts when the agent first uses it.");
	});

	it("parses viewer frame header", () => {
		const frame = parseViewerFrame(viewerFrame({ seq: 7, ts: 1700, inputSeq: 2, q: 60, meta: { deviceWidth: 1280, deviceHeight: 720 } }, [1, 2, 3, 4]));
		expect(frame.seq).toBe(7);
		expect(frame.frame).toMatchObject({ sequence: 7n, capturedAtMs: 1700n, width: 1280, height: 720 });
		expect([...new Uint8Array(frame.frame.jpeg)]).toEqual([1, 2, 3, 4]);
		expect(() => parseViewerFrame(new ArrayBuffer(2))).toThrow();
		expect(() => parseViewerFrame(viewerFrame({ seq: 1 }).slice(0, 6))).toThrow();
	});

	it("carries a fullscreen crop as the page part of a larger JPEG", () => {
		// A JPEG whose SOF0 says 1920×1080: SOI, then SOF0 with height, width.
		const jpeg = [0xff, 0xd8, 0xff, 0xe0, 0x00, 0x04, 0x00, 0x00, 0xff, 0xc0, 0x00, 0x11, 0x08, 0x04, 0x38, 0x07, 0x80, 0x03];
		const { frame } = parseViewerFrame(viewerFrame({ seq: 1, crop: [1280, 720], meta: { deviceWidth: 1280, deviceHeight: 720 } }, jpeg));
		expect(frame).toMatchObject({ width: 1280, height: 720, crop: { width: 1280, height: 720, imageWidth: 1920, imageHeight: 1080 } });
		// No crop, or one the JPEG can't be measured for, draws the whole image.
		expect(parseViewerFrame(viewerFrame({ seq: 2, meta: { deviceWidth: 1280, deviceHeight: 720 } }, jpeg)).frame.crop).toBeUndefined();
		expect(parseViewerFrame(viewerFrame({ seq: 3, crop: [1280, 720] }, [0xff, 0xd8])).frame.crop).toBeUndefined();
	});

	it("reads a JPEG's size from its frame header, skipping other markers", () => {
		expect(jpegSize(Uint8Array.from([0xff, 0xd8, 0xff, 0xdb, 0x00, 0x03, 0x00, 0xff, 0xc2, 0x00, 0x11, 0x08, 0x00, 0x64, 0x00, 0xc8]))).toEqual({ width: 200, height: 100 });
		expect(jpegSize(Uint8Array.from([0x00, 0x01]))).toBeNull();
		expect(jpegSize(Uint8Array.from([0xff, 0xd8, 0xff, 0xdb, 0x00, 0x40]))).toBeNull();
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

	it("doesn't dial after backgrounding while the token loads", async () => {
		let listener!: (state: string) => void;
		let resolveToken!: (token: string) => void;
		const createSocket = vi.fn();
		const transport = new CloudBrowserTransport({
			baseUrl: "https://api.example.dev", orgId: "o", sessionId: "s",
			getToken: () => new Promise((resolve) => { resolveToken = resolve; }),
			createSocket,
			subscribeAppState: (next) => { listener = next; return () => {}; },
		});
		transport.connect({ onStatus: vi.fn(), onState: vi.fn(), onFrame: vi.fn() });
		listener("background");
		resolveToken("tok");
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(createSocket).not.toHaveBeenCalled();
	});

	it("reports a missing sign-in without dialing", async () => {
		const createSocket = vi.fn();
		const transport = new CloudBrowserTransport({ baseUrl: "https://api.example.dev", orgId: "o", sessionId: "s", getToken: async () => null, createSocket });
		const onStatus = vi.fn();
		transport.connect({ onStatus, onState: vi.fn(), onFrame: vi.fn() });
		await vi.waitFor(() => expect(onStatus).toHaveBeenLastCalledWith("error", "Sign in to Orchestrator Cloud first."));
		expect(createSocket).not.toHaveBeenCalled();
	});

	describe("pages that ask things", () => {
		it("passes on the page's dialogs, closed or not", async () => {
			const { socket, handlers } = await connected();
			socket.message({ t: "dialog", type: "prompt", message: "Your name?", defaultPrompt: "Ada", url: "https://a.example/" });
			expect(handlers.onDialog).toHaveBeenLastCalledWith({ kind: "prompt", message: "Your name?", defaultPrompt: "Ada" });
			socket.message({ t: "dialog", type: "beforeunload", message: "" });
			expect(handlers.onDialog).toHaveBeenLastCalledWith({ kind: "beforeunload", message: "" });
			socket.message({ t: "dialogClosed" });
			expect(handlers.onDialogClosed).toHaveBeenCalled();
			// The page's words are capped: it writes whatever it likes.
			socket.message({ t: "dialog", type: "alert", message: "x".repeat(10_000) });
			expect((handlers.onDialog.mock.lastCall![0] as { message: string }).message.length).toBeLessThanOrEqual(1000);
			socket.message({ t: "dialog", type: "nonsense", message: "?" });
			expect(handlers.onDialog).toHaveBeenCalledTimes(3);
		});

		it("asks to sign in, and answers or cancels", async () => {
			const { socket, transport, handlers } = await connected();
			socket.message({ t: "auth", origin: "https://intranet.example", url: "https://intranet.example/x", realm: "Staff", failed: true });
			expect(handlers.onDialog).toHaveBeenLastCalledWith({ kind: "auth", origin: "https://intranet.example", realm: "Staff", failed: true });
			socket.sent.length = 0;
			transport.send({ type: "auth", payload: { origin: "https://intranet.example", username: "ada", password: "pw" } });
			transport.send({ type: "auth", payload: { origin: "https://intranet.example", cancel: true } });
			expect(socket.sent).toEqual([
				{ t: "authResp", origin: "https://intranet.example", username: "ada", password: "pw" },
				{ t: "authResp", origin: "https://intranet.example", cancel: true },
			]);
		});

		it("never logs what is typed to sign in", async () => {
			const logged: unknown[] = [];
			for (const method of ["log", "info", "warn", "error", "debug"] as const) vi.spyOn(console, method).mockImplementation((...args) => { logged.push(...args); });
			try {
				const { socket, transport } = await connected();
				socket.message({ t: "auth", origin: "https://a.example", realm: "", failed: false });
				transport.send({ type: "auth", payload: { origin: "https://a.example", username: "ada", password: "s3cret-pw" } });
				expect(JSON.stringify(logged)).not.toContain("s3cret");
			} finally { vi.restoreAllMocks(); }
		});

		it("offers a select's options and answers with the picked one", async () => {
			const { socket, transport, handlers } = await connected();
			socket.message({ t: "select", data: { id: "sel-1", options: [{ label: "Red" }, { label: "Green", disabled: true }, { label: "Blue" }], selected: 2, x: 10, y: 20 }, ctx: "S:3", dx: 0, dy: 0 });
			expect(handlers.onDialog).toHaveBeenLastCalledWith({
				kind: "select", id: "sel-1", ctx: "S:3", selected: 2,
				options: [{ label: "Red" }, { label: "Green", disabled: true }, { label: "Blue" }],
			});
			socket.sent.length = 0;
			transport.send({ type: "select", payload: { id: "sel-1", index: 0, ctx: "S:3" } });
			expect(socket.sent).toEqual([{ t: "selectResp", id: "sel-1", index: 0, ctx: "S:3" }]);
		});

		it("drops a select with no options and caps a long list", async () => {
			const { socket, handlers } = await connected();
			socket.message({ t: "select", data: { id: "x", options: [] }, ctx: "" });
			expect(handlers.onDialog).not.toHaveBeenCalled();
			socket.message({ t: "select", data: { id: "y", options: Array.from({ length: 5000 }, (_, i) => ({ label: `o${i}` })), selected: 0 }, ctx: "" });
			expect((handlers.onDialog.mock.lastCall![0] as { options: unknown[] }).options.length).toBeLessThanOrEqual(1000);
		});

		it("asks for files, uploads them over HTTP, then hands them to the page", async () => {
			const { socket, transport, handlers, upload } = await connected();
			socket.message({ t: "filechooser", mode: "selectMultiple", accept: "image/*", folder: false });
			expect(handlers.onDialog).toHaveBeenLastCalledWith({ kind: "filechooser", multiple: true, accept: "image/*", folder: false });
			socket.sent.length = 0;
			transport.send({ type: "files", payload: { folder: false, files: [{ uri: "file:///a.png", name: "a.png", type: "image/png" }, { uri: "file:///b b.jpg", name: "b b.jpg" }] } });
			await vi.waitFor(() => expect(socket.sent).toHaveLength(1));
			const sent = socket.sent[0] as { t: string; dir: string; names: string[]; folder: boolean };
			expect(sent).toMatchObject({ t: "upload", names: ["a.png", "b b.jpg"], folder: false });
			expect(sent.dir).toMatch(/^[0-9a-f]{24}$/);
			expect(upload).toHaveBeenCalledTimes(2);
			const [url, headers, file] = upload.mock.calls[1]!;
			expect(url).toBe(`https://api.example.dev/api/cloud/v1/orgs/org%201/sessions/s%2F1/agent-browser/upload?dir=${sent.dir}&name=b%20b.jpg`);
			expect(headers).toEqual({ Authorization: "Bearer tok" });
			expect(file).toEqual({ uri: "file:///b b.jpg", name: "b b.jpg" });
		});

		it("gives the page no files when an upload fails, and says so", async () => {
			const upload = vi.fn(async () => { throw new TypeError("network down"); });
			const { socket, transport, handlers } = await connected(upload);
			socket.sent.length = 0;
			transport.send({ type: "files", payload: { folder: false, files: [{ uri: "file:///a", name: "a" }] } });
			await vi.waitFor(() => expect(handlers.onStatus).toHaveBeenLastCalledWith("open", expect.stringContaining("a")));
			expect(socket.sent).toEqual([]);
		});

		it("says so when an upload can't even start", async () => {
			const { socket, transport, handlers, upload } = await connected(undefined, () => { throw new TypeError("no random source"); });
			socket.sent.length = 0;
			transport.send({ type: "files", payload: { folder: false, files: [{ uri: "file:///a", name: "a.png" }] } });
			await vi.waitFor(() => expect(handlers.onStatus).toHaveBeenLastCalledWith("open", "Couldn't send a.png to the page."));
			expect(upload).not.toHaveBeenCalled();
			expect(socket.sent).toEqual([]);
		});

		it("sends nothing for a chooser the person cancelled", async () => {
			const { socket, transport, upload } = await connected();
			socket.sent.length = 0;
			transport.send({ type: "files", payload: { folder: false, files: [] } });
			await Promise.resolve();
			expect(socket.sent).toEqual([]);
			expect(upload).not.toHaveBeenCalled();
		});

		it("lists downloads and fetches them through the relay", async () => {
			const { socket, transport, handlers } = await connected();
			socket.message({ t: "download", guid: "g-1", name: "report.pdf", size: 2048 });
			expect(handlers.onDownload).toHaveBeenCalledWith({ id: "g-1", name: "report.pdf", size: 2048 });
			await expect(transport.downloadRequest("g-1")).resolves.toEqual({
				url: "https://api.example.dev/api/cloud/v1/orgs/org%201/sessions/s%2F1/agent-browser/download/g-1",
				headers: { Authorization: "Bearer tok" },
			});
		});
	});
});
