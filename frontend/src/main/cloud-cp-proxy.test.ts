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

	it("refuses a text body, a path outside the API, and a body over the limit", async () => {
		const proxy = createCloudCpProxy(() => "/data", { controlPlaneUrl, fetchImpl: vi.fn() as unknown as typeof fetch });
		await expect(proxy.requestBinary({ ...base, body: "text" })).rejects.toThrow(/bytes/);
		await expect(proxy.requestBinary({ ...base, path: "/elsewhere" })).rejects.toThrow(/api\/cloud\/v1/);
		await expect(proxy.requestBinary({ ...base, body: new Uint8Array(MAX_CLOUD_CP_BINARY_BYTES + 1) })).rejects.toThrow(/too large/);
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
