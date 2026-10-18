import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mkdtemp, rm, stat } from "node:fs/promises";
import { createServer, type IncomingMessage, type Server } from "node:http";
import { request as httpsRequest } from "node:https";
import type { AddressInfo } from "node:net";
import os from "node:os";
import path from "node:path";
import { connect as tlsConnect, type ConnectionOptions } from "node:tls";
import { WebSocket, WebSocketServer } from "ws";
import { previewCertificate } from "./preview-certificate";

const token = vi.hoisted(() => ({ value: "cloud-token" as string | null }));
// Listens to fail before the real one runs.
const listenFailures = vi.hoisted(() => ({ left: 0 }));
vi.mock("electron", () => ({ ipcMain: {} }));
vi.mock("./cloud-auth", () => ({ getCloudAccessToken: vi.fn(async () => token.value) }));
vi.mock("./agent-viewer", async (importOriginal) => {
	const actual = await importOriginal<typeof import("./agent-viewer")>();
	return {
		...actual,
		listenOnStablePort: vi.fn(async (...args: Parameters<typeof actual.listenOnStablePort>) => {
			if (listenFailures.left-- > 0) throw new Error("could not listen on loopback");
			return actual.listenOnStablePort(...args);
		}),
	};
});

import { createCloudAgentProxy, type CloudAgentProxy } from "./cloud-agent-proxy";

type Seen = { method: string; url: string; auth?: string; cookie?: string; origin?: string; body: string };

let cp: Server | undefined;
let proxy: CloudAgentProxy | undefined;
let dataDir = "";

beforeEach(async () => {
	token.value = "cloud-token";
	listenFailures.left = 0;
	dataDir = await mkdtemp(path.join(os.tmpdir(), "cloud-agent-proxy-"));
	vi.spyOn(console, "warn").mockImplementation(() => undefined);
});

afterEach(async () => {
	vi.restoreAllMocks();
	await proxy?.close();
	cp?.closeAllConnections();
	await new Promise<void>((resolve) => (cp ? cp.close(() => resolve()) : resolve()));
	await rm(dataDir, { recursive: true, force: true });
	cp = undefined;
	proxy = undefined;
});

// A fake control plane on loopback: answers every request with the handler's
// status and records what reached it.
async function startCp(status: (req: IncomingMessage) => number = () => 200, answer = "ok"): Promise<{ baseUrl: string; seen: Seen[] }> {
	const seen: Seen[] = [];
	cp = createServer((req, res) => {
		let body = "";
		req.on("data", (chunk) => (body += chunk));
		req.on("end", () => {
			seen.push({ method: req.method ?? "", url: req.url ?? "", auth: req.headers.authorization, cookie: req.headers.cookie, origin: req.headers.origin, body });
			res.writeHead(status(req), { "content-type": "text/plain", "set-cookie": "cp=1" });
			res.end(answer);
		});
	});
	await new Promise<void>((resolve) => cp?.listen(0, "127.0.0.1", resolve));
	return { baseUrl: `http://127.0.0.1:${(cp.address() as AddressInfo).port}`, seen };
}

const route = (tail: string) => `/api/cloud/v1/orgs/org-1/sessions/ses-1/agent-browser/${tail}`;

const trust = (url: URL): ConnectionOptions => ({ servername: url.hostname, ca: previewCertificate().cert, checkServerIdentity: () => undefined });

function send(agent: URL, method: string, target: string, headers: Record<string, string> = {}, body?: string, host = agent.host) {
	return new Promise<{ status: number; headers: IncomingMessage["headers"] }>((resolve, reject) => {
		const req = httpsRequest({ hostname: "127.0.0.1", port: Number(agent.port), path: target, method, ...trust(agent), headers: { Host: host, ...headers } }, (res) => {
			res.resume();
			res.on("end", () => resolve({ status: res.statusCode ?? 0, headers: res.headers }));
		});
		req.on("error", reject);
		req.end(body);
	});
}

// The raw handshake, so a refused upgrade shows as the socket closing unanswered.
function upgrade(agent: URL, origin?: string, host = agent.host): Promise<string> {
	return new Promise((resolve) => {
		const socket = tlsConnect({ host: "127.0.0.1", port: Number(agent.port), ...trust(agent) });
		let data = "";
		socket.on("data", (chunk) => {
			data += chunk.toString();
			if (data.includes("\r\n\r\n")) socket.destroy();
		});
		socket.on("close", () => resolve(data));
		socket.on("error", () => undefined);
		socket.on("secureConnect", () => socket.write(`GET /ws/cdp?token=null HTTP/1.1\r\nHost: ${host}\r\n${origin ? `Origin: ${origin}\r\n` : ""}Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGVzdA==\r\nSec-WebSocket-Version: 13\r\n\r\n`));
	});
}

describe("cloud agent proxy", () => {
	it("serves a cloud session's agent browser on its own origin once the control plane says it has one", async () => {
		const { baseUrl, seen } = await startCp();
		proxy = createCloudAgentProxy(() => dataDir);
		const agent = new URL(await proxy.agentBrowserUrl(baseUrl, "org-1", "ses-1"));
		expect(agent.hostname).toMatch(/^ao-preview-[0-9a-f]{32}\.localhost$/);
		expect(`${agent.protocol}${agent.pathname}${agent.search}`).toBe("https:/v/cdp?embed=1&sq=70&ss=1");
		expect(seen).toEqual([expect.objectContaining({ method: "GET", url: route("stats"), auth: "Bearer cloud-token" })]);
		expect(proxy.isAgentBrowserUrl(agent.href)).toBe(true);
		expect(proxy.isAgentBrowserUrl(`${agent.origin}/download/x`)).toBe(false);
		expect(proxy.isAgentBrowserUrl(agent.href.replace(agent.hostname, `ao-preview-${"0".repeat(32)}.localhost`))).toBe(false);
		expect((await stat(path.join(dataDir, "cloud-agent-browser.key"))).mode & 0o777).toBe(0o600);
	});

	it("gives no agent browser when the sandbox has none, the control plane is unreachable, or no one is signed in", async () => {
		const { baseUrl } = await startCp(() => 502);
		proxy = createCloudAgentProxy(() => dataDir);
		expect(await proxy.agentBrowserUrl(baseUrl, "org-1", "ses-1")).toBe("");
		expect(await proxy.agentBrowserUrl("http://127.0.0.1:1", "org-1", "ses-1")).toBe("");
		token.value = null;
		expect(await proxy.agentBrowserUrl(baseUrl, "org-1", "ses-1")).toBe("");
		await expect(proxy.agentBrowserUrl("http://cp.example", "org-1", "ses-1")).rejects.toThrow(/https/);
		await expect(proxy.agentBrowserUrl(baseUrl, "../x", "ses-1")).rejects.toThrow(/invalid/);
	});

	it("gives no agent browser while the sandbox's has not started: showing it would start it", async () => {
		const { baseUrl, seen } = await startCp(() => 200, `{"running":false}`);
		proxy = createCloudAgentProxy(() => dataDir);
		expect(await proxy.agentBrowserUrl(baseUrl, "org-1", "ses-1")).toBe("");
		expect(seen.map((request) => request.url)).toEqual([route("stats")]);
	});

	it("tries again after its server failed to start", async () => {
		const { baseUrl } = await startCp();
		proxy = createCloudAgentProxy(() => dataDir);
		listenFailures.left = 1;
		await expect(proxy.agentBrowserUrl(baseUrl, "org-1", "ses-1")).rejects.toThrow(/listen/);
		expect(new URL(await proxy.agentBrowserUrl(baseUrl, "org-1", "ses-1")).pathname).toBe("/v/cdp");
	});

	it("keeps a session's origin across restarts, and only with the same secret", async () => {
		const { baseUrl } = await startCp();
		proxy = createCloudAgentProxy(() => dataDir);
		const first = new URL(await proxy.agentBrowserUrl(baseUrl, "org-1", "ses-1"));
		expect(new URL(await proxy.agentBrowserUrl(baseUrl, "org-1", "ses-2")).hostname).not.toBe(first.hostname);
		await proxy.close();
		expect(proxy.isAgentBrowserUrl(first.href)).toBe(false);
		proxy = createCloudAgentProxy(() => dataDir);
		expect(new URL(await proxy.agentBrowserUrl(baseUrl, "org-1", "ses-1")).hostname).toBe(first.hostname);
		await proxy.close();
		const otherDir = await mkdtemp(path.join(os.tmpdir(), "cloud-agent-proxy-"));
		try {
			proxy = createCloudAgentProxy(() => otherDir);
			expect(new URL(await proxy.agentBrowserUrl(baseUrl, "org-1", "ses-1")).hostname).not.toBe(first.hostname);
		} finally {
			await proxy.close();
			await rm(otherDir, { recursive: true, force: true });
		}
	});

	it("forwards the viewer's requests with the bearer and without its own credentials", async () => {
		const { baseUrl, seen } = await startCp();
		proxy = createCloudAgentProxy(() => dataDir);
		const agent = new URL(await proxy.agentBrowserUrl(baseUrl, "org-1", "ses-1"));
		token.value = "rotated-token";
		const page = await send(agent, "GET", "/v/cdp?embed=1&tab=R1", { Cookie: "session=viewer", Authorization: "Bearer viewer" });
		expect(page.status).toBe(200);
		expect(page.headers["set-cookie"]).toBeUndefined();
		expect(seen.at(-1)).toEqual({ method: "GET", url: route("v/cdp?embed=1&tab=R1"), auth: "Bearer rotated-token", cookie: undefined, origin: undefined, body: "" });
		expect((await send(agent, "POST", "/upload?dir=abc&name=a.txt", { Origin: agent.origin }, "file-bytes")).status).toBe(200);
		expect(seen.at(-1)).toMatchObject({ method: "POST", url: route("upload?dir=abc&name=a.txt"), auth: "Bearer rotated-token", origin: undefined, body: "file-bytes" });
		expect((await send(agent, "GET", "/download/0a1b-2c3d")).status).toBe(200);
		expect(seen.at(-1)).toMatchObject({ url: route("download/0a1b-2c3d") });
	});

	it("refuses other origins, hosts and routes before the control plane sees them", async () => {
		const { baseUrl, seen } = await startCp();
		proxy = createCloudAgentProxy(() => dataDir);
		const agent = new URL(await proxy.agentBrowserUrl(baseUrl, "org-1", "ses-1"));
		const count = seen.length;
		expect((await send(agent, "POST", "/upload", { Origin: "https://evil.example" }, "x")).status).toBe(403);
		expect((await send(agent, "POST", "/upload", {}, "x")).status).toBe(403);
		expect((await send(agent, "GET", "/v/cdp", { Origin: "https://evil.example" })).status).toBe(403);
		expect((await send(agent, "GET", "/v/cdp", {}, undefined, `ao-preview-${"0".repeat(32)}.localhost:${agent.port}`)).status).toBe(404);
		expect((await send(agent, "POST", "/agent", { Origin: agent.origin }, "{}")).status).toBe(404);
		expect((await send(agent, "POST", "/control/release", { Origin: agent.origin })).status).toBe(404);
		expect((await send(agent, "GET", "/download/..%2F..%2Fx")).status).toBe(404);
		expect((await send(agent, "GET", "/upload")).status).toBe(404);
		expect(seen).toHaveLength(count);
	});

	it("answers 502 when the control plane is unreachable", async () => {
		const { baseUrl } = await startCp();
		proxy = createCloudAgentProxy(() => dataDir);
		const agent = new URL(await proxy.agentBrowserUrl(baseUrl, "org-1", "ses-1"));
		cp?.closeAllConnections();
		await new Promise<void>((resolve) => cp?.close(() => resolve()));
		cp = undefined;
		expect((await send(agent, "GET", "/v/cdp")).status).toBe(502);
	});

	it("bridges the viewer's socket to the control plane with the bearer", async () => {
		const { baseUrl } = await startCp();
		const wss = new WebSocketServer({ noServer: true });
		let upgradeUrl = "";
		let upgradeAuth = "";
		let upgradeCookie: string | undefined;
		cp?.on("upgrade", (req, socket, head) => {
			upgradeUrl = req.url ?? "";
			upgradeAuth = String(req.headers.authorization);
			upgradeCookie = req.headers.cookie;
			wss.handleUpgrade(req, socket, head, (ws) => ws.on("message", (data, isBinary) => ws.send(isBinary ? data : `echo ${data}`)));
		});
		proxy = createCloudAgentProxy(() => dataDir);
		const agent = new URL(await proxy.agentBrowserUrl(baseUrl, "org-1", "ses-1"));
		const viewer = new WebSocket(`wss://127.0.0.1:${agent.port}/ws/cdp?token=null`, { ...trust(agent), origin: agent.origin, headers: { Host: agent.host, Cookie: "session=viewer" } });
		const echoed = await new Promise<string>((resolve, reject) => {
			viewer.once("error", reject);
			viewer.once("open", () => viewer.send("hello"));
			viewer.once("message", (data) => resolve(String(data)));
		});
		expect(echoed).toBe("echo hello");
		expect(upgradeUrl).toBe(route("ws/cdp?token=null"));
		expect(upgradeAuth).toBe("Bearer cloud-token");
		expect(upgradeCookie).toBeUndefined();
		// Either side closing closes the other.
		const [upstream] = [...wss.clients];
		const closed = new Promise<void>((resolve) => viewer.once("close", () => resolve()));
		upstream.close();
		await closed;
		wss.close();
	});

	it("refuses a socket from another origin or for an unknown host", async () => {
		const { baseUrl } = await startCp();
		let upgrades = 0;
		cp?.on("upgrade", (_req, socket) => {
			upgrades++;
			socket.destroy();
		});
		proxy = createCloudAgentProxy(() => dataDir);
		const agent = new URL(await proxy.agentBrowserUrl(baseUrl, "org-1", "ses-1"));
		expect(await upgrade(agent, "https://evil.example")).toBe("");
		expect(await upgrade(agent)).toBe("");
		expect(await upgrade(agent, agent.origin, `ao-preview-${"0".repeat(32)}.localhost:${agent.port}`)).toBe("");
		expect(upgrades).toBe(0);
	});
});
