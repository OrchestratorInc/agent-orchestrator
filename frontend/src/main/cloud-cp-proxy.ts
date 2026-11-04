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
//
// Every CP request carries an X-Request-ID the proxy mints (the CP echoes it
// and logs under it), so even a request that never reached the CP has an id
// to match. Failures are logged here without tokens, bodies or query strings,
// and the last few are kept for Report a problem (cloudCp:diagnostics).

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
import {
	CLOUD_UNREACHABLE,
	RELEASE_HEADER,
	REQUEST_ID_HEADER,
	isOpaqueRequestId,
	networkFailureCause,
	safeCloudRelease,
	safeErrorCode,
} from "../shared/cloud-cp-errors";

export const CLOUD_CP_REQUEST_CHANNEL = "cloudCp:request";
export const CLOUD_CP_REQUEST_BINARY_CHANNEL = "cloudCp:requestBinary";
export const CLOUD_CP_CANCEL_BINARY_CHANNEL = "cloudCp:cancelBinary";
export const CLOUD_CP_OPEN_STREAM_CHANNEL = "cloudCp:openStream";
export const CLOUD_CP_CLOSE_STREAM_CHANNEL = "cloudCp:closeStream";
export const CLOUD_CP_DIAGNOSTICS_CHANNEL = "cloudCp:diagnostics";

/** Per-stream renderer event channel; preload subscribes with this exact shape. */
export function cloudCpStreamChannel(streamId: string): string {
	return `cloudCp:stream:${streamId}`;
}

/** Only CP API paths may be proxied; anything else is rejected before fetch. */
const API_PREFIX = "/api/cloud/v1";

/** The share link routes, read-only through this proxy. */
const SHARE_LINKS_ROUTE = "/me/artifact-shares";
/** A cloud session's file is shared through POST .../sessions/{id}/artifacts/share. */
const SESSION_FILE_SHARE_ROUTE = /\/sessions\/[^/]+\/artifacts\/share\/?$/;

/** Upper bound on live SSE streams per renderer WebContents. */
export const MAX_STREAMS_PER_WEBCONTENTS = 8;

/** How many failed CP requests main keeps for Report a problem. */
export const MAX_RECENT_CLOUD_CP_ERRORS = 3;

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

/**
 * A CP failure as structured fields. A rejected IPC promise keeps only its
 * message, so main answers with these instead and the renderer rebuilds a
 * typed error from them. Status 0 means the CP was never reached.
 */
export interface CloudCpProxyFailure {
	status: number;
	message: string;
	code?: string;
	requestId?: string;
	/** Why the CP was not reached, as a bounded token (ENOTFOUND, ECONNREFUSED, TimeoutError); never a message. */
	cause?: string;
}

/** openStream's answer: the stream's id, or why the CP did not open it. */
export type CloudCpOpenStreamResult = { streamId: string } | { error: CloudCpProxyFailure };

export type CloudCpStreamEvent =
	| { type: "chunk"; data: string }
	| { type: "end" }
	| ({ type: "error" } & CloudCpProxyFailure);

/** One failed CP request as Report a problem lists it: no message, path or body. */
export interface CloudCpErrorRecord {
	/** ISO time the request failed. */
	at: string;
	/** cloud: the CP answered with an error. network: the CP was not reached. */
	origin: "cloud" | "network";
	/** HTTP status; 0 when the CP was not reached. */
	status: number;
	code?: string;
	requestId?: string;
}

/** What main knows about the CP for Report a problem. */
export interface CloudCpDiagnostics {
	/** The configured control plane, or null when none is. */
	baseUrl: string | null;
	/** The CP release named by the last response's X-AO-Release header. */
	release?: string;
	/** The last few failed requests, oldest first. */
	recentErrors: CloudCpErrorRecord[];
}

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
	openStream(sender: CloudCpStreamSender, init: unknown): Promise<CloudCpOpenStreamResult>;
	closeStream(sender: Pick<CloudCpStreamSender, "id">, streamId: unknown): void;
	diagnostics(): Promise<CloudCpDiagnostics>;
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
	/** Where failed requests are logged; defaults to console.warn, which the main log captures. */
	warn?: (line: string) => void;
	/** Clock override for tests. */
	now?: () => number;
}

function invalid(detail: string): Error {
	return new Error(`Invalid cloud control-plane request: ${detail}`);
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
function resolveTargetUrl(init: Pick<CloudCpProxyRequestInit, "baseUrl" | "path" | "method">): string {
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
	// Share links are published, updated and revoked only through the main
	// process's cloudShare flow, which reads files only inside a session's
	// artifacts folder and publishes only what its inspection showed; through
	// this proxy the renderer may only read them.
	const sharePrefix = `${expectedPrefix}${SHARE_LINKS_ROUTE}`;
	const shareRoute = target.pathname === sharePrefix || target.pathname.startsWith(`${sharePrefix}/`) ||
		SESSION_FILE_SHARE_ROUTE.test(decodeURIComponent(target.pathname));
	if (shareRoute && !["GET", "HEAD"].includes(init.method.toUpperCase())) {
		throw invalid("share links can only be changed from the desktop's share flow.");
	}
	return target.toString();
}

function buildHeaders(requested: Record<string, string> | undefined, token: string): { headers: Headers; requestId: string } {
	const headers = new Headers();
	for (const [name, value] of Object.entries(requested ?? {})) {
		const lower = name.toLowerCase();
		// The main process owns credentials on this connection; a renderer can
		// never smuggle its own Authorization/Cookie/Host through the proxy.
		if (lower === "authorization" || lower === "cookie" || lower === "host") continue;
		headers.set(name, value);
	}
	headers.set("Authorization", `Bearer ${token}`);
	// One id per request, so the CP's logs for it can be found even when its
	// answer never arrived. A caller's own id is kept only when it is a UUID.
	const callerId = headers.get(REQUEST_ID_HEADER);
	const requestId = isOpaqueRequestId(callerId) ? callerId : randomUUID();
	headers.set(REQUEST_ID_HEADER, requestId);
	return { headers, requestId };
}

/** Collections whose next path segment is an identifier or a name. */
const ID_COLLECTIONS = new Set([
	"orgs", "projects", "sessions", "cues", "artifacts", "versions", "invitations", "merge-requests", "approvals",
	"provider-connections", "installations", "notifications", "transport", "artifact-shares", "templates", "workers",
]);

/**
 * A CP URL as logged: its path under /api/cloud/v1 with identifiers and names
 * replaced by ":id" and no query string, which can carry file paths or search
 * text.
 */
export function cloudCpLogPath(url: string): string {
	let pathname: string;
	try {
		pathname = new URL(url).pathname;
	} catch {
		return "unknown";
	}
	const index = pathname.indexOf(API_PREFIX);
	const segments = (index === -1 ? pathname : pathname.slice(index + API_PREFIX.length)).split("/").filter(Boolean);
	const out: string[] = [];
	for (let i = 0; i < segments.length && out.length < 12; i += 1) {
		const segment = segments[i];
		const afterCollection = i > 0 && ID_COLLECTIONS.has(segments[i - 1]);
		out.push(!afterCollection && /^[a-z][a-z-]{0,39}$/.test(segment) ? segment : ":id");
	}
	return `${API_PREFIX}/${out.join("/")}`.replace(/\/$/, "");
}

function unreachableFailure(requestId: string, cause: string): CloudCpProxyFailure {
	return {
		status: 0,
		code: CLOUD_UNREACHABLE,
		message: "AO Cloud could not be reached. Check your connection and try again.",
		requestId,
		cause,
	};
}

/** A response main makes itself when the CP was not reached: status 0, in the CP's error envelope. */
function unreachableResponse(requestId: string, cause: string): CloudCpProxyResponse {
	const failure = unreachableFailure(requestId, cause);
	return {
		status: 0,
		headers: { "content-type": "application/json", [REQUEST_ID_HEADER]: requestId },
		body: JSON.stringify({ error: "cloud_unreachable", code: failure.code, message: failure.message, requestId, cause }),
	};
}

/** The code and request id of an error response body, when it is the CP's envelope. */
function envelopeFields(body: string): { code?: string; requestId?: string; message?: string } {
	try {
		const parsed = JSON.parse(body) as { code?: unknown; requestId?: unknown; message?: unknown; error?: unknown } | null;
		const message = typeof parsed?.message === "string" && parsed.message !== "" ? parsed.message
			: typeof parsed?.error === "string" && parsed.error !== "" ? parsed.error : undefined;
		return {
			code: safeErrorCode(parsed?.code),
			requestId: isOpaqueRequestId(parsed?.requestId) ? parsed.requestId : undefined,
			message,
		};
	} catch {
		return {};
	}
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

interface ActiveStream {
	controller: AbortController;
	sender: CloudCpStreamSender;
	channel: string;
	closed: boolean;
	method: string;
	url: string;
	requestId: string;
	startedAt: number;
}

/**
 * The request's URL on the configured control plane. A renderer names its
 * baseUrl, but only the configured one is ever dialed: any other is refused.
 */
export async function pinnedTargetUrl(
	init: Pick<CloudCpProxyRequestInit, "baseUrl" | "path" | "method">,
	controlPlaneUrl: ControlPlaneUrlResolver,
): Promise<string> {
	const configured = await controlPlaneUrl();
	if (!configured) throw invalid("no AO Cloud control plane is configured.");
	parseCloudBaseUrl(configured);
	if (!sameControlPlaneUrl(init.baseUrl, configured)) {
		throw invalid("baseUrl is not the configured AO Cloud control plane.");
	}
	return resolveTargetUrl({ baseUrl: configured, path: init.path, method: init.method });
}

export function createCloudCpProxy(getDataDir: () => string, options: CloudCpProxyOptions): CloudCpProxy {
	// Wrap the default so the global fetch is never invoked detached from its
	// realm (Chromium/Node throw "Illegal invocation" for a bare reference).
	const doFetch: typeof fetch = options.fetchImpl ?? ((input, init) => fetch(input, init));
	const warn = options.warn ?? ((line: string) => console.warn(line));
	const now = options.now ?? Date.now;

	// What Report a problem shows about the CP: its last release and failures.
	let lastRelease: string | undefined;
	const recentErrors: CloudCpErrorRecord[] = [];

	function noteResponse(response: Response): void {
		lastRelease = safeCloudRelease(response.headers.get(RELEASE_HEADER)) ?? lastRelease;
	}

	/** The id the CP reported for a request, else the one main sent. */
	function responseRequestId(response: Response, sent: string, fromBody?: string): string {
		const header = response.headers.get(REQUEST_ID_HEADER);
		return fromBody ?? (isOpaqueRequestId(header) ? header : sent);
	}

	function noteFailure(
		request: { method: string; url: string; startedAt: number },
		failure: { status: number; code?: string; requestId: string },
		cause?: string,
	): void {
		const origin = failure.status === 0 ? "network" : "cloud";
		const failedAt = now();
		recentErrors.push({
			at: new Date(failedAt).toISOString(),
			origin,
			status: failure.status,
			...(failure.code ? { code: failure.code } : {}),
			requestId: failure.requestId,
		});
		if (recentErrors.length > MAX_RECENT_CLOUD_CP_ERRORS) recentErrors.shift();
		// Never the token, a body or the query string.
		warn(
			`[cloud-cp] ${request.method.toUpperCase()} ${cloudCpLogPath(request.url)} failed: origin=${origin} status=${failure.status}` +
				` code=${failure.code ?? "-"} requestId=${failure.requestId} durationMs=${Math.max(0, failedAt - request.startedAt)}` +
				(cause ? ` cause=${cause}` : ""),
		);
	}

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
			// A stream main closed itself (closeStream, its window going away) ends
			// silently; any other read failure is the connection to the CP dropping.
			if (!stream.closed) {
				const cause = networkFailureCause(error);
				noteFailure(stream, { status: 0, code: CLOUD_UNREACHABLE, requestId: stream.requestId }, cause);
				emit(stream, {
					type: "error",
					...unreachableFailure(stream.requestId, cause),
					message: error instanceof Error ? error.message : String(error),
				});
			}
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
		const { headers: requestHeaders, requestId } = buildHeaders(valid.headers, token);
		const startedAt = now();
		const attempt = { method: valid.method, url, startedAt };
		let response: Response;
		let body: string;
		try {
			response = await doFetch(url, {
				method: valid.method.toUpperCase(),
				headers: requestHeaders,
				body: valid.body,
			});
			body = await response.text();
		} catch (error) {
			// DNS, TLS, offline, a refused or dropped connection: the renderer gets
			// the CP's error envelope with status 0 instead of an IPC rejection,
			// which would keep only a message.
			const cause = networkFailureCause(error);
			noteFailure(attempt, { status: 0, code: CLOUD_UNREACHABLE, requestId }, cause);
			return unreachableResponse(requestId, cause);
		}
		noteResponse(response);
		const headers: Record<string, string> = {};
		response.headers.forEach((value, key) => {
			headers[key] = value;
		});
		if (!response.ok) {
			const envelope = envelopeFields(body);
			noteFailure(attempt, { status: response.status, code: envelope.code, requestId: responseRequestId(response, requestId, envelope.requestId) });
		}
		// The renderer reads the id from here when the CP's body names none.
		if (headers[REQUEST_ID_HEADER] === undefined) headers[REQUEST_ID_HEADER] = requestId;
		return { status: response.status, headers, body };
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
		const { headers: requestHeaders, requestId } = buildHeaders(valid.headers, token);
		const attempt = { method: valid.method, url, startedAt: now() };
		try {
			const response = await doFetch(url, {
				method: valid.method.toUpperCase(),
				headers: requestHeaders,
				// A Uint8Array over IPC may view a SharedArrayBuffer-typed buffer; copy
				// into a plain ArrayBuffer that fetch accepts as a body.
				body: valid.body === undefined ? undefined : new Uint8Array(valid.body).buffer,
				signal: AbortSignal.any([cancelled.signal, timeout]),
			});
			noteResponse(response);
			const headers: Record<string, string> = {};
			response.headers.forEach((value, key) => {
				headers[key] = value;
			});
			if (headers[REQUEST_ID_HEADER] === undefined) headers[REQUEST_ID_HEADER] = requestId;
			const tooLarge = () => binaryError(413, CLOUD_CP_BINARY_TOO_LARGE,
				`The file is larger than ${MAX_CLOUD_CP_BINARY_BYTES / MiB} MiB, the most the app takes from AO Cloud at once.`);
			const declared = Number(response.headers.get("content-length"));
			if (Number.isFinite(declared) && declared > MAX_CLOUD_CP_BINARY_BYTES) {
				await response.body?.cancel().catch(() => undefined);
				return tooLarge();
			}
			const body = new Uint8Array(await response.arrayBuffer());
			if (body.byteLength > MAX_CLOUD_CP_BINARY_BYTES) return tooLarge();
			if (!response.ok) {
				const envelope = envelopeFields(new TextDecoder().decode(body.subarray(0, 64 * 1024)));
				noteFailure(attempt, { status: response.status, code: envelope.code, requestId: responseRequestId(response, requestId, envelope.requestId) });
			}
			return { status: response.status, headers, body };
		} catch (error) {
			if (timeout.aborted && !cancelled.signal.aborted) {
				noteFailure(attempt, { status: 0, code: CLOUD_CP_BINARY_TIMEOUT, requestId }, "TimeoutError");
				return binaryError(504, CLOUD_CP_BINARY_TIMEOUT,
					`AO Cloud did not answer within ${Math.round(timeoutMs / 1000)} seconds. Try again.`);
			}
			if (!cancelled.signal.aborted) noteFailure(attempt, { status: 0, code: CLOUD_UNREACHABLE, requestId }, networkFailureCause(error));
			throw error;
		} finally {
			untrack();
		}
	}

	async function openStream(sender: CloudCpStreamSender, init: unknown): Promise<CloudCpOpenStreamResult> {
		const valid = validateInit(init);
		const url = await pinnedTargetUrl(valid, options.controlPlaneUrl);
		const ids = trackSender(sender);
		if (ids.size >= MAX_STREAMS_PER_WEBCONTENTS) {
			return { error: { status: 429, message: `This window already has ${MAX_STREAMS_PER_WEBCONTENTS} open cloud streams.` } };
		}
		const token = await getCloudAccessToken(getDataDir());
		if (token === null || token === "") {
			return { error: { status: 401, code: "no_token", message: "No AO Cloud session is available. Sign in and try again." } };
		}
		const controller = new AbortController();
		const { headers: requestHeaders, requestId } = buildHeaders(valid.headers, token);
		const attempt = { method: valid.method, url, startedAt: now() };
		let response: Response;
		try {
			response = await doFetch(url, {
				method: valid.method.toUpperCase(),
				headers: requestHeaders,
				body: valid.body,
				signal: controller.signal,
			});
		} catch (error) {
			const cause = networkFailureCause(error);
			noteFailure(attempt, { status: 0, code: CLOUD_UNREACHABLE, requestId }, cause);
			return { error: unreachableFailure(requestId, cause) };
		}
		noteResponse(response);
		if (!response.ok) {
			const envelope = envelopeFields(await response.text().catch(() => ""));
			const failure = {
				status: response.status,
				message: envelope.message ?? `Cloud control plane stream request failed with status ${response.status}.`,
				...(envelope.code ? { code: envelope.code } : {}),
				requestId: responseRequestId(response, requestId, envelope.requestId),
			};
			noteFailure(attempt, failure);
			return { error: failure };
		}
		if (response.body === null) {
			return { error: { status: response.status, message: "The event stream response has no body.", requestId: responseRequestId(response, requestId) } };
		}

		const streamId = randomUUID();
		const stream: ActiveStream = {
			controller,
			sender,
			channel: cloudCpStreamChannel(streamId),
			closed: false,
			...attempt,
			requestId: responseRequestId(response, requestId),
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

	async function diagnostics(): Promise<CloudCpDiagnostics> {
		let baseUrl: string | null = null;
		try {
			baseUrl = (await options.controlPlaneUrl()) || null;
		} catch {
			// Report a problem still lists the release and failures.
		}
		return {
			baseUrl,
			...(lastRelease ? { release: lastRelease } : {}),
			recentErrors: recentErrors.map((record) => ({ ...record })),
		};
	}

	return { request, requestBinary, cancelBinary, openStream, closeStream, diagnostics };
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
	ipcMain.handle(CLOUD_CP_DIAGNOSTICS_CHANNEL, () => proxy.diagnostics());
}
