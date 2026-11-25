import { ApiError, apiRequest } from "./api";
import { authHeaders, type ServerConfig } from "./config";
import { browserLiveURL, decodeBrowserFrame } from "./browserLiveProtocol";
import { commandSupported, toViewState, type BrowserCapabilities, type BrowserCommand, type BrowserTransport, type BrowserTransportHandlers } from "./browserTransport";

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

/** The desktop's browser, streamed through the daemon's live socket. */
export class LocalBrowserTransport implements BrowserTransport {
	// The desktop accepts key and text input; the rest arrive in later releases.
	readonly capabilities: BrowserCapabilities = { text: true, keys: true, viewport: false, quality: false, control: false, dialogs: false, auth: false, select: false, files: false, downloads: false };
	private socket: WebSocket | null = null;
	private closed = false;
	private reportedError = false;
	private handlers: BrowserTransportHandlers = { onStatus() {}, onState() {}, onFrame() {} };

	constructor(private readonly config: ServerConfig, private readonly sessionID: string) {}

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
		if (this.socket?.readyState === WebSocket.OPEN) this.socket.send(JSON.stringify(command));
	}

	close(): void {
		this.closed = true;
		this.socket?.close();
		this.socket = null;
	}

	private async handleMessage(data: unknown): Promise<void> {
		if (typeof data === "string") {
			const message = JSON.parse(data) as { type?: string; payload?: BrowserLiveState; code?: string; message?: string };
			if (message.type === "state" && message.payload) this.handlers.onState(toViewState(message.payload));
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
