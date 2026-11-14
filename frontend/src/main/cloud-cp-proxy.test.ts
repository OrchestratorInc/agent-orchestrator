import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("electron", () => ({ ipcMain: { handle: vi.fn(), on: vi.fn() } }));
const { tokenMock } = vi.hoisted(() => ({ tokenMock: vi.fn() }));
vi.mock("./cloud-auth", () => ({ getCloudAccessToken: tokenMock }));

import { createCloudCpProxy, MAX_CLOUD_CP_BINARY_BYTES } from "./cloud-cp-proxy";

const base = { baseUrl: "https://cp.example.com", path: "/api/cloud/v1/orgs/o/sessions/s/attachments?name=a.png", method: "POST" };
const controlPlaneUrl = async () => "https://cp.example.com";

beforeEach(() => {
	tokenMock.mockReset();
	tokenMock.mockResolvedValue("token-1");
});

// Session attachments are bytes both ways, which the text channel cannot carry.
describe("cloud control-plane binary requests", () => {
	it("sends the bytes with the main process's token and returns the response bytes", async () => {
		const fetchImpl = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
			expect(new Uint8Array(init?.body as ArrayBuffer)).toEqual(new Uint8Array([1, 2, 3]));
			expect(new Headers(init?.headers).get("authorization")).toBe("Bearer token-1");
			return new Response(new Uint8Array([9, 8]), { status: 201, headers: { "content-type": "application/octet-stream" } });
		});
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: fetchImpl as typeof fetch });
		const response = await proxy.requestBinary({ ...base, headers: { Authorization: "Bearer forged" }, body: new Uint8Array([1, 2, 3]) });
		expect(fetchImpl).toHaveBeenCalledWith("https://cp.example.com/api/cloud/v1/orgs/o/sessions/s/attachments?name=a.png", expect.anything());
		expect(response.status).toBe(201);
		expect(Array.from(response.body)).toEqual([9, 8]);
	});

	it("refuses a text body and a path outside the API, and answers a body over the limit with a message, unsent", async () => {
		const fetchImpl = vi.fn();
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: fetchImpl as unknown as typeof fetch });
		await expect(proxy.requestBinary({ ...base, body: "text" })).rejects.toThrow(/bytes/);
		await expect(proxy.requestBinary({ ...base, path: "/elsewhere" })).rejects.toThrow(/api\/cloud\/v1/);
		const tooLarge = await proxy.requestBinary({ ...base, body: new Uint8Array(MAX_CLOUD_CP_BINARY_BYTES + 1) });
		expect(tooLarge.status).toBe(413);
		expect(JSON.parse(new TextDecoder().decode(tooLarge.body))).toEqual({
			error: "attachment_too_large",
			code: "attachment_too_large",
			message: "The file is larger than 32 MiB, the most the app sends to Orchestrator Cloud at once.",
		});
		expect(fetchImpl).not.toHaveBeenCalled();
	});

	// A fetch that answers only when its signal aborts, as a stalled upload does.
	const stalledFetch = () => vi.fn((_url: RequestInfo | URL, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
		init?.signal?.addEventListener("abort", () => reject(init.signal?.reason), { once: true });
	}));

	it("ends a request that outlives its time with a message the person can read", async () => {
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: stalledFetch() as unknown as typeof fetch });
		const response = await proxy.requestBinary({ ...base, body: new Uint8Array([1]), timeoutMs: 20 });
		expect(response.status).toBe(504);
		expect(JSON.parse(new TextDecoder().decode(response.body))).toMatchObject({
			code: "request_timeout",
			message: "Orchestrator Cloud did not answer within 0 seconds. Try again.",
		});
	});

	it("cancels a WebContents' request by its id, and one going away cancels all of its own", async () => {
		const fetchImpl = stalledFetch();
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: fetchImpl as unknown as typeof fetch });
		let destroyed: (() => void) | undefined;
		const sender = { id: 7, once: vi.fn((_event: "destroyed", listener: () => void) => { destroyed = listener; }) };
		const other = { id: 8, once: vi.fn() };
		const first = proxy.requestBinary({ ...base, requestId: "upload-1" }, sender);
		const second = proxy.requestBinary({ ...base, requestId: "upload-2" }, sender);
		await vi.waitFor(() => expect(fetchImpl).toHaveBeenCalledTimes(2));
		// Another WebContents cannot cancel it.
		proxy.cancelBinary(other, "upload-1");
		proxy.cancelBinary(sender, "upload-1");
		await expect(first).rejects.toThrow();
		expect((fetchImpl.mock.calls[1][1]?.signal as AbortSignal).aborted).toBe(false);
		destroyed?.();
		await expect(second).rejects.toThrow();
	});

	it("refuses a request id that is not a short token", async () => {
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: vi.fn() as unknown as typeof fetch });
		await expect(proxy.requestBinary({ ...base, requestId: "../x" })).rejects.toThrow(/requestId/);
		await expect(proxy.requestBinary({ ...base, timeoutMs: -1 })).rejects.toThrow(/timeoutMs/);
	});

	it("answers a response over the limit with a message instead of the bytes", async () => {
		const fetchImpl = vi.fn(async () => new Response(new Uint8Array(1), { status: 200, headers: { "content-length": String(MAX_CLOUD_CP_BINARY_BYTES + 1) } }));
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: fetchImpl as unknown as typeof fetch });
		const response = await proxy.requestBinary({ ...base, method: "GET" });
		expect(response.status).toBe(413);
		expect(JSON.parse(new TextDecoder().decode(response.body)).message).toMatch(/larger than 32 MiB/);
	});

	it("answers 401 without a request when signed out", async () => {
		tokenMock.mockResolvedValue(null);
		const fetchImpl = vi.fn();
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: fetchImpl as unknown as typeof fetch });
		const response = await proxy.requestBinary(base);
		expect(response.status).toBe(401);
		expect(fetchImpl).not.toHaveBeenCalled();
	});
});

// The token goes only to the control plane the daemon is configured for: a
// renderer that names another server gets an error, and nothing is sent.
describe("cloud control-plane URL pinning", () => {
	const sender = { id: 1, isDestroyed: () => false, send: vi.fn(), once: vi.fn() };
	const elsewhere = ["https://evil.example", "https://cp.example.com.evil.example", "https://cp.example.com:8443", "http://127.0.0.1:9999", "https://cp.example.com/other"];

	it("refuses a baseUrl other than the configured control plane on every channel, before reading the token", async () => {
		const fetchImpl = vi.fn();
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: fetchImpl as unknown as typeof fetch });
		for (const baseUrl of elsewhere) {
			await expect(proxy.request({ ...base, baseUrl, method: "GET" })).rejects.toThrow(/configured Orchestrator Cloud control plane/);
			await expect(proxy.requestBinary({ ...base, baseUrl })).rejects.toThrow(/configured Orchestrator Cloud control plane/);
			await expect(proxy.openStream(sender, { ...base, baseUrl, method: "GET" })).rejects.toThrow(/configured Orchestrator Cloud control plane/);
		}
		expect(fetchImpl).not.toHaveBeenCalled();
		expect(tokenMock).not.toHaveBeenCalled();
	});

	it("refuses every request while no control plane is configured", async () => {
		const fetchImpl = vi.fn();
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl: async () => null, fetchImpl: fetchImpl as unknown as typeof fetch });
		await expect(proxy.request({ ...base, method: "GET" })).rejects.toThrow(/no Orchestrator Cloud control plane/);
		await expect(proxy.requestBinary(base)).rejects.toThrow(/no Orchestrator Cloud control plane/);
		expect(fetchImpl).not.toHaveBeenCalled();
	});

	it("sends the configured control plane's requests there, with the token", async () => {
		const fetchImpl = vi.fn(async (_url: string, _init?: RequestInit) => new Response("{}", { status: 200 }));
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl: async () => "https://cp.example.com/", fetchImpl: fetchImpl as unknown as typeof fetch });
		await proxy.request({ ...base, baseUrl: "https://cp.example.com", path: "/api/cloud/v1/me", method: "GET" });
		await proxy.requestBinary({ ...base, baseUrl: "https://CP.example.com/" });
		expect(fetchImpl.mock.calls.map(([url]) => url)).toEqual([
			"https://cp.example.com/api/cloud/v1/me",
			"https://cp.example.com/api/cloud/v1/orgs/o/sessions/s/attachments?name=a.png",
		]);
		for (const [, init] of fetchImpl.mock.calls) {
			expect(new Headers(init?.headers).get("authorization")).toBe("Bearer token-1");
		}
	});
});

// Host tokens open a shell on the account's machines; only the main process reads them.
describe("the account's machine directory", () => {
	const listing = JSON.stringify({ hosts: [{ hostId: "h1", label: "VM", url: "https://h1.example", token: "a".repeat(64) }], fixedAddresses: true });
	const hosts = { baseUrl: "https://cp.example.com", path: "/api/cloud/v1/me/hosts", method: "GET" };
	const fetchListing = () => vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => new Response(listing, { status: 200 }));

	it("strips host tokens from renderer requests, including an encoded path", async () => {
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: fetchListing() as unknown as typeof fetch });
		for (const path of [hosts.path, "/api/cloud/v1/me/ho%73ts"]) {
			const response = await proxy.request({ ...hosts, path });
			expect(JSON.parse(response.body)).toEqual({ hosts: [{ hostId: "h1", label: "VM", url: "https://h1.example" }], fixedAddresses: true });
		}
	});

	it("keeps host tokens for the main process and passes its signal to the request", async () => {
		const fetchImpl = fetchListing();
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: fetchImpl as unknown as typeof fetch });
		const { signal } = new AbortController();
		expect((await proxy.request(hosts, { hostTokens: true, signal })).body).toBe(listing);
		expect(fetchImpl.mock.calls[0][1]?.signal).toBe(signal);
	});

	it("refuses the directory on the binary and stream channels", async () => {
		const fetchImpl = fetchListing();
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: fetchImpl as unknown as typeof fetch });
		await expect(proxy.requestBinary(hosts)).rejects.toThrow(/cloudCp:request/);
		const sender = { id: 1, isDestroyed: () => false, send: vi.fn(), once: vi.fn() };
		await expect(proxy.openStream(sender, hosts)).rejects.toThrow(/cloudCp:request/);
		expect(fetchImpl).not.toHaveBeenCalled();
	});
});
