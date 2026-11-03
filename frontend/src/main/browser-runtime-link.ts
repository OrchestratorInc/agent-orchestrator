import net from "node:net";
import { StringDecoder } from "node:string_decoder";
import { HANDSHAKE_CLOSED_BEFORE_ACCEPT } from "../shared/daemon-takeover";
import type { BrowserRuntimeState } from "./browser-view-host";

// v3 answers broker liveness pings. Unlike the supervisor link — where the
// open socket IS the liveness signal — the browser runtime embeds a
// third-party automation stack whose host process wedges often enough that an
// open socket says nothing about responsiveness, so the daemon probes us.
const PROTOCOL_VERSION = 3;
const BACKOFF_INIT_MS = 200;
const BACKOFF_MAX_MS = 2_000;
const MAX_COMMAND_BYTES = 1 << 20;
const MAX_RESULT_BYTES = 8 << 20;

export type BrowserRuntimeCommand = {
	type: "command";
	requestId: string;
	sessionId: string;
	action: string;
	args?: Record<string, unknown>;
};

type BrowserRuntimeCancel = {
	type: "cancel";
	requestId: string;
};

type BrowserRuntimePing = {
	type: "ping";
};

// The daemon's answer to the hello (backend/internal/browserruntime/broker.go).
type BrowserRuntimeHelloReply =
	| { type: "hello-ok" }
	| { type: "hello-rejected"; error?: BrowserRuntimeCommandError };

type Handshake = {
	accept: () => void;
	refuse: (error: BrowserRuntimeCommandError | undefined) => void;
};

const CONNECTING: BrowserRuntimeState = { connected: false, connecting: true, rejected: false };
const CONNECTED: BrowserRuntimeState = { connected: true, connecting: false, rejected: false };
const REJECTED: BrowserRuntimeState = { connected: false, connecting: false, rejected: true };
export const BROWSER_RUNTIME_DISCONNECTED: BrowserRuntimeState = { connected: false, connecting: false, rejected: false };

export type BrowserRuntimeCommandError = {
	code: string;
	message: string;
};

export interface BrowserRuntimeLinkHandle {
	readonly state: BrowserRuntimeState;
	/** Tears the link down without reporting a state; the caller publishes one. */
	dispose(): void;
}

type BrowserRuntimeLinkOptions = {
	execute: (command: BrowserRuntimeCommand, signal: AbortSignal) => Promise<unknown>;
	token?: string;
	log?: (message: string) => void;
	onStateChange?: (state: BrowserRuntimeState) => void;
	/**
	 * Reported when the daemon refuses this app's hello, or drops the link before
	 * accepting it — which is how a daemon predating hello-rejected refuses a stale
	 * token. main.ts turns an unrecoverable reason into a replacement of the daemon
	 * it merely adopted; see browserRuntimeRecoveryDecision.
	 */
	onHandshakeRejected?: (reason: string) => void;
};

/**
 * The recovery decision's vocabulary, derived from the broker's stable error code
 * (backend/internal/browserruntime/broker.go). Only a rejected token is
 * unrecoverable by retrying; a protocol mismatch resolves once the app and the
 * daemon are upgraded together, so it keeps its own code.
 */
function handshakeRejectionReason(error: BrowserRuntimeCommandError | undefined): string {
	if (error?.code === "BROWSER_RUNTIME_TOKEN_REJECTED") return "invalid-token";
	return error?.code ?? "handshake-rejected";
}

export function connectBrowserRuntime(
	address: string | net.TcpNetConnectOpts,
	options: BrowserRuntimeLinkOptions,
): BrowserRuntimeLinkHandle {
	const log = options.log ?? (() => undefined);
	let disposed = false;
	let state = CONNECTING;
	let unansweredStreak = 0;
	let socket: net.Socket | null = null;
	let retryTimer: ReturnType<typeof setTimeout> | null = null;
	let backoff = BACKOFF_INIT_MS;
	let buffer = "";
	let decoder = new StringDecoder("utf8");
	let connectionEpoch = 0;
	const commandChains = new Map<string, Promise<void>>();
	const commandControllers = new Map<string, AbortController>();
	const activeCommands = new Map<
		string,
		{ command: BrowserRuntimeCommand; target: net.Socket; epoch: number }
	>();
	const setState = (next: BrowserRuntimeState) => {
		if (disposed || state === next) return;
		state = next;
		options.onStateChange?.(state);
	};

	const sendCancelledResultOnTarget = (command: BrowserRuntimeCommand, target: net.Socket) => {
		if (target.destroyed) return;
		const frame = `${JSON.stringify({
			type: "result",
			requestId: command.requestId,
			ok: false,
			error: { code: "BROWSER_COMMAND_CANCELED", message: "Browser runtime link closed" },
		})}\n`;
		try {
			target.write(frame);
		} catch {
			// Socket already torn down; the daemon will observe disconnect.
		}
	};

	const cancelConnectionCommands = () => {
		for (const { command, target } of activeCommands.values()) {
			sendCancelledResultOnTarget(command, target);
		}
		// Drop tracking before aborting so in-flight respond() catch paths do not
		// emit a second cancellation frame for the same requestId.
		activeCommands.clear();
		for (const controller of commandControllers.values()) controller.abort();
		commandControllers.clear();
		commandChains.clear();
	};

	const clearRetry = () => {
		if (retryTimer !== null) {
			clearTimeout(retryTimer);
			retryTimer = null;
		}
	};

	const destroySocket = () => {
		if (!socket) return;
		const target = socket;
		cancelConnectionCommands();
		connectionEpoch += 1;
		target.removeAllListeners();
		target.destroy();
		socket = null;
	};

	const send = async (message: unknown, target: net.Socket, epoch: number): Promise<void> => {
		if (socket !== target || connectionEpoch !== epoch || target.destroyed) return;
		const frame = `${JSON.stringify(message)}\n`;
		if (Buffer.byteLength(frame, "utf8") > MAX_RESULT_BYTES) {
			throw Object.assign(new Error(`Browser result exceeds ${MAX_RESULT_BYTES} bytes`), {
				code: "BROWSER_RESULT_TOO_LARGE",
			});
		}
		await new Promise<void>((resolve, reject) => {
			const onError = (error: Error) => {
				target.off("error", onError);
				reject(error);
			};
			target.once("error", onError);
			target.write(frame, (error) => {
				target.off("error", onError);
				if (error) reject(error);
				else resolve();
			});
		});
	};

	const sendCancelledResult = async (
		command: BrowserRuntimeCommand,
		target: net.Socket,
		epoch: number,
	) => {
		try {
			await send(
				{
					type: "result",
					requestId: command.requestId,
					ok: false,
					error: { code: "BROWSER_COMMAND_CANCELED", message: "Browser runtime link closed" },
				},
				target,
				epoch,
			);
		} catch {
			// Socket already torn down; the daemon will observe disconnect.
		}
	};

	const respond = async (
		command: BrowserRuntimeCommand,
		target: net.Socket,
		epoch: number,
		controller: AbortController,
	) => {
		try {
			controller.signal.throwIfAborted();
			const result = await options.execute(command, controller.signal);
			controller.signal.throwIfAborted();
			await send({ type: "result", requestId: command.requestId, ok: true, result }, target, epoch);
		} catch (error) {
			if (controller.signal.aborted) {
				// Daemon-initiated cancel frames abort one controller without clearing
				// activeCommands; connection teardown clears the map before aborting.
				if (activeCommands.has(command.requestId)) {
					await sendCancelledResult(command, target, epoch);
				}
				return;
			}
			const normalized = normalizeCommandError(error);
			try {
				await send({ type: "result", requestId: command.requestId, ok: false, error: normalized }, target, epoch);
			} catch (sendError) {
				log(`browser-runtime-link: response failed: ${String(sendError)}`);
				target.destroy();
			}
		} finally {
			activeCommands.delete(command.requestId);
			if (commandControllers.get(command.requestId) === controller) {
				commandControllers.delete(command.requestId);
			}
		}
	};

	const consumeLine = (line: string, target: net.Socket, epoch: number, handshake: Handshake) => {
		if (!line.trim()) return;
		let message:
			| BrowserRuntimeCommand
			| BrowserRuntimeCancel
			| BrowserRuntimeHelloReply
			| BrowserRuntimePing;
		try {
			message = JSON.parse(line) as
				| BrowserRuntimeCommand
				| BrowserRuntimeCancel
				| BrowserRuntimeHelloReply
				| BrowserRuntimePing;
		} catch {
			return;
		}
		if (message.type === "hello-rejected") {
			handshake.refuse(message.error);
			return;
		}
		// An older daemon never sends hello-ok, but it writes commands (and pings)
		// only to a runtime it accepted, so any other frame accepts the link as well.
		handshake.accept();
		if (message.type === "hello-ok") return;
		if (message.type === "ping") {
			// Liveness is answered outside the per-session command chains so a
			// wedged operation can never starve the heartbeat.
			void send({ type: "pong" }, target, epoch).catch(() => {
				// Socket already torn down; the daemon will observe disconnect.
			});
			return;
		}
		if (message.type === "cancel" && typeof message.requestId === "string") {
			commandControllers.get(message.requestId)?.abort();
			return;
		}
		const command = message as BrowserRuntimeCommand;
		if (
			command.type !== "command" ||
			typeof command.requestId !== "string" ||
			typeof command.sessionId !== "string" ||
			typeof command.action !== "string"
		) {
			return;
		}
		const controller = new AbortController();
		commandControllers.set(command.requestId, controller);
		activeCommands.set(command.requestId, { command, target, epoch });
		const previous = commandChains.get(command.sessionId) ?? Promise.resolve();
		const next = previous.then(() => respond(command, target, epoch, controller));
		commandChains.set(command.sessionId, next);
		void next.finally(() => {
			if (commandChains.get(command.sessionId) === next) commandChains.delete(command.sessionId);
		});
	};

	const consume = (chunk: Buffer, target: net.Socket, epoch: number, handshake: Handshake) => {
		if (socket !== target || connectionEpoch !== epoch) return;
		buffer += decoder.write(chunk);
		for (;;) {
			const newline = buffer.indexOf("\n");
			if (newline < 0) {
				if (Buffer.byteLength(buffer, "utf8") > MAX_COMMAND_BYTES) {
					log("browser-runtime-link: oversized command frame; reconnecting");
					target.destroy();
				}
				return;
			}
			const line = buffer.slice(0, newline);
			buffer = buffer.slice(newline + 1);
			if (Buffer.byteLength(line, "utf8") > MAX_COMMAND_BYTES) {
				log("browser-runtime-link: oversized command frame; reconnecting");
				target.destroy();
				return;
			}
			consumeLine(line, target, epoch, handshake);
		}
	};

	const scheduleReconnect = () => {
		if (disposed) return;
		clearRetry();
		const delay = backoff;
		backoff = Math.min(backoff * 2, BACKOFF_MAX_MS);
		retryTimer = setTimeout(connect, delay);
	};

	function connect() {
		if (disposed) return;
		destroySocket();
		buffer = "";
		decoder = new StringDecoder("utf8");
		const epoch = ++connectionEpoch;
		const next = typeof address === "string" ? net.connect(address) : net.connect(address);
		socket = next;
		let opened = false;
		let accepted = false;
		let refused = false;
		// A TCP connect is not an accepted link: only the daemon's answer to the
		// hello settles the handshake. Treating connect as success flipped the state
		// and reset the backoff on every refused attempt (#6466).
		const handshake: Handshake = {
			accept: () => {
				if (accepted || refused || socket !== next || connectionEpoch !== epoch) return;
				accepted = true;
				unansweredStreak = 0;
				backoff = BACKOFF_INIT_MS;
				setState(CONNECTED);
				log("browser-runtime-link: connected");
			},
			refuse: (error) => {
				if (accepted || refused || socket !== next || connectionEpoch !== epoch) return;
				refused = true;
				// Keep the daemon's reason: a stale token, a protocol mismatch and a
				// malformed hello are all refusals, with different causes.
				const reason = error ? `${error.code}: ${error.message}` : "no reason given";
				log(
					`browser-runtime-link: the AO daemon refused this app's browser runtime (${reason}); ` +
						"run `ao stop`, then reopen AO.",
				);
				setState({ ...REJECTED, rejectionCode: error?.code });
				// A refused token cannot be retried into acceptance, so the caller may
				// have to retire the daemon it adopted instead of waiting for a redial.
				options.onHandshakeRejected?.(handshakeRejectionReason(error));
			},
		};
		next.on("connect", () => {
			if (disposed) {
				next.destroy();
				return;
			}
			opened = true;
			void send({ type: "hello", version: PROTOCOL_VERSION, token: options.token }, next, epoch).catch((error) => {
				log(`browser-runtime-link: hello failed: ${String(error)}`);
				next.destroy();
			});
		});
		next.on("data", (chunk) => consume(chunk, next, epoch, handshake));
		next.on("error", (error) => log(`browser-runtime-link: error: ${error.message}`));
		let connectionTornDown = false;
		const tearDownConnection = () => {
			if (connectionTornDown || socket !== next || connectionEpoch !== epoch) return;
			connectionTornDown = true;
			cancelConnectionCommands();
			socket = null;
			connectionEpoch += 1;
			// The same token would be refused again. Stay down until the user asks to
			// reconnect or the daemon changes, both of which build a new link.
			if (refused) return;
			if (accepted) {
				// A daemon restart or replaced link. Redial at once, and report a
				// disconnect only if that attempt fails too.
				setState(CONNECTING);
			} else {
				// An unreachable daemon, a transport error, or an older daemon closing on
				// a hello it refused. None of these proves a refusal, so keep retrying
				// without resetting the backoff.
				unansweredStreak += 1;
				if (unansweredStreak === 1) log("browser-runtime-link: link closed before the daemon answered the hello; retrying");
				// A daemon predating hello-rejected refuses a stale token by simply
				// hanging up, so this is the only signal the recovery path gets for it.
				// An accepted link that drops is an ordinary disconnect, reported above
				// instead, and must never retire a healthy daemon.
				if (opened) options.onHandshakeRejected?.(HANDSHAKE_CLOSED_BEFORE_ACCEPT);
				setState(BROWSER_RUNTIME_DISCONNECTED);
			}
			if (!disposed) scheduleReconnect();
		};
		// Cancel while the socket may still accept writes; 'close' can arrive too late.
		next.on("end", tearDownConnection);
		next.on("close", tearDownConnection);
	}

	options.onStateChange?.(state);
	connect();
	return {
		get state() {
			return state;
		},
		dispose() {
			disposed = true;
			clearRetry();
			destroySocket();
		},
	};
}

function normalizeCommandError(error: unknown): BrowserRuntimeCommandError {
	if (isCommandError(error)) {
		return { code: error.code, message: error.message };
	}
	return {
		code: "BROWSER_COMMAND_FAILED",
		message: error instanceof Error ? error.message : "Browser command failed",
	};
}

function isCommandError(error: unknown): error is BrowserRuntimeCommandError {
	return Boolean(
		error &&
		typeof error === "object" &&
		typeof (error as BrowserRuntimeCommandError).code === "string" &&
		typeof (error as BrowserRuntimeCommandError).message === "string",
	);
}
