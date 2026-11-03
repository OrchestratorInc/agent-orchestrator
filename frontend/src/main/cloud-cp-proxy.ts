// Electron-main transport bridge for cloud control-plane (CP) calls.
//
// The CP (cloud/internal/httpapi) has no CORS, and the WorkOS access token
// lives only in the main process (cloud-auth.ts). The renderer therefore
// never talks to the CP directly: it hands a { baseUrl, path, ... } request
// to this proxy over IPC, main attaches `Authorization: Bearer <token>` via
// cloud-auth's refresh-aware accessor, performs the fetch, and returns the
// response (or forwards SSE chunks over a per-stream channel). The bearer
// token never crosses the context bridge in either direction — a renderer
// supplied Authorization header is discarded, and only status/headers/body
// travel back. The token goes only to the configured control plane (see
// cloud-control-plane-url.ts): a request whose baseUrl names any other server
// is refused before the token is read.

import { ipcMain } from "electron";
import { randomUUID } from "node:crypto";
import { getCloudAccessToken } from "./cloud-auth";
import { sameControlPlaneUrl, type ControlPlaneUrlResolver } from "./cloud-control-plane-url";
import {
	CLOUD_CP_BINARY_TIMEOUT,
	CLOUD_CP_BINARY_TOO_LARGE,
	DEFAULT_CLOUD_CP_BINARY_TIMEOUT_MS,
	MAX_CLOUD_CP_BINARY_BYTES,
	MAX_CLOUD_CP_BINARY_TIMEOUT_MS,
} from "../shared/cloud-binary";

export const CLOUD_CP_REQUEST_CHANNEL = "cloudCp:request";
export const CLOUD_CP_REQUEST_BINARY_CHANNEL = "cloudCp:requestBinary";
export const CLOUD_CP_CANCEL_BINARY_CHANNEL = "cloudCp:cancelBinary";
export const CLOUD_CP_OPEN_STREAM_CHANNEL = "cloudCp:openStream";
export const CLOUD_CP_CLOSE_STREAM_CHANNEL = "cloudCp:closeStream";

/** Per-stream renderer event channel; preload subscribes with this exact shape. */
export function cloudCpStreamChannel(streamId: string): string {
	return `cloudCp:stream:${streamId}`;
}

/** Only CP API paths may be proxied; anything else is rejected before fetch. */
const API_PREFIX = "/api/cloud/v1";

/** Upper bound on live SSE streams per renderer WebContents. */
export const MAX_STREAMS_PER_WEBCONTENTS = 8;

/**
 * openStream failures cross IPC as rejected promises, which only preserve the
 * message string. This marker + status prefix lets the renderer stream bridge
 * rebuild a typed error (401 → auth error) from the flattened message.
 */
export const CLOUD_CP_STREAM_ERROR_MARKER = "CLOUD_CP_STREAM_ERROR";

export interface CloudCpProxyRequestInit {
	baseUrl: string;
	path: string;
	method: string;
	headers?: Record<string, string>;
	body?: string;
}

export interface CloudCpProxyResponse {
	status: number;
	headers: Record<string, string>;
	body: string;
}

/**
 * A request whose body or response is bytes: a session attachment's upload or
 * download. The text channel above cannot carry binary.
 */
export interface CloudCpProxyBinaryRequestInit {
	baseUrl: string;
	path: string;
	method: string;
	headers?: Record<string, string>;
	body?: Uint8Array;
	/** Names the request for cloudCp:cancelBinary; one the renderer chose. */
	requestId?: string;
	/** How long the request may take, at most MAX_CLOUD_CP_BINARY_TIMEOUT_MS. */
	timeoutMs?: number;
}

export interface CloudCpProxyBinaryResponse {
	status: number;
	headers: Record<string, string>;
	body: Uint8Array;
}

/** Bytes either way on the binary channel; the control plane takes attachments up to 25 MiB. */
export { MAX_CLOUD_CP_BINARY_BYTES };

export type CloudCpStreamEvent =
	| { type: "chunk"; data: string }
	| { type: "end" }
	| { type: "error"; message: string };

/**
 * Structural slice of Electron.WebContents the stream side needs. Kept
 * minimal so tests can drive streams with a plain object.
 */
export interface CloudCpStreamSender {
	readonly id: number;
	isDestroyed(): boolean;
	send(channel: string, event: CloudCpStreamEvent): void;
	once(event: "destroyed", listener: () => void): unknown;
}

export interface CloudCpProxy {
	request(init: unknown): Promise<CloudCpProxyResponse>;
	/** sender is the WebContents that asked: its requests end when it goes away. */
	requestBinary(init: unknown, sender?: CloudCpBinarySender): Promise<CloudCpProxyBinaryResponse>;
	/** Abort one of a WebContents' binary requests; another's is never touched. */
	cancelBinary(sender: Pick<CloudCpBinarySender, "id">, requestId: unknown): void;
	openStream(sender: CloudCpStreamSender, init: unknown): Promise<{ streamId: string }>;
	closeStream(sender: Pick<CloudCpStreamSender, "id">, streamId: unknown): void;
}

/** Structural slice of Electron.WebContents the binary side needs. */
export interface CloudCpBinarySender {
	readonly id: number;
	once(event: "destroyed", listener: () => void): unknown;
}

export interface CloudCpProxyOptions {
	/** The configured control plane, the only server the token is sent to. */
	controlPlaneUrl: ControlPlaneUrlResolver;
	/** Transport override for tests; defaults to the main-process global fetch. */
	fetchImpl?: typeof fetch;
}

function invalid(detail: string): Error {
	return new Error(`Invalid cloud control-plane request: ${detail}`);
}

function streamError(status: number, detail: string): Error {
	return new Error(`${CLOUD_CP_STREAM_ERROR_MARKER} ${status} ${detail}`);
}

function isLoopbackHost(hostname: string): boolean {
	return hostname === "localhost" || hostname === "127.0.0.1";
}

/** A renderer-supplied CP base URL: https, or http on loopback only. */
export function parseCloudBaseUrl(baseUrl: unknown): URL {
	if (typeof baseUrl !== "string" || baseUrl === "") throw invalid("baseUrl must be a non-empty string.");
	let base: URL;
	try {
		base = new URL(baseUrl);
	} catch {
		throw invalid(`baseUrl ${JSON.stringify(baseUrl)} is not a valid URL.`);
	}
	const httpsOk = base.protocol === "https:";
	const loopbackHttpOk = base.protocol === "http:" && isLoopbackHost(base.hostname);
	if (!httpsOk && !loopbackHttpOk) {
		throw invalid("baseUrl must use https (http is allowed only for localhost/127.0.0.1).");
	}
	if (base.username !== "" || base.password !== "" || base.search !== "" || base.hash !== "") {
		throw invalid("baseUrl must not carry credentials, a query string, or a fragment.");
	}
	return base;
}

function validateInit(init: unknown): CloudCpProxyRequestInit {
	const { body } = (typeof init === "object" && init !== null ? init : {}) as Record<string, unknown>;
	if (body !== undefined && typeof body !== "string") throw invalid("body must be a string when present.");
	return validateRequestTarget(init) as CloudCpProxyRequestInit;
}

const REQUEST_ID = /^[\w-]{1,64}$/;

function validateBinaryInit(init: unknown): CloudCpProxyBinaryRequestInit {
	const { body, requestId, timeoutMs } = (typeof init === "object" && init !== null ? init : {}) as Record<string, unknown>;
	if (body !== undefined && !(body instanceof Uint8Array)) throw invalid("body must be bytes when present.");
	if (requestId !== undefined && (typeof requestId !== "string" || !REQUEST_ID.test(requestId))) throw invalid("requestId must be a short token.");
	if (timeoutMs !== undefined && (typeof timeoutMs !== "number" || !Number.isFinite(timeoutMs) || timeoutMs <= 0)) {
		throw invalid("timeoutMs must be a positive number.");
	}
	return {
		...(validateRequestTarget(init) as CloudCpProxyBinaryRequestInit),
		requestId: requestId as string | undefined,
		timeoutMs: timeoutMs as number | undefined,
	};
}

/** A binary response main makes itself, in the control plane's error envelope. */
function binaryError(status: number, code: string, message: string): CloudCpProxyBinaryResponse {
	return {
		status,
		headers: { "content-type": "application/json" },
		body: new TextEncoder().encode(JSON.stringify({ error: code, code, message })),
	};
}

const MiB = 1024 * 1024;

function validateRequestTarget(init: unknown): CloudCpProxyRequestInit | CloudCpProxyBinaryRequestInit {
	if (typeof init !== "object" || init === null) {
		throw invalid("the request payload must be an object.");
	}
	const { baseUrl, path, method, headers, body } = init as Record<string, unknown>;
	parseCloudBaseUrl(baseUrl);
	if (typeof path !== "string") throw invalid("path must be a string.");
	if (typeof method !== "string" || !/^[A-Za-z]+$/.test(method)) {
		throw invalid("method must be an HTTP method token.");
	}
	if (headers !== undefined) {
		if (typeof headers !== "object" || headers === null || Array.isArray(headers)) {
			throw invalid("headers must be a plain record of strings.");
		}
		for (const value of Object.values(headers)) {
			if (typeof value !== "string") throw invalid("headers must be a plain record of strings.");
		}
	}

	if (!path.startsWith(API_PREFIX)) {
		throw invalid(`path must start with ${API_PREFIX}.`);
	}
	const boundary = path.charAt(API_PREFIX.length);
	if (boundary !== "" && boundary !== "/" && boundary !== "?") {
		throw invalid(`path must start with ${API_PREFIX}.`);
	}

	return {
		baseUrl: baseUrl as string,
		path,
		method,
		headers: headers as Record<string, string> | undefined,
		body: body as string | Uint8Array | undefined,
	} as CloudCpProxyRequestInit | CloudCpProxyBinaryRequestInit;
}

/**
 * Resolve the final URL and re-check it post-normalization: WHATWG URL
 * resolves dot segments (including percent-encoded ones) and backslashes, so
 * a raw-string prefix check alone would let "/api/cloud/v1/../../x" escape.
 */
function resolveTargetUrl(init: Pick<CloudCpProxyRequestInit, "baseUrl" | "path">): string {
	const base = new URL(init.baseUrl);
	const basePath = base.pathname.replace(/\/+$/, "");
	const target = new URL(base.origin + basePath + init.path);
	const expectedPrefix = `${basePath}${API_PREFIX}`;
	const inBounds =
		target.origin === base.origin &&
		(target.pathname === expectedPrefix || target.pathname.startsWith(`${expectedPrefix}/`));
	if (!inBounds) {
		throw invalid(`path must stay under ${API_PREFIX} after URL normalization.`);
	}
	return target.toString();
}

function buildHeaders(requested: Record<string, string> | undefined, token: string): Headers {
	const headers = new Headers();
	for (const [name, value] of Object.entries(requested ?? {})) {
		const lower = name.toLowerCase();
		// The main process owns credentials on this connection; a renderer can
		// never smuggle its own Authorization/Cookie/Host through the proxy.
		if (lower === "authorization" || lower === "cookie" || lower === "host") continue;
		headers.set(name, value);
	}
	headers.set("Authorization", `Bearer ${token}`);
	return headers;
}

function unauthorizedResponse(): CloudCpProxyResponse {
	return {
		status: 401,
		headers: { "content-type": "application/json" },
		body: JSON.stringify({
			error: "unauthorized",
			code: "no_token",
			message: "No AO Cloud session is available. Sign in and try again.",
		}),
	};
}

async function envelopeMessage(response: Response): Promise<string> {
	const fallback = `Cloud control plane stream request failed with status ${response.status}.`;
	try {
		const body = (await response.json()) as { message?: unknown; error?: unknown } | null;
		if (typeof body?.message === "string" && body.message !== "") return body.message;
		if (typeof body?.error === "string" && body.error !== "") return body.error;
	} catch {
		// Non-JSON or empty body: keep the status-derived message.
	}
	return fallback;
}

interface ActiveStream {
	controller: AbortController;
	sender: CloudCpStreamSender;
	channel: string;
	closed: boolean;
}

/**
 * The request's URL on the configured control plane. A renderer names its
 * baseUrl, but only the configured one is ever dialed: any other is refused.
 */
export async function pinnedTargetUrl(
	init: Pick<CloudCpProxyRequestInit, "baseUrl" | "path">,
	controlPlaneUrl: ControlPlaneUrlResolver,
): Promise<string> {
	const configured = await controlPlaneUrl();
	if (!configured) throw invalid("no AO Cloud control plane is configured.");
	parseCloudBaseUrl(configured);
	if (!sameControlPlaneUrl(init.baseUrl, configured)) {
		throw invalid("baseUrl is not the configured AO Cloud control plane.");
	}
	return resolveTargetUrl({ baseUrl: configured, path: init.path });
}

export function createCloudCpProxy(getDataDir: () => string, options: CloudCpProxyOptions): CloudCpProxy {
	// Wrap the default so the global fetch is never invoked detached from its
	// realm (Chromium/Node throw "Illegal invocation" for a bare reference).
	const doFetch: typeof fetch = options.fetchImpl ?? ((input, init) => fetch(input, init));

	const streams = new Map<string, ActiveStream>();
	const streamIdsBySender = new Map<number, Set<string>>();

	function teardown(streamId: string, stream: ActiveStream): void {
		stream.closed = true;
		streams.delete(streamId);
		streamIdsBySender.get(stream.sender.id)?.delete(streamId);
		stream.controller.abort();
	}

	function emit(stream: ActiveStream, event: CloudCpStreamEvent): void {
		if (stream.closed || stream.sender.isDestroyed()) return;
		stream.sender.send(stream.channel, event);
	}

	function trackSender(sender: CloudCpStreamSender): Set<string> {
		let ids = streamIdsBySender.get(sender.id);
		if (ids === undefined) {
			ids = new Set();
			streamIdsBySender.set(sender.id, ids);
			sender.once("destroyed", () => {
				const orphaned = streamIdsBySender.get(sender.id);
				streamIdsBySender.delete(sender.id);
				for (const streamId of orphaned ?? []) {
					const stream = streams.get(streamId);
					if (stream !== undefined) teardown(streamId, stream);
				}
			});
		}
		return ids;
	}

	async function pump(streamId: string, stream: ActiveStream, body: ReadableStream<Uint8Array>): Promise<void> {
		const reader = body.getReader();
		const decoder = new TextDecoder();
		try {
			for (;;) {
				const { done, value } = await reader.read();
				if (value !== undefined) {
					const data = decoder.decode(value, { stream: true });
					if (data !== "") emit(stream, { type: "chunk", data });
				}
				if (done) break;
			}
			const tail = decoder.decode();
			if (tail !== "") emit(stream, { type: "chunk", data: tail });
			emit(stream, { type: "end" });
		} catch (error) {
			emit(stream, {
				type: "error",
				message: error instanceof Error ? error.message : String(error),
			});
		} finally {
			teardown(streamId, stream);
			reader.releaseLock();
		}
	}

	async function request(init: unknown): Promise<CloudCpProxyResponse> {
		const valid = validateInit(init);
		const url = await pinnedTargetUrl(valid, options.controlPlaneUrl);
		const token = await getCloudAccessToken(getDataDir());
		// 401-shaped without touching the network, so the renderer client's
		// standard auth-error path handles signed-out exactly like a server 401.
		if (token === null || token === "") return unauthorizedResponse();
		const response = await doFetch(url, {
			method: valid.method.toUpperCase(),
			headers: buildHeaders(valid.headers, token),
			body: valid.body,
		});
		const headers: Record<string, string> = {};
		response.headers.forEach((value, key) => {
			headers[key] = value;
		});
		return { status: response.status, headers, body: await response.text() };
	}

	// In-flight binary requests by WebContents and request id, so a cancel
	// or the WebContents going away (a reload included: preload cancels on
	// pagehide) ends them instead of leaving an upload running unseen.
	const binaryRequests = new Map<number, Map<string, AbortController>>();

	function trackBinaryRequest(sender: CloudCpBinarySender | undefined, requestId: string | undefined, controller: AbortController): () => void {
		if (sender === undefined || requestId === undefined) return () => undefined;
		let requests = binaryRequests.get(sender.id);
		if (requests === undefined) {
			requests = new Map();
			binaryRequests.set(sender.id, requests);
			sender.once("destroyed", () => {
				for (const pending of binaryRequests.get(sender.id)?.values() ?? []) pending.abort();
				binaryRequests.delete(sender.id);
			});
		}
		requests.get(requestId)?.abort();
		requests.set(requestId, controller);
		return () => {
			if (requests.get(requestId) === controller) requests.delete(requestId);
		};
	}

	function cancelBinary(sender: Pick<CloudCpBinarySender, "id">, requestId: unknown): void {
		if (typeof requestId !== "string") return;
		binaryRequests.get(sender.id)?.get(requestId)?.abort();
	}

	async function requestBinary(init: unknown, sender?: CloudCpBinarySender): Promise<CloudCpProxyBinaryResponse> {
		const valid = validateBinaryInit(init);
		const url = await pinnedTargetUrl(valid, options.controlPlaneUrl);
		// Over the channel's limit is a refusal with a message the person can
		// read, not a request.
		if (valid.body !== undefined && valid.body.byteLength > MAX_CLOUD_CP_BINARY_BYTES) {
			return binaryError(413, CLOUD_CP_BINARY_TOO_LARGE,
				`The file is larger than ${MAX_CLOUD_CP_BINARY_BYTES / MiB} MiB, the most the app sends to AO Cloud at once.`);
		}
		const token = await getCloudAccessToken(getDataDir());
		if (token === null || token === "") {
			const unauthorized = unauthorizedResponse();
			return { ...unauthorized, body: new TextEncoder().encode(unauthorized.body) };
		}
		const timeoutMs = Math.min(valid.timeoutMs ?? DEFAULT_CLOUD_CP_BINARY_TIMEOUT_MS, MAX_CLOUD_CP_BINARY_TIMEOUT_MS);
		const cancelled = new AbortController();
		const timeout = AbortSignal.timeout(timeoutMs);
		const untrack = trackBinaryRequest(sender, valid.requestId, cancelled);
		try {
			const response = await doFetch(url, {
				method: valid.method.toUpperCase(),
				headers: buildHeaders(valid.headers, token),
				// A Uint8Array over IPC may view a SharedArrayBuffer-typed buffer; copy
				// into a plain ArrayBuffer that fetch accepts as a body.
				body: valid.body === undefined ? undefined : new Uint8Array(valid.body).buffer,
				signal: AbortSignal.any([cancelled.signal, timeout]),
			});
			const headers: Record<string, string> = {};
			response.headers.forEach((value, key) => {
				headers[key] = value;
			});
			const tooLarge = () => binaryError(413, CLOUD_CP_BINARY_TOO_LARGE,
				`The file is larger than ${MAX_CLOUD_CP_BINARY_BYTES / MiB} MiB, the most the app takes from AO Cloud at once.`);
			const declared = Number(response.headers.get("content-length"));
			if (Number.isFinite(declared) && declared > MAX_CLOUD_CP_BINARY_BYTES) {
				await response.body?.cancel().catch(() => undefined);
				return tooLarge();
			}
			const body = new Uint8Array(await response.arrayBuffer());
			if (body.byteLength > MAX_CLOUD_CP_BINARY_BYTES) return tooLarge();
			return { status: response.status, headers, body };
		} catch (error) {
			if (timeout.aborted && !cancelled.signal.aborted) {
				return binaryError(504, CLOUD_CP_BINARY_TIMEOUT,
					`AO Cloud did not answer within ${Math.round(timeoutMs / 1000)} seconds. Try again.`);
			}
			throw error;
		} finally {
			untrack();
		}
	}

	async function openStream(sender: CloudCpStreamSender, init: unknown): Promise<{ streamId: string }> {
		const valid = validateInit(init);
		const url = await pinnedTargetUrl(valid, options.controlPlaneUrl);
		const ids = trackSender(sender);
		if (ids.size >= MAX_STREAMS_PER_WEBCONTENTS) {
			throw streamError(429, `This window already has ${MAX_STREAMS_PER_WEBCONTENTS} open cloud streams.`);
		}
		const token = await getCloudAccessToken(getDataDir());
		if (token === null || token === "") {
			throw streamError(401, "No AO Cloud session is available. Sign in and try again.");
		}
		const controller = new AbortController();
		const response = await doFetch(url, {
			method: valid.method.toUpperCase(),
			headers: buildHeaders(valid.headers, token),
			body: valid.body,
			signal: controller.signal,
		});
		if (!response.ok) {
			throw streamError(response.status, await envelopeMessage(response));
		}
		if (response.body === null) {
			throw streamError(response.status, "The event stream response has no body.");
		}

		const streamId = randomUUID();
		const stream: ActiveStream = {
			controller,
			sender,
			channel: cloudCpStreamChannel(streamId),
			closed: false,
		};
		streams.set(streamId, stream);
		ids.add(streamId);
		const body = response.body;
		// Start pumping on the next macrotask: the invoke reply for openStream is
		// dispatched from the handler's promise chain (microtasks), so deferring
		// guarantees the renderer learns the streamId — and subscribes — before
		// the first chunk event can be sent.
		setImmediate(() => {
			void pump(streamId, stream, body);
		});
		return { streamId };
	}

	function closeStream(sender: Pick<CloudCpStreamSender, "id">, streamId: unknown): void {
		if (typeof streamId !== "string") return;
		const stream = streams.get(streamId);
		// Only the WebContents that opened a stream may close it.
		if (stream === undefined || stream.sender.id !== sender.id) return;
		teardown(streamId, stream);
	}

	return { request, requestBinary, cancelBinary, openStream, closeStream };
}

export function installCloudCpProxy(getDataDir: () => string, controlPlaneUrl: ControlPlaneUrlResolver): void {
	const proxy = createCloudCpProxy(getDataDir, { controlPlaneUrl });
	ipcMain.handle(CLOUD_CP_REQUEST_CHANNEL, (_event, init: unknown) => proxy.request(init));
	ipcMain.handle(CLOUD_CP_REQUEST_BINARY_CHANNEL, (event, init: unknown) => proxy.requestBinary(init, event.sender));
	ipcMain.on(CLOUD_CP_CANCEL_BINARY_CHANNEL, (event, requestId: unknown) => {
		proxy.cancelBinary(event.sender, requestId);
	});
	ipcMain.handle(CLOUD_CP_OPEN_STREAM_CHANNEL, (event, init: unknown) => proxy.openStream(event.sender, init));
	ipcMain.on(CLOUD_CP_CLOSE_STREAM_CHANNEL, (event, streamId: unknown) => {
		proxy.closeStream(event.sender, streamId);
	});
}
