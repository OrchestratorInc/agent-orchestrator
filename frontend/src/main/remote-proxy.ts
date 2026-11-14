import {
	createServer,
	request as httpRequest,
	type IncomingHttpHeaders,
	type IncomingMessage,
	type OutgoingHttpHeaders,
	type Server,
} from "node:http";
import {
	connect as http2Connect,
	constants as http2Constants,
	createSecureServer,
	type ClientHttp2Session,
	type Http2ServerRequest,
} from "node:http2";
import { request as httpsRequest } from "node:https";
import { connect as netConnect, isIP, type AddressInfo, type Socket } from "node:net";
import type { Readable, Writable } from "node:stream";
import { connect as tlsConnect, type TLSSocket } from "node:tls";
import { createHmac, randomBytes, timingSafeEqual } from "node:crypto";
import path from "node:path";
import type { Duplex } from "node:stream";
import { WebSocket, createWebSocketStream } from "ws";
import { agentBrowserRunning, agentViewerUrl, HOP_BY_HOP_HEADERS, listenOnStablePort } from "./agent-viewer";
import { previewCertificate } from "./preview-certificate";
import { IncompatibleRemoteVersionError, readRemoteIdentity } from "./remote-request";
import type { RemoteEntry } from "./remotes-store";

export type ActiveProxy = {
	base: string;
	listeningAddress?: string;
	previewUrl: (sessionId: string, sourceUrl: string) => string;
	resolvePreviewUrl: (sessionId: string, viewedUrl: string) => string;
	agentBrowserUrl: (sessionId: string) => Promise<string>;
	isAgentBrowserUrl: (url: string) => boolean;
	externalUrl: (url: string) => string;
	close: () => Promise<void>;
};

type PreviewTarget = { sessionId: string; kind: "static" | "app" | "artifact" | "agent"; entry: string };

type ProxyRequest = Readable & { headers: IncomingHttpHeaders; url?: string; method?: string };
type ProxyResponse = Writable & {
	headersSent: boolean;
	writableFinished: boolean;
	writeHead(status: number, headers?: OutgoingHttpHeaders): unknown;
	flushHeaders?: () => void;
};

class UpstreamHttp2Unavailable extends Error {}

const UNREACHABLE_CODES = new Set(["ECONNREFUSED", "ECONNRESET", "EHOSTDOWN", "EHOSTUNREACH", "ENETDOWN", "ENETUNREACH", "ETIMEDOUT", "ENOTFOUND", "EAI_AGAIN", "UND_ERR_SOCKET", "UND_ERR_CONNECT_TIMEOUT"]);
const unreachable = (error: unknown): boolean =>
	(error as Error | undefined)?.name === "TimeoutError" ||
	(error instanceof TypeError && UNREACHABLE_CODES.has(String((error.cause as { code?: unknown } | undefined)?.code)));

const failureText = (error: Error): string => {
	const cause = error.cause;
	return cause instanceof Error ? `${error.message}: ${(cause as { code?: string }).code ?? cause.message}` : error.message;
};

export function webSocketTunnelStream(socket: WebSocket): Duplex {
	const stream = createWebSocketStream(socket);
	stream.once("end", () => stream.destroy());
	return stream;
}

const MAX_PENDING_UPSTREAM_STREAMS = 100;
const RENDERER_STREAM_HEADROOM = 20;
const HTTP2_RETRY_MS = 60_000;
const UPSTREAM_STREAM_WINDOW = 4 << 20;
const UPSTREAM_SESSION_WINDOW = 32 << 20;
const UPSTREAM_TIMING = { openMs: 5_000, pingMs: 10_000 };

function previewHostForSession(id: string, label = "ao-preview"): string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567";
	let bits = 0;
	let value = 0;
	let encoded = "";
	for (const byte of Buffer.from(id, "utf8")) {
		value = (value << 8) | byte;
		bits += 8;
		while (bits >= 5) {
			encoded += alphabet[(value >>> (bits -= 5)) & 31];
		}
	}
	if (bits) encoded += alphabet[(value << (5 - bits)) & 31];
	return `${label}.${encoded.match(/.{1,50}/g)?.join(".")}.localhost`;
}

function previewAssetPath(entry: string, requested: string): string {
	const file = path.posix.normalize(entry).replace(/^\/+/, "");
	const clean = path.posix.normalize(requested).replace(/^\/+/, "");
	if (!clean) return file;
	const root = path.posix.dirname(file);
	if (root === ".") return clean;
	if (clean === root) return file;
	return path.posix.join(root, clean.startsWith(`${root}/`) ? clean.slice(root.length + 1) : clean);
}

const RENDERER_ORIGIN = "app://renderer";
const STRIP_REQUEST_HEADERS = [
	"host",
	"origin",
	"proxy-authorization",
	"x-ao-preview-app-authorization",
	"x-ao-preview-app-origin",
	"x-ao-expected-host-id",
	...HOP_BY_HOP_HEADERS,
];

function log(message: string): void {
	console.log(`[remote-proxy] ${message}`);
}

function warn(message: string): void {
	console.warn(`[remote-proxy] ${message}`);
}

function safeLogPath(path: string, previewKind?: PreviewTarget["kind"]): string {
	return previewKind ? `preview/${previewKind}` : path.split("?", 1)[0];
}

function equalsToken(candidate: string, token: string): boolean {
	const a = Buffer.from(candidate);
	const b = Buffer.from(token);
	return a.length === b.length && timingSafeEqual(a, b);
}

export async function startRemoteProxy(entry: RemoteEntry, rendererOrigin = RENDERER_ORIGIN, timing = UPSTREAM_TIMING): Promise<ActiveProxy> {
	const token = randomBytes(16).toString("hex");
	const corsHeaders: Record<string, string> = {
		"access-control-allow-origin": rendererOrigin,
		"access-control-allow-methods": "GET, POST, PUT, PATCH, DELETE, OPTIONS",
		vary: "origin",
	};
	const upstream = new URL(entry.url.includes("://") ? entry.url : `http://${entry.url}`);
	if (!["http:", "https:"].includes(upstream.protocol) || upstream.username || upstream.password)
		throw new Error("remote host must use an HTTP(S) URL without embedded credentials");
	const secure = upstream.protocol === "https:";
	const originKey = entry.password || randomBytes(32).toString("hex");
	const originMac = (subject: string) => createHmac("sha256", originKey).update(`${upstream.host}\n${subject}`).digest();
	const upstreamPort = Number(upstream.port || (secure ? 443 : 80));
	const hostname = upstream.hostname.replace(/^\[|\]$/g, "");
	const upstreamRequest = secure ? httpsRequest : httpRequest;
	const dialUpstream = (onReady: () => void): Socket =>
		secure
			? tlsConnect(upstreamPort, hostname, { servername: isIP(hostname) ? undefined : hostname }, onReady)
			: netConnect(upstreamPort, hostname, onReady);
	const prefix = upstream.pathname.replace(/\/+$/, "");
	const server: Server = createServer();
	const previewHosts = new Map<string, PreviewTarget>();
	const artifactHosts = new Map<string, { host: string; sourceHref: string }>();
	const previewSessions = new Map<string, { host: string; sourceUrl: string; sourceHref: string; url: string }>();
	const tunnels = new Set<() => void>();
	server.requestTimeout = 0;

	const stripToken = (rawUrl: string | undefined): string | null => {
		if (!rawUrl || !rawUrl.startsWith("/")) return null;
		const slash = rawUrl.indexOf("/", 1);
		const first = slash === -1 ? rawUrl.slice(1) : rawUrl.slice(1, slash);
		if (!equalsToken(first, token)) return null;
		return prefix + (slash === -1 ? "/" : rawUrl.slice(slash));
	};

	const previewPath = (rawHost: string | undefined, rawUrl: string | undefined, method: string | undefined, scheme: "http" | "https"): { path: string; kind: PreviewTarget["kind"] } | null => {
		if (!rawHost || !rawUrl?.startsWith("/")) return null;
		const target = previewHosts.get(rawHost.toLowerCase().replace(/:\d+$/, ""));
		if (!target || (scheme === "http" && target.kind === "agent")) return null;
		if (target.kind !== "app" && target.kind !== "agent" && method !== "GET" && method !== "HEAD") return null;
		const parsed = new URL(`http://localhost${rawUrl}`);
		const asset = target.kind === "static" ? previewAssetPath(target.entry, parsed.pathname) : parsed.pathname.replace(/^\/+/, "");
		if (target.kind === "agent") {
			return { path: `${prefix}/api/v1/sessions/${encodeURIComponent(target.sessionId)}/agent-browser/${asset}${parsed.search}`, kind: target.kind };
		}
		if (target.kind === "artifact") {
			const query = new URLSearchParams(parsed.search);
			query.set("source", "artifact");
			return { path: `${prefix}/api/v1/sessions/${encodeURIComponent(target.sessionId)}/preview/files/${asset}?${query}`, kind: target.kind };
		}
		const route = target.kind === "static" ? "files" : "app";
		return { path: `${prefix}/api/v1/sessions/${encodeURIComponent(target.sessionId)}/preview/${route}/${asset}${parsed.search}`, kind: target.kind };
	};

	const forwardHeaders = (incoming: IncomingHttpHeaders, previewKind?: PreviewTarget["kind"]): OutgoingHttpHeaders => {
		const out: OutgoingHttpHeaders = {};
		for (const [name, value] of Object.entries(incoming)) {
			if (!name.startsWith(":") && !STRIP_REQUEST_HEADERS.includes(name.toLowerCase())) out[name] = value;
		}
		if (previewKind === "app") {
			if (incoming.authorization) out["x-ao-preview-app-authorization"] = incoming.authorization;
			if (incoming.origin) out["x-ao-preview-app-origin"] = incoming.origin;
		}
		if (previewKind === undefined) {
			delete out["sec-fetch-site"];
			delete out["sec-fetch-mode"];
			delete out["sec-fetch-dest"];
		}
		out.host = upstream.host;
		out.authorization = `Bearer ${entry.password}`;
		if (entry.hostId) out["x-ao-expected-host-id"] = entry.hostId;
		return out;
	};

	const validControlOrigin = (req: { headers: IncomingHttpHeaders; method?: string; url?: string }): boolean =>
		req.headers.origin === undefined
			? req.headers["sec-fetch-site"] === undefined || (
				(req.method === "GET" || req.method === "HEAD") &&
				/^\/api\/v1\/sessions\/[^/]+\/renders\/(?!check(?:$|[?]))[^/?]+(?:\?.*)?$/.test((stripToken(req.url) ?? "").slice(prefix.length))
			)
			: req.headers.origin === rendererOrigin;

	const validPreviewOrigin = (scheme: "http" | "https", host: string | undefined, origin: string | undefined, kind?: PreviewTarget["kind"]): boolean =>
		(kind !== "app" && kind !== "agent") || !origin || origin === `${scheme}://${host}`;

	let identityProbe: Promise<void> | null = null;
	const verifyUpstream = (): Promise<void> => {
		if (!entry.hostId) return Promise.resolve();
		identityProbe ??= readRemoteIdentity(entry)
			.then((actual) => {
				if (actual !== entry.hostId) throw new Error("remote host identity changed");
			})
			.finally(() => {
				identityProbe = null;
			});
		return identityProbe;
	};

	let upstreamSession: Promise<ClientHttp2Session> | null = null;
	let http2RetryAt = 0;
	let closing = false;
	let tunnelRetryAt = secure ? 0 : Number.POSITIVE_INFINITY;
	const openTunnel = async (): Promise<Duplex> => {
		await verifyUpstream();
		const socket = new WebSocket(`wss://${upstream.host}${prefix}/api/v1/tunnel`, {
			headers: { authorization: `Bearer ${entry.password}`, ...(entry.hostId ? { "x-ao-expected-host-id": entry.hostId } : {}) },
			perMessageDeflate: false,
			handshakeTimeout: timing.openMs,
			...(isIP(hostname) ? {} : { servername: hostname }),
		});
		const stream = webSocketTunnelStream(socket);
		socket.once("close", (code: number) => {
			if (!stream.writableEnded && !stream.destroyed) warn(`upstream ${upstream.host} tunnel WebSocket closed with code ${code}`);
		});
		stream.on("error", () => undefined);
		try {
			await new Promise<void>((resolve, reject) => {
				socket.once("open", () => resolve());
				socket.once("error", (error: Error) => reject(new UpstreamHttp2Unavailable(`tunnel: ${error.message}`)));
			});
		} catch (error) {
			stream.destroy();
			throw error;
		}
		return stream;
	};
	const tunneled = new WeakSet<ClientHttp2Session>();
	const sessionPath = (session: ClientHttp2Session, path: string): string =>
		tunneled.has(session) ? path.slice(prefix.length) : path;
	const getOverSession = (session: ClientHttp2Session, href: string): Promise<Response> =>
		new Promise((resolve, reject) => {
			const url = new URL(href);
			const stream = session.request({ ":method": "GET", ":path": sessionPath(session, `${url.pathname}${url.search}`), ":authority": upstream.host }, { endStream: true });
			stream.setTimeout(5_000, () => stream.close(http2Constants.NGHTTP2_CANCEL));
			const chunks: Buffer[] = [];
			let status = 0;
			stream.on("response", (headers) => {
				status = Number(headers[":status"]);
			});
			stream.on("data", (chunk: Buffer) => chunks.push(chunk));
			stream.on("end", () => {
				try {
					resolve(new Response(Buffer.concat(chunks), { status }));
				} catch (error) {
					reject(new UpstreamHttp2Unavailable((error as Error).message));
				}
			});
			stream.on("error", (error: Error) => reject(new UpstreamHttp2Unavailable(error.message)));
			stream.on("close", () => reject(new UpstreamHttp2Unavailable("session closed")));
		});
	const keepAlive = (session: ClientHttp2Session): void => {
		let awaitingPong = false;
		let received = 0;
		const timer = setInterval(() => {
			if (session.destroyed) return;
			const quiet = session.socket.bytesRead === received;
			received = session.socket.bytesRead;
			if (awaitingPong && quiet) return void session.destroy(new Error("upstream stopped answering pings"));
			if (!awaitingPong) awaitingPong = session.ping(() => { awaitingPong = false; });
		}, timing.pingMs);
		timer.unref();
		session.once("close", () => clearInterval(timer));
	};
	const openUpstreamSession = (tunnel?: Duplex): Promise<ClientHttp2Session> => {
		const session = http2Connect(`${upstream.protocol}//${upstream.host}`, {
			settings: { initialWindowSize: UPSTREAM_STREAM_WINDOW },
			...(tunnel ? { createConnection: () => tunnel } : {}),
			...(secure && !isIP(hostname) ? { servername: hostname } : {}),
		});
		if (tunnel) tunneled.add(session);
		let deadline: ReturnType<typeof setTimeout> | undefined;
		const opened = Promise.race([
			new Promise<ClientHttp2Session>((resolve, reject) => {
				session.once("connect", () => {
					session.setLocalWindowSize(UPSTREAM_SESSION_WINDOW);
					keepAlive(session);
					resolve(session);
				});
				session.on("error", (error: Error) => reject(new UpstreamHttp2Unavailable(error.message)));
				session.once("close", () => reject(new UpstreamHttp2Unavailable("session closed")));
			}).then(async (connected) => {
				const identityPath = `${upstream.origin}${prefix}/api/v1/identity`;
				if (!entry.hostId) {
					await getOverSession(connected, identityPath);
					return connected;
				}
				const actual = await readRemoteIdentity({ url: `${upstream.origin}${prefix}` }, (url) => getOverSession(connected, String(url)));
				if (actual !== entry.hostId) throw new Error("remote host identity changed");
				return connected;
			}),
			new Promise<never>((_, reject) => {
				deadline = setTimeout(() => reject(new UpstreamHttp2Unavailable("session open timed out")), timing.openMs);
				deadline.unref();
			}),
		]).finally(() => clearTimeout(deadline));
		opened.catch(() => session.destroy());
		return opened;
	};
	const openPreferredSession = async (): Promise<ClientHttp2Session> => {
		if (Date.now() >= tunnelRetryAt) {
			try {
				return await openUpstreamSession(await openTunnel());
			} catch (error) {
				if (!(error instanceof UpstreamHttp2Unavailable)) throw error;
				tunnelRetryAt = Date.now() + HTTP2_RETRY_MS;
			}
		}
		return openUpstreamSession();
	};
	const sessionForRequest = async (fallBack: boolean): Promise<ClientHttp2Session | null> => {
		for (let attempt = 0; attempt < 2 && Date.now() >= http2RetryAt; attempt++) {
			if (closing) throw new Error("remote proxy closed");
			const pending = (upstreamSession ??= openPreferredSession());
			try {
				const session = await pending;
				if (!session.closed && !session.destroyed) return session;
			} catch (error) {
				if (upstreamSession === pending) upstreamSession = null;
				if (!(error instanceof UpstreamHttp2Unavailable)) throw error;
				if (fallBack) http2RetryAt = Date.now() + HTTP2_RETRY_MS;
				return null;
			}
			if (upstreamSession === pending) upstreamSession = null;
		}
		return null;
	};
	let pendingStreams = 0;
	const streamWaiters = new Set<{ preview: boolean; start: () => void }>();
	const streamLimit = (preview: boolean): number => MAX_PENDING_UPSTREAM_STREAMS - (preview ? RENDERER_STREAM_HEADROOM : 0);
	const acquireStream = (preview: boolean, res: ProxyResponse): Promise<boolean> => {
		if (pendingStreams < streamLimit(preview)) {
			pendingStreams++;
			return Promise.resolve(true);
		}
		return new Promise((resolve) => {
			const leave = () => {
				streamWaiters.delete(waiter);
				resolve(false);
			};
			const waiter = { preview, start: () => { res.off("close", leave); resolve(true); } };
			streamWaiters.add(waiter);
			res.once("close", leave);
		});
	};
	const releaseStream = (): void => {
		pendingStreams--;
		for (const waiter of streamWaiters) {
			if (pendingStreams >= streamLimit(waiter.preview)) continue;
			streamWaiters.delete(waiter);
			pendingStreams++;
			waiter.start();
			return;
		}
	};

	const answerIdentityFailure = (res: ProxyResponse, error: unknown, method: string | undefined, path: string, kind?: PreviewTarget["kind"]): void => {
		warn(`upstream ${upstream.host} identity check failed on ${method} ${safeLogPath(path, kind)} (${failureText(error as Error)})`);
		const incompatible = error instanceof IncompatibleRemoteVersionError;
		res.writeHead(incompatible ? 426 : 502, { "content-type": "application/json", ...corsHeaders });
		res.end(incompatible
			? JSON.stringify({ error: "incompatible", code: "HOST_API_INCOMPATIBLE", message: (error as Error).message })
			: '{"error":"remote host identity not verified","code":"HOST_IDENTITY_UNVERIFIED","message":"remote host identity not verified"}');
	};

	const answerUpstreamFailure = (res: ProxyResponse, error: Error, method: string | undefined, path: string, kind?: PreviewTarget["kind"]): void => {
		warn(`upstream ${upstream.host} failed on ${method} ${safeLogPath(path, kind)} (${failureText(error)}); answering 502`);
		if (res.headersSent) {
			res.destroy(error);
			return;
		}
		res.writeHead(502, {
			"content-type": "application/json",
			...corsHeaders,
		});
		res.end('{"error":"remote daemon unreachable","code":"UPSTREAM_UNAVAILABLE","message":"remote daemon unreachable"}');
	};

	const relayResponse = (
		res: ProxyResponse,
		status: number,
		upstreamHeaders: IncomingHttpHeaders,
		body: Readable,
		method: string | undefined,
		path: string,
		kind?: PreviewTarget["kind"],
		cutOff?: () => boolean,
	): void => {
		if (!(status >= 200 && status <= 599)) {
			body.resume();
			answerUpstreamFailure(res, new Error(`invalid status ${status}`), method, path, kind);
			return;
		}
		if (kind === "app" && status === 409 && upstreamHeaders["x-ao-preview-managed-required"] === "1") {
			body.resume();
			res.writeHead(409, { "content-type": "text/html; charset=utf-8", "cache-control": "no-store" });
			res.end("<main style='font:16px system-ui;padding:24px'><h1>Remote preview needs a managed server</h1><p>Run <code>ao preview start</code> in this session on the host.</p></main>");
			return;
		}
		let dropped = false;
		const closeDroppedStream = () => {
			if (dropped) return;
			dropped = true;
			warn(`upstream ${upstream.host} stream ended on ${method} ${safeLogPath(path, kind)}`);
			res.destroy(new Error("upstream stream ended"));
		};
		body.on("aborted", closeDroppedStream);
		body.on("error", closeDroppedStream);
		const headers: OutgoingHttpHeaders = {};
		for (const [name, value] of Object.entries(upstreamHeaders)) {
			if (!name.startsWith(":") && !HOP_BY_HOP_HEADERS.includes(name)) headers[name] = value;
		}
		if (kind !== undefined && upstreamHeaders["x-ao-preview-api-sandbox"] === "1") {
			const policy = headers["content-security-policy"];
			const sandbox = "sandbox allow-scripts allow-forms";
			if (typeof policy === "string") {
				if (policy === sandbox) delete headers["content-security-policy"];
				else if (policy.endsWith(`, ${sandbox}`)) headers["content-security-policy"] = policy.slice(0, -sandbox.length - 2);
			}
		}
		delete headers["x-ao-preview-api-sandbox"];
		const vary = (Array.isArray(headers.vary) ? headers.vary.join(", ") : String(headers.vary ?? ""))
			.split(",").map((value) => value.trim()).filter(Boolean);
		if (!vary.some((value) => value === "*" || value.toLowerCase() === "origin")) vary.push("origin");
		Object.assign(headers, corsHeaders, { vary: vary.join(", ") });
		if (kind !== undefined && kind !== "app") delete headers["set-cookie"];
		res.writeHead(status, headers);
		res.flushHeaders?.();
		if (!cutOff) {
			body.pipe(res);
			return;
		}
		body.pipe(res, { end: false });
		body.once("end", () => (cutOff() ? closeDroppedStream() : res.end()));
		body.once("close", () => {
			if (!res.writableEnded) closeDroppedStream();
		});
	};

	const forwardOverHttp1 = (req: ProxyRequest, res: ProxyResponse, path: string, kind?: PreviewTarget["kind"]): void => {
		const proxied = upstreamRequest(
			{
				host: hostname,
				port: upstreamPort,
				method: req.method,
				path,
				headers: forwardHeaders(req.headers, kind),
			},
			(upstreamRes) => relayResponse(res, upstreamRes.statusCode ?? 502, upstreamRes.headers, upstreamRes, req.method, path, kind),
		);
		proxied.setTimeout(0);
		proxied.on("error", (error: Error) => answerUpstreamFailure(res, error, req.method, path, kind));
		res.once("close", () => {
			if (!res.writableEnded) proxied.destroy();
		});
		req.pipe(proxied);
	};

	const hasBody = (req: ProxyRequest): boolean =>
		Number(req.headers["content-length"] ?? 0) > 0 ||
		req.headers["transfer-encoding"] !== undefined ||
		(req as Partial<Http2ServerRequest>).stream?.endAfterHeaders === false;

	const forwardOverSession = (session: ClientHttp2Session, req: ProxyRequest, res: ProxyResponse, path: string, kind: PreviewTarget["kind"] | undefined, retry?: () => void): void => {
		let stream: ReturnType<ClientHttp2Session["request"]> | undefined;
		let clientGone = false;
		res.once("close", () => {
			clientGone = !res.writableEnded;
			if (clientGone) stream?.close(http2Constants.NGHTTP2_CANCEL);
		});
		let pending = true;
		const settle = () => {
			if (!pending) return;
			pending = false;
			releaseStream();
		};
		const headers = forwardHeaders(req.headers, kind);
		delete headers.host;
		const method = req.method ?? "GET";
		try {
			stream = session.request(
				{ ...headers, ":method": method, ":path": sessionPath(session, path), ":authority": upstream.host },
				hasBody(req) ? { endStream: false } : undefined,
			);
		} catch (error) {
			settle();
			answerUpstreamFailure(res, error as Error, req.method, path, kind);
			return;
		}
		const upstreamStream = stream;
		const safe = method === "GET" || method === "HEAD";
		const retriable = () => retry !== undefined &&
			(upstreamStream.rstCode === http2Constants.NGHTTP2_REFUSED_STREAM || (safe && session.destroyed));
		let responded = false;
		let failure: Error | undefined;
		upstreamStream.once("close", () => {
			settle();
			if (responded || clientGone || res.writableEnded) return;
			const reason = failure?.message ?? `stream closed with code ${upstreamStream.rstCode}`;
			if (!retriable()) return void answerUpstreamFailure(res, new Error(reason), req.method, path, kind);
			warn(`upstream ${upstream.host} failed on ${method} ${safeLogPath(path, kind)} (${reason}); sending it again`);
			retry?.();
		});
		upstreamStream.once("response", (responseHeaders) => {
			responded = true;
			settle();
			relayResponse(res, Number(responseHeaders[":status"]), responseHeaders as IncomingHttpHeaders, upstreamStream, req.method, path, kind,
				() => session.destroyed || upstreamStream.rstCode !== http2Constants.NGHTTP2_NO_ERROR);
		});
		upstreamStream.on("error", (error: Error) => {
			failure = error;
			if (!retriable()) answerUpstreamFailure(res, error, req.method, path, kind);
		});
		req.pipe(upstreamStream);
	};

	const handleRequest = async (req: ProxyRequest, res: ProxyResponse, scheme: "http" | "https"): Promise<void> => {
		const host = req.headers.host ?? (req as Http2ServerRequest).authority;
		const preview = previewPath(host, req.url, req.method, scheme);
		const path = preview?.path ?? stripToken(req.url);
		if (path === null) {
			res.writeHead(404, {
				"content-type": "application/json",
				...corsHeaders,
			});
			res.end('{"error":"unknown path"}');
			return;
		}
		if ((!preview && !validControlOrigin(req)) || !validPreviewOrigin(scheme, host, req.headers.origin, preview?.kind)) {
			res.writeHead(403, { "content-type": "application/json" });
			res.end('{"error":"preview origin mismatch"}');
			return;
		}
		if (req.method === "OPTIONS") {
			res.writeHead(204, {
				...corsHeaders,
				"access-control-allow-headers": String(req.headers["access-control-request-headers"] ?? "content-type"),
				...(req.headers["access-control-request-private-network"] === "true"
					? { "access-control-allow-private-network": "true" }
					: {}),
				"access-control-max-age": "600",
			});
			res.end();
			return;
		}
		let left = false;
		res.once("close", () => { left = true; });
		const forward = async (mayRetry: boolean): Promise<void> => {
			if (!(await acquireStream(preview !== null, res))) return;
			let session: ClientHttp2Session | null = null;
			try {
				session = await sessionForRequest(mayRetry);
				if (!session) await verifyUpstream();
			} catch (error) {
				if (closing || unreachable(error)) answerUpstreamFailure(res, error as Error, req.method, path, preview?.kind);
				else answerIdentityFailure(res, error, req.method, path, preview?.kind);
			}
			if (!session || left || res.writableEnded) releaseStream();
			if (left || res.writableEnded) return;
			if (session) forwardOverSession(session, req, res, path, preview?.kind, mayRetry && !hasBody(req) ? () => void forward(false) : undefined);
			else forwardOverHttp1(req, res, path, preview?.kind);
		};
		await forward(true);
	};

	const handleUpgrade = (req: IncomingMessage, socket: Socket, head: Buffer, scheme: "http" | "https"): void => {
		const preview = previewPath(req.headers.host, req.url, req.method, scheme);
		if (preview && preview.kind !== "app" && preview.kind !== "agent") {
			socket.destroy();
			return;
		}
		const path = preview ? preview.path : stripToken(req.url);
		if (path === null) {
			socket.destroy();
			return;
		}
		if ((!preview && !validControlOrigin(req)) || !validPreviewOrigin(scheme, req.headers.host, req.headers.origin, preview?.kind)) {
			socket.destroy();
			return;
		}
		const stopPending = () => {
			tunnels.delete(stopPending);
			socket.destroy();
		};
		tunnels.add(stopPending);
		socket.once("close", stopPending);
		void verifyUpstream().then(() => {
			tunnels.delete(stopPending);
			socket.off("close", stopPending);
			if (socket.destroyed) return;
			const upstreamSocket = dialUpstream(() => {
				const headers = forwardHeaders(req.headers, preview?.kind);
				if (!preview) {
					delete headers["sec-fetch-site"];
					delete headers["sec-fetch-mode"];
					delete headers["sec-fetch-dest"];
				}
				const lines = [`${req.method} ${path} HTTP/1.1`];
				headers.connection = "Upgrade";
				headers.upgrade = String(req.headers.upgrade ?? "websocket");
				for (const [name, value] of Object.entries(headers)) {
					for (const v of Array.isArray(value) ? value : [value]) lines.push(`${name}: ${v}`);
				}
				upstreamSocket.write(lines.join("\r\n") + "\r\n\r\n");
				if (head.length > 0) upstreamSocket.write(head);
				socket.pipe(upstreamSocket);
				upstreamSocket.pipe(socket);
			});
			upstreamSocket.on("error", (error: Error) => {
				warn(`upstream ${upstream.host} tunnel failed on ${safeLogPath(path, preview?.kind)} (${error.message})`);
			});
			const drop = () => {
				tunnels.delete(drop);
				socket.destroy();
				upstreamSocket.destroy();
			};
			tunnels.add(drop);
			for (const event of ["error", "end", "close"] as const) {
				upstreamSocket.on(event, drop);
				socket.on(event, drop);
			}
		}).catch((error: Error) => {
			warn(`upstream ${upstream.host} ${unreachable(error) ? "unreachable" : "identity check failed"} on ${safeLogPath(path, preview?.kind)} (${failureText(error)})`);
			stopPending();
		});
	};

	server.on("request", (req, res) => void handleRequest(req, res, "http"));
	server.on("upgrade", (req, socket: Socket, head: Buffer) => handleUpgrade(req, socket, head, "http"));

	const { cert, key } = previewCertificate();
	const secureServer = createSecureServer({ cert, key, allowHTTP1: true });
	const secureSockets = new Set<TLSSocket>();
	secureServer.on("secureConnection", (socket: TLSSocket) => {
		secureSockets.add(socket);
		socket.once("close", () => secureSockets.delete(socket));
	});
	secureServer.on("request", (req, res) => void handleRequest(req, res, "https"));
	secureServer.on("upgrade", (req: IncomingMessage, socket: Socket, head: Buffer) => handleUpgrade(req, socket, head, "https"));
	const securePort = await listenOnStablePort(secureServer, originMac("port"));

	await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
	const { address: listeningAddress, port } = server.address() as AddressInfo;
	log(`started on 127.0.0.1:${port} (previews on ${securePort}) for ${upstream.host}`);
	return {
		base: `http://127.0.0.1:${port}/${token}`,
		listeningAddress,
		previewUrl: (sessionId, sourceUrl) => {
			if (!sessionId || sessionId.length > 256) throw new Error("invalid preview session");
			const raw = sourceUrl.trim();
			if (!raw) {
				const previous = previewSessions.get(sessionId);
				if (previous) { previewHosts.delete(previous.host); previewSessions.delete(sessionId); }
				return "";
			}
			const parsed = new URL(/^(?:localhost|127\.\d+\.\d+\.\d+|0\.0\.0\.0|\[::1\])(?::\d+)?(?:[/?#]|$)/i.test(raw) ? `http://${raw}` : raw);
			if (parsed.protocol !== "http:" && parsed.protocol !== "https:") throw new Error("unsupported preview URL");
			if (parsed.hostname === previewHostForSession(sessionId, "ao-preview-artifact")) {
				let artifact = artifactHosts.get(sessionId);
				if (!artifact) {
					const host = `ao-preview-${randomBytes(16).toString("hex")}.localhost`;
					artifact = { host: `${host}:${port}`, sourceHref: parsed.href };
					artifactHosts.set(sessionId, artifact);
					previewHosts.set(host, { sessionId, kind: "artifact", entry: "" });
				}
				return `http://${artifact.host}${parsed.pathname}${parsed.search}${parsed.hash}`;
			}
			const previous = previewSessions.get(sessionId);
			if (previous?.sourceUrl === sourceUrl) return previous.url;
			if (previous) {
				previewHosts.delete(previous.host);
				previewSessions.delete(sessionId);
			}
			let kind: PreviewTarget["kind"];
			const previewHost = parsed.hostname.replace(/^\[|\]$/g, "").replace(/\.+$/, "");
			const loopback = previewHost === "localhost" || previewHost === "0.0.0.0" || previewHost === "::1" ||
				(isIP(previewHost) === 4 && previewHost.startsWith("127.")) ||
				(isIP(previewHost) === 6 && /^::ffff:7f[0-9a-f]{2}:/i.test(previewHost));
			if (previewHost === previewHostForSession(sessionId)) kind = "static";
			else if (parsed.protocol === "http:" && loopback) kind = "app";
			else if (loopback) throw new Error("unsupported local preview protocol");
			else if (previewHost.endsWith(".localhost")) throw new Error("preview does not belong to this session");
			else return sourceUrl;
			const host = `ao-preview-${originMac(`${kind}\n${sessionId}`).toString("hex", 0, 16)}.localhost`;
			previewHosts.set(host, { sessionId, kind, entry: parsed.pathname });
			const url = `https://${host}:${securePort}${parsed.pathname}${parsed.search}${parsed.hash}`;
			previewSessions.set(sessionId, { host, sourceUrl, sourceHref: parsed.href, url });
			return url;
		},
		resolvePreviewUrl: (sessionId, viewedUrl) => {
			let viewed: URL;
			try { viewed = new URL(viewedUrl); } catch { return viewedUrl; }
			if (!/^ao-preview-[0-9a-f]{32}\.localhost$/.test(viewed.hostname)) return viewedUrl;
			const artifact = artifactHosts.get(sessionId);
			if (artifact && viewed.host === artifact.host) {
				const source = new URL(artifact.sourceHref);
				source.pathname = viewed.pathname;
				source.search = viewed.search;
				source.hash = viewed.hash;
				return source.href;
			}
			const active = previewSessions.get(sessionId);
			if (!active || viewed.origin !== new URL(active.url).origin) return "";
			const source = new URL(active.sourceHref);
			source.pathname = viewed.pathname;
			source.search = viewed.search;
			source.hash = viewed.hash;
			return source.href;
		},
		agentBrowserUrl: async (sessionId) => {
			if (!sessionId || sessionId.length > 256) throw new Error("invalid preview session");
			const probe = await fetch(`http://127.0.0.1:${port}/${token}/api/v1/sessions/${encodeURIComponent(sessionId)}/agent-browser/stats`, { signal: AbortSignal.timeout(8_000) })
				.catch(() => null);
			if (!(await agentBrowserRunning(probe))) return "";
			const host = `ao-preview-${originMac(`agent\n${sessionId}`).toString("hex", 0, 16)}.localhost`;
			previewHosts.set(host, { sessionId, kind: "agent", entry: "" });
			return agentViewerUrl(host, securePort);
		},
		isAgentBrowserUrl: (url) => {
			try {
				const parsed = new URL(url);
				return parsed.protocol === "https:" && parsed.port === String(securePort) && parsed.pathname === "/v/cdp" && previewHosts.get(parsed.hostname)?.kind === "agent";
			} catch {
				return false;
			}
		},
		externalUrl: (url) => {
			let parsed: URL;
			try { parsed = new URL(url); } catch { return url; }
			const target = previewHosts.get(parsed.hostname);
			if (!target || target.kind === "agent") return url;
			parsed.protocol = "http:";
			parsed.port = String(port);
			return parsed.href;
		},
		close: () =>
			new Promise((resolve) => {
				closing = true;
				for (const drop of tunnels) drop();
				server.closeAllConnections();
				for (const socket of secureSockets) socket.destroy();
				void upstreamSession?.then((session) => session.destroy(), () => undefined);
				let open = 2;
				const closed = () => {
					if (--open > 0) return;
					log(`stopped on 127.0.0.1:${port} for ${upstream.host}`);
					resolve();
				};
				server.close(closed);
				secureServer.close(closed);
			}),
	};
}
