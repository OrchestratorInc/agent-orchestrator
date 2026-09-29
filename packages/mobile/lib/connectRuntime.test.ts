import { afterEach, describe, expect, it, vi } from "vitest";

// Everything below the seam reaches into native storage; only the refresh
// wiring is under test here.
vi.mock("./config", () => ({}));
vi.mock("./hosts", () => ({
	findHost: vi.fn(),
	updateHostEndpoints: vi.fn(),
	adoptHostIdentity: vi.fn(),
	touchHost: vi.fn(),
}));

import { rejectedEndpointNeedsRace, runtimeConnectDeps } from "./connectRuntime";

const config = { host: "192.168.1.5", httpPort: "3011", password: "stale", secure: false } as Parameters<ReturnType<typeof runtimeConnectDeps>["refreshEndpoints"]>[0];

afterEach(() => vi.unstubAllGlobals());

// The endpoint refresh is authenticated, so with a stale password it counts
// towards the daemon's lockout. Settings' Test connection turns it off so a
// tap spends one attempt (its own ping), not two.
describe("runtimeConnectDeps", () => {
	it("checks a rejected address without presenting the saved bearer", async () => {
		const fetch = vi.fn()
			.mockResolvedValueOnce({ ok: true, json: async () => ({ hostId: "another-host" }) })
			.mockResolvedValueOnce({ ok: true, json: async () => ({ hostId: "paired-host" }) });
		vi.stubGlobal("fetch", fetch);
		const paired = { ...config, hostId: "paired-host", endpointKind: "lan" as const };

		await expect(rejectedEndpointNeedsRace(paired, 401)).resolves.toBe(true);
		await expect(rejectedEndpointNeedsRace(paired, 403)).resolves.toBe(false);
		await expect(rejectedEndpointNeedsRace(paired, 429)).resolves.toBe(false);
		expect(fetch).toHaveBeenCalledTimes(2);
		for (const [url, init] of fetch.mock.calls) {
			expect(url).toBe("http://192.168.1.5:3011/api/v1/identity");
			expect(init).toMatchObject({ method: "GET" });
			expect(init.headers).toBeUndefined();
		}
	});

	it("skips the authenticated endpoint refresh when asked", async () => {
		const fetch = vi.fn();
		vi.stubGlobal("fetch", fetch);
		await expect(runtimeConnectDeps({ refreshEndpoints: false }).refreshEndpoints(config)).resolves.toEqual([]);
		expect(fetch).not.toHaveBeenCalled();
	});

	it("refreshes by default", async () => {
		const fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ endpoints: [] }) });
		vi.stubGlobal("fetch", fetch);
		await runtimeConnectDeps().refreshEndpoints(config);
		expect(fetch).toHaveBeenCalledWith("http://192.168.1.5:3011/api/v1/endpoints", {
			headers: { Authorization: "Bearer stale" },
		});
	});
});
