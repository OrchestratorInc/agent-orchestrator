import { openChannel, type Channel } from "./channel";
import { E2EEError } from "./handshake";

// HTTP/1.1 over an encrypted channel. React Native's fetch and WebSocket cannot
// be pointed at a custom transport, so requests to a machine with a pinned key
// are written here instead.

/** Where a machine's encrypted channel is opened, and the key it must prove. */
export type SecureTarget = { url: string; hostKey: Uint8Array; authority: string };

export type SecureRequest = { method: string; path: string; headers?: Record<string, string>; body?: string };

const EMPTY = new Uint8Array(0);
const MAX_HEAD_LINE = 64 * 1024;

function concat(a: Uint8Array, b: Uint8Array): Uint8Array {
	const out = new Uint8Array(a.length + b.length);
	out.set(a);
	out.set(b, a.length);
	return out;
}

/** A closed channel before the response started: the request may not have reached the machine. */
export class NoResponseError extends E2EEError {}

/** Buffered reads from a channel. */
export class Wire {
	private buf: Uint8Array = EMPTY;
	private waiting: (() => void) | null = null;
	private ended: { error?: Error } | null = null;
	/** Bytes received for the current request, to tell a reused channel that died unanswered. */
	received = 0;

	constructor(readonly channel: Channel) {
		channel.onData = (bytes) => {
			this.received += bytes.length;
			this.buf = this.buf.length ? concat(this.buf, bytes) : bytes;
			this.wake();
		};
		channel.onClose = (error) => {
			this.ended = { error };
			this.wake();
		};
	}

	get closed(): boolean {
		return this.ended !== null || this.channel.closed;
	}

	write(bytes: Uint8Array): void {
		this.channel.write(bytes);
	}

	close(): void {
		this.channel.close();
	}

	private wake() {
		const waiting = this.waiting;
		this.waiting = null;
		waiting?.();
	}

	private more(): Promise<void> {
		if (this.ended) {
			const error = this.ended.error ?? new E2EEError("The machine closed the connection.");
			return Promise.reject(this.received === 0 ? new NoResponseError(error.message) : error);
		}
		return new Promise((resolve) => {
			this.waiting = resolve;
		});
	}

	async readLine(): Promise<string> {
		for (;;) {
			for (let i = 0; i + 1 < this.buf.length; i++) {
				if (this.buf[i] === 13 && this.buf[i + 1] === 10) {
					const line = this.buf.subarray(0, i);
					this.buf = this.buf.subarray(i + 2);
					return new TextDecoder().decode(line);
				}
			}
			if (this.buf.length > MAX_HEAD_LINE) throw new E2EEError("The machine sent a header line that is too long.");
			await this.more();
		}
	}

	/** Up to `max` bytes, waiting for at least one; null once the channel ended. */
	async readUpTo(max: number): Promise<Uint8Array | null> {
		while (this.buf.length === 0) {
			if (this.ended && !this.ended.error) return null;
			await this.more();
		}
		const taken = this.buf.subarray(0, Math.min(max, this.buf.length));
		this.buf = this.buf.subarray(taken.length);
		return taken;
	}

	async readExactly(count: number): Promise<Uint8Array> {
		const out = new Uint8Array(count);
		for (let at = 0; at < count;) {
			const piece = await this.readUpTo(count - at);
			if (!piece) throw new E2EEError("The machine closed the connection mid-message.");
			out.set(piece, at);
			at += piece.length;
		}
		return out;
	}
}

const FORBIDDEN = new Set(["host", "content-length", "connection", "transfer-encoding", "upgrade"]);

/** The request line and headers, ready to write. */
export function requestHead(authority: string, method: string, path: string, headers: Record<string, string>, extra: Record<string, string> = {}): Uint8Array {
	const lines = [`${method} ${path} HTTP/1.1`, `Host: ${authority}`];
	for (const [name, value] of Object.entries(headers)) {
		if (!FORBIDDEN.has(name.toLowerCase())) lines.push(`${name}: ${value}`);
	}
	for (const [name, value] of Object.entries(extra)) lines.push(`${name}: ${value}`);
	if (lines.some((line) => /[\r\n]/.test(line)) || /\s/.test(path)) throw new E2EEError("A request header holds a line break.");
	return new TextEncoder().encode(`${lines.join("\r\n")}\r\n\r\n`);
}

export type ResponseHead = { status: number; statusText: string; headers: Headers };

export async function readHead(wire: Wire): Promise<ResponseHead> {
	for (;;) {
		const match = /^HTTP\/1\.[01] (\d{3})(?: (.*))?$/.exec(await wire.readLine());
		if (!match) throw new E2EEError("The machine sent an unreadable response.");
		const headers = new Headers();
		for (let line = await wire.readLine(); line !== ""; line = await wire.readLine()) {
			const colon = line.indexOf(":");
			if (colon > 0) headers.append(line.slice(0, colon).trim(), line.slice(colon + 1).trim());
		}
		const status = Number(match[1]);
		// 100 Continue and other interim answers precede the real one.
		if (status >= 100 && status < 200 && status !== 101) continue;
		return { status, statusText: match[2] ?? "", headers };
	}
}

/** Reads one response body; `reusable` says whether the channel can carry another request after it. */
function bodyReader(wire: Wire, head: ResponseHead, method: string): { next: () => Promise<Uint8Array | null>; reusable: boolean } {
	const reusable = !/\bclose\b/i.test(head.headers.get("connection") ?? "");
	if (method === "HEAD" || head.status === 204 || head.status === 304) return { next: async () => null, reusable };
	if (/\bchunked\b/i.test(head.headers.get("transfer-encoding") ?? "")) {
		let left = 0;
		let done = false;
		return {
			reusable,
			next: async () => {
				if (done) return null;
				if (left === 0) {
					const size = parseInt((await wire.readLine()).split(";")[0].trim(), 16);
					if (!Number.isFinite(size) || size < 0) throw new E2EEError("The machine sent an unreadable chunk.");
					if (size === 0) {
						while ((await wire.readLine()) !== "");
						done = true;
						return null;
					}
					left = size;
				}
				const piece = await wire.readUpTo(left);
				if (!piece) throw new E2EEError("The machine closed the connection mid-response.");
				left -= piece.length;
				if (left === 0) await wire.readLine();
				return piece;
			},
		};
	}
	const length = head.headers.get("content-length");
	if (length !== null) {
		let left = Number(length);
		if (!Number.isSafeInteger(left) || left < 0) throw new E2EEError("The machine sent an unreadable length.");
		return {
			reusable,
			next: async () => {
				if (left === 0) return null;
				const piece = await wire.readUpTo(left);
				if (!piece) throw new E2EEError("The machine closed the connection mid-response.");
				left -= piece.length;
				return piece;
			},
		};
	}
	return { next: () => wire.readUpTo(Number.POSITIVE_INFINITY), reusable: false };
}

/** A response read off the channel, with the parts of fetch's Response the app uses. */
export class SecureResponse {
	readonly ok: boolean;
	readonly body: {
		getReader(): { read(): Promise<{ done: boolean; value?: Uint8Array }>; cancel(): Promise<void>; releaseLock(): void };
		cancel(): Promise<void>;
	};

	constructor(
		readonly status: number,
		readonly statusText: string,
		readonly headers: Headers,
		private readonly next: () => Promise<Uint8Array | null>,
		cancel: () => void,
	) {
		this.ok = status >= 200 && status < 300;
		const cancelled = async () => cancel();
		this.body = {
			getReader: () => ({
				read: async () => {
					const value = await next();
					return value ? { done: false, value } : { done: true };
				},
				cancel: cancelled,
				releaseLock: () => {},
			}),
			cancel: cancelled,
		};
	}

	async bytes(): Promise<Uint8Array> {
		const pieces: Uint8Array[] = [];
		let length = 0;
		for (let piece = await this.next(); piece; piece = await this.next()) {
			pieces.push(piece);
			length += piece.length;
		}
		const out = new Uint8Array(length);
		let at = 0;
		for (const piece of pieces) {
			out.set(piece, at);
			at += piece.length;
		}
		return out;
	}

	async arrayBuffer(): Promise<ArrayBuffer> {
		return (await this.bytes()).buffer as ArrayBuffer;
	}

	async text(): Promise<string> {
		return new TextDecoder().decode(await this.bytes());
	}

	async json(): Promise<unknown> {
		return JSON.parse(await this.text());
	}
}

const IDLE_MS = 60_000;
const MAX_IDLE = 4;
const idle = new Map<string, { wire: Wire; timer: ReturnType<typeof setTimeout> }[]>();
const poolKey = (target: SecureTarget) => `${target.url}\n${Array.from(target.hostKey).join(",")}`;

function takeIdle(key: string): Wire | null {
	const list = idle.get(key) ?? [];
	for (let entry = list.pop(); entry; entry = list.pop()) {
		clearTimeout(entry.timer);
		if (!entry.wire.closed) return entry.wire;
	}
	return null;
}

function putIdle(key: string, wire: Wire): void {
	const list = idle.get(key) ?? [];
	idle.set(key, list);
	if (wire.closed || list.length >= MAX_IDLE) return wire.close();
	const entry = {
		wire,
		timer: setTimeout(() => {
			list.splice(list.indexOf(entry), 1);
			wire.close();
		}, IDLE_MS),
	};
	list.push(entry);
}

/** Opens a channel to the machine, for one request or one WebSocket. */
export async function openWire(target: SecureTarget): Promise<Wire> {
	return new Wire(await openChannel(target.url, target.hostKey));
}

/** Sends one request over a pooled channel to the machine. */
export async function secureFetch(target: SecureTarget, request: SecureRequest, signal?: AbortSignal): Promise<SecureResponse> {
	const key = poolKey(target);
	const method = request.method.toUpperCase();
	for (let attempt = 0; ; attempt++) {
		if (signal?.aborted) throw abortError();
		const reused = takeIdle(key);
		const wire = reused ?? (await openWire(target));
		const onAbort = () => wire.close();
		signal?.addEventListener("abort", onAbort);
		try {
			const body = request.body === undefined ? EMPTY : new TextEncoder().encode(request.body);
			const sized = request.body !== undefined || !["GET", "HEAD"].includes(method);
			const head = requestHead(target.authority, method, request.path, request.headers ?? {}, sized ? { "Content-Length": String(body.length) } : {});
			wire.received = 0;
			wire.write(body.length ? concat(head, body) : head);
			const response = await readHead(wire);
			const reader = bodyReader(wire, response, method);
			let finished = false;
			const finish = (reuse: boolean) => {
				if (finished) return;
				finished = true;
				signal?.removeEventListener("abort", onAbort);
				if (reuse) putIdle(key, wire);
				else wire.close();
			};
			const next = async () => {
				try {
					const piece = await reader.next();
					if (!piece) finish(reader.reusable);
					return piece;
				} catch (error) {
					finish(false);
					throw signal?.aborted ? abortError() : error;
				}
			};
			return new SecureResponse(response.status, response.statusText, response.headers, next, () => finish(false));
		} catch (error) {
			signal?.removeEventListener("abort", onAbort);
			wire.close();
			if (signal?.aborted) throw abortError();
			// A pooled channel the machine closed while idle: send it again on a new one.
			if (reused && attempt === 0 && error instanceof NoResponseError && (method === "GET" || method === "HEAD")) continue;
			throw error;
		}
	}
}

function abortError(): Error {
	return Object.assign(new Error("Request cancelled"), { name: "AbortError" });
}

/** Closes every pooled channel, as after a machine is forgotten. */
export function closeIdleChannels(): void {
	for (const list of idle.values()) {
		for (const entry of list.splice(0)) {
			clearTimeout(entry.timer);
			entry.wire.close();
		}
	}
}
