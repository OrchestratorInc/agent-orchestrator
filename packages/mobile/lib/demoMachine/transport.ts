import { DEMO_HOSTNAME, DEMO_TICK_MS, DemoServer, type DemoEvent } from "./server";

// Development builds only. Requests to the demo machine's address never reach
// the network: global fetch, expo/fetch's event stream and WebSocket are
// wrapped so that address is answered by DemoServer, after a delay that stands
// in for the network. Every other address passes straight through.

export type DemoNetwork = "fast" | "slow" | "bad" | "terrible" | "offline";

/** One-way-and-back delay per request. "offline" fails every request, like a machine that is off. */
export const DEMO_NETWORKS: Record<DemoNetwork, { label: string; latencyMs: number }> = {
	fast: { label: "Fast", latencyMs: 30 },
	slow: { label: "Slow (400 ms)", latencyMs: 400 },
	bad: { label: "Bad (1.5 s)", latencyMs: 1_500 },
	terrible: { label: "Terrible (4 s)", latencyMs: 4_000 },
	offline: { label: "Machine off", latencyMs: 0 },
};

/**
 * True only in a development build. Read on every call rather than once, so a
 * release build (where Metro inlines __DEV__ as false) can never route a request
 * to the demo machine, and tests can flip it.
 */
export function isDevBuild(): boolean {
	return typeof __DEV__ !== "undefined" && __DEV__ === true;
}

let network: DemoNetwork = "fast";
const networkListeners = new Set<() => void>();
let server: DemoServer | null = null;
let ticker: ReturnType<typeof setInterval> | null = null;
let chatPagesInFlight = 0;
let installed = false;

export function demoNetwork(): DemoNetwork {
	return network;
}

export function setDemoNetwork(next: DemoNetwork): void {
	network = next;
	for (const listener of networkListeners) listener();
}

export function subscribeDemoNetwork(listener: () => void): () => void {
	networkListeners.add(listener);
	return () => { networkListeners.delete(listener); };
}

/** The one demo machine, created and set replying on first use. */
export function demoServer(): DemoServer {
	if (!server) {
		server = new DemoServer();
		ticker = setInterval(() => server?.tick(), DEMO_TICK_MS);
	}
	return server;
}

/** For tests: forget the machine and its replies. */
export function resetDemoServer(): void {
	if (ticker) clearInterval(ticker);
	ticker = null;
	server = null;
	chatPagesInFlight = 0;
	network = "fast";
}

function urlOf(input: unknown): string {
	if (typeof input === "string") return input;
	if (input instanceof URL) return input.href;
	return String((input as { url?: unknown })?.url ?? "");
}

const DEMO_URL = new RegExp(`^(https?|wss?)://${DEMO_HOSTNAME.replace(/\./g, "\\.")}(:\\d+)?(/|$)`, "i");

/** Whether a request goes to the demo machine. Always false outside a development build. */
export function isDemoUrl(input: unknown): boolean {
	return isDevBuild() && DEMO_URL.test(urlOf(input));
}

function abortError(): Error {
	return Object.assign(new Error("Aborted"), { name: "AbortError" });
}

function jittered(ms: number): number {
	return ms <= 0 ? 0 : Math.round(ms * (0.8 + Math.random() * 0.4));
}

/** Waits out the network, or fails the way React Native's fetch does. */
function travel(signal: AbortSignal | null | undefined, ms = jittered(DEMO_NETWORKS[network].latencyMs)): Promise<void> {
	return new Promise((resolve, reject) => {
		if (signal?.aborted) return reject(abortError());
		const offline = network === "offline";
		const timer = setTimeout(() => {
			signal?.removeEventListener("abort", onAbort);
			if (offline) reject(new TypeError("Network request failed"));
			else resolve();
		}, offline ? 200 : ms);
		function onAbort() {
			clearTimeout(timer);
			reject(abortError());
		}
		signal?.addEventListener("abort", onAbort, { once: true });
	});
}

/** Answers one request to the demo machine. */
export async function demoFetch(input: unknown, init?: RequestInit): Promise<Response> {
	if (!isDevBuild()) throw new TypeError("Network request failed");
	const url = urlOf(input);
	const method = (init?.method ?? "GET").toUpperCase();
	const label = `${method} ${url.replace(/^https?:\/\/[^/]+/, "")}`;
	const isChatPage = method === "GET" && /\/conversation(\?|$)/.test(url);
	// The Problem 2 signal: chat pages still downloading when the next one starts.
	if (isChatPage) console.info(`[demo] ${label} · ${++chatPagesInFlight} chat page(s) downloading on ${DEMO_NETWORKS[network].label}`);
	try {
		await travel(init?.signal);
		const answer = demoServer().handle(method, url, typeof init?.body === "string" ? init.body : undefined);
		return new Response(answer.text ?? JSON.stringify(answer.body), {
			status: answer.status,
			statusText: answer.status === 200 ? "OK" : "Error",
			headers: { "Content-Type": answer.contentType ?? "application/json" },
		});
	} finally {
		if (isChatPage) chatPagesInFlight--;
	}
}

type StreamResponse = { ok: boolean; status: number; headers: Headers; body: { getReader(): StreamReader } };
type StreamReader = { read(): Promise<{ done: boolean; value?: Uint8Array }>; cancel(): Promise<void>; releaseLock(): void };

/**
 * The daemon's /api/v1/events stream: one SSE frame per chat change, sent as
 * the demo chats reply. The shape is what streamGlobalConversationEvents reads
 * from expo/fetch, not a full Response.
 */
export async function demoEventStream(_input: unknown, init?: { signal?: AbortSignal | null }): Promise<StreamResponse> {
	if (!isDevBuild()) throw new TypeError("Network request failed");
	await travel(init?.signal);
	const source = demoServer();
	const encoder = new TextEncoder();
	const queue: Uint8Array[] = [];
	let waiting: ((result: { done: boolean; value?: Uint8Array }) => void) | null = null;
	let closed = false;
	const timers = new Set<ReturnType<typeof setTimeout>>();

	const push = (chunk: Uint8Array) => {
		if (closed) return;
		if (waiting) {
			const resolve = waiting;
			waiting = null;
			resolve({ done: false, value: chunk });
		} else queue.push(chunk);
	};
	const close = () => {
		if (closed) return;
		closed = true;
		stopEvents();
		stopNetwork();
		for (const timer of timers) clearTimeout(timer);
		if (waiting) {
			const resolve = waiting;
			waiting = null;
			resolve({ done: true });
		}
	};
	const stopEvents = source.onEvent((event: DemoEvent) => {
		// Frames arrive late on a slow link, but in order and at the same cadence.
		const timer = setTimeout(() => {
			timers.delete(timer);
			push(encoder.encode(`id: ${event.seq}\ndata: ${JSON.stringify(event)}\n\n`));
		}, jittered(DEMO_NETWORKS[network].latencyMs / 2));
		timers.add(timer);
	});
	// Switching to "Machine off" drops the stream, so the app's reconnect runs.
	const stopNetwork = subscribeDemoNetwork(() => { if (network === "offline") close(); });
	init?.signal?.addEventListener("abort", close, { once: true });

	return {
		ok: true,
		status: 200,
		headers: new Headers({ "X-AO-Event-After": String(source.latestEventSeq) }),
		body: {
			getReader: () => ({
				read: () => {
					const next = queue.shift();
					if (next) return Promise.resolve({ done: false, value: next });
					if (closed) return Promise.resolve({ done: true });
					return new Promise((resolve) => { waiting = resolve; });
				},
				cancel: async () => close(),
				releaseLock: () => {},
			}),
		},
	};
}

function base64(text: string): string {
	const bytes = new TextEncoder().encode(text);
	let binary = "";
	for (const byte of bytes) binary += String.fromCharCode(byte);
	return btoa(binary);
}

/**
 * The /mux socket: opens, accepts subscriptions quietly, and gives a terminal
 * a banner and an echo so a TUI worker shows something.
 */
class DemoSocket {
	static readonly CONNECTING = 0;
	static readonly OPEN = 1;
	static readonly CLOSING = 2;
	static readonly CLOSED = 3;
	readyState = DemoSocket.CONNECTING;
	onopen: ((event: unknown) => void) | null = null;
	onmessage: ((event: { data: string }) => void) | null = null;
	onerror: ((event: unknown) => void) | null = null;
	onclose: ((event: unknown) => void) | null = null;
	private readonly stopNetwork: () => void;

	constructor(readonly url: string) {
		this.stopNetwork = subscribeDemoNetwork(() => { if (network === "offline") this.close(); });
		travel(null).then(
			() => {
				if (this.readyState !== DemoSocket.CONNECTING) return;
				this.readyState = DemoSocket.OPEN;
				this.onopen?.({});
			},
			() => {
				this.onerror?.({});
				this.close();
			},
		);
	}

	addEventListener(type: "open" | "message" | "error" | "close", listener: (event: never) => void): void {
		const key = `on${type}` as "onopen";
		const previous = this[key];
		this[key] = (event: unknown) => { previous?.(event); (listener as (event: unknown) => void)(event); };
	}

	removeEventListener(): void {}

	send(raw: string): void {
		if (this.readyState !== DemoSocket.OPEN) return;
		let msg: { ch?: string; type?: string; id?: string; data?: string };
		try { msg = JSON.parse(raw) as typeof msg; } catch { return; }
		if (msg.ch !== "terminal" || !msg.id) return;
		const reply = (frame: object) => setTimeout(() => this.onmessage?.({ data: JSON.stringify(frame) }), jittered(DEMO_NETWORKS[network].latencyMs / 2));
		if (msg.type === "open") {
			reply({ ch: "terminal", id: msg.id, type: "opened" });
			reply({ ch: "terminal", id: msg.id, type: "data", data: base64(`\x1b[1;32mdemo machine\x1b[0m · ${msg.id}\r\nThis terminal echoes what you type.\r\n$ `) });
		} else if (msg.type === "data" && msg.data) {
			reply({ ch: "terminal", id: msg.id, type: "data", data: msg.data });
		}
	}

	close(): void {
		if (this.readyState === DemoSocket.CLOSED) return;
		this.readyState = DemoSocket.CLOSED;
		this.stopNetwork();
		this.onclose?.({ code: 1000 });
	}
}

/** Routes the demo machine's address to DemoServer. Development builds only; safe to call twice. */
export function installDemoTransport(): void {
	if (!isDevBuild() || installed) return;
	installed = true;
	const realFetch = globalThis.fetch;
	globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) =>
		isDemoUrl(input) ? demoFetch(input, init) : realFetch(input, init)) as typeof fetch;

	const RealSocket = globalThis.WebSocket;
	function DemoAwareSocket(this: unknown, url: string, protocols?: string | string[], options?: unknown) {
		if (isDemoUrl(url)) return new DemoSocket(url);
		return new (RealSocket as unknown as new (...args: unknown[]) => WebSocket)(url, protocols, options);
	}
	DemoAwareSocket.prototype = RealSocket.prototype;
	Object.assign(DemoAwareSocket, { CONNECTING: 0, OPEN: 1, CLOSING: 2, CLOSED: 3 });
	globalThis.WebSocket = DemoAwareSocket as unknown as typeof WebSocket;
}
