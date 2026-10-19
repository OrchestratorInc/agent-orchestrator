import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createServer, request as httpRequest, type ClientRequest, type IncomingMessage, type RequestOptions, type Server } from "node:http";
import { request as httpsRequest } from "node:https";
import { connect as netConnect, createServer as createNetServer, type AddressInfo, type Socket } from "node:net";
import { connect as tlsConnect, type ConnectionOptions } from "node:tls";
import {
	connect as http2Connect, constants as http2Constants, createSecureServer, createServer as createHttp2Server,
	type Http2SecureServer, type Http2Server, type Http2ServerRequest, type Http2ServerResponse, type ServerHttp2Session,
} from "node:http2";
import { WebSocketServer, createWebSocketStream, type WebSocket } from "ws";
import * as agentViewer from "./agent-viewer";
import { previewCertificate } from "./preview-certificate";
import { startRemoteProxy, type ActiveProxy } from "./remote-proxy";

// App previews are HTTPS. Trust exactly the proxy's preview certificate; its
// "*.localhost" name is a TLD wildcard Node won't match, which Electron's
// verify proc accepts by certificate instead (preview-certificate.ts).
const trustPreview = (preview: URL): ConnectionOptions => ({
	servername: preview.hostname,
	ca: previewCertificate().cert,
	checkServerIdentity: () => undefined,
});

function previewRequest(preview: URL, options: RequestOptions, onResponse: (res: IncomingMessage) => void): ClientRequest {
	return httpsRequest({
		hostname: "127.0.0.1",
		port: Number(preview.port),
		...trustPreview(preview),
		...options,
		headers: { Host: preview.host, ...options.headers },
	}, onResponse);
}

type Seen = {
	url: string;
	auth: string | undefined;
	expectedHost: string | undefined;
	origin: string | undefined;
	appAuth: string | undefined;
	appOrigin: string | undefined;
	body: string;
};

let upstream: Server | undefined;
let proxy: ActiveProxy | undefined;
// Lifecycle logging is the point of these spies, not a side effect to silence:
// every assertion below reads them, and swallowing the output keeps the suite
// readable while the proxy narrates itself.
let logged: string[];
let warned: string[];

beforeEach(() => {
	logged = [];
	warned = [];
	vi.spyOn(console, "log").mockImplementation((message: unknown) => {
		logged.push(String(message));
	});
	vi.spyOn(console, "warn").mockImplementation((message: unknown) => {
		warned.push(String(message));
	});
});

afterEach(async () => {
	vi.restoreAllMocks();
	await proxy?.close();
	upstream?.closeAllConnections();
	await new Promise<void>((resolve) => (upstream ? upstream.close(() => resolve()) : resolve()));
	upstream = undefined;
	proxy = undefined;
});

async function startUpstream(
	handler: (req: IncomingMessage, seen: Seen[]) => { status: number; body: string; headers?: Record<string, string> },
): Promise<{ port: number; seen: Seen[] }> {
	const seen: Seen[] = [];
	upstream = createServer((req, res) => {
		let body = "";
		req.on("data", (chunk) => (body += chunk));
		req.on("end", () => {
			seen.push({
				url: req.url ?? "",
				auth: req.headers.authorization,
				expectedHost: req.headers["x-ao-expected-host-id"] as string | undefined,
				origin: req.headers.origin,
				appAuth: req.headers["x-ao-preview-app-authorization"] as string | undefined,
				appOrigin: req.headers["x-ao-preview-app-origin"] as string | undefined,
				body,
			});
			const out = handler(req, seen);
			res.writeHead(out.status, { "content-type": "application/json", ...out.headers });
			res.end(out.body);
		});
	});
	await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
	return { port: (upstream.address() as AddressInfo).port, seen };
}

describe("startRemoteProxy", () => {
	it("serves authenticated artifact files and sibling assets without replacing an app preview", async () => {
		const { port, seen } = await startUpstream(() => ({ status: 200, body: "artifact", headers: { "set-cookie": "secret=1" } }));
		proxy = await startRemoteProxy({ label: "box", url: `http://127.0.0.1:${port}/ao`, password: "pw" });
		const app = proxy.previewUrl("ao-1", "http://localhost:5173/");
		const source = `http://ao-preview-artifact.mfxs2mi.localhost:${port}/docs/report.md?raw=true`;
		const raw = proxy.previewUrl("ao-1", source);
		const html = proxy.previewUrl("ao-1", source.replace("report.md?raw=true", "index.html"));
		expect(new URL(html).host).toBe(new URL(raw).host);
		expect(proxy.previewUrl("ao-1", source)).toBe(raw);
		expect(proxy.resolvePreviewUrl("ao-1", raw)).toBe(source);
		expect(proxy.resolvePreviewUrl("ao-2", raw)).toBe("");
		const request = async (url: string, method = "GET") => {
			const target = new URL(url);
			return new Promise<number>((resolve, reject) => {
				const req = httpRequest({ hostname: "127.0.0.1", port: Number(new URL(proxy!.base).port), path: target.pathname + target.search, method, headers: { Host: target.host } }, (res) => {
					expect(res.headers["set-cookie"]).toBeUndefined();
					res.resume();
					res.on("end", () => resolve(res.statusCode ?? 0));
				});
				req.on("error", reject);
				req.end();
			});
		};
		expect(await request(raw)).toBe(200);
		expect(await request(html)).toBe(200);
		expect(await request(new URL("chart.png", raw).href)).toBe(200);
		expect(await request(new URL("/assets/chart.png", raw).href)).toBe(200);
		expect(await request(raw, "POST")).toBe(404);
		expect(seen.map((entry) => entry.url)).toEqual([
			"/ao/api/v1/sessions/ao-1/preview/files/docs/report.md?raw=true&source=artifact",
			"/ao/api/v1/sessions/ao-1/preview/files/docs/index.html?source=artifact",
			"/ao/api/v1/sessions/ao-1/preview/files/docs/chart.png?source=artifact",
			"/ao/api/v1/sessions/ao-1/preview/files/assets/chart.png?source=artifact",
		]);
		expect(seen.every((entry) => entry.auth === "Bearer pw" && !entry.appAuth)).toBe(true);
		// Clearing Browser's app preview does not invalidate raw artifact reads.
		expect(proxy.previewUrl("ao-1", "http://localhost:5173/")).toBe(app);
		proxy.previewUrl("ao-1", "");
		expect(await request(raw)).toBe(200);
	});

	it("keeps an app preview's origin across restarts with the same preview port", async () => {
		// Port contention permits a different origin; isolate stable-port URL mapping.
		vi.spyOn(agentViewer, "listenOnStablePort").mockResolvedValue(44000);
		const { port } = await startUpstream(() => ({ status: 200, body: "{}" }));
		const entry = { label: "box", url: `http://127.0.0.1:${port}/ao`, password: "pw" };
		proxy = await startRemoteProxy(entry);
		const first = new URL(proxy.previewUrl("ao-1", "http://localhost:5173/"));
		// A restarted dev server comes back on another port: same origin, so cookies and storage survive.
		expect(new URL(proxy.previewUrl("ao-1", "http://localhost:5174/")).origin).toBe(first.origin);
		expect(new URL(proxy.previewUrl("ao-2", "http://localhost:5173/")).hostname).not.toBe(first.hostname);
		await proxy.close();
		proxy = await startRemoteProxy(entry);
		expect(new URL(proxy.previewUrl("ao-1", "http://localhost:5173/")).origin).toBe(first.origin);
		await proxy.close();
		// Without the host's password the origin cannot be guessed.
		proxy = await startRemoteProxy({ ...entry, password: "other" });
		expect(new URL(proxy.previewUrl("ao-1", "http://localhost:5173/")).hostname).not.toBe(first.hostname);
	});

	it("scopes a stable static preview origin to one session and serves root assets", async () => {
		const { port, seen } = await startUpstream((request) => ({
			status: request.url === "/api/v1/projects" ? 200 : 200,
			body: request.url === "/api/v1/projects" ? "SECRET API" : "preview file",
		}));
		proxy = await startRemoteProxy({ label: "workbox", url: `http://127.0.0.1:${port}`, password: "pw" });
		const source = `http://ao-preview.mfxs2mi.localhost:${port}/dist/index.html`;
		const preview = proxy.previewUrl("ao-1", source);
		expect(proxy.resolvePreviewUrl("ao-1", new URL("/design?x=1#note", preview).href)).toBe(`http://ao-preview.mfxs2mi.localhost:${port}/design?x=1#note`);
		expect(proxy.resolvePreviewUrl("ao-2", preview)).toBe("");
		expect(proxy.resolvePreviewUrl("ao-1", "https://example.com/design")).toBe("https://example.com/design");
		const shorthand = proxy.previewUrl("ao-3", "localhost:5173/");
		expect(proxy.resolvePreviewUrl("ao-3", new URL("/details", shorthand).href)).toBe("http://localhost:5173/details");
		expect(proxy.previewUrl("ao-1", source)).toBe(preview);
		expect(new URL(preview).hostname).not.toBe(new URL(proxy.base).hostname);
		expect(preview).not.toContain(new URL(proxy.base).pathname.slice(1));
		const request = async (url: string): Promise<{ status: number; body: string }> => {
			const target = new URL(url);
			return new Promise((resolve, reject) => {
				const req = httpRequest({ hostname: "127.0.0.1", port: Number(new URL(proxy!.base).port), path: target.pathname + target.search, headers: { Host: target.host } }, (res) => {
					let body = "";
					res.on("data", (chunk) => { body += chunk.toString(); });
					res.on("end", () => resolve({ status: res.statusCode ?? 0, body }));
				});
				req.on("error", reject);
				req.end();
			});
		};
		const asset = await request(new URL("/assets/app.css", preview).href);
		expect(asset.body).toBe("preview file");
		expect(seen.at(-1)?.url).toBe("/api/v1/sessions/ao-1/preview/files/dist/assets/app.css");
		const attemptedAPI = await request(new URL("/api/v1/projects", preview).href);
		expect(attemptedAPI.body).toBe("preview file");
		expect(seen.at(-1)?.url).toBe("/api/v1/sessions/ao-1/preview/files/dist/api/v1/projects");
		expect(() => proxy!.previewUrl("ao-2", source)).toThrow(/unsupported|preview/i);
		expect(new URL(proxy.previewUrl("ao-2", "http://127.0.0.2:5173/")).hostname).toMatch(/^ao-preview-[0-9a-f]{32}\.localhost$/);
		expect(new URL(proxy.previewUrl("ao-2", "http://[::ffff:127.0.0.1]:5173/")).hostname).toMatch(/^ao-preview-[0-9a-f]{32}\.localhost$/);
		expect(new URL(proxy.previewUrl("ao-2", "http://localhost.:5173/")).hostname).toMatch(/^ao-preview-[0-9a-f]{32}\.localhost$/);
		const changed = proxy.previewUrl("ao-1", `http://localhost:5173/`);
		expect(changed).not.toBe(preview);
		expect(proxy.resolvePreviewUrl("ao-1", preview)).toBe("");
		expect((await request(preview)).status).toBe(404);
		expect(proxy.previewUrl("ao-1", "https://example.com/")).toBe("https://example.com/");
		expect((await request(changed)).status).toBe(404);
		const clearing = proxy.previewUrl("ao-1", "http://localhost:5173/");
		expect(proxy.previewUrl("ao-1", "")).toBe("");
		expect((await request(clearing)).status).toBe(404);
	});

	it("relays app Authorization and same-origin intent only on preview requests", async () => {
		const { port, seen } = await startUpstream(() => ({ status: 200, body: "ok" }));
		proxy = await startRemoteProxy({ label: "workbox", url: `http://127.0.0.1:${port}`, password: "host-password" });
		const preview = new URL(proxy.previewUrl("ao-1", "http://localhost:5173/"));
		const post = (origin?: string, authorization?: string) => new Promise<number>((resolve, reject) => {
			const req = previewRequest(preview, {
				path: "/submit", method: "POST",
				headers: {
					...(origin ? { Origin: origin } : {}), ...(authorization ? { Authorization: authorization } : {}),
					"X-AO-Preview-App-Authorization": "forged", "X-AO-Preview-App-Origin": "forged",
				},
			}, (res) => { res.resume(); res.on("end", () => resolve(res.statusCode ?? 0)); });
			req.on("error", reject);
			req.end("data");
		});
		expect(await post(preview.origin, "Basic app-token")).toBe(200);
		expect(seen.at(-1)).toMatchObject({
			url: "/api/v1/sessions/ao-1/preview/app/submit", auth: "Bearer host-password", origin: undefined,
			appAuth: "Basic app-token", appOrigin: preview.origin, body: "data",
		});
		expect(await post()).toBe(200);
		expect(seen.at(-1)).toMatchObject({ appAuth: undefined, appOrigin: undefined });
		expect(await post("http://evil.example")).toBe(403);
		expect(seen).toHaveLength(2);
	});

	it("does not log preview paths or query secrets on connection failure", async () => {
		const { port } = await startUpstream(() => ({ status: 200, body: '{"hostId":"wrong","apiVersion":1}' }));
		proxy = await startRemoteProxy({ hostId: "expected", label: "workbox", url: `http://127.0.0.1:${port}`, password: "pw" });
		const preview = new URL(proxy.previewUrl("ao-1", "http://localhost:5173/reset/path-secret?code=query-secret"));
		const status = await new Promise<number>((resolve, reject) => {
			const req = previewRequest(preview, { path: preview.pathname + preview.search }, (res) => {
				res.resume();
				res.on("end", () => resolve(res.statusCode ?? 0));
			});
			req.on("error", reject);
			req.end();
		});
		expect(status).toBe(502);
		expect(warned.join("\n")).toContain("preview/app");
		expect(warned.join("\n")).not.toMatch(/path-secret|query-secret/);
	});

	it("shows managed-server guidance only for AO's own preview conflict", async () => {
		let managed = false;
		const { port } = await startUpstream(() => ({
			status: 409, body: managed ? '{"code":"PREVIEW_MANAGED_REQUIRED"}' : "app conflict",
			headers: managed ? { "x-ao-preview-managed-required": "1" } : undefined,
		}));
		proxy = await startRemoteProxy({ label: "workbox", url: `http://127.0.0.1:${port}`, password: "pw" });
		const preview = new URL(proxy.previewUrl("ao-1", "http://localhost:5173/"));
		const request = () => new Promise<string>((resolve, reject) => {
			const req = previewRequest(preview, { path: "/" }, (res) => {
				let body = "";
				res.on("data", (chunk) => { body += chunk.toString(); });
				res.on("end", () => resolve(body));
			});
			req.on("error", reject);
			req.end();
		});
		expect(await request()).toBe("app conflict");
		managed = true;
		expect(await request()).toContain("ao preview start");
	});

	it("forwards a managed preview websocket on its preview-only origin", async () => {
		upstream = createServer((_request, response) => response.end("ok"));
		let upgradePath = "";
		let upgradeAuth = "";
		let upgradeAppAuth = "";
		let upgradeAppOrigin = "";
		upstream.on("upgrade", (request, socket) => {
			upgradePath = request.url ?? "";
			upgradeAuth = String(request.headers.authorization ?? "");
			upgradeAppAuth = String(request.headers["x-ao-preview-app-authorization"] ?? "");
			upgradeAppOrigin = String(request.headers["x-ao-preview-app-origin"] ?? "");
			socket.write("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n");
			socket.end();
		});
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		const port = (upstream.address() as AddressInfo).port;
		proxy = await startRemoteProxy({ label: "workbox", url: `http://127.0.0.1:${port}`, password: "pw" });
		const preview = new URL(proxy.previewUrl("ao-1", "http://localhost:5173/"));
		const socket = tlsConnect({ host: "127.0.0.1", port: Number(preview.port), ...trustPreview(preview) });
		const reply = await new Promise<string>((resolve, reject) => {
			let data = "";
			socket.on("error", reject);
			socket.on("data", (chunk) => {
				data += chunk.toString();
				if (data.includes("\r\n\r\n")) resolve(data);
			});
			socket.on("secureConnect", () => socket.write(`GET /socket HTTP/1.1\r\nHost: ${preview.host}\r\nOrigin: ${preview.origin}\r\nAuthorization: Basic app-token\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGVzdA==\r\nSec-WebSocket-Version: 13\r\n\r\n`));
		});
		socket.destroy();
		expect(reply).toContain("101 Switching Protocols");
		expect(upgradePath).toBe("/api/v1/sessions/ao-1/preview/app/socket");
		expect(upgradeAuth).toBe("Bearer pw");
		expect(upgradeAppAuth).toBe("Basic app-token");
		expect(upgradeAppOrigin).toBe(preview.origin);
	});
	it("serves a session's agent browser on its own origin through the daemon's agent-browser route", async () => {
		const { port, seen } = await startUpstream(() => ({ status: 200, body: "ok" }));
		proxy = await startRemoteProxy({ label: "workbox", url: `http://127.0.0.1:${port}`, password: "host-password" });
		const agent = new URL(await proxy.agentBrowserUrl("ao-1"));
		expect(agent.hostname).toMatch(/^ao-preview-[0-9a-f]{32}\.localhost$/);
		expect(`${agent.pathname}${agent.search}`).toBe("/v/cdp?embed=1&sq=70&ss=1");
		expect(proxy.isAgentBrowserUrl(agent.href)).toBe(true);
		expect(proxy.isAgentBrowserUrl(proxy.previewUrl("ao-1", "http://localhost:5173/v/cdp"))).toBe(false);
		expect(seen.at(-1)).toMatchObject({ url: "/api/v1/sessions/ao-1/agent-browser/stats", auth: "Bearer host-password" });
		// Never the app's origin, and the same one again for the same session.
		expect(agent.origin).not.toBe(new URL(proxy.previewUrl("ao-1", "http://localhost:5173/")).origin);
		expect(new URL(await proxy.agentBrowserUrl("ao-1")).origin).toBe(agent.origin);
		const send = (method: string, path: string, origin?: string) => new Promise<number>((resolve, reject) => {
			const req = previewRequest(agent, { path, method, headers: origin ? { Origin: origin } : {} }, (res) => { res.resume(); res.on("end", () => resolve(res.statusCode ?? 0)); });
			req.on("error", reject);
			req.end(method === "POST" ? "file-bytes" : undefined);
		});
		expect(await send("GET", "/v/cdp?sq=70")).toBe(200);
		expect(seen.at(-1)).toMatchObject({ url: "/api/v1/sessions/ao-1/agent-browser/v/cdp?sq=70", auth: "Bearer host-password" });
		expect(await send("POST", "/upload?dir=abc&name=a.txt", agent.origin)).toBe(200);
		expect(seen.at(-1)).toMatchObject({ url: "/api/v1/sessions/ao-1/agent-browser/upload?dir=abc&name=a.txt", body: "file-bytes" });
		const count = seen.length;
		expect(await send("POST", "/upload", "http://evil.example")).toBe(403);
		expect(seen).toHaveLength(count);
	});

	it("opens an app preview outside AO over plain http, but never the agent viewer", async () => {
		const { port, seen } = await startUpstream(() => ({ status: 200, body: "ok" }));
		proxy = await startRemoteProxy({ label: "workbox", url: `http://127.0.0.1:${port}`, password: "pw" });
		const plainPort = Number(new URL(proxy.base).port);
		const get = (url: string, to = Number(new URL(url).port)) => new Promise<number>((resolve, reject) => {
			const target = new URL(url);
			const req = httpRequest({ hostname: "127.0.0.1", port: to, path: target.pathname + target.search, headers: { Host: target.host } }, (res) => {
				res.resume();
				res.on("end", () => resolve(res.statusCode ?? 0));
			});
			req.on("error", reject);
			req.end();
		});
		const preview = new URL(proxy.previewUrl("ao-1", "http://localhost:5173/"));
		// Same host, path, query and hash, on the plain server: no certificate for the system browser to refuse.
		const external = proxy.externalUrl(`${preview.origin}/dash?x=1#top`);
		expect(external).toBe(`http://${preview.hostname}:${plainPort}/dash?x=1#top`);
		expect(proxy.externalUrl(external)).toBe(external);
		expect(await get(external)).toBe(200);
		expect(seen.at(-1)).toMatchObject({ url: "/api/v1/sessions/ao-1/preview/app/dash?x=1", auth: "Bearer pw" });
		expect(proxy.externalUrl("https://github.com/x")).toBe("https://github.com/x");
		// The agent viewer is the Browser panel's alone, whatever port its Host names.
		const agent = await proxy.agentBrowserUrl("ao-1");
		expect(proxy.externalUrl(agent)).toBe(agent);
		const count = seen.length;
		expect(await get(agent, plainPort)).toBe(404);
		expect(await get(`http://${new URL(agent).hostname}:${plainPort}/v/cdp`)).toBe(404);
		expect(seen).toHaveLength(count);
	});

	it("gives no agent browser when the host has none", async () => {
		const { port } = await startUpstream(() => ({ status: 404, body: '{"error":"not found"}' }));
		proxy = await startRemoteProxy({ label: "workbox", url: `http://127.0.0.1:${port}`, password: "pw" });
		expect(await proxy.agentBrowserUrl("ao-1")).toBe("");
	});

	it("forwards the agent browser's stream socket", async () => {
		upstream = createServer((_request, response) => response.end("ok"));
		let upgradePath = "";
		let upgradeAuth = "";
		upstream.on("upgrade", (request, socket) => {
			upgradePath = request.url ?? "";
			upgradeAuth = String(request.headers.authorization ?? "");
			socket.write("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n");
			socket.end();
		});
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		proxy = await startRemoteProxy({ label: "workbox", url: `http://127.0.0.1:${(upstream.address() as AddressInfo).port}`, password: "pw" });
		const agent = new URL(await proxy.agentBrowserUrl("ao-1"));
		const socket = tlsConnect({ host: "127.0.0.1", port: Number(agent.port), ...trustPreview(agent) });
		const reply = await new Promise<string>((resolve, reject) => {
			let data = "";
			socket.on("error", reject);
			socket.on("data", (chunk) => {
				data += chunk.toString();
				if (data.includes("\r\n\r\n")) resolve(data);
			});
			socket.on("secureConnect", () => socket.write(`GET /ws/cdp?token=null HTTP/1.1\r\nHost: ${agent.host}\r\nOrigin: ${agent.origin}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGVzdA==\r\nSec-WebSocket-Version: 13\r\n\r\n`));
		});
		socket.destroy();
		expect(reply).toContain("101 Switching Protocols");
		expect(upgradePath).toBe("/api/v1/sessions/ao-1/agent-browser/ws/cdp?token=null");
		expect(upgradeAuth).toBe("Bearer pw");
	});

	it("proxies an IPv6 host instead of dialing its URL brackets", async (context) => {
		upstream = createServer((_request, response) => response.end("ok"));
		try {
			await new Promise<void>((resolve, reject) => {
				upstream?.once("error", reject);
				upstream?.listen(0, "::1", resolve);
			});
		} catch (error) {
			if (["EADDRNOTAVAIL", "EAFNOSUPPORT"].includes((error as NodeJS.ErrnoException).code ?? "")) {
				upstream = undefined;
				context.skip();
			}
			throw error;
		}
		const port = (upstream.address() as AddressInfo).port;
		proxy = await startRemoteProxy({ label: "IPv6 box", url: `http://[::1]:${port}`, password: "pw" });
		const response = await fetch(`${proxy.base}/api/v1/projects`);
		expect(response.status).toBe(200);
		expect(await response.text()).toBe("ok");
	});

	it("allows only the Forge dev renderer origin when configured", async () => {
		const { port } = await startUpstream(() => ({ status: 200, body: "{}" }));
		proxy = await startRemoteProxy(
			{ label: "workbox", url: `http://127.0.0.1:${port}`, password: "pw" },
			"http://localhost:5173",
		);
		const response = await fetch(`${proxy.base}/api/v1/projects`, {
			method: "OPTIONS",
			headers: { Origin: "http://localhost:5173" },
		});
		expect(response.headers.get("access-control-allow-origin")).toBe("http://localhost:5173");
		const foreign = await fetch(`${proxy.base}/api/v1/projects`, {
			method: "OPTIONS",
			headers: { Origin: "http://evil.example" },
		});
		expect(foreign.headers.get("access-control-allow-origin")).toBe("http://localhost:5173");
	});

	it("serves a verified host with the token stripped", async () => {
		const { port, seen } = await startUpstream((request) => ({
			status: 200,
			body: request.url === "/api/v1/identity"
				? '{"hostId":"h_workbox","apiVersion":1}'
				: '{"ok":true}',
		}));
		proxy = await startRemoteProxy({ hostId: "h_workbox", label: "workbox", url: `http://127.0.0.1:${port}`, password: "secret" });
		const response = await fetch(`${proxy.base}/api/v1/projects`, {
			headers: { "X-AO-Expected-Host-ID": "h_spoofed" },
		});
		expect(response.status).toBe(200);
		expect(seen.map(({ url, auth, expectedHost }) => ({ url, auth, expectedHost }))).toEqual([
			{ url: "/api/v1/identity", auth: undefined, expectedHost: undefined },
			{ url: "/api/v1/projects", auth: "Bearer secret", expectedHost: "h_workbox" },
		]);
	});

	it("rechecks host identity before a later request can send its credential", async () => {
		const { port, seen } = await startUpstream((request) => ({
			status: 200,
			body: request.url === "/api/v1/identity"
				? '{"hostId":"h_replacement","apiVersion":1}'
				: "{}",
		}));
		proxy = await startRemoteProxy({
			hostId: "h_expected",
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "secret",
		});
		const response = await fetch(`${proxy.base}/api/v1/projects?code=secret-code`);
		expect(response.status).toBe(502);
		expect(await response.json()).toEqual({
			error: "remote host identity not verified",
			code: "HOST_IDENTITY_UNVERIFIED",
			message: "remote host identity not verified",
		});
		expect(seen.map((request) => request.url)).toEqual(["/api/v1/identity"]);
		expect(seen[0].auth).toBeUndefined();
		expect(warned.join("\n")).not.toContain("secret-code");
	});

	it("stops forwarding after a host upgrades to an incompatible API", async () => {
		const { port, seen } = await startUpstream((request) => ({
			status: 200,
			body: request.url === "/api/v1/identity"
				? '{"hostId":"h_workbox","apiVersion":2}'
				: "{}",
		}));
		proxy = await startRemoteProxy({ hostId: "h_workbox", label: "workbox", url: `http://127.0.0.1:${port}`, password: "secret" });
		const response = await fetch(`${proxy.base}/api/v1/projects`);
		expect(response.status).toBe(426);
		expect(await response.json()).toMatchObject({ code: "HOST_API_INCOMPATIBLE", message: expect.stringContaining("Update AO") });
		expect(seen.map((request) => request.url)).toEqual(["/api/v1/identity"]);
		expect(seen[0].auth).toBeUndefined();
	});

	it("connects a saved address without an explicit scheme", async () => {
		const { port, seen } = await startUpstream(() => ({ status: 200, body: "{}" }));
		proxy = await startRemoteProxy({ label: "workbox", url: `127.0.0.1:${port}`, password: "pw" });
		const response = await fetch(`${proxy.base}/healthz`);
		expect(response.status).toBe(200);
		expect(seen[0].auth).toBe("Bearer pw");
	});

	it("forwards with the token stripped and the credential injected", async () => {
		const { port, seen } = await startUpstream(() => ({
			status: 200,
			body: '{"ok":true}',
		}));
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const res = await fetch(`${proxy.base}/api/v1/projects`, {
			method: "POST",
			headers: { "content-type": "application/json", origin: "app://renderer" },
			body: '{"path":"/srv/repo"}',
		});

		expect(res.status).toBe(200);
		expect(await res.json()).toEqual({ ok: true });
		expect(seen).toHaveLength(1);
		expect(seen[0].url).toBe("/api/v1/projects"); // token gone
		expect(seen[0].auth).toBe("Bearer pw");
		expect(seen[0].origin).toBeUndefined(); // app://renderer never reaches the daemon
		expect(seen[0].body).toBe('{"path":"/srv/repo"}');
	});

	// The add-host dialog accepts https://, so this is what a Tailscale Serve or
	// reverse-proxy address does today: the upstream is a TLS endpoint and the
	// proxy must not put the connection password on the wire in the clear.
	it("never sends the credential in the clear to an https host", async () => {
		const { port, seen } = await startUpstream(() => ({ status: 200, body: "{}" }));
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `https://127.0.0.1:${port}`,
			password: "pw",
		});

		// A cleartext listener cannot complete a TLS handshake, so the honest
		// outcome is a failed request — never a plaintext one that succeeds.
		const res = await fetch(`${proxy.base}/api/v1/projects`);
		expect(res.status).toBe(502);
		expect(await res.json()).toEqual({
			error: "remote daemon unreachable",
			code: "UPSTREAM_UNAVAILABLE",
			message: "remote daemon unreachable",
		});
		expect(seen).toHaveLength(0);
	});

	// A daemon can live behind a reverse proxy at a path. The renderer's base is
	// the loopback proxy's own root, so the upstream prefix is restored here or
	// every credentialled request lands on whatever else that vhost serves.
	it("restores the host's path prefix upstream", async () => {
		const { port, seen } = await startUpstream(() => ({ status: 200, body: "{}" }));
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}/ao`,
			password: "pw",
		});

		await fetch(`${proxy.base}/api/v1/projects`);
		expect(seen[0].url).toBe("/ao/api/v1/projects");
	});

	it("refuses a request without the token and sends nothing upstream", async () => {
		const { port, seen } = await startUpstream(() => ({
			status: 200,
			body: "{}",
		}));
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const bare = new URL(proxy.base);
		const res = await fetch(`${bare.origin}/api/v1/projects`);
		expect(res.status).toBe(404);
		expect(seen).toHaveLength(0);
	});

	it("refuses a near-miss token prefix", async () => {
		const { port, seen } = await startUpstream(() => ({
			status: 200,
			body: "{}",
		}));
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		// A path that merely starts with the token's characters is not the token:
		// /<token>x/... must not authorize, or the token stops being a boundary.
		const bare = new URL(proxy.base);
		const res = await fetch(`${bare.origin}${bare.pathname}x/api/v1/projects`);
		expect(res.status).toBe(404);
		expect(seen).toHaveLength(0);
	});

	it("answers CORS preflight itself for the renderer origin", async () => {
		const { port, seen } = await startUpstream(() => ({
			status: 200,
			body: "{}",
		}));
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const res = await fetch(`${proxy.base}/api/v1/projects`, {
			method: "OPTIONS",
			headers: {
				origin: "app://renderer",
				"access-control-request-method": "POST",
				"access-control-request-headers": "content-type",
				"access-control-request-private-network": "true",
			},
		});
		expect(res.status).toBe(204);
		expect(res.headers.get("access-control-allow-origin")).toBe("app://renderer");
		expect(res.headers.get("access-control-allow-headers")).toMatch(/content-type/i);
		expect(res.headers.get("access-control-allow-private-network")).toBe("true");
		expect(seen).toHaveLength(0); // preflight never leaves the machine
	});

	it("adds the renderer origin to real responses so cross-origin fetch succeeds", async () => {
		const { port } = await startUpstream(() => ({
			status: 400,
			body: '{"error":"bad"}',
		}));
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const res = await fetch(`${proxy.base}/api/v1/projects`, {
			headers: { origin: "app://renderer" },
		});
		expect(res.status).toBe(400); // errors pass through untouched…
		expect(res.headers.get("access-control-allow-origin")).toBe("app://renderer"); // …but stay readable
	});

	// "The app can't reach my host" had no answer anywhere before this, and the
	// only thing that could make these lines unshippable is a secret in one.
	it("logs its own lifecycle without the token or the password", async () => {
		const { port } = await startUpstream(() => ({ status: 200, body: "{}" }));
		proxy = await startRemoteProxy({ label: "workbox", url: `http://127.0.0.1:${port}`, password: "hunter2secret" });
		const token = new URL(proxy.base).pathname.slice(1);

		expect(logged.some((line) => line.includes("[remote-proxy] started on 127.0.0.1:"))).toBe(true);
		await proxy.close();
		proxy = undefined;
		expect(logged.some((line) => line.includes("[remote-proxy] stopped on 127.0.0.1:"))).toBe(true);

		const everything = [...logged, ...warned].join("\n");
		expect(everything).not.toContain("hunter2secret");
		expect(everything).not.toContain(token);
	});

	it("warns which upstream failed when it answers 502, naming no secret", async () => {
		const { port } = await startUpstream(() => ({ status: 200, body: "{}" }));
		await new Promise<void>((resolve) => (upstream ? upstream.close(() => resolve()) : resolve()));
		upstream = undefined;
		proxy = await startRemoteProxy({ label: "workbox", url: `http://127.0.0.1:${port}`, password: "hunter2secret" });
		const token = new URL(proxy.base).pathname.slice(1);

		const res = await fetch(`${proxy.base}/api/v1/projects`);
		expect(res.status).toBe(502);
		const warning = warned.find((line) => line.includes("answering 502"));
		expect(warning).toContain(`127.0.0.1:${port}`);
		// The post-strip path, which is the useful half; req.url starts with the token.
		expect(warning).toContain("/api/v1/projects");
		expect(warning).not.toContain("hunter2secret");
		expect(warning).not.toContain(token);
	});

	it("binds only to 127.0.0.1, not every interface", async () => {
		const { port } = await startUpstream(() => ({ status: 200, body: "{}" }));
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});
		expect((await fetch(`${proxy.base}/healthz`)).status).toBe(200);
		expect(proxy.listeningAddress).toBe("127.0.0.1");
	});
});

describe("startRemoteProxy streams", () => {
	it("closes a stream when the remote daemon drops it after headers", async () => {
		upstream = createServer((_req, res) => {
			res.writeHead(200, { "content-type": "text/event-stream" });
			res.write("data: first\n\n");
			setTimeout(() => res.destroy(), 20);
		});
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		const port = (upstream.address() as AddressInfo).port;
		proxy = await startRemoteProxy({ label: "workbox", url: `http://127.0.0.1:${port}`, password: "pw" });

		const response = await fetch(`${proxy.base}/api/v1/events`);
		expect(response.status).toBe(200);
		await expect(response.text()).rejects.toThrow();
		expect(warned.some((line) => line.includes("stream ended"))).toBe(true);
	});

	it("refuses a WebSocket when the host identity changed", async () => {
		let sawUpgrade = false;
		upstream = createServer((request, response) => {
			response.setHeader("content-type", "application/json");
			response.end(request.url === "/api/v1/identity" ? '{"hostId":"h_other","apiVersion":1}' : "{}");
		});
		upstream.on("upgrade", (_request, socket) => {
			sawUpgrade = true;
			socket.destroy();
		});
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		const port = (upstream.address() as AddressInfo).port;
		proxy = await startRemoteProxy({ hostId: "h_expected", label: "workbox", url: `http://127.0.0.1:${port}`, password: "secret" });
		const proxyUrl = new URL(proxy.base);
		const socket = netConnect(Number(proxyUrl.port), "127.0.0.1");
		await new Promise<void>((resolve) => socket.once("connect", resolve));
		const closed = new Promise<void>((resolve) => socket.once("close", () => resolve()));
		socket.write(`GET ${proxyUrl.pathname}/mux HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGVzdA==\r\nSec-WebSocket-Version: 13\r\n\r\n`);
		await closed;
		expect(sawUpgrade).toBe(false);
	});

	it("closes while a WebSocket is waiting for the identity probe", async () => {
		let started!: () => void;
		const identityStarted = new Promise<void>((resolve) => { started = resolve; });
		upstream = createServer((request, response) => {
			if (request.url !== "/api/v1/identity") return;
			started();
			setTimeout(() => {
				response.setHeader("content-type", "application/json");
				response.end('{"hostId":"h_workbox","apiVersion":1}');
			}, 500);
		});
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		const port = (upstream.address() as AddressInfo).port;
		proxy = await startRemoteProxy({ hostId: "h_workbox", label: "workbox", url: `http://127.0.0.1:${port}`, password: "pw" });
		const proxyUrl = new URL(proxy.base);
		const socket = netConnect(Number(proxyUrl.port), "127.0.0.1");
		await new Promise<void>((resolve) => socket.once("connect", resolve));
		socket.write(`GET ${proxyUrl.pathname}/mux HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGVzdA==\r\nSec-WebSocket-Version: 13\r\n\r\n`);
		await identityStarted;
		const closing = proxy.close();
		const outcome = await Promise.race([
			closing.then(() => "closed"),
			new Promise<string>((resolve) => setTimeout(() => resolve("pending"), 200)),
		]);
		socket.destroy();
		await closing;
		expect(outcome).toBe("closed");
	});

	it("closes while an upgraded socket is still open", async () => {
		upstream = createServer();
		const upgraded = new Promise<void>((resolve) => {
			upstream?.on("upgrade", (_req, socket) => {
				socket.on("error", () => undefined);
				socket.write("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n");
				socket.on("end", () => socket.destroy());
				resolve();
			});
		});
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		const port = (upstream.address() as AddressInfo).port;
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const proxyUrl = new URL(proxy.base);
		const socket = netConnect(Number(proxyUrl.port), "127.0.0.1");
		socket.on("error", () => undefined);
		socket.write(
			`GET ${proxyUrl.pathname}/mux HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGVzdA==\r\nSec-WebSocket-Version: 13\r\n\r\n`,
		);
		await upgraded;

		const closing = proxy.close();
		const result = await Promise.race([
			closing.then(() => "closed"),
			new Promise<string>((resolve) => setTimeout(() => resolve("timed out"), 500)),
		]);
		socket.destroy();
		await closing;
		proxy = undefined;

		expect(result).toBe("closed");
	});

	// Regression for the connection-status hang: the previous test's upstream
	// writes its first chunk synchronously, so headers and that chunk reach the
	// client together even without an explicit flush — it never exercised the
	// gap. A real SSE upstream (GET /api/v1/events) can hold its first byte
	// indefinitely; a client's EventSource must still see the response headers
	// right away; otherwise it reports CONNECTING forever, which is exactly what
	// "not receiving live updates" looked like before this was found.
	it("flushes response headers before the first SSE byte arrives", async () => {
		let upstreamClosed = false;
		upstream = createServer((_req, res) => {
			res.once("close", () => { upstreamClosed = true; });
			res.writeHead(200, { "content-type": "text/event-stream" });
			// The real daemon flushes its own headers immediately (confirmed by a
			// direct curl against it) — this upstream must too, or the test would
			// measure the fake upstream's header delay instead of the proxy's.
			res.flushHeaders();
			// The body is what's withheld for 500ms, like a daemon with nothing to
			// say yet.
			setTimeout(() => {
				res.write("data: first\n\n");
				res.end();
			}, 500);
		});
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		const port = (upstream.address() as AddressInfo).port;
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const proxyUrl = new URL(proxy.base);
		const started = Date.now();
		const head = await new Promise<string>((resolve) => {
			const socket = netConnect(Number(proxyUrl.port), "127.0.0.1", () => {
				socket.write(`GET ${proxyUrl.pathname}/api/v1/events HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n`);
			});
			socket.on("error", () => undefined);
			let buf = "";
			socket.on("data", (chunk: Buffer) => {
				buf += chunk.toString();
				if (buf.includes("\r\n\r\n")) {
					socket.destroy();
					resolve(buf);
				}
			});
		});
		const headersArrivedAfterMs = Date.now() - started;

		expect(head).toContain("200");
		expect(head.toLowerCase()).toContain("content-type: text/event-stream");
		// The upstream withholds its first byte for 500ms; seeing the header
		// block well before that proves the proxy flushes headers on their own
		// rather than only when they can piggyback on the first body write.
		expect(headersArrivedAfterMs).toBeLessThan(300);
		await vi.waitFor(() => expect(upstreamClosed).toBe(true), { timeout: 1_000 });
	});

	it("delivers SSE chunks as they are written, not on close", async () => {
		upstream = createServer((_req, res) => {
			res.writeHead(200, { "content-type": "text/event-stream" });
			res.write("data: first\n\n");
			setTimeout(() => {
				res.write("data: second\n\n");
				res.end();
			}, 500);
		});
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		const port = (upstream.address() as AddressInfo).port;
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const res = await fetch(`${proxy.base}/api/v1/events`);
		const reader = res.body!.getReader();
		const started = Date.now();
		const first = new TextDecoder().decode((await reader.read()).value);
		const firstArrivedAfterMs = Date.now() - started;

		expect(first).toContain("data: first");
		// The second chunk is written 500ms later; receiving the first well before
		// that proves streaming rather than buffer-until-close.
		expect(firstArrivedAfterMs).toBeLessThan(300);
		let rest = "";
		for (;;) {
			const { done, value } = await reader.read();
			if (done) break;
			rest += new TextDecoder().decode(value);
		}
		expect(rest).toContain("data: second");
	});

	it("tunnels a WebSocket upgrade with the credential injected", async () => {
		const sawAuth: Array<string | undefined> = [];
		upstream = createServer();
		upstream.on("upgrade", (req, socket) => {
			sawAuth.push(req.headers.authorization);
			socket.write("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n");
			socket.on("data", (d) => socket.write(d)); // echo frames back verbatim
			// Upgraded sockets are half-open by default and would hold close() open.
			socket.on("end", () => socket.destroy());
		});
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		const port = (upstream.address() as AddressInfo).port;
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const proxyUrl = new URL(proxy.base);
		const received: Buffer[] = [];
		const socket = netConnect(Number(proxyUrl.port), "127.0.0.1");
		await new Promise<void>((resolve) => socket.on("connect", () => resolve()));
		socket.on("data", (d) => received.push(d));
		socket.write(
			`GET ${proxyUrl.pathname}/mux HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGVzdA==\r\nSec-WebSocket-Version: 13\r\n\r\n`,
		);
		await new Promise((resolve) => setTimeout(resolve, 200));
		socket.write("payload-bytes");
		await new Promise((resolve) => setTimeout(resolve, 200));
		socket.destroy();

		const all = Buffer.concat(received).toString();
		expect(all).toContain("101 Switching Protocols");
		expect(all).toContain("payload-bytes"); // echoed through both pipes
		expect(sawAuth).toEqual(["Bearer pw"]);
	});

	it("destroys an upgrade that carries no token", async () => {
		const sawUpgrade: string[] = [];
		upstream = createServer();
		upstream.on("upgrade", (upgraded) => sawUpgrade.push(upgraded.url ?? ""));
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		const port = (upstream.address() as AddressInfo).port;
		proxy = await startRemoteProxy({
			label: "workbox",
			url: `http://127.0.0.1:${port}`,
			password: "pw",
		});

		const proxyUrl = new URL(proxy.base);
		const socket = netConnect(Number(proxyUrl.port), "127.0.0.1");
		await new Promise<void>((resolve) => socket.on("connect", () => resolve()));
		const closed = new Promise<void>((resolve) => socket.on("close", () => resolve()));
		socket.on("error", () => undefined);
		socket.write(
			"GET /mux HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGVzdA==\r\nSec-WebSocket-Version: 13\r\n\r\n",
		);
		await closed;
		expect(sawUpgrade).toEqual([]);
	});
});

describe("startRemoteProxy over HTTP/2", () => {
	let h2upstream: Http2Server | undefined;

	afterEach(async () => {
		await new Promise<void>((resolve) => (h2upstream ? h2upstream.close(() => resolve()) : resolve()));
		h2upstream = undefined;
	});

	// A daemon whose listener speaks HTTP/2 without TLS, as AO's remote listener does.
	async function startHttp2Upstream(hostId: string): Promise<{ port: number; seen: Seen[]; sessions: ServerHttp2Session[] }> {
		const seen: Seen[] = [];
		const sessions: ServerHttp2Session[] = [];
		h2upstream = createHttp2Server();
		h2upstream.on("session", (session) => sessions.push(session));
		h2upstream.on("request", (req, res) => {
			seen.push({
				url: req.url,
				auth: req.headers.authorization,
				expectedHost: req.headers["x-ao-expected-host-id"] as string | undefined,
				origin: req.headers.origin,
				appAuth: req.headers["x-ao-preview-app-authorization"] as string | undefined,
				appOrigin: req.headers["x-ao-preview-app-origin"] as string | undefined,
				body: "",
			});
			res.writeHead(200, { "content-type": "application/json" });
			res.end(req.url === "/api/v1/identity" ? JSON.stringify({ hostId, apiVersion: 1 }) : JSON.stringify({ url: req.url, proto: req.httpVersion }));
		});
		await new Promise<void>((resolve) => h2upstream?.listen(0, "127.0.0.1", resolve));
		return { port: (h2upstream.address() as AddressInfo).port, seen, sessions };
	}

	it("serves an app preview to the browser over HTTP/2", async () => {
		const { port, seen } = await startUpstream(() => ({ status: 200, body: "module" }));
		proxy = await startRemoteProxy({ label: "workbox", url: `http://127.0.0.1:${port}`, password: "pw" });
		const preview = new URL(proxy.previewUrl("ao-1", "http://localhost:5173/src/main.ts"));
		expect(preview.protocol).toBe("https:");
		const client = http2Connect(`https://127.0.0.1:${preview.port}`, trustPreview(preview));
		const get = (path: string) => new Promise<string>((resolve, reject) => {
			const stream = client.request({ ":path": path, ":authority": preview.host });
			let body = "";
			stream.on("data", (chunk) => (body += chunk));
			stream.on("end", () => resolve(body));
			stream.on("error", reject);
		});
		// Many module requests at once, on one connection.
		const bodies = await Promise.all(Array.from({ length: 20 }, (_, i) => get(`/src/m${i}.ts`)));
		client.close();
		expect(client.alpnProtocol).toBe("h2");
		expect(bodies.every((body) => body === "module")).toBe(true);
		expect(seen).toHaveLength(20);
		expect(seen.every((entry) => entry.auth === "Bearer pw" && entry.url.startsWith("/api/v1/sessions/ao-1/preview/app/src/m"))).toBe(true);
	});

	it("reads a request target starting with two slashes as a path, not a host", async () => {
		const { port, seen } = await startUpstream(() => ({ status: 200, body: "ok" }));
		proxy = await startRemoteProxy({ label: "workbox", url: `http://127.0.0.1:${port}`, password: "pw" });
		const preview = new URL(proxy.previewUrl("ao-1", "http://localhost:5173/"));
		const client = http2Connect(`https://127.0.0.1:${preview.port}`, trustPreview(preview));
		const status = (path: string) => new Promise<number>((resolve, reject) => {
			const stream = client.request({ ":path": path, ":authority": preview.host });
			stream.on("response", (headers) => resolve(Number(headers[":status"])));
			stream.on("error", reject);
			stream.resume();
		});
		expect(await status("//")).toBe(200);
		expect(await status("//:x")).toBe(200);
		client.close();
		expect(seen.map((entry) => entry.url)).toEqual(["/api/v1/sessions/ao-1/preview/app/", "/api/v1/sessions/ao-1/preview/app/:x"]);
		// An upgrade it cannot serve is dropped, not thrown on.
		const artifact = new URL(proxy.previewUrl("ao-1", `http://ao-preview-artifact.mfxs2mi.localhost:${port}/index.html`));
		const socket = netConnect(Number(new URL(proxy.base).port), "127.0.0.1");
		socket.on("error", () => undefined);
		const closed = new Promise<void>((resolve) => socket.once("close", () => resolve()));
		socket.write(`GET // HTTP/1.1\r\nHost: ${artifact.host}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGVzdA==\r\nSec-WebSocket-Version: 13\r\n\r\n`);
		await closed;
		expect(seen).toHaveLength(2);
	});

	it("verifies the host once per upstream session, before the credential", async () => {
		const { port, seen, sessions } = await startHttp2Upstream("h_workbox");
		proxy = await startRemoteProxy({ hostId: "h_workbox", label: "workbox", url: `http://127.0.0.1:${port}`, password: "pw" });
		const responses = await Promise.all(Array.from({ length: 20 }, (_, i) => fetch(`${proxy!.base}/api/v1/projects?i=${i}`).then((res) => res.json())));
		expect(responses.every((body) => body.proto === "2.0")).toBe(true);
		expect(sessions).toHaveLength(1);
		expect(seen[0]).toMatchObject({ url: "/api/v1/identity", auth: undefined });
		expect(seen.filter((entry) => entry.url === "/api/v1/identity")).toHaveLength(1);
		expect(seen.slice(1).every((entry) => entry.auth === "Bearer pw" && entry.expectedHost === "h_workbox")).toBe(true);
		expect(seen).toHaveLength(21);
	});

	it("never sends the credential over a session that reached another host", async () => {
		const { port, seen } = await startHttp2Upstream("h_replacement");
		proxy = await startRemoteProxy({ hostId: "h_expected", label: "workbox", url: `http://127.0.0.1:${port}`, password: "secret" });
		const response = await fetch(`${proxy.base}/api/v1/projects`);
		expect(response.status).toBe(502);
		expect(await response.json()).toMatchObject({ code: "HOST_IDENTITY_UNVERIFIED" });
		expect(seen.map((entry) => entry.url)).toEqual(["/api/v1/identity"]);
		expect(seen[0].auth).toBeUndefined();
	});
});

describe("startRemoteProxy through an HTTPS host's tunnel", () => {
	let secureUpstream: Http2SecureServer | undefined;
	let rejectUnauthorized: string | undefined;

	beforeEach(() => {
		// The test host's certificate is self-signed.
		rejectUnauthorized = process.env.NODE_TLS_REJECT_UNAUTHORIZED;
		process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
	});

	afterEach(async () => {
		if (rejectUnauthorized === undefined) delete process.env.NODE_TLS_REJECT_UNAUTHORIZED;
		else process.env.NODE_TLS_REJECT_UNAUTHORIZED = rejectUnauthorized;
		await proxy?.close();
		proxy = undefined;
		await new Promise<void>((resolve) => (secureUpstream ? secureUpstream.close(() => resolve()) : resolve()));
		secureUpstream = undefined;
	});

	// An HTTPS daemon (as behind a Cloudflare tunnel). With tunnel=true it serves
	// HTTP/2 inside a WebSocket at /api/v1/tunnel, as the AO daemon does. With a
	// prefix it sits behind a reverse proxy that serves it there, stripped; the
	// tunnel still ends at the daemon itself.
	async function startSecureUpstream(tunnel: boolean, prefix = ""): Promise<{ port: number; direct: string[]; tunneled: Seen[]; tunnels: number }> {
		const state = { port: 0, direct: [] as string[], tunneled: [] as Seen[], tunnels: 0 };
		const identity = JSON.stringify({ hostId: "h_workbox", apiVersion: 1 });
		const inner = createHttp2Server((req, res) => {
			state.tunneled.push({ url: req.url, auth: req.headers.authorization, expectedHost: undefined, origin: undefined, appAuth: undefined, appOrigin: undefined, body: req.httpVersion });
			res.end(req.url === "/api/v1/identity" ? identity : JSON.stringify({ proto: req.httpVersion }));
		});
		const sockets = new WebSocketServer({ noServer: true });
		const { cert, key } = previewCertificate();
		const unprefixed = (url = "") => (url.startsWith(`${prefix}/`) ? url.slice(prefix.length) : "");
		secureUpstream = createSecureServer({ cert, key, allowHTTP1: true }, (req, res) => {
			state.direct.push(`${req.httpVersion} ${req.url}`);
			res.end(unprefixed(req.url) === "/api/v1/identity" ? identity : JSON.stringify({ proto: req.httpVersion }));
		});
		secureUpstream.on("upgrade", (req: IncomingMessage, socket, head) => {
			if (!tunnel || unprefixed(req.url) !== "/api/v1/tunnel" || req.headers.authorization !== "Bearer pw") {
				socket.end("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n");
				return;
			}
			state.tunnels++;
			sockets.handleUpgrade(req, socket, head, (ws) => inner.emit("connection", createWebSocketStream(ws)));
		});
		await new Promise<void>((resolve) => secureUpstream?.listen(0, "127.0.0.1", resolve));
		state.port = (secureUpstream.address() as AddressInfo).port;
		return state;
	}

	it("carries requests inside one WebSocket, proving the host before the credential", async () => {
		const host = await startSecureUpstream(true);
		proxy = await startRemoteProxy({ hostId: "h_workbox", label: "workbox", url: `https://127.0.0.1:${host.port}`, password: "pw" });
		const responses = await Promise.all(Array.from({ length: 20 }, (_, i) => fetch(`${proxy!.base}/api/v1/projects?i=${i}`).then((res) => res.json())));
		expect(responses.every((body) => body.proto === "2.0")).toBe(true);
		expect(host.tunnels).toBe(1);
		// The only direct request is the unauthenticated identity probe that precedes the upgrade.
		expect(host.direct.map((line) => line.split(" ")[1])).toEqual(["/api/v1/identity"]);
		expect(host.tunneled[0]).toMatchObject({ url: "/api/v1/identity", auth: undefined });
		expect(host.tunneled.slice(1).every((entry) => entry.auth === "Bearer pw")).toBe(true);
		expect(host.tunneled).toHaveLength(21);
	});

	it("falls back to plain HTTP/2 when the host has no tunnel", async () => {
		const host = await startSecureUpstream(false);
		proxy = await startRemoteProxy({ hostId: "h_workbox", label: "workbox", url: `https://127.0.0.1:${host.port}`, password: "pw" });
		const body = await (await fetch(`${proxy.base}/api/v1/projects`)).json();
		expect(body.proto).toBe("2.0");
		expect(host.tunnels).toBe(0);
		expect(host.direct.at(-1)).toBe("2.0 /api/v1/projects");
	});

	it("keeps a host's path prefix to its reverse proxy and drops it inside the tunnel", async () => {
		const host = await startSecureUpstream(true, "/ao");
		proxy = await startRemoteProxy({ hostId: "h_workbox", label: "workbox", url: `https://127.0.0.1:${host.port}/ao`, password: "pw" });
		expect(await (await fetch(`${proxy.base}/api/v1/projects`)).json()).toEqual({ proto: "2.0" });
		expect(host.tunnels).toBe(1);
		expect(host.direct.map((line) => line.split(" ")[1])).toEqual(["/ao/api/v1/identity"]);
		expect(host.tunneled.map((entry) => entry.url)).toEqual(["/api/v1/identity", "/api/v1/projects"]);
	});

	it("keeps a host's path prefix over plain HTTP/2 to its reverse proxy", async () => {
		const host = await startSecureUpstream(false, "/ao");
		proxy = await startRemoteProxy({ hostId: "h_workbox", label: "workbox", url: `https://127.0.0.1:${host.port}/ao`, password: "pw" });
		expect(await (await fetch(`${proxy.base}/api/v1/projects`)).json()).toEqual({ proto: "2.0" });
		expect(host.direct.at(-1)).toBe("2.0 /ao/api/v1/projects");
	});
});

describe("startRemoteProxy when the upstream fails", () => {
	const identity = JSON.stringify({ hostId: "h_workbox", apiVersion: 1 });
	const daemonEntry = (url: string) => ({ hostId: "h_workbox", label: "workbox", url, password: "pw" });
	// Short deadlines so a dead path shows within a test.
	const fast = { openMs: 1_000, pingMs: 200 };
	const cleanups: Array<() => unknown> = [];
	let rejectUnauthorized: string | undefined;

	beforeEach(() => {
		// The HTTPS test hosts' certificate is self-signed.
		rejectUnauthorized = process.env.NODE_TLS_REJECT_UNAUTHORIZED;
		process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
	});

	afterEach(async () => {
		await proxy?.close();
		proxy = undefined;
		for (const cleanup of cleanups.splice(0).reverse()) await cleanup();
		if (rejectUnauthorized === undefined) delete process.env.NODE_TLS_REJECT_UNAUTHORIZED;
		else process.env.NODE_TLS_REJECT_UNAUTHORIZED = rejectUnauthorized;
	});

	// Settles as the promise does, or with "hung" if it has not settled in time.
	const within = <T>(promise: Promise<T>, ms = 2_000): Promise<T | "hung"> =>
		Promise.race([promise, new Promise<"hung">((resolve) => setTimeout(() => resolve("hung"), ms))]);

	type DaemonHandler = (req: Http2ServerRequest, res: Http2ServerResponse, drop: () => void) => void;

	// AO's remote listener speaks HTTP/2 without TLS. drop() closes every
	// connection to it, as a daemon restart does; onIdentity decides when the
	// identity probe is answered.
	async function startH2Daemon(handler: DaemonHandler, onIdentity = (answer: () => void) => answer()): Promise<number> {
		const sockets = new Set<Socket>();
		const drop = () => { for (const socket of sockets) socket.destroy(); };
		const server = createHttp2Server((req, res) => {
			if (req.url !== "/api/v1/identity") return handler(req, res, drop);
			onIdentity(() => res.end(identity));
		});
		server.on("connection", (socket: Socket) => {
			sockets.add(socket);
			socket.once("close", () => sockets.delete(socket));
		});
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		cleanups.push(() => {
			drop();
			return new Promise<void>((resolve) => server.close(() => resolve()));
		});
		return (server.address() as AddressInfo).port;
	}

	// An HTTPS daemon carrying HTTP/2 inside a WebSocket at /api/v1/tunnel, as
	// AO's does behind a Cloudflare tunnel. drop() closes every tunnel; direct
	// requests answer {direct}.
	// holdMs sends what the daemon wrote within that time as one tunnel
	// message, as the Cloudflare edge passes a small answer on.
	async function startTunnelDaemon(handler: DaemonHandler, options: { failUpgrades?: number; answerUpgrades?: boolean; holdMs?: number } = {}) {
		const tunnels = new Set<WebSocket>();
		const drop = () => { for (const tunnel of tunnels) tunnel.terminate(); };
		const inner = createHttp2Server((req, res) => (req.url === "/api/v1/identity" ? void res.end(identity) : handler(req, res, drop)));
		const sockets = new WebSocketServer({ noServer: true });
		const { cert, key } = previewCertificate();
		const host = createSecureServer({ cert, key, allowHTTP1: true }, (req, res) => {
			res.end(req.url === "/api/v1/identity" ? identity : JSON.stringify({ direct: req.httpVersion }));
		});
		const connections = new Set<Socket>();
		const directSessions = new Set<ServerHttp2Session>();
		host.on("connection", (socket: Socket) => {
			connections.add(socket);
			socket.once("close", () => connections.delete(socket));
		});
		host.on("session", (session) => directSessions.add(session));
		const state = { port: 0, upgrades: 0, dropDirect: () => { for (const session of directSessions) session.destroy(); } };
		host.on("upgrade", (req: IncomingMessage, socket: Socket, head: Buffer) => {
			state.upgrades++;
			if (options.answerUpgrades === false) return;
			if (state.upgrades <= (options.failUpgrades ?? 0)) return void socket.end("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n");
			sockets.handleUpgrade(req, socket, head, (ws) => {
				tunnels.add(ws);
				ws.once("close", () => tunnels.delete(ws));
				if (options.holdMs) holdSends(ws, options.holdMs);
				const stream = createWebSocketStream(ws);
				stream.on("error", () => undefined);
				inner.emit("connection", stream);
			});
		});
		await new Promise<void>((resolve) => host.listen(0, "127.0.0.1", resolve));
		state.port = (host.address() as AddressInfo).port;
		cleanups.push(() => {
			drop();
			for (const socket of connections) socket.destroy();
			return new Promise<void>((resolve) => host.close(() => resolve()));
		});
		return state;
	}

	function holdSends(ws: WebSocket, holdMs: number): void {
		const send = ws.send.bind(ws);
		let held: Buffer[] = [];
		let timer: ReturnType<typeof setTimeout> | undefined;
		ws.send = ((data: Buffer, callback?: (error?: Error) => void) => {
			held.push(Buffer.from(data));
			callback?.();
			timer ??= setTimeout(() => {
				timer = undefined;
				send(Buffer.concat(held.splice(0)));
			}, holdMs);
		}) as typeof ws.send;
	}

	// A TCP relay to a daemon. silence() makes every connection it carries go
	// quiet with no FIN or RST, as a network change does; the first quietFirst
	// connections are quiet from the start.
	async function startRelay(target: number, quietFirst = 0) {
		const links = new Set<{ quiet: boolean; ends: Socket[] }>();
		let accepted = 0;
		const relay = createNetServer((client) => {
			const daemon = netConnect(target, "127.0.0.1");
			const link = { quiet: accepted++ < quietFirst, ends: [client, daemon] };
			links.add(link);
			client.on("data", (chunk) => { if (!link.quiet) daemon.write(chunk); });
			daemon.on("data", (chunk) => { if (!link.quiet) client.write(chunk); });
			for (const end of link.ends) {
				end.on("error", () => undefined);
				end.once("close", () => {
					links.delete(link);
					for (const other of link.ends) other.destroy();
				});
			}
		});
		await new Promise<void>((resolve) => relay.listen(0, "127.0.0.1", resolve));
		cleanups.push(() => {
			for (const link of links) for (const end of link.ends) end.destroy();
			return new Promise<void>((resolve) => relay.close(() => resolve()));
		});
		return { port: (relay.address() as AddressInfo).port, silence: () => { for (const link of links) link.quiet = true; } };
	}

	// A TCP relay to a daemon that holds the daemon's answers to pings back for
	// holdMs, as a slow uplink does: there a ping waits behind the data queued
	// ahead of it. Every other frame goes straight through.
	async function startPongHoldingRelay(target: number, holdMs: number) {
		const relay = createNetServer((client) => {
			const daemon = netConnect(target, "127.0.0.1");
			let frames = Buffer.alloc(0);
			client.pipe(daemon);
			daemon.on("data", (chunk: Buffer) => {
				frames = Buffer.concat([frames, chunk]);
				while (frames.length >= 9 && frames.length >= 9 + frames.readUIntBE(0, 3)) {
					const frame = frames.subarray(0, 9 + frames.readUIntBE(0, 3));
					frames = frames.subarray(frame.length);
					// A PING frame with its ACK flag set.
					if (frame[3] === 6 && frame[4] & 1) setTimeout(() => client.write(frame), holdMs);
					else client.write(frame);
				}
			});
			for (const end of [client, daemon]) {
				end.on("error", () => undefined);
				end.once("close", () => { client.destroy(); daemon.destroy(); });
			}
		});
		await new Promise<void>((resolve) => relay.listen(0, "127.0.0.1", resolve));
		cleanups.push(() => new Promise<void>((resolve) => relay.close(() => resolve())));
		return (relay.address() as AddressInfo).port;
	}

	// The browser panel's side of a preview: an HTTP/2 connection to the proxy.
	function previewBrowser(active: ActiveProxy) {
		const preview = new URL(active.previewUrl("ao-1", "http://localhost:5173/"));
		const browser = http2Connect(`https://127.0.0.1:${preview.port}`, trustPreview(preview));
		browser.on("error", () => undefined);
		cleanups.push(() => browser.destroy());
		return (path: string) => {
			const stream = browser.request({ ":path": path, ":authority": preview.host });
			stream.on("error", () => undefined);
			return stream;
		};
	}

	it("answers 502 when the host's connection closes before a response, then reconnects", async () => {
		const port = await startH2Daemon((req, res, drop) => {
			if (req.url === "/api/v1/slow") return void setTimeout(drop, 50);
			res.end(JSON.stringify({ proto: req.httpVersion }));
		});
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${port}`));
		expect(await (await fetch(`${proxy.base}/api/v1/projects`)).json()).toEqual({ proto: "2.0" });
		const dropped = await within(fetch(`${proxy.base}/api/v1/slow`));
		expect(dropped === "hung" ? "hung" : dropped.status).toBe(502);
		expect(await (await fetch(`${proxy.base}/api/v1/projects`)).json()).toEqual({ proto: "2.0" });
	});

	it("fails a body the host's connection cut off instead of ending it as complete", async () => {
		const port = await startH2Daemon((req, res, drop) => {
			res.writeHead(200, { "content-type": "text/javascript", ...(req.url.endsWith("?sized") ? { "content-length": "20000" } : {}) });
			res.write("x".repeat(5_000));
			setTimeout(drop, 50);
		});
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${port}`));
		for (const path of ["/src/main.js", "/src/main.js?sized"]) {
			const response = await fetch(`${proxy.base}${path}`);
			expect(response.status).toBe(200);
			await expect(within(response.text())).rejects.toThrow();
		}
	});

	it("relays a small body whose tunnelled stream closed before its end", async () => {
		// Through the tunnel, a small answer's stream closes with NO_ERROR before
		// its body ends. Once the request has gone out, as it has over any real
		// network, Node destroys that stream before "end": the body is complete.
		const host = await startTunnelDaemon((_req, res) => void setTimeout(() => res.end('{"ok":true}'), 30), { holdMs: 10 });
		proxy = await startRemoteProxy(daemonEntry(`https://127.0.0.1:${host.port}`));
		for (let attempt = 0; attempt < 3; attempt++) {
			const response = await fetch(`${proxy.base}/api/v1/projects`);
			expect(response.status).toBe(200);
			expect(await within(response.json())).toEqual({ ok: true });
		}
		expect(host.upgrades).toBe(1);
	});

	it("fails a body whose host destroyed its session mid-body", async () => {
		// A destroyed session ends its streams with code 0 and no reset: only the
		// destroyed session tells this cut from a finished body.
		const port = await startH2Daemon((_req, res) => {
			res.writeHead(200, { "content-type": "text/javascript" });
			res.write("x".repeat(5_000));
			setTimeout(() => res.stream.session?.destroy(), 50);
		});
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${port}`));
		const response = await fetch(`${proxy.base}/src/main.js`);
		expect(response.status).toBe(200);
		await expect(within(response.text())).rejects.toThrow();
	});

	it("answers 502 or fails the body when the tunnel closes, then opens a new tunnel", async () => {
		const host = await startTunnelDaemon((req, res, drop) => {
			if (req.url === "/api/v1/slow") return void setTimeout(drop, 50);
			if (req.url === "/api/v1/cut") {
				res.writeHead(200);
				res.write("x".repeat(5_000));
				return void setTimeout(drop, 50);
			}
			res.end(JSON.stringify({ proto: req.httpVersion }));
		});
		proxy = await startRemoteProxy(daemonEntry(`https://127.0.0.1:${host.port}`));
		expect(await (await fetch(`${proxy.base}/api/v1/projects`)).json()).toEqual({ proto: "2.0" });
		const dropped = await within(fetch(`${proxy.base}/api/v1/slow`));
		expect(dropped === "hung" ? "hung" : dropped.status).toBe(502);
		const cut = await fetch(`${proxy.base}/api/v1/cut`);
		await expect(within(cut.text())).rejects.toThrow();
		expect(await (await fetch(`${proxy.base}/api/v1/projects`)).json()).toEqual({ proto: "2.0" });
		expect(host.upgrades).toBe(3);
	});

	it("cancels the upstream stream when the browser cancels a preview stream", async () => {
		let open = 0;
		const port = await startH2Daemon((_req, res) => {
			open++;
			res.once("close", () => open--);
			res.writeHead(200, { "content-type": "text/event-stream" });
			res.write(": ready\n\n");
		});
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${port}`));
		const events = previewBrowser(proxy)("/events");
		await new Promise((resolve) => events.once("data", resolve));
		expect(open).toBe(1);
		events.close(http2Constants.NGHTTP2_CANCEL);
		await vi.waitFor(() => expect(open).toBe(0));
	});

	it("cancels the upstream request when the browser cancels a preview stream a HTTP/1.1 host serves", async () => {
		let open = 0;
		upstream = createServer((req, res) => {
			if (req.url === "/api/v1/identity") return void res.end(identity);
			open++;
			res.once("close", () => open--);
			res.writeHead(200, { "content-type": "text/event-stream" });
			res.write(": ready\n\n");
		});
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${(upstream.address() as AddressInfo).port}`));
		const events = previewBrowser(proxy)("/events");
		await new Promise((resolve) => events.once("data", resolve));
		expect(open).toBe(1);
		events.close(http2Constants.NGHTTP2_CANCEL);
		await vi.waitFor(() => expect(open).toBe(0));
	});

	it("forwards nothing for a browser that left while the host was being verified", async () => {
		const seen: string[] = [];
		let answerIdentity!: () => void;
		let identityAsked!: () => void;
		const verifying = new Promise<void>((resolve) => { identityAsked = resolve; });
		const port = await startH2Daemon((req, res) => {
			seen.push(req.url);
			res.end("{}");
		}, (answer) => {
			answerIdentity = answer;
			identityAsked();
		});
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${port}`));
		const request = previewBrowser(proxy)("/src/main.ts");
		// The proxy holds the request while it verifies the host...
		await verifying;
		request.close(http2Constants.NGHTTP2_CANCEL);
		// ...and the browser's reset reaches it before the host answers.
		await new Promise((resolve) => setTimeout(resolve, 200));
		answerIdentity();
		await new Promise((resolve) => setTimeout(resolve, 300));
		expect(seen).toEqual([]);
	});

	it("retires a session whose host stopped answering pings", async () => {
		const port = await startH2Daemon((req, res) => res.end(JSON.stringify({ proto: req.httpVersion })));
		const relay = await startRelay(port);
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${relay.port}`), undefined, fast);
		expect((await fetch(`${proxy.base}/api/v1/projects`)).status).toBe(200);
		relay.silence();
		const stalled = await within(fetch(`${proxy.base}/api/v1/projects`));
		expect(stalled === "hung" ? "hung" : stalled.status).toBe(502);
		// A fresh session, over a fresh connection.
		expect((await fetch(`${proxy.base}/api/v1/projects`)).status).toBe(200);
	});

	it("forwards a DELETE request's body over HTTP/2", async () => {
		const seen: Array<{ method: string; body: string; endAfterHeaders: boolean }> = [];
		const port = await startH2Daemon((req, res) => {
			let body = "";
			req.on("data", (chunk) => (body += chunk));
			req.on("end", () => {
				seen.push({ method: req.method, body, endAfterHeaders: req.stream.endAfterHeaders });
				res.end("{}");
			});
		});
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${port}`));
		const items = `${proxy.base}/api/v1/sessions/ao-1/preview/app/api/items`;
		expect((await fetch(items, { method: "DELETE", headers: { "content-type": "application/json" }, body: '{"ids":["a"]}' })).status).toBe(200);
		expect((await fetch(items, { method: "DELETE" })).status).toBe(200);
		expect((await fetch(items)).status).toBe(200);
		expect(seen).toEqual([
			{ method: "DELETE", body: '{"ids":["a"]}', endAfterHeaders: false },
			// Without a body a request still ends with its headers.
			{ method: "DELETE", body: "", endAfterHeaders: true },
			{ method: "GET", body: "", endAfterHeaders: true },
		]);
	});

	it("falls back to HTTP/1.1 within its deadline when an HTTP/2 session cannot open", async () => {
		upstream = createServer((req, res) => res.end(req.url === "/api/v1/identity" ? identity : '{"proto":"1.1"}'));
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		// The first connection, the HTTP/2 attempt, never hears back.
		const relay = await startRelay((upstream.address() as AddressInfo).port, 1);
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${relay.port}`), undefined, { openMs: 200, pingMs: 10_000 });
		const response = await within(fetch(`${proxy.base}/api/v1/projects`));
		expect(response === "hung" ? "hung" : await response.json()).toEqual({ proto: "1.1" });
	});

	it("gives up on a tunnel upgrade nobody answers and goes direct", async () => {
		const host = await startTunnelDaemon(() => undefined, { answerUpgrades: false });
		proxy = await startRemoteProxy(daemonEntry(`https://127.0.0.1:${host.port}`), undefined, { openMs: 200, pingMs: 10_000 });
		const response = await within(fetch(`${proxy.base}/api/v1/projects`));
		// Direct, over HTTP/2 or (on a slow machine, past the deadline) HTTP/1.1.
		expect(response === "hung" ? "hung" : await response.json()).toEqual({ direct: expect.any(String) });
	});

	it("tries the tunnel again when a later session opens after its retry window", async () => {
		const host = await startTunnelDaemon((req, res) => res.end(JSON.stringify({ proto: req.httpVersion })), { failUpgrades: 1 });
		proxy = await startRemoteProxy(daemonEntry(`https://127.0.0.1:${host.port}`));
		expect(await (await fetch(`${proxy.base}/api/v1/projects`)).json()).toEqual({ direct: "2.0" });
		host.dropDirect();
		await new Promise((resolve) => setTimeout(resolve, 100));
		const now = Date.now;
		vi.spyOn(Date, "now").mockImplementation(() => now() + 61_000);
		expect(await (await fetch(`${proxy.base}/api/v1/projects`)).json()).toEqual({ proto: "2.0" });
		expect(host.upgrades).toBe(2);
	});

	// How the browser panel saw a preview stream end: 0 (NO_ERROR) reads as a complete body.
	const rstCodeOf = (stream: ReturnType<ReturnType<typeof previewBrowser>>) => new Promise<number>((resolve) => {
		stream.resume();
		stream.on("close", () => resolve(stream.rstCode));
	});

	it("resets a preview stream whose body the host cut off, so the browser cannot take it as complete", async () => {
		const port = await startH2Daemon((req, res, drop) => {
			res.writeHead(200, { "content-type": "text/javascript", "content-length": "20000" });
			res.write("x".repeat(5_000));
			setTimeout(() => (req.url.endsWith("/reset.js") ? res.stream.close(http2Constants.NGHTTP2_INTERNAL_ERROR) : drop()), 50);
		});
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${port}`));
		const request = previewBrowser(proxy);
		// Reset by the host, then cut with its connection (that stream just ends, with code 0).
		expect(await rstCodeOf(request("/src/reset.js"))).not.toBe(http2Constants.NGHTTP2_NO_ERROR);
		expect(await rstCodeOf(request("/src/drop.js"))).not.toBe(http2Constants.NGHTTP2_NO_ERROR);
	});

	it("resets a preview stream whose HTTP/1.1 host cut its body off", async () => {
		upstream = createServer((req, res) => {
			if (req.url === "/api/v1/identity") return void res.end(identity);
			res.writeHead(200, { "content-type": "text/javascript", "content-length": "20000" });
			res.write("x".repeat(5_000));
			setTimeout(() => res.destroy(), 50);
		});
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${(upstream.address() as AddressInfo).port}`));
		expect(await rstCodeOf(previewBrowser(proxy)("/src/main.js"))).not.toBe(http2Constants.NGHTTP2_NO_ERROR);
	});

	it("answers 502 for a status HTTP/2 cannot carry, and drops connection-specific headers", async () => {
		upstream = createServer((req, res) => {
			if (req.url === "/api/v1/identity") return void res.end(identity);
			if (req.url?.endsWith("/odd")) return void res.writeHead(999).end("odd");
			res.writeHead(200, { te: "gzip", "http2-settings": "AAMAAABk", "x-app": "kept" }).end("ok");
		});
		await new Promise<void>((resolve) => upstream?.listen(0, "127.0.0.1", resolve));
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${(upstream.address() as AddressInfo).port}`));
		const request = previewBrowser(proxy);
		const head = (path: string) => new Promise<Record<string, unknown>>((resolve) => request(path).once("response", resolve));
		expect(await head("/odd")).toMatchObject({ ":status": 502 });
		const ok = await head("/");
		expect(ok).toMatchObject({ ":status": 200, "x-app": "kept" });
		expect(ok).not.toHaveProperty("te");
		expect(ok).not.toHaveProperty("http2-settings");
	});

	it("keeps slots for the renderer's own requests while a preview's requests hang", async () => {
		let hung = 0;
		const port = await startH2Daemon((req, res) => {
			// A dev server that never answers; the daemon sets no deadline on it.
			if (req.url.includes("/preview/app/")) return void hung++;
			res.end("{}");
		});
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${port}`));
		const request = previewBrowser(proxy);
		for (let i = 0; i < 100; i++) request(`/src/m${i}.ts`);
		await vi.waitFor(() => expect(hung).toBeGreaterThanOrEqual(80));
		const response = await within(fetch(`${proxy.base}/api/v1/sessions`));
		expect(response === "hung" ? "hung" : response.status).toBe(200);
	});

	it("sends requests that waited past the host's GOAWAY on a new session", async () => {
		const held: Http2ServerResponse[] = [];
		const sessions = new Set<unknown>();
		let closing = false;
		const port = await startH2Daemon((req, res) => {
			sessions.add(req.stream.session);
			if (closing) return void res.end("{}");
			held.push(res);
		});
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${port}`));
		const request = previewBrowser(proxy);
		const statuses = Array.from({ length: 110 }, (_, i) => new Promise<number>((resolve) => {
			const stream = request(`/src/m${i}.ts`);
			let status = 0;
			stream.on("response", (headers) => { status = Number(headers[":status"]); });
			stream.resume();
			stream.on("close", () => resolve(status));
		}));
		// The host holds every request the proxy lets through; the rest wait for a slot.
		await vi.waitFor(() => expect(held.length).toBeGreaterThanOrEqual(80));
		await new Promise((resolve) => setTimeout(resolve, 100));
		// A graceful GOAWAY: the requests already sent finish, no new ones start.
		closing = true;
		held[0].stream.session?.close();
		for (const res of held) res.end("{}");
		expect(new Set(await Promise.all(statuses))).toEqual(new Set([200]));
		expect(sessions.size).toBe(2);
	});

	it("sends a request without a body again, once, when the host refused its stream", async () => {
		const attempts: string[] = [];
		const port = await startH2Daemon((req, res) => {
			const attempt = `${req.method} ${req.url}`;
			attempts.push(attempt);
			// A refused stream was never acted on (RFC 9113 §8.7).
			if (req.url.endsWith("/busy") || attempts.filter((line) => line === attempt).length === 1)
				return void res.stream.close(http2Constants.NGHTTP2_REFUSED_STREAM);
			res.end("{}");
		});
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${port}`));
		expect((await fetch(`${proxy.base}/api/v1/projects`)).status).toBe(200);
		expect((await fetch(`${proxy.base}/api/v1/busy`)).status).toBe(502);
		expect((await fetch(`${proxy.base}/api/v1/projects`, { method: "POST", body: "{}" })).status).toBe(502);
		expect(attempts).toEqual(["GET /api/v1/projects", "GET /api/v1/projects", "GET /api/v1/busy", "GET /api/v1/busy", "POST /api/v1/projects"]);
	});

	it("keeps a session whose ping waits behind an upload the host is still taking in", async () => {
		const port = await startH2Daemon((req, res) => {
			let size = 0;
			req.on("data", (chunk: Buffer) => (size += chunk.length));
			req.on("end", () => res.end(JSON.stringify({ size })));
		});
		const relay = await startPongHoldingRelay(port, 1_000);
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${relay}`), undefined, fast);
		// The upload arrives over several ping intervals; the host's window
		// updates for it come back long before the ping's answer.
		const answer = await new Promise<{ status: number; body: string }>((resolve, reject) => {
			const upload = httpRequest(`${proxy!.base}/api/v1/sessions/ao-1/attachments`, { method: "POST" }, (res) => {
				let body = "";
				res.on("data", (chunk) => (body += chunk));
				res.on("end", () => resolve({ status: res.statusCode ?? 0, body }));
			});
			upload.on("error", reject);
			let chunks = 0;
			const timer = setInterval(() => {
				upload.write(Buffer.alloc(32 << 10));
				if (++chunks < 16) return;
				clearInterval(timer);
				upload.end();
			}, 50);
		});
		expect(answer).toEqual({ status: 200, body: JSON.stringify({ size: 512 << 10 }) });
	});

	it("gives no agent tab when the host is unreachable or its agent browser fails", async () => {
		// Nothing listens here: the proxy answers its own 502.
		const vacant = createServer();
		await new Promise<void>((resolve) => vacant.listen(0, "127.0.0.1", resolve));
		const { port } = vacant.address() as AddressInfo;
		await new Promise<void>((resolve) => vacant.close(() => resolve()));
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${port}`));
		expect(await proxy.agentBrowserUrl("ao-1")).toBe("");
		await proxy.close();
		const daemon = await startUpstream((req) => (req.url === "/api/v1/identity" ? { status: 200, body: identity } : { status: 502, body: "Agent browser unavailable" }));
		proxy = await startRemoteProxy(daemonEntry(`http://127.0.0.1:${daemon.port}`));
		expect(await proxy.agentBrowserUrl("ao-1")).toBe("");
	});
});
