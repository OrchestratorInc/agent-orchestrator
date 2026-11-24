import { ApiError, apiRequest } from "./api";
import { authHeaders, type ServerConfig } from "./config";
import { browserLiveURL, decodeBrowserFrame } from "./browserLiveProtocol";
import { commandSupported, toViewState, type BrowserCapabilities, type BrowserCommand, type BrowserDialog, type BrowserPickedFile, type BrowserTransport, type BrowserTransportHandlers } from "./browserTransport";

export { browserLiveURL, decodeBrowserFrame, type BrowserLiveFrame } from "./browserLiveProtocol";

export type BrowserLiveState = {
	url: string;
	title: string;
	loading: boolean;
	canGoBack: boolean;
	canGoForward: boolean;
	activeTabId: string;
	tabs: Array<{ id: string; title: string; url: string; active: boolean }>;
	width: number;
	height: number;
};

export type BrowserLiveCommand =
	| { type: "input"; payload: Record<string, unknown> }
	| { type: "navigate"; payload: { action: "open" | "back" | "forward" | "reload" | "stop"; url?: string } }
	| { type: "tab"; payload: { action: "new" | "select" | "close"; tabId?: string; url?: string } };

/**
 * Host errors that refuse one command and leave the stream healthy: the page
 * stays up and the error is shown as a notice. Each maps to the copy the phone
 * shows, or to null to show the desktop's own message.
 */
const RECOVERABLE_ERRORS = new Map<string, string | null>([
	["BROWSER_REMOTE_READ_ONLY", null],
	["BROWSER_REMOTE_NOT_VIEWED", null],
	["INVALID_ARGUMENT", null],
	// The desktop's wording for these is written for the `ao browser` CLI.
	["URL_REQUIRED", "Enter an address to open."],
	["INVALID_URL", "Enter a web address, like localhost:3000 or example.com."],
	["BROWSER_URL_FORBIDDEN", "The desktop browser opens only http and https addresses."],
	["TAB_NOT_FOUND", "That tab has already closed."],
	["TAB_ID_REQUIRED", "That tab has already closed."],
]);

const FILE_CHUNK_BYTES = 32 << 10;
const BASE64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

function toBase64(bytes: Uint8Array): string {
	let out = "";
	for (let i = 0; i < bytes.length; i += 3) {
		const a = bytes[i]!, b = bytes[i + 1], c = bytes[i + 2];
		out += BASE64[a >> 2]! + BASE64[((a & 3) << 4) | ((b ?? 0) >> 4)]!;
		out += b === undefined ? "=" : BASE64[((b & 15) << 2) | ((c ?? 0) >> 6)]!;
		out += c === undefined ? "=" : BASE64[c & 63]!;
	}
	return out;
}

/** A desktop ask the phone can show, or null. Its text comes from the page. */
function localAsk(payload: unknown): BrowserDialog | null {
	const ask = (payload ?? {}) as Record<string, unknown>;
	if (ask.kind === "auth" && typeof ask.origin === "string" && ask.origin) {
		return { kind: "auth", origin: ask.origin.slice(0, 200), ...(typeof ask.realm === "string" && ask.realm ? { realm: ask.realm.slice(0, 200) } : {}), failed: Boolean(ask.failed) };
	}
	if (ask.kind === "filechooser") {
		return { kind: "filechooser", multiple: Boolean(ask.multiple), accept: typeof ask.accept === "string" ? ask.accept.slice(0, 200) : "", folder: Boolean(ask.folder) };
	}
	return null;
}

/** The desktop's browser, streamed through the daemon's live socket. */
export class LocalBrowserTransport implements BrowserTransport {
	// The desktop accepts key and text input. What its page may ask (sign-ins,
	// files) it says in its state; an older desktop asks nothing.
	readonly capabilities: BrowserCapabilities = { text: true, keys: true, viewport: false, quality: false, control: false, dialogs: false, auth: false, select: false, files: false, downloads: false };
	private socket: WebSocket | null = null;
	private closed = false;
	private reportedError = false;
	private handlers: BrowserTransportHandlers = { onStatus() {}, onState() {}, onFrame() {} };

	constructor(
		private readonly config: ServerConfig,
		private readonly sessionID: string,
		private readonly options: { readFile?(uri: string): Promise<Uint8Array> } = {},
	) {}

	connect(handlers: BrowserTransportHandlers): void {
		this.handlers = handlers;
		this.closed = false;
		this.reportedError = false;
		this.handlers.onStatus("connecting");
		try {
			const Socket = WebSocket as unknown as {
				new (url: string, protocols?: string | string[], options?: { headers?: Record<string, string> }): WebSocket;
			};
			const socket = new Socket(browserLiveURL(this.config, this.sessionID), undefined, {
				headers: { Origin: "http://localhost", ...authHeaders(this.config) },
			});
			socket.binaryType = "arraybuffer";
			this.socket = socket;
			socket.onopen = () => this.handlers.onStatus("open");
			socket.onerror = () => {
				this.reportedError = true;
				this.handlers.onStatus("error");
				// Native WebSocket errors omit the HTTP handshake response body.
				if (this.handlers.onDisabled) void this.checkSharing(socket);
			};
			socket.onclose = () => {
				// Retain the socket identity for a pending handshake-error check.
				// Explicit close/reconnect invalidates that check.
				if (this.closed) this.handlers.onStatus("closed");
				else if (!this.reportedError) this.handlers.onStatus("error", "The browser stream ended. Try reconnecting.");
			};
			socket.onmessage = (event) => { void this.handleMessage(event.data); };
		} catch (error) {
			this.handlers.onStatus("error", error instanceof Error ? error.message : "The browser stream could not connect.");
		}
	}

	private async checkSharing(socket: WebSocket): Promise<void> {
		try {
			await apiRequest(this.config, `/api/v1/sessions/${encodeURIComponent(this.sessionID)}/browser/live`, undefined, 5_000);
		} catch (error) {
			if (this.closed || !this.reportedError || this.socket !== socket || !(error instanceof ApiError)) return;
			// OTA mobile updates can reach desktops that predate this route.
			// Keep their selected app preview accessible without enabling streaming.
			if (error.status === 404) {
				this.handlers.onStatus("error", "Update the desktop app to view its browser from your phone.");
				this.handlers.onDisabled?.();
			} else if (error.code === "BROWSER_REMOTE_DISABLED") {
				this.handlers.onDisabled?.();
			}
		}
	}

	send(command: BrowserCommand): void {
		if (!commandSupported(this.capabilities, command)) return;
		if (command.type === "files") {
			void this.sendFiles(command.payload.files);
			return;
		}
		this.sendRaw(command);
	}

	private sendRaw(command: unknown): void {
		if (this.socket?.readyState === WebSocket.OPEN) this.socket.send(JSON.stringify(command));
	}

	// The daemon takes a command of at most 64 KiB, so each file goes in base64
	// chunks of 32 KiB, in order, then "done". A file that can't be read leaves
	// the page's input as it was.
	private async sendFiles(files: BrowserPickedFile[]): Promise<void> {
		const socket = this.socket;
		const cancel = () => { if (this.socket === socket) this.sendRaw({ type: "files", payload: { action: "cancel" } }); };
		if (!files.length) return cancel();
		const read = this.options.readFile ?? (async (uri: string) => new Uint8Array(await (await fetch(uri)).arrayBuffer()));
		for (const [index, file] of files.entries()) {
			let bytes: Uint8Array;
			try {
				bytes = await read(file.uri);
			} catch {
				this.handlers.onStatus("open", `Couldn't send ${file.name} to the page.`);
				return cancel();
			}
			for (let offset = 0; offset === 0 || offset < bytes.length; offset += FILE_CHUNK_BYTES) {
				if (this.socket !== socket) return;
				this.sendRaw({ type: "files", payload: { action: "chunk", file: index, name: file.name, offset, data: toBase64(bytes.subarray(offset, offset + FILE_CHUNK_BYTES)) } });
			}
		}
		if (this.socket === socket) this.sendRaw({ type: "files", payload: { action: "done", count: files.length } });
	}

	close(): void {
		this.closed = true;
		this.socket?.close();
		this.socket = null;
	}

	private async handleMessage(data: unknown): Promise<void> {
		if (typeof data === "string") {
			const message = JSON.parse(data) as { type?: string; payload?: unknown; code?: string; message?: string };
			if (message.type === "state" && message.payload) {
				const state = message.payload as BrowserLiveState & { asks?: unknown };
				const asks = Array.isArray(state.asks) ? state.asks : [];
				this.capabilities.auth = asks.includes("auth");
				this.capabilities.files = asks.includes("files");
				this.handlers.onState(toViewState(state));
			}
			if (message.type === "ask") {
				const ask = localAsk(message.payload);
				if (ask) this.handlers.onDialog?.(ask);
			}
			if (message.type === "askClosed") this.handlers.onDialogClosed?.();
			if (message.type === "notice" && typeof message.message === "string") this.handlers.onStatus("open", message.message.slice(0, 300));
			if (message.type === "error") {
				const detail = message.message ?? message.code ?? "Browser host error";
				if (message.code && RECOVERABLE_ERRORS.has(message.code)) {
					this.handlers.onStatus("open", RECOVERABLE_ERRORS.get(message.code) ?? detail);
				} else {
					this.handlers.onStatus("error", detail);
				}
			}
			return;
		}
		const buffer = data instanceof ArrayBuffer
			? data
			: typeof Blob !== "undefined" && data instanceof Blob
				? await data.arrayBuffer()
				: null;
		if (buffer) {
			const frame = decodeBrowserFrame(buffer);
			// Host errors can be recoverable while the WebSocket remains connected.
			// A later valid frame proves the stream is healthy again, so remove the
			// blocking error state without forcing the user to reconnect.
			this.reportedError = false;
			this.handlers.onStatus("open");
			this.handlers.onFrame(frame);
		}
	}
}
