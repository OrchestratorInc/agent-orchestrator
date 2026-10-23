import { describe, expect, it, vi } from "vitest";

vi.mock("electron", () => ({ ipcMain: { handle: vi.fn() } }));
vi.mock("./cloud-auth", () => ({ getCloudAccessToken: vi.fn(async () => "token") }));

import { createCloudCpProxy } from "./cloud-cp-proxy";

function recordingFetch() {
	const calls: { url: string; init: RequestInit }[] = [];
	const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
		calls.push({ url: String(input), init: init ?? {} });
		return new Response("{}", { status: 201, headers: { "content-type": "application/json" } });
	}) as unknown as typeof fetch;
	return { calls, fetchImpl };
}

describe("cloud control-plane proxy request bodies", () => {
	it("sends a base64 body as its raw bytes", async () => {
		const { calls, fetchImpl } = recordingFetch();
		const proxy = createCloudCpProxy(() => "/tmp", { fetchImpl });
		const bytes = Uint8Array.from([0x89, 0x50, 0x4e, 0x47, 0x00, 0xff]);
		const response = await proxy.request({
			baseUrl: "http://127.0.0.1:8081",
			path: "/api/cloud/v1/me/artifact-shares?name=a.png",
			method: "POST",
			bodyBase64: Buffer.from(bytes).toString("base64"),
		});
		expect(response.status).toBe(201);
		const sent = calls[0]?.init.body as Uint8Array;
		expect(Array.from(sent)).toEqual(Array.from(bytes));
		expect(new Headers(calls[0]?.init.headers).get("authorization")).toBe("Bearer token");
	});

	it("still sends a string body unchanged", async () => {
		const { calls, fetchImpl } = recordingFetch();
		const proxy = createCloudCpProxy(() => "/tmp", { fetchImpl });
		await proxy.request({ baseUrl: "http://127.0.0.1:8081", path: "/api/cloud/v1/orgs", method: "POST", body: "{\"a\":1}" });
		expect(calls[0]?.init.body).toBe("{\"a\":1}");
	});

	it("refuses a request carrying both body kinds or a non-string base64 body", async () => {
		const proxy = createCloudCpProxy(() => "/tmp", { fetchImpl: recordingFetch().fetchImpl });
		const base = { baseUrl: "http://127.0.0.1:8081", path: "/api/cloud/v1/me", method: "POST" };
		await expect(proxy.request({ ...base, body: "x", bodyBase64: "eA==" })).rejects.toThrow(/mutually exclusive/);
		await expect(proxy.request({ ...base, bodyBase64: 7 })).rejects.toThrow(/bodyBase64 must be a string/);
	});
});
