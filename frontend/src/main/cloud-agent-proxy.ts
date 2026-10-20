import { createHmac, randomBytes } from "node:crypto";
import { chmod, mkdir, readFile, writeFile } from "node:fs/promises";
import { request as httpRequest, type ClientRequest, type IncomingHttpHeaders, type IncomingMessage, type OutgoingHttpHeaders } from "node:http";
import { createSecureServer } from "node:http2";
import { request as httpsRequest } from "node:https";
import type { Socket } from "node:net";
import path from "node:path";
import type { Readable, Writable } from "node:stream";
import type { TLSSocket } from "node:tls";
import { WebSocket, WebSocketServer } from "ws";
import { agentBrowserRunning, agentViewerUrl, HOP_BY_HOP_HEADERS, listenOnStablePort } from "./agent-viewer";
import { getCloudAccessToken } from "./cloud-auth";
import { parseCloudBaseUrl } from "./cloud-cp-proxy";
import { previewCertificate } from "./preview-certificate";

// Loopback HTTPS proxy fronting AO Cloud sessions' agent browsers (the
// Chromium a sandbox's agent drives, streamed by its CDP viewer), which the
// Browser panel shows as agent tabs. A tab cannot put the person's cloud
// bearer on its requests, and the viewer reaches its socket, uploads and
// downloads by paths on its own origin. So each session's viewer gets its own
// preview origin here, and every request it makes goes to the control
// plane's agent-browser route with a fresh bearer and without its cookies.
//
// The agent browser can drive the person's logged-in pages: a socket or an
// upload is taken only from the viewer's own origin.
export type CloudAgentProxy = {
	/** The session's agent browser for its agent tab; "" when the sandbox has none. */
	agentBrowserUrl: (baseUrl: unknown, orgId: unknown, sessionId: unknown) => Promise<string>;
	/** Whether a URL is one of the agent-browser viewers this proxy serves now. */
	isAgentBrowserUrl: (url: string) => boolean;
	close: () => Promise<void>;
};

type Target = { base: URL; orgId: string; sessionId: string };
type Running = { port: number; hosts: Map<string, Target>; stop: () => Promise<void> };

// What both HTTP/1.1 and HTTP/2 requests to the TLS server look like here.
type ProxyRequest = Readable & { headers: IncomingHttpHeaders; url?: string; method?: string; authority?: string };
type ProxyResponse = Writable & { headersSent: boolean; writeHead(status: number, headers?: OutgoingHttpHeaders): unknown };

// Next to cloud-auth.bin: origins stay the same across restarts, so the
// viewer keeps its storage, and cannot be guessed without this file.
const SECRET_FILE = "cloud-agent-browser.key";
const ID = /^[\w-]{1,128}$/;
// The viewer's own routes, by method; its socket is ws/cdp.
const GET_TAILS = /^(?:v\/cdp|stats|download\/[\w-]+)$/;
const STRIP_REQUEST_HEADERS = new Set(["host", "origin", "cookie", "authorization", "proxy-authorization", ...HOP_BY_HOP_HEADERS]);
// The CP's cookies are not the viewer's.
const STRIP_RESPONSE_HEADERS = new Set([...HOP_BY_HOP_HEADERS, "set-cookie"]);

// Never the URL: the viewer puts its token in the query.
function warn(message: string): void {
	console.warn(`[cloud-agent-proxy] ${message}`);
}

async function loadSecret(dataDir: string): Promise<Buffer> {
	const file = path.join(dataDir, SECRET_FILE);
	const existing = await readFile(file).catch(() => null);
	if (existing?.length === 32) return existing;
	const secret = randomBytes(32);
	await mkdir(dataDir, { recursive: true });
	await writeFile(file, secret, { mode: 0o600 });
	await chmod(file, 0o600);
	return secret;
}

function cpUrl({ base, orgId, sessionId }: Target, tail: string, search = ""): URL {
	const basePath = base.pathname.replace(/\/+$/, "");
	return new URL(`${base.origin}${basePath}/api/cloud/v1/orgs/${encodeURIComponent(orgId)}/sessions/${encodeURIComponent(sessionId)}/agent-browser/${tail}${search}`);
}

export function createCloudAgentProxy(getDataDir: () => string): CloudAgentProxy {
	const bearer = async (): Promise<string | null> => (await getCloudAccessToken(getDataDir()).catch(() => null)) || null;
	let secret: Promise<Buffer> | undefined;
	const loadKey = (): Promise<Buffer> =>
		(secret ??= loadSecret(getDataDir()).catch((error: unknown) => {
			secret = undefined;
			throw error;
		}));
	let current: Promise<Running> | undefined;
	let live: Running | undefined;

	const start = async (key: Buffer): Promise<Running> => {
		const hosts = new Map<string, Target>();
		const sockets = new Set<TLSSocket>();
		const wss = new WebSocketServer({ noServer: true, perMessageDeflate: false });
		let port = 0;

		// The registered session a request is for, the viewer's own origin, and
		// the route it asks for; null for any other host.
		const route = (rawHost: string | undefined, rawUrl: string | undefined) => {
			const hostname = rawHost?.toLowerCase().replace(/:\d+$/, "") ?? "";
			const target = hosts.get(hostname);
			if (!target || !rawUrl?.startsWith("/")) return null;
			const url = new URL(`http://localhost${rawUrl}`);
			return { target, origin: `https://${hostname}:${port}`, tail: url.pathname.slice(1), search: url.search };
		};

		const answer = (res: ProxyResponse, status: number, error: string): void => {
			res.writeHead(status, { "content-type": "application/json" });
			res.end(JSON.stringify({ error }));
		};

		const handleRequest = async (req: ProxyRequest, res: ProxyResponse): Promise<void> => {
			// HTTP/2 carries the host in :authority rather than a Host header.
			const found = route(req.headers.host ?? req.authority, req.url);
			const allowed = found && ((req.method === "GET" && GET_TAILS.test(found.tail)) || (req.method === "POST" && found.tail === "upload"));
			if (!found || !allowed) return answer(res, 404, "unknown path");
			const origin = req.headers.origin;
			if (req.method === "GET" ? origin !== undefined && origin !== found.origin : origin !== found.origin) {
				return answer(res, 403, "agent browser origin mismatch");
			}
			// Closing the tab must also close its request to the CP.
			let proxied: ClientRequest | undefined;
			let left = false;
			res.once("close", () => {
				left = !res.writableEnded;
				if (left) proxied?.destroy();
			});
			const token = await bearer();
			if (left) return;
			if (!token) return answer(res, 401, "no AO Cloud session");
			const headers: OutgoingHttpHeaders = {};
			for (const [name, value] of Object.entries(req.headers)) {
				// HTTP/2 pseudo-headers (":authority", ":path") are not forwardable headers.
				if (!name.startsWith(":") && !STRIP_REQUEST_HEADERS.has(name)) headers[name] = value;
			}
			headers.authorization = `Bearer ${token}`;
			const fail = (error: Error): void => {
				// The tab left (destroying the request reports an error): no one to answer.
				if (left) return;
				warn(`control plane failed on ${req.method} ${found.tail.split("/", 1)[0]} (${error.message})`);
				if (res.headersSent) res.destroy(error);
				else answer(res, 502, "AO Cloud unreachable");
			};
			const url = cpUrl(found.target, found.tail, found.search);
			proxied = (url.protocol === "https:" ? httpsRequest : httpRequest)(url, { method: req.method, headers }, (upstream) => {
				const status = upstream.statusCode ?? 0;
				// HTTP/2 carries only 200-599 as a final status: writeHead would throw on the rest.
				if (!(status >= 200 && status <= 599)) {
					upstream.resume();
					return fail(new Error(`invalid status ${status}`));
				}
				const out: OutgoingHttpHeaders = {};
				for (const [name, value] of Object.entries(upstream.headers)) {
					if (!STRIP_RESPONSE_HEADERS.has(name)) out[name] = value;
				}
				res.writeHead(status, out);
				// Destroyed with an error, a cut-off download is not taken as complete.
				const cut = () => res.destroy(new Error("control plane response ended early"));
				upstream.on("aborted", cut);
				upstream.on("error", cut);
				upstream.pipe(res);
			});
			proxied.setTimeout(0);
			proxied.on("error", fail);
			// Streamed both ways: uploads and downloads can be large.
			req.pipe(proxied);
		};

		const handleUpgrade = async (req: IncomingMessage, socket: Socket, head: Buffer): Promise<void> => {
			const found = route(req.headers.host, req.url);
			if (!found || found.tail !== "ws/cdp" || req.headers.origin !== found.origin) return void socket.destroy();
			const token = await bearer();
			if (!token || socket.destroyed) return void socket.destroy();
			const url = cpUrl(found.target, found.tail, found.search);
			url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
			const upstream = new WebSocket(url, { headers: { authorization: `Bearer ${token}` }, perMessageDeflate: false, handshakeTimeout: 8_000 });
			let client: WebSocket | undefined;
			// Unheard, a socket error would crash the main process; "close" follows it.
			upstream.on("error", (error: Error) => warn(`control plane socket failed (${error.message})`));
			const abandon = () => upstream.terminate();
			socket.once("close", abandon);
			upstream.once("close", () => (client ? client.close() : socket.destroy()));
			upstream.once("open", () => {
				socket.off("close", abandon);
				if (socket.destroyed) return void upstream.terminate();
				wss.handleUpgrade(req, socket, head, (accepted) => {
					client = accepted;
					accepted.on("error", () => undefined);
					accepted.on("message", (data, isBinary) => upstream.send(data, { binary: isBinary }));
					upstream.on("message", (data, isBinary) => accepted.send(data, { binary: isBinary }));
					accepted.once("close", () => upstream.close());
				});
			});
		};

		const { cert, key: tlsKey } = previewCertificate();
		// HTTPS so the Browser panel trusts it as a preview (preview-certificate.ts);
		// HTTP/1.1 stays on for the socket.
		const server = createSecureServer({ cert, key: tlsKey, allowHTTP1: true });
		server.on("secureConnection", (socket: TLSSocket) => {
			sockets.add(socket);
			socket.once("close", () => sockets.delete(socket));
		});
		// A sign-out mid-request (the bearer lookup reports it) stops this server
		// under the request: that must not reach the main process as a rejection.
		server.on("request", (req, res) => void handleRequest(req, res).catch(() => res.destroy()));
		server.on("upgrade", (req: IncomingMessage, socket: Socket, head: Buffer) => void handleUpgrade(req, socket, head).catch(() => socket.destroy()));
		port = await listenOnStablePort(server, createHmac("sha256", key).update("port").digest());
		return {
			port,
			hosts,
			stop: () => new Promise<void>((resolve) => {
				hosts.clear();
				// close() alone waits on open sockets forever.
				for (const socket of sockets) socket.destroy();
				server.close(() => resolve());
			}),
		};
	};

	const running = (key: Buffer): Promise<Running> => {
		if (!current) {
			const opening: Promise<Running> = start(key).then(async (started) => {
				// Closed while it was starting.
				if (current !== opening) {
					await started.stop();
					throw new Error("the cloud agent browser proxy closed");
				}
				live = started;
				return started;
			});
			current = opening;
			// A start that failed is not kept: the next session tries again.
			opening.catch(() => {
				if (current === opening) current = undefined;
			});
		}
		return current;
	};

	return {
		agentBrowserUrl: async (baseUrl, orgId, sessionId) => {
			const base = parseCloudBaseUrl(baseUrl);
			if (typeof orgId !== "string" || typeof sessionId !== "string" || !ID.test(orgId) || !ID.test(sessionId)) {
				throw new Error("invalid cloud agent browser session");
			}
			const target: Target = { base, orgId, sessionId };
			// Only a sandbox whose agent browser runs has an agent tab: the CP answers
			// 502 for one without, or asleep; one the agent has not used yet answers
			// that it is not running. A CP this cannot reach gets none either.
			const token = await bearer();
			if (!token) return "";
			const probe = await fetch(cpUrl(target, "stats"), { headers: { authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(8_000) })
				.catch(() => null);
			if (!(await agentBrowserRunning(probe))) return "";
			const key = await loadKey();
			const { port, hosts } = await running(key);
			// Its own origin per session, unguessable without the secret.
			const host = `ao-preview-${createHmac("sha256", key).update(`agent\n${orgId}\n${sessionId}`).digest("hex").slice(0, 32)}.localhost`;
			hosts.set(host, target);
			return agentViewerUrl(host, port);
		},
		isAgentBrowserUrl: (url) => {
			try {
				const parsed = new URL(url);
				return live !== undefined && parsed.protocol === "https:" && parsed.port === String(live.port) &&
					parsed.pathname === "/v/cdp" && live.hosts.has(parsed.hostname);
			} catch {
				return false;
			}
		},
		close: async () => {
			const closing = current;
			current = undefined;
			live = undefined;
			await (await closing?.catch(() => undefined))?.stop();
		},
	};
}
