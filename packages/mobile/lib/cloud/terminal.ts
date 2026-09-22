import { CloudApiError, type CloudClient } from "@aoagents/cloud-client";

export type CloudTerminalStatus = "connecting" | "waiting" | "ready" | "disconnected" | "exited" | "error";

export function createCloudTerminal(input: {
	client: CloudClient;
	orgId: string;
	sessionId: string;
	WebSocketImpl?: typeof WebSocket;
	onStatus(status: CloudTerminalStatus): void;
	onOutput(bytes: Uint8Array): void;
	onSize(columns: number, rows: number): void;
	onReset?(): void;
	onError(message: string): void;
}) {
	const WS = input.WebSocketImpl ?? WebSocket;
	let socket: WebSocket | null = null;
	let retryTimer: ReturnType<typeof setTimeout> | null = null;
	let disposed = false;
	let ready = false;
	let backoff = 1000;
	let dimensions = { columns: 0, rows: 0 };
	let visible = true;
	let bracketedPasteActive = false;
	let pasteMarkerCarry = "";

	const observeOutputModes = (bytes: Uint8Array) => {
		let chunk = pasteMarkerCarry;
		for (const byte of bytes) chunk += String.fromCharCode(byte);
		pasteMarkerCarry = chunk.slice(-7);
		const enabled = chunk.lastIndexOf("\x1b[?2004h");
		const disabled = chunk.lastIndexOf("\x1b[?2004l");
		if (enabled !== -1 || disabled !== -1) bracketedPasteActive = enabled > disabled;
	};

	const sendInput = (text: string): boolean => {
		if (!ready || !socket || socket.readyState !== WS.OPEN) return false;
		socket.send(JSON.stringify({ type: "input", data: text }));
		return true;
	};
	const sendViewer = () => {
		if (!socket || socket.readyState !== WS.OPEN) return;
		socket.send(JSON.stringify({ type: "viewer", role: "secondary", visible, ...dimensions }));
	};

	const retry = () => {
		if (disposed || retryTimer) return;
		retryTimer = setTimeout(() => {
			retryTimer = null;
			void connect();
		}, backoff);
		backoff = Math.min(backoff * 2, 8000);
	};

	const connect = async () => {
		if (disposed || socket) return;
		ready = false;
		input.onReset?.();
		bracketedPasteActive = false;
		pasteMarkerCarry = "";
		input.onStatus("connecting");
		let ticket: string;
		try {
			ticket = (await input.client.createTerminalTicket(input.orgId, input.sessionId, "agent")).ticket;
		} catch (cause) {
			if (disposed) return;
			if (cause instanceof CloudApiError && cause.code === "TERMINAL_SESSION_EXITED") {
				input.onStatus("exited");
				input.onError("The coding-agent terminal has exited.");
				return;
			}
			if (cause instanceof CloudApiError && (cause.status === 401 || cause.status === 403)) {
				input.onStatus("error");
				input.onError(cause.message);
				return;
			}
			input.onStatus("waiting");
			retry();
			return;
		}
		if (disposed) return;
		const url = new URL(input.client.terminalUrl(ticket, { after: 0, kind: "agent" }));
		url.searchParams.set("protocol", "3");
		let ws: WebSocket;
		try {
			ws = new WS(url.toString());
		} catch (cause) {
			input.onStatus("disconnected");
			input.onError(cause instanceof Error ? cause.message : String(cause));
			retry();
			return;
		}
		socket = ws;
		ws.onopen = () => {
			if (disposed || socket !== ws) return;
			sendViewer();
		};
		ws.onmessage = (event) => {
			if (disposed || socket !== ws || typeof event.data !== "string") return;
			let frame: { type?: string; data?: string; columns?: number; rows?: number };
			try { frame = JSON.parse(event.data); } catch { return; }
			if (frame.type === "ready") {
				ready = true;
				backoff = 1000;
				input.onStatus("ready");
			} else if (frame.type === "reset") {
				input.onReset?.();
				input.onOutput(new Uint8Array([27, 91, 51, 74, 27, 91, 72, 27, 91, 50, 74]));
			} else if (frame.type === "size" && Number.isInteger(frame.columns) && Number.isInteger(frame.rows) &&
				(frame.columns ?? 0) > 0 && (frame.rows ?? 0) > 0 &&
				(frame.columns ?? 0) <= 65535 && (frame.rows ?? 0) <= 65535) {
				input.onSize(frame.columns!, frame.rows!);
			} else if (frame.type === "output" && typeof frame.data === "string") {
				try {
					const binary = atob(frame.data);
					const bytes = new Uint8Array(binary.length);
					for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
					observeOutputModes(bytes);
					input.onOutput(bytes);
				} catch { /* Ignore a malformed output frame; keep the terminal attached. */ }
			}
		};
		ws.onclose = (event) => {
			if (socket !== ws) return;
			socket = null;
			ready = false;
			if (disposed) return;
			if (event.code === 1000) {
				input.onStatus("exited");
				return;
			}
			if (event.code === 1008) {
				input.onStatus("error");
				input.onError("The Cloud agent terminal is unavailable or terminal access is not allowed.");
				return;
			}
			input.onStatus("disconnected");
			retry();
		};
		ws.onerror = () => {
			if (!disposed && socket === ws) input.onStatus("disconnected");
		};
	};

	return {
		connect,
		disconnect: () => {
			disposed = true;
			ready = false;
			if (retryTimer) clearTimeout(retryTimer);
			retryTimer = null;
			const current = socket;
			socket = null;
			current?.close();
		},
		sendInput,
		sendPrompt: async (text: string): Promise<boolean> => {
			const currentSocket = socket;
			if (!text || !currentSocket || !ready) return false;
			// A TUI can interpret text and CR in one PTY read as pasted text with
			// a newline. End the paste explicitly when supported, then send Enter
			// as its own terminal event after the worker has read the text.
			const payload = bracketedPasteActive ? `\x1b[200~${text}\x1b[201~` : text;
			if (!sendInput(payload)) return false;
			await new Promise((resolve) => setTimeout(resolve, 50));
			if (disposed || socket !== currentSocket) return false;
			return sendInput("\r");
		},
		resize: (columns: number, rows: number) => {
			if (!Number.isInteger(columns) || !Number.isInteger(rows) || columns < 1 || rows < 1 || columns > 65535 || rows > 65535) return;
			dimensions = { columns, rows };
			sendViewer();
		},
		setVisible: (next: boolean) => {
			visible = next;
			sendViewer();
		},
	};
}
