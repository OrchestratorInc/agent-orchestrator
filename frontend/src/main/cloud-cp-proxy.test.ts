import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("electron", () => ({ ipcMain: { handle: vi.fn(), on: vi.fn() } }));
const { tokenMock } = vi.hoisted(() => ({ tokenMock: vi.fn() }));
vi.mock("./cloud-auth", () => ({ getCloudAccessToken: tokenMock }));

import { createCloudCpProxy, MAX_CLOUD_CP_BINARY_BYTES } from "./cloud-cp-proxy";

const base = { baseUrl: "https://cp.example.com", path: "/api/cloud/v1/orgs/o/sessions/s/attachments?name=a.png", method: "POST" };

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
		const proxy = createCloudCpProxy(() => "/data", { fetchImpl: fetchImpl as typeof fetch });
		const response = await proxy.requestBinary({ ...base, headers: { Authorization: "Bearer forged" }, body: new Uint8Array([1, 2, 3]) });
		expect(fetchImpl).toHaveBeenCalledWith("https://cp.example.com/api/cloud/v1/orgs/o/sessions/s/attachments?name=a.png", expect.anything());
		expect(response.status).toBe(201);
		expect(Array.from(response.body)).toEqual([9, 8]);
	});

	it("refuses a text body, a path outside the API, and a body over the limit", async () => {
		const proxy = createCloudCpProxy(() => "/data", { fetchImpl: vi.fn() as unknown as typeof fetch });
		await expect(proxy.requestBinary({ ...base, body: "text" })).rejects.toThrow(/bytes/);
		await expect(proxy.requestBinary({ ...base, path: "/elsewhere" })).rejects.toThrow(/api\/cloud\/v1/);
		await expect(proxy.requestBinary({ ...base, body: new Uint8Array(MAX_CLOUD_CP_BINARY_BYTES + 1) })).rejects.toThrow(/too large/);
	});

	it("answers 401 without a request when signed out", async () => {
		tokenMock.mockResolvedValue(null);
		const fetchImpl = vi.fn();
		const proxy = createCloudCpProxy(() => "/data", { fetchImpl: fetchImpl as unknown as typeof fetch });
		const response = await proxy.requestBinary(base);
		expect(response.status).toBe(401);
		expect(fetchImpl).not.toHaveBeenCalled();
	});
});
