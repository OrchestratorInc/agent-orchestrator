import { beforeEach, expect, it, vi } from "vitest";

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("../api", () => ({ apiRequest: request, ApiError: class extends Error { status = 0; }, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ authHeaders: () => ({}), httpBase: () => "http://host" }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
import { forkSession, getForkOptions } from "./api";
import type { ServerConfig } from "../config";

const config = {} as ServerConfig;
beforeEach(() => request.mockReset());

it("forks a turn with a stable request id", async () => {
	request.mockResolvedValueOnce({ json: async () => ({ sessionId: "p-9", displayName: "Fork of P" }) });
	await expect(forkSession(config, "p-1", "turn-2", "req-1")).resolves.toEqual({ sessionId: "p-9", displayName: "Fork of P" });
	expect(request.mock.lastCall?.[1]).toBe("/api/v1/sessions/p-1/fork");
	expect(JSON.parse(request.mock.lastCall?.[2].body)).toEqual({ clientRequestId: "req-1", turnId: "turn-2" });
});

it("reads fork options, and treats a daemon without forks as none", async () => {
	request.mockResolvedValueOnce({ json: async () => ({ allowed: true, anyTurn: true }) });
	await expect(getForkOptions(config, "p-1")).resolves.toEqual({ allowed: true, anyTurn: true });
	expect(request.mock.lastCall?.[1]).toBe("/api/v1/sessions/p-1/fork-options");
	request.mockRejectedValueOnce(Object.assign(new Error("not found"), { status: 404 }));
	await expect(getForkOptions(config, "p-1")).resolves.toBeNull();
});
