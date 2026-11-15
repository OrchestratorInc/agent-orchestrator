import { base64ToBytes, type MuxConnectionState, type TerminalMux } from "./terminal-mux";
import { PLAN_REFUSAL_CODES } from "./cloud-cp/client";
import { publishCloudNotificationHint } from "./cloud-notification-hints";
import { appI18n } from "../i18n";
import { useUiStore } from "../stores/ui-store";

export interface CloudTerminalMuxOptions {
	wsBaseUrl: string;
	kind: "agent" | "workspace";
	terminalId?: string;
	mintTicket: (kind: "agent" | "workspace") => Promise<string>;
	cursor?: { value: number };
	initialInput?: string;
	WebSocketImpl?: typeof WebSocket;
}

type DataListener = (bytes: Uint8Array) => void;
type ExitListener = () => void;
type OpenedListener = () => void;
type ErrorListener = (message: string) => void;
type ConnectionListener = (state: MuxConnectionState) => void;

export function createCloudTerminalMux(options: CloudTerminalMuxOptions): TerminalMux {
	const WS = options.WebSocketImpl ?? WebSocket;
	const dataListeners = new Set<DataListener>();
	const exitListeners = new Set<ExitListener>();
	const openedListeners = new Set<OpenedListener>();
	const errorListeners = new Set<ErrorListener>();
	const connectionListeners = new Set<ConnectionListener>();

	let socket: WebSocket | null = null;
	let after = options.cursor?.value ?? 0;
	const advanceCursor = (sequence: number) => {
		after = sequence;
		if (options.cursor) options.cursor.value = sequence;
	};
	let disposed = false;
	let exited = false;
	let connectionState: MuxConnectionState | undefined;
	let pendingResize: { cols: number; rows: number } | null = null;
	let sentResize: { cols: number; rows: number } | null = null;
	let resizeTimer: ReturnType<typeof setTimeout> | null = null;
	let reflowed = false;
	const pendingInput: string[] = [];
	let initialInput = options.initialInput;

	const setConnectionState = (next: MuxConnectionState) => {
		if (disposed || connectionState === next) return;
		connectionState = next;
		connectionListeners.forEach((listener) => listener(next));
	};

	const terminalExited = (error: unknown): boolean =>
		typeof error === "object" && error !== null && (error as { code?: unknown }).code === "TERMINAL_SESSION_EXITED";

	const reportTerminalExited = () => {
		if (disposed || exited) return;
		exited = true;
		errorListeners.forEach((listener) =>
			listener("The coding-agent terminal has exited. Start a new session to continue."),
		);
	};

	const planRefusal = (error: unknown): string | undefined => {
		if (typeof error !== "object" || error === null) return undefined;
		const { code, message } = error as { code?: unknown; message?: unknown };
		if (typeof code !== "string" || !PLAN_REFUSAL_CODES.has(code)) return undefined;
		return typeof message === "string" && message !== "" ? message : "Your plan does not allow this session to run right now.";
	};

	const sendJSON = (message: unknown): boolean => {
		if (socket && socket.readyState === WS.OPEN) {
			socket.send(JSON.stringify(message));
			return true;
		}
		return false;
	};

	const handleMessage = (event: MessageEvent) => {
		if (typeof event.data !== "string") return;
		let message: { type?: string; data?: string; sequence?: number; eventId?: string; eventType?: string; occurredAt?: string; payload?: unknown };
		try {
			message = JSON.parse(event.data);
		} catch {
			return;
		}
		switch (message.type) {
			case "ready":
				if (typeof message.sequence === "number") advanceCursor(message.sequence);
				openedListeners.forEach((listener) => listener());
				if (initialInput && sendJSON({ type: "input", data: initialInput })) initialInput = undefined;
				break;
			case "output":
				if (typeof message.sequence === "number") advanceCursor(message.sequence);
				if (message.data) {
					const bytes = base64ToBytes(message.data);
					dataListeners.forEach((listener) => listener(bytes));
				}
				break;
			case "reset":
				advanceCursor(0);
				{
					const clear = new TextEncoder().encode("\x1b[3J\x1b[H\x1b[2J");
					dataListeners.forEach((listener) => listener(clear));
				}
				break;
			case "notification_hint":
				if (typeof message.eventId === "string" && typeof message.eventType === "string" &&
					typeof message.occurredAt === "string" && message.payload !== null && typeof message.payload === "object" && !Array.isArray(message.payload)) {
					publishCloudNotificationHint({ source: "cloud", eventId: message.eventId, type: message.eventType, occurredAt: message.occurredAt, payload: message.payload as Record<string, unknown> });
				}
				break;
			case "input_refused":
				useUiStore.getState().showGlobalToast(appI18n.t("chat.shared.terminalRefused"), undefined, {
					placement: "top-center", dedupeKey: "composer-terminal-refused",
				});
				break;
			default:
				break;
		}
	};

	const openSocket = (kind: "agent" | "workspace", ticket: string) => {
		if (disposed) return;
	const query = new URLSearchParams({
			ticket,
			kind,
			after: String(after),
			protocol: "2",
	});
	if (options.terminalId) query.set("terminalId", options.terminalId);
		const url = `${options.wsBaseUrl.replace(/\/+$/, "")}/terminal?${query.toString()}`;
		const ws = new WS(url);
		socket = ws;
		ws.addEventListener("open", () => {
			if (disposed || socket !== ws) return;
			sentResize = null;
			flushResize();
			for (const input of pendingInput.splice(0)) sendJSON({ type: "input", data: input });
			setConnectionState("open");
		});
		ws.addEventListener("message", (event) => {
			if (socket === ws) handleMessage(event);
		});
		ws.addEventListener("close", (event: CloseEvent) => {
			if (socket !== ws) return;
			if (event.code === 1000 && !exited) {
				exited = true;
				exitListeners.forEach((listener) => listener());
			}
			setConnectionState("closed");
		});
		ws.addEventListener("error", () => {
			if (socket === ws) setConnectionState("closed");
		});
	};

	const connect = async (kind: "agent" | "workspace") => {
		let ticket: string;
		try {
			ticket = await options.mintTicket(kind);
		} catch (error) {
			if (disposed) return;
			if (terminalExited(error)) {
				reportTerminalExited();
				return;
			}
			const refusal = planRefusal(error);
			if (refusal) {
				errorListeners.forEach((listener) => listener(refusal));
				return;
			}
			setConnectionState("waiting");
			return;
		}
		if (disposed) return;
		openSocket(kind, ticket);
	};

	void connect(options.kind);

	const MIN_RESIZE_COLS = 20;
	const MIN_RESIZE_ROWS = 4;
	const RESIZE_DEBOUNCE_MS = 150;
	const flushResize = () => {
		if (resizeTimer !== null) {
			clearTimeout(resizeTimer);
			resizeTimer = null;
		}
		if (!pendingResize) return;
		if (sentResize && sentResize.cols === pendingResize.cols && sentResize.rows === pendingResize.rows) {
			if (reflowed && sendJSON({ type: "resize", columns: pendingResize.cols, rows: pendingResize.rows, repaint: true })) {
				reflowed = false;
			}
			return;
		}
		if (sendJSON({ type: "resize", columns: pendingResize.cols, rows: pendingResize.rows })) {
			sentResize = pendingResize;
			reflowed = false;
		}
	};
	const sendResize = (cols: number, rows: number) => {
		if (cols < MIN_RESIZE_COLS || rows < MIN_RESIZE_ROWS) return;
		if (sentResize && (sentResize.cols !== cols || sentResize.rows !== rows)) reflowed = true;
		pendingResize = { cols, rows };
		if (resizeTimer !== null) clearTimeout(resizeTimer);
		resizeTimer = setTimeout(flushResize, RESIZE_DEBOUNCE_MS);
	};

	return {
		open: (_id, cols, rows) => {
			sendResize(cols, rows);
		},
		sendInput: (_id, input) => {
			if (!sendJSON({ type: "input", data: input })) pendingInput.push(input);
		},
		resize: (_id, cols, rows) => {
			sendResize(cols, rows);
		},
		close: () => {
			if (socket) {
				try {
					socket.close(1000, "closed by client");
				} catch {
				}
			}
		},
		onData: (_id, listener) => {
			dataListeners.add(listener);
			return () => dataListeners.delete(listener);
		},
		onExit: (_id, listener) => {
			exitListeners.add(listener);
			return () => exitListeners.delete(listener);
		},
		onOpened: (_id, listener) => {
			openedListeners.add(listener);
			return () => openedListeners.delete(listener);
		},
		onError: (_id, listener) => {
			errorListeners.add(listener);
			return () => errorListeners.delete(listener);
		},
		onConnectionChange: (listener) => {
			connectionListeners.add(listener);
			return () => connectionListeners.delete(listener);
		},
		dispose: () => {
			if (disposed) return;
			disposed = true;
			if (resizeTimer !== null) clearTimeout(resizeTimer);
			resizeTimer = null;
			dataListeners.clear();
			exitListeners.clear();
			openedListeners.clear();
			errorListeners.clear();
			connectionListeners.clear();
			if (socket) {
				try {
					socket.close();
				} catch {
				}
			}
			socket = null;
		},
	};
}
