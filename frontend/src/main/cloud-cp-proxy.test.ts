import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("electron", () => ({ ipcMain: { handle: vi.fn(), on: vi.fn() } }));
const { tokenMock } = vi.hoisted(() => ({ tokenMock: vi.fn() }));
vi.mock("./cloud-auth", () => ({ getCloudAccessToken: tokenMock }));

import { cloudCpLogPath, createCloudCpProxy, MAX_CLOUD_CP_BINARY_BYTES } from "./cloud-cp-proxy";

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
			message: "The file is larger than 32 MiB, the most the app sends to AO Cloud at once.",
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
			message: "AO Cloud did not answer within 0 seconds. Try again.",
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
			await expect(proxy.request({ ...base, baseUrl, method: "GET" })).rejects.toThrow(/configured AO Cloud control plane/);
			await expect(proxy.requestBinary({ ...base, baseUrl })).rejects.toThrow(/configured AO Cloud control plane/);
			await expect(proxy.openStream(sender, { ...base, baseUrl, method: "GET" })).rejects.toThrow(/configured AO Cloud control plane/);
		}
		expect(fetchImpl).not.toHaveBeenCalled();
		expect(tokenMock).not.toHaveBeenCalled();
	});

	it("refuses every request while no control plane is configured", async () => {
		const fetchImpl = vi.fn();
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl: async () => null, fetchImpl: fetchImpl as unknown as typeof fetch });
		await expect(proxy.request({ ...base, method: "GET" })).rejects.toThrow(/no AO Cloud control plane/);
		await expect(proxy.requestBinary(base)).rejects.toThrow(/no AO Cloud control plane/);
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

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

// Every CP request gets an id to match against the CP's logs, failures say
// whether the CP answered, and nothing secret is logged.
describe("cloud control-plane request ids, failures and diagnostics", () => {
	const get = { baseUrl: "https://cp.example.com", path: "/api/cloud/v1/orgs/org_1/sessions/s-9/workspace/file?path=secret.txt", method: "GET" };
	const sender = () => ({ id: 3, isDestroyed: () => false, send: vi.fn(), once: vi.fn() });
	const unreachable = () => Object.assign(new TypeError("fetch failed"), { cause: Object.assign(new Error("getaddrinfo ENOTFOUND cp.example.com"), { code: "ENOTFOUND" }) });

	it("sends a fresh UUID X-Request-ID, keeping only a caller's own UUID", async () => {
		const fetchImpl = vi.fn(async (_url: string, _init?: RequestInit) => new Response("{}", { status: 200 }));
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: fetchImpl as unknown as typeof fetch });
		const own = "0f8fad5b-d9cb-469f-a165-70867728950e";
		await proxy.request(get);
		await proxy.request(get);
		await proxy.request({ ...get, headers: { "X-Request-ID": own } });
		await proxy.request({ ...get, headers: { "X-Request-ID": "laptop.local/abc-000001" } });
		const ids = fetchImpl.mock.calls.map(([, init]) => new Headers(init?.headers).get("x-request-id"));
		for (const id of ids) expect(id).toMatch(UUID);
		expect(ids[0]).not.toBe(ids[1]);
		expect(ids[2]).toBe(own);
		expect(ids[3]).not.toBe("laptop.local/abc-000001");
	});

	it("answers a CP it could not reach with status 0, CLOUD_UNREACHABLE and the id it sent, and logs nothing secret", async () => {
		const warn = vi.fn();
		const fetchImpl = vi.fn(async () => { throw unreachable(); });
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: fetchImpl as unknown as typeof fetch, warn });
		const response = await proxy.request(get);
		const sent = new Headers((fetchImpl.mock.calls[0] as unknown as [string, RequestInit])[1].headers).get("x-request-id");
		expect(response.status).toBe(0);
		expect(response.headers["x-request-id"]).toBe(sent);
		expect(JSON.parse(response.body)).toMatchObject({ code: "CLOUD_UNREACHABLE", requestId: sent, cause: "ENOTFOUND" });
		expect(warn).toHaveBeenCalledOnce();
		const line = warn.mock.calls[0][0] as string;
		expect(line).toContain("GET /api/cloud/v1/orgs/:id/sessions/:id/workspace/file failed: origin=network status=0 code=CLOUD_UNREACHABLE");
		expect(line).toContain(`requestId=${sent}`);
		expect(line).toContain("cause=ENOTFOUND");
		expect(line).not.toMatch(/token-1|secret\.txt|org_1|s-9|\?/);
	});

	it("reads a failed response's code and id from the envelope, else the X-Request-ID header, and keeps the CP release", async () => {
		const warn = vi.fn();
		const headerId = "7c9e6679-7425-40de-944b-e07fc1f90ae7";
		const responses = [
			new Response(JSON.stringify({ error: "x", code: "WORKER_UNAVAILABLE", message: "down", requestId: "9b2c1f3e-1111-4222-8333-944455556666" }), { status: 503, headers: { "x-ao-release": "cp-2026.11.01" } }),
			new Response("<html>bad gateway</html>", { status: 502, headers: { "x-request-id": headerId } }),
		];
		const fetchImpl = vi.fn(async () => responses.shift() as Response);
		let now = 1_000;
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: fetchImpl as unknown as typeof fetch, warn, now: () => (now += 5) });
		expect((await proxy.request(get)).status).toBe(503);
		const second = await proxy.request(get);
		expect(second.headers["x-request-id"]).toBe(headerId);
		expect(warn.mock.calls[0][0]).toContain("origin=cloud status=503 code=WORKER_UNAVAILABLE requestId=9b2c1f3e-1111-4222-8333-944455556666 durationMs=5");
		expect(warn.mock.calls[1][0]).toContain(`status=502 code=- requestId=${headerId}`);
		const diagnostics = await proxy.diagnostics();
		expect(diagnostics.baseUrl).toBe("https://cp.example.com");
		expect(diagnostics.release).toBe("cp-2026.11.01");
		expect(diagnostics.recentErrors.map(({ origin, status, code, requestId }) => ({ origin, status, code, requestId }))).toEqual([
			{ origin: "cloud", status: 503, code: "WORKER_UNAVAILABLE", requestId: "9b2c1f3e-1111-4222-8333-944455556666" },
			{ origin: "cloud", status: 502, code: undefined, requestId: headerId },
		]);
		expect(JSON.stringify(diagnostics)).not.toContain("down");
	});

	it("keeps only the last three failures for Report a problem", async () => {
		const fetchImpl = vi.fn(async () => new Response("{}", { status: 500 }));
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: fetchImpl as unknown as typeof fetch, warn: vi.fn() });
		for (let i = 0; i < 5; i += 1) await proxy.request(get);
		expect((await proxy.diagnostics()).recentErrors).toHaveLength(3);
		// A success is not a failure, and a signed-out request never reaches the CP.
		tokenMock.mockResolvedValue(null);
		await proxy.request(get);
		expect((await proxy.diagnostics()).recentErrors).toHaveLength(3);
	});

	it("answers a stream the CP refused or could not be reached for as fields, not a message to parse", async () => {
		const requestId = "9b2c1f3e-1111-4222-8333-944455556666";
		const fetchImpl = vi.fn()
			.mockResolvedValueOnce(new Response(JSON.stringify({ code: "SESSION_TERMINATED", message: "gone", requestId }), { status: 410 }))
			.mockRejectedValueOnce(unreachable());
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: fetchImpl as unknown as typeof fetch, warn: vi.fn() });
		expect(await proxy.openStream(sender(), get)).toEqual({ error: { status: 410, code: "SESSION_TERMINATED", message: "gone", requestId } });
		expect(await proxy.openStream(sender(), get)).toEqual({ error: expect.objectContaining({ status: 0, code: "CLOUD_UNREACHABLE", cause: "ENOTFOUND", requestId: expect.stringMatching(UUID) }) });
		tokenMock.mockResolvedValue(null);
		expect(await proxy.openStream(sender(), get)).toEqual({ error: expect.objectContaining({ status: 401, code: "no_token" }) });
	});

	it("ends a stream whose connection drops with the code and the stream's request id", async () => {
		const requestId = "9b2c1f3e-1111-4222-8333-944455556666";
		let fail: (error: unknown) => void = () => undefined;
		const body = new ReadableStream<Uint8Array>({ start(controller) { fail = (error) => controller.error(error); } });
		const fetchImpl = vi.fn(async () => new Response(body, { status: 200, headers: { "x-request-id": requestId } }));
		const warn = vi.fn();
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: fetchImpl as unknown as typeof fetch, warn });
		const target = sender();
		const opened = await proxy.openStream(target, get);
		expect(opened).toEqual({ streamId: expect.any(String) });
		await new Promise((resolve) => setImmediate(resolve));
		fail(Object.assign(new TypeError("terminated"), { cause: { code: "UND_ERR_SOCKET" } }));
		await vi.waitFor(() => expect(target.send).toHaveBeenCalled());
		expect(target.send.mock.calls[0][1]).toMatchObject({ type: "error", status: 0, code: "CLOUD_UNREACHABLE", requestId, cause: "UND_ERR_SOCKET" });
		expect(warn.mock.calls[0][0]).toContain("cause=UND_ERR_SOCKET");
	});
});

describe("cloudCpLogPath", () => {
	it("keeps the route's words and replaces identifiers, names and the query", () => {
		expect(cloudCpLogPath("https://cp.example.com/api/cloud/v1/orgs/org_1/projects/p-1/cues?x=1")).toBe("/api/cloud/v1/orgs/:id/projects/:id/cues");
		expect(cloudCpLogPath("https://cp.example.com/mount/api/cloud/v1/orgs/acme/sessions/abc/artifacts/notes")).toBe("/api/cloud/v1/orgs/:id/sessions/:id/artifacts/:id");
		expect(cloudCpLogPath("https://cp.example.com/api/cloud/v1/me")).toBe("/api/cloud/v1/me");
		expect(cloudCpLogPath("not a url")).toBe("unknown");
	});
});
