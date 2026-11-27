import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
const capture = vi.hoisted(() => vi.fn());
const requestTimed = vi.hoisted(() => vi.fn());
vi.mock("./telemetry/uiUsageRegistry", async (importOriginal) => ({ ...(await importOriginal<object>()), reportRequestTimed: requestTimed }));
vi.mock("./sentry", () => ({ captureMobileApiError: capture, httpCategory: (status: number) => (status >= 500 ? "http_5xx" : "http_4xx") }));

import { apiRequest, getTelemetryIdentity, killSession } from "./api";
import type { ServerConfig } from "./config";

const cfg: ServerConfig = { host: "ao.test", httpPort: "3011", muxPort: "3011", secure: false, password: "secret12" };

afterEach(() => {
	vi.unstubAllGlobals();
	capture.mockReset();
	requestTimed.mockReset();
});

function respond(status: number, body: unknown) {
	vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status })));
}

describe("getTelemetryIdentity", () => {
	it("reads the sign-in state, lifetime sessions and daemon version", async () => {
		respond(200, { distinctId: "ins_1", isSignedIn: false, lifetimeSessions: 7, daemonVersion: "1.9.0", optedOut: false });
		expect(await getTelemetryIdentity(cfg)).toMatchObject({ isSignedIn: false, lifetimeSessions: 7, daemonVersion: "1.9.0" });
	});

	it("leaves out what an older daemon does not send", async () => {
		respond(200, { distinctId: "ins_1", optedOut: false });
		const identity = await getTelemetryIdentity(cfg);
		expect(identity).not.toHaveProperty("isSignedIn");
		expect(identity).not.toHaveProperty("lifetimeSessions");
		expect(identity.daemonVersion).toBeUndefined();
	});
});

describe("API failure capture", () => {
	it("names the failed request by method and route, never by session", async () => {
		respond(500, { code: "INTERNAL_ERROR", message: "boom", requestId: "req_1" });
		await expect(killSession(cfg, "acme-secret-7")).rejects.toThrow();
		const [operation, category, status, code] = capture.mock.calls[0];
		expect(operation).toBe("POST /api/v1/sessions/:id/kill");
		expect([category, status, code]).toEqual(["http_5xx", 500, "INTERNAL_ERROR"]);
		expect(JSON.stringify(capture.mock.calls)).not.toContain("acme-secret");
	});
});

describe("request timing", () => {
	it("reports each answer's time by the connection kind it went over", async () => {
		respond(200, { distinctId: "ins_1", optedOut: false });
		await getTelemetryIdentity({ ...cfg, endpointKind: "tailscale" });
		const [ms, transport, ok] = requestTimed.mock.calls[0];
		expect(typeof ms).toBe("number");
		expect([transport, ok]).toEqual(["tailscale", true]);
	});

	it("reports a failed answer and an unanswered request as failures", async () => {
		respond(500, { code: "INTERNAL_ERROR" });
		await expect(killSession(cfg, "s1")).rejects.toThrow();
		expect(requestTimed.mock.calls[0].slice(1)).toEqual(["unknown", false]);
		vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("Network request failed")));
		await expect(killSession(cfg, "s1")).rejects.toThrow();
		expect(requestTimed.mock.calls[1]).toEqual([null, "unknown", false]);
	});

	describe("when the body stalls", () => {
		const stalled = (status: number) => new Response(new ReadableStream({ start() {} }), { status });
		afterEach(() => vi.useRealTimers());

		it("reports a body that times out once, as a failure", async () => {
			vi.useFakeTimers();
			vi.stubGlobal("fetch", vi.fn().mockResolvedValue(stalled(200)));
			const done = apiRequest(cfg, "/api/v1/sessions", undefined, 100).catch(() => undefined);
			await vi.advanceTimersByTimeAsync(100);
			await done;
			expect(requestTimed.mock.calls).toEqual([[null, "unknown", false]]);
		});

		it("reports an error status once, with its time to the headers", async () => {
			vi.useFakeTimers();
			vi.stubGlobal("fetch", vi.fn().mockResolvedValue(stalled(401)));
			const done = apiRequest(cfg, "/api/v1/sessions", undefined, 100).catch(() => undefined);
			await vi.advanceTimersByTimeAsync(100);
			await done;
			expect(requestTimed.mock.calls).toHaveLength(1);
			expect(requestTimed.mock.calls[0]).toEqual([expect.any(Number), "unknown", false]);
		});

		it("reports nothing when the screen cancels", async () => {
			vi.useFakeTimers();
			vi.stubGlobal("fetch", vi.fn().mockResolvedValue(stalled(200)));
			const controller = new AbortController();
			const done = apiRequest(cfg, "/api/v1/sessions", { signal: controller.signal }).catch(() => undefined);
			await vi.advanceTimersByTimeAsync(1);
			controller.abort();
			await vi.advanceTimersByTimeAsync(1);
			await done;
			expect(requestTimed).not.toHaveBeenCalled();
		});
	});
});
