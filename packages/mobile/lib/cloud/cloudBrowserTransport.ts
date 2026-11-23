import { toViewState, type BrowserCapabilities, type BrowserCommand, type BrowserControl, type BrowserDialog, type BrowserFrame, type BrowserTransport, type BrowserTransportHandlers, type BrowserViewState, type FocusedEditable } from "../browserTransport";
import { CLOUD_API_PREFIX, cloudWebSocketBase } from "./config";

/**
 * A Cloud session's agent browser, streamed from the sandbox's browser host
 * through the control plane's relay. It speaks the host's viewer protocol
 * (ao-control-plane internal/agentbrowser/host/viewer.go): JSON messages keyed
 * by `t`, and binary frames of `uint32 BE headerLen` + header JSON + JPEG.
 */

type SocketLike = {
	readyState: number;
	binaryType: string;
	send(data: string): void;
	close(): void;
	onopen: (() => void) | null;
	onclose: (() => void) | null;
	onerror: (() => void) | null;
	onmessage: ((event: { data: unknown }) => void) | null;
};

export type CloudBrowserTransportOptions = {
	baseUrl: string;
	orgId: string;
	sessionId: string;
	getToken(): Promise<string | null>;
	/** Defaults to React Native's WebSocket, which takes headers as a third argument. */
	createSocket?(url: string, headers: Record<string, string>): SocketLike;
	/** App foreground/background changes; the stream closes in the background so the sandbox's browser can idle out. */
	subscribeAppState?(listener: (state: string) => void): () => void;
};

const OPEN = 1;
// The host's own viewer pings every 2 s. Without traffic a still page sends
// nothing after its first frame, and a proxy on the way closes the socket as idle.
const PING_MS = 5_000;
// The host's own defaults: the phone shows the desktop layout and doesn't
// resize the agent's page to its own screen (phone layout is a later option).
const HELLO = { t: "hello", w: 1280, h: 800, dpr: 1, quality: 60, maxInFlight: 0 };
const KEY_CODES: Record<string, number> = {
	Backspace: 8, Tab: 9, Enter: 13, Escape: 27, Space: 32, " ": 32,
	PageUp: 33, PageDown: 34, End: 35, Home: 36, ArrowLeft: 37, ArrowUp: 38, ArrowRight: 39, ArrowDown: 40, Delete: 46,
};
const EDITABLE_KINDS = new Set(["text", "password", "email", "number", "url", "search", "tel", "textarea", "contenteditable"]);
const DIALOG_KINDS = new Set(["alert", "confirm", "prompt", "beforeunload", "auth"]);

export function cloudBrowserURL(baseUrl: string, orgId: string, sessionId: string): string {
	return `${cloudWebSocketBase(baseUrl.replace(/\/+$/, ""))}${CLOUD_API_PREFIX}/orgs/${encodeURIComponent(orgId)}/sessions/${encodeURIComponent(sessionId)}/agent-browser/ws/cdp`;
}

/** A JPEG's pixel size from its start-of-frame marker, or null if it has none. */
export function jpegSize(bytes: Uint8Array): { width: number; height: number } | null {
	if (bytes.length < 4 || bytes[0] !== 0xff || bytes[1] !== 0xd8) return null;
	let offset = 2;
	while (offset + 4 <= bytes.length) {
		if (bytes[offset] !== 0xff) return null;
		const marker = bytes[offset + 1];
		const length = (bytes[offset + 2] << 8) | bytes[offset + 3];
		// SOF0–SOF15, less DHT (C4), JPG (C8) and DAC (CC).
		if (marker >= 0xc0 && marker <= 0xcf && marker !== 0xc4 && marker !== 0xc8 && marker !== 0xcc) {
			if (offset + 9 > bytes.length) return null;
			const height = (bytes[offset + 5] << 8) | bytes[offset + 6];
			const width = (bytes[offset + 7] << 8) | bytes[offset + 8];
			return width > 0 && height > 0 ? { width, height } : null;
		}
		if (length < 2) return null;
		offset += 2 + length;
	}
	return null;
}

type FrameHeader = { seq: number; ts?: number; inputSeq?: number; q?: number; crop?: number[]; meta?: { deviceWidth?: number; deviceHeight?: number } };

/** A viewer frame as the pane's BrowserFrame, plus the sequence to ack. */
export function parseViewerFrame(buffer: ArrayBuffer, fallback = { width: 1280, height: 800 }): { seq: number; frame: BrowserFrame } {
	if (buffer.byteLength < 4) throw new Error("Browser frame is truncated");
	const headerLength = new DataView(buffer).getUint32(0);
	if (4 + headerLength > buffer.byteLength) throw new Error("Browser frame is truncated");
	const header = JSON.parse(new TextDecoder().decode(new Uint8Array(buffer, 4, headerLength))) as FrameHeader;
	if (typeof header.seq !== "number") throw new Error("Browser frame has no sequence");
	// The JPEG's own size isn't known without decoding it; the pane needs only
	// its shape, which the crop (fullscreen) or the page's CSS size gives.
	const [cropWidth, cropHeight] = header.crop ?? [];
	const width = Math.round(cropWidth || header.meta?.deviceWidth || fallback.width);
	const height = Math.round(cropHeight || header.meta?.deviceHeight || fallback.height);
	const jpeg = buffer.slice(4 + headerLength);
	const image = cropWidth && cropHeight ? jpegSize(new Uint8Array(jpeg)) : null;
	const crop = image && cropWidth && cropHeight && (cropWidth < image.width || cropHeight < image.height)
		? { width: Math.min(cropWidth, image.width), height: Math.min(cropHeight, image.height), imageWidth: image.width, imageHeight: image.height }
		: undefined;
	return {
		seq: header.seq,
		frame: {
			sequence: BigInt(Math.trunc(header.seq)),
			capturedAtMs: BigInt(Math.trunc(header.ts ?? 0)),
			width: Math.max(1, width),
			height: Math.max(1, height),
			jpeg,
			...(crop ? { crop } : {}),
		},
	};
}

type HostTab = { id: string; url: string; title: string };

export class CloudBrowserTransport implements BrowserTransport {
	readonly capabilities: BrowserCapabilities = { text: true, keys: true, viewport: true, quality: true, control: true, dialogs: true };
	private socket: SocketLike | null = null;
	private closed = false;
	// Bumped by every connect, close and trip to the background: a dial that
	// was waiting for the token gives up if its generation has passed.
	private generation = 0;
	private inputSeq = 0;
	private unsubscribe: (() => void) | null = null;
	private pinger: ReturnType<typeof setInterval> | null = null;
	private handlers: BrowserTransportHandlers = { onStatus() {}, onState() {}, onFrame() {} };
	private tabs: HostTab[] = [];
	private state = {
		url: "", loading: false, canGoBack: false, canGoForward: false, activeTabId: "",
		width: HELLO.w, height: HELLO.h, control: "none" as BrowserControl, focusedEditable: null as FocusedEditable,
	};

	constructor(private readonly options: CloudBrowserTransportOptions) {}

	connect(handlers: BrowserTransportHandlers): void {
		this.handlers = handlers;
		this.closed = false;
		const generation = ++this.generation;
		handlers.onStatus("connecting");
		this.unsubscribe?.();
		this.unsubscribe = this.options.subscribeAppState?.((next) => {
			if (next !== "background" || this.closed) return;
			this.generation++;
			this.drop();
			handlers.onStatus("error", "The browser stream paused while the app was in the background.");
		}) ?? null;
		void this.dial(generation);
	}

	private async dial(generation: number): Promise<void> {
		let token: string | null;
		try {
			token = await this.options.getToken();
		} catch {
			token = null;
		}
		if (this.closed || generation !== this.generation) return;
		if (!token) {
			this.handlers.onStatus("error", "Sign in to Orchestrator Cloud first.");
			return;
		}
		const headers = { Authorization: `Bearer ${token}` };
		const url = cloudBrowserURL(this.options.baseUrl, this.options.orgId, this.options.sessionId);
		let socket: SocketLike;
		try {
			socket = this.options.createSocket
				? this.options.createSocket(url, headers)
				: new (WebSocket as unknown as new (url: string, protocols: undefined, options: { headers: Record<string, string> }) => SocketLike)(url, undefined, { headers });
		} catch (error) {
			this.handlers.onStatus("error", error instanceof Error ? error.message : "The browser stream could not connect.");
			return;
		}
		socket.binaryType = "arraybuffer";
		this.socket = socket;
		socket.onopen = () => {
			if (this.socket !== socket) return;
			socket.send(JSON.stringify(HELLO));
			this.stopPinging();
			this.pinger = setInterval(() => {
				if (this.socket === socket && socket.readyState === OPEN) socket.send(JSON.stringify({ t: "ping", c: Date.now() }));
			}, PING_MS);
			this.handlers.onStatus("open");
		};
		// A refused handshake errors and then closes: the close keeps the error's words.
		let failed = false;
		socket.onerror = () => {
			if (this.socket !== socket) return;
			failed = true;
			this.handlers.onStatus("error", "Couldn't reach this session's browser. It starts when the agent first uses it.");
		};
		socket.onclose = () => {
			if (this.socket !== socket) return;
			this.socket = null;
			this.stopPinging();
			if (this.closed) this.handlers.onStatus("closed");
			else if (!failed) this.handlers.onStatus("error", "The browser stream ended. Try reconnecting.");
		};
		socket.onmessage = (event) => { void this.receive(socket, event.data); };
	}

	send(command: BrowserCommand): void {
		const message = this.encode(command);
		if (message && this.socket?.readyState === OPEN) this.socket.send(JSON.stringify(message));
	}

	close(): void {
		this.closed = true;
		this.generation++;
		this.unsubscribe?.();
		this.unsubscribe = null;
		this.drop();
	}

	private stopPinging(): void {
		if (this.pinger) clearInterval(this.pinger);
		this.pinger = null;
	}

	private drop(): void {
		this.stopPinging();
		const socket = this.socket;
		this.socket = null;
		socket?.close();
	}

	private encode(command: BrowserCommand): Record<string, unknown> | null {
		switch (command.type) {
			case "input": return this.encodeInput(command.payload);
			case "navigate": {
				const { action, url } = command.payload;
				if (action === "open") return url ? { t: "nav", url } : null;
				return { t: action };
			}
			case "tab": {
				const { action, tabId } = command.payload;
				if (action === "new") return { t: "newtab" };
				if (!tabId) return null;
				return action === "select" ? { t: "tab", id: tabId } : { t: "closetab", id: tabId };
			}
			// Null restores the page's own size, the host's default.
			case "viewport": return command.payload
				? { t: "resize", w: command.payload.width, h: command.payload.height, dpr: command.payload.dpr }
				: { t: "resize", w: HELLO.w, h: HELLO.h, dpr: HELLO.dpr };
			case "quality": return { t: "quality", quality: command.payload.quality };
			// The host has no explicit take: the person's input takes it.
			case "control": return command.payload.action === "release" ? { t: "release" } : null;
			case "dialog": return { t: "dialog", accept: command.payload.accept, promptText: command.payload.promptText ?? "" };
		}
	}

	private encodeInput(payload: Record<string, unknown>): Record<string, unknown> | null {
		const seq = ++this.inputSeq;
		const at = (x: unknown, y: unknown) => ({ x: Number(x) * this.state.width, y: Number(y) * this.state.height });
		switch (payload.kind) {
			case "pointer": {
				const type = payload.phase === "down" ? "mousePressed" : payload.phase === "up" ? "mouseReleased" : "mouseMoved";
				const button = typeof payload.button === "string" ? payload.button : "left";
				return { t: "mouse", type, ...at(payload.x, payload.y), button: type === "mouseMoved" ? "none" : button, buttons: type === "mousePressed" ? 1 : 0, clickCount: type === "mouseMoved" ? 0 : 1, mods: 0, seq };
			}
			case "wheel":
				// The pane's deltas use Electron's drag sign; CDP's wheel is the opposite.
				return { t: "mouse", type: "mouseWheel", ...at(0.5, 0.5), dx: -Number(payload.deltaX), dy: -Number(payload.deltaY), button: "none", buttons: 0, mods: 0, seq };
			case "key": {
				const key = String(payload.key ?? "");
				const code = String(payload.code ?? key);
				return { t: "key", type: payload.phase === "up" ? "keyUp" : "rawKeyDown", key, code, keyCode: KEY_CODES[key] ?? 0, mods: 0, seq };
			}
			case "text": {
				const text = typeof payload.value === "string" ? payload.value : "";
				return text ? { t: "text", text, seq } : null;
			}
			default: return null;
		}
	}

	private async receive(socket: SocketLike, data: unknown): Promise<void> {
		if (this.socket !== socket) return;
		if (typeof data === "string") {
			let message: Record<string, unknown>;
			try { message = JSON.parse(data) as Record<string, unknown>; } catch { return; }
			this.onMessage(message);
			return;
		}
		const buffer = data instanceof ArrayBuffer ? data : typeof Blob !== "undefined" && data instanceof Blob ? await data.arrayBuffer() : null;
		if (!buffer || this.socket !== socket) return;
		let parsed: ReturnType<typeof parseViewerFrame>;
		try {
			parsed = parseViewerFrame(buffer, { width: this.state.width, height: this.state.height });
		} catch {
			return;
		}
		// Acked on arrival: the host keeps only its newest frame, so the ack
		// paces it to what reaches the phone.
		if (socket.readyState === OPEN) socket.send(JSON.stringify({ t: "ack", seq: parsed.seq }));
		this.handlers.onFrame(parsed.frame);
	}

	private onMessage(message: Record<string, unknown>): void {
		const s = this.state;
		switch (message.t) {
			case "tabs":
				this.tabs = Array.isArray(message.tabs) ? (message.tabs as HostTab[]).map((tab) => ({ id: String(tab.id), url: String(tab.url ?? ""), title: String(tab.title ?? "") })) : [];
				if (!s.url) s.url = this.tabs.find((tab) => tab.id === s.activeTabId)?.url ?? "";
				break;
			case "active":
				s.activeTabId = String(message.id ?? "");
				s.url = this.tabs.find((tab) => tab.id === s.activeTabId)?.url ?? "";
				s.loading = false;
				break;
			case "url": s.url = String(message.url ?? ""); break;
			case "loading": s.loading = Boolean(message.on); break;
			case "history": s.canGoBack = Boolean(message.back); s.canGoForward = Boolean(message.fwd); break;
			case "viewport":
				if (Number(message.w) > 0 && Number(message.h) > 0) { s.width = Number(message.w); s.height = Number(message.h); }
				break;
			case "control": s.control = message.owner === "agent" || message.owner === "human" ? message.owner : "none"; break;
			case "focus": {
				const kind = String(message.kind ?? "");
				s.focusedEditable = EDITABLE_KINDS.has(kind)
					? { kind: kind as NonNullable<FocusedEditable>["kind"], ...(kind !== "password" && typeof message.value === "string" ? { value: message.value } : {}) }
					: null;
				break;
			}
			case "dialog": {
				const kind = String(message.type ?? "");
				if (DIALOG_KINDS.has(kind)) this.handlers.onDialog?.({ kind: kind as BrowserDialog["kind"], message: String(message.message ?? "") });
				return;
			}
			default: return;
		}
		this.handlers.onState(this.viewState());
	}

	private viewState(): BrowserViewState {
		const s = this.state;
		const active = this.tabs.find((tab) => tab.id === s.activeTabId);
		return toViewState({
			...s,
			title: active?.title ?? "",
			tabs: this.tabs.map((tab) => ({ ...tab, active: tab.id === s.activeTabId })),
		});
	}
}
