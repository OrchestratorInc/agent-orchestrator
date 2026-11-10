import type { CloudClient } from "./client";
import { CloudError, cloudErrorMessage, isWorkerUnavailable } from "./errors";
import type { CloudTerminalKind } from "./types";

export type CloudTerminalStatus = "connecting" | "open" | "exited" | "closed" | "error";

export type CloudTerminalHandlers = {
	onStatus(status: CloudTerminalStatus, message?: string): void;
	/** Raw PTY bytes, in order. */
	onOutput(bytes: Uint8Array): void;
	/** The server replays from the start: clear the screen first. */
	onReset(): void;
	/** Input refused, usually because someone else holds a shared session's composer. */
	onRefused?(message: string): void;
	/** Whether this ticket may type (terminal:operate). */
	onAccess?(canOperate: boolean): void;
};

export type CloudTerminalConnection = {
	send(data: string): void;
	resize(columns: number, rows: number): void;
	/** Reconnect now, with a fresh ticket (after a manual retry). */
	reconnect(): void;
	close(): void;
};

type SocketLike = Pick<WebSocket, "send" | "close" | "readyState"> & {
	onopen: ((event: unknown) => void) | null;
	onmessage: ((event: { data: unknown }) => void) | null;
	onclose: ((event: { code: number; reason: string }) => void) | null;
	onerror: ((event: unknown) => void) | null;
};

export type TerminalDeps = {
	createSocket: (url: string) => SocketLike;
	setTimeout: (callback: () => void, ms: number) => unknown;
	clearTimeout: (handle: unknown) => void;
};

const defaultDeps: TerminalDeps = {
	createSocket: (url) => new WebSocket(url) as unknown as SocketLike,
	setTimeout: (callback, ms) => setTimeout(callback, ms),
	clearTimeout: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
};

/** Decode the server's base64 output frames into bytes for xterm. */
export function decodeBase64(data: string): Uint8Array {
	const binary = atob(data);
	const bytes = new Uint8Array(binary.length);
	for (let index = 0; index < binary.length; index++) bytes[index] = binary.charCodeAt(index);
	return bytes;
}

// Close codes the server uses (terminal_handlers.go): 1000 the terminal process
// exited, 1008 policy (read-only or unavailable), 1013 a workspace terminal is
// restarting, 1011 the stream was interrupted.
const EXITED = 1000;
const POLICY = 1008;
const MAX_TICKET_ATTEMPTS = 12;

/**
 * One terminal stream of a cloud session.
 *
 * Every connection mints a fresh single-use ticket (the ticket is the only
 * credential on the socket; the account token never rides a WebSocket URL),
 * and minting one also wakes a paused sandbox. A sandbox that is still waking
 * answers WORKER_UNAVAILABLE, so the ticket is asked for again with backoff.
 * The server replays the whole screen from sequence 0 after `reset`, so a
 * reconnect never needs local scrollback.
 */
export function connectCloudTerminal(input: {
	client: Pick<CloudClient, "createTerminalTicket" | "terminalUrl">;
	orgId: string;
	sessionId: string;
	kind: CloudTerminalKind;
	handlers: CloudTerminalHandlers;
	deps?: TerminalDeps;
}): CloudTerminalConnection {
	const { client, orgId, sessionId, kind, handlers } = input;
	const deps = input.deps ?? defaultDeps;
	let socket: SocketLike | null = null;
	let closed = false;
	let attempt = 0;
	let timer: unknown;
	let size: { columns: number; rows: number } | null = null;
	let generation = 0;

	const schedule = (ms: number) => {
		if (closed) return;
		if (timer !== undefined) deps.clearTimeout(timer);
		timer = deps.setTimeout(() => { timer = undefined; void open(); }, ms);
	};

	const sendFrame = (frame: Record<string, unknown>) => {
		if (socket && socket.readyState === 1) socket.send(JSON.stringify(frame));
	};

	async function open(): Promise<void> {
		if (closed) return;
		const own = ++generation;
		handlers.onStatus("connecting");
		let ticket: string;
		try {
			const issued = await client.createTerminalTicket(orgId, sessionId, kind);
			ticket = issued.ticket;
			handlers.onAccess?.(issued.scopes.includes("terminal:operate"));
		} catch (error) {
			if (closed || own !== generation) return;
			if (error instanceof CloudError && error.code === "TERMINAL_SESSION_EXITED") {
				handlers.onStatus("exited", "The terminal has exited.");
				return;
			}
			attempt++;
			if (isWorkerUnavailable(error) && attempt < MAX_TICKET_ATTEMPTS) {
				handlers.onStatus("connecting", "Waiting for the sandbox to start…");
				schedule(Math.min(1_500 * attempt, 6_000));
				return;
			}
			handlers.onStatus("error", cloudErrorMessage(error));
			return;
		}
		if (closed || own !== generation) return;
		const ws = deps.createSocket(client.terminalUrl(ticket, kind, 0));
		socket = ws;
		ws.onopen = () => {
			if (own !== generation) return;
			attempt = 0;
			if (size) sendFrame({ type: "resize", columns: size.columns, rows: size.rows });
		};
		ws.onmessage = (event) => {
			if (own !== generation || typeof event.data !== "string") return;
			let frame: Record<string, unknown>;
			try { frame = JSON.parse(event.data) as Record<string, unknown>; } catch { return; }
			switch (frame.type) {
				case "reset":
					handlers.onReset();
					break;
				case "starting":
					handlers.onStatus("connecting", "Starting the terminal…");
					break;
				case "ready":
				case "replay_complete":
					handlers.onStatus("open");
					break;
				case "output":
					if (typeof frame.data === "string") handlers.onOutput(decodeBase64(frame.data));
					break;
				case "input_refused":
					handlers.onRefused?.(typeof frame.message === "string" && frame.message ? frame.message : "Someone else is typing in this session.");
					break;
				default:
					// input_ack, notification_hint and newer frames need nothing here.
					break;
			}
		};
		ws.onerror = () => { /* onclose follows with the code. */ };
		ws.onclose = (event) => {
			if (own !== generation) return;
			socket = null;
			if (closed) return;
			if (event.code === EXITED) {
				handlers.onStatus("exited", "The terminal has exited.");
				return;
			}
			if (event.code === POLICY && /read-only/i.test(event.reason)) {
				handlers.onAccess?.(false);
				handlers.onStatus("error", "This terminal is read-only.");
				return;
			}
			attempt++;
			if (attempt >= MAX_TICKET_ATTEMPTS) {
				handlers.onStatus("error", event.reason || "The terminal disconnected.");
				return;
			}
			handlers.onStatus("connecting", "Reconnecting…");
			schedule(Math.min(1_000 * attempt, 5_000));
		};
	}

	void open();

	return {
		send(data) {
			if (data) sendFrame({ type: "input", data });
		},
		resize(columns, rows) {
			// The server ignores a 0x0 grid; keep a minimum like the desktop does.
			const next = { columns: Math.max(20, Math.floor(columns)), rows: Math.max(4, Math.floor(rows)) };
			if (size && size.columns === next.columns && size.rows === next.rows) return;
			size = next;
			sendFrame({ type: "resize", ...next });
		},
		reconnect() {
			if (closed) return;
			// A drop's retry may still be waiting; left armed, it would open a
			// second socket that close() can no longer reach.
			if (timer !== undefined) deps.clearTimeout(timer);
			timer = undefined;
			attempt = 0;
			generation++;
			try { socket?.close(); } catch { /* already closed */ }
			socket = null;
			void open();
		},
		close() {
			closed = true;
			generation++;
			if (timer !== undefined) deps.clearTimeout(timer);
			try { socket?.close(); } catch { /* already closed */ }
			socket = null;
			handlers.onStatus("closed");
		},
	};
}
