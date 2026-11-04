import { ApiError, apiRequest } from "./api";
import { authHeaders, type ServerConfig } from "./config";
import { browserLiveURL, decodeBrowserFrame, type BrowserLiveFrame } from "./browserLiveProtocol";

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

type BrowserLiveHandlers = {
	onStatus(status: "connecting" | "open" | "closed" | "error", message?: string): void;
	onState(state: BrowserLiveState): void;
	onFrame(frame: BrowserLiveFrame): void;
	// Also used when an older desktop does not support browser streaming.
	onDisabled?(): void;
};

export class BrowserLiveClient {
	private socket: WebSocket | null = null;
	private closed = false;
	private reportedError = false;

	constructor(private readonly config: ServerConfig, private readonly sessionID: string, private readonly handlers: BrowserLiveHandlers) {}

	connect(): void {
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

	send(command: BrowserLiveCommand): void {
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
			if (message.type === "state" && message.payload) this.handlers.onState(message.payload);
			if (message.type === "error") {
				const detail = message.message ?? message.code ?? "Browser host error";
				if (message.code === "BROWSER_REMOTE_READ_ONLY" || message.code === "BROWSER_REMOTE_NOT_VIEWED" || message.code === "INVALID_ARGUMENT") {
					this.handlers.onStatus("open", detail);
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
