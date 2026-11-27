import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
const capture = vi.hoisted(() => vi.fn());
vi.mock("./sentry", () => ({ captureMobileApiError: capture, httpCategory: (status: number) => (status >= 500 ? "http_5xx" : "http_4xx") }));

import { getTelemetryIdentity, killSession } from "./api";
import type { ServerConfig } from "./config";

const cfg: ServerConfig = { host: "ao.test", httpPort: "3011", muxPort: "3011", secure: false, password: "secret12" };

afterEach(() => {
	vi.unstubAllGlobals();
	capture.mockReset();
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
