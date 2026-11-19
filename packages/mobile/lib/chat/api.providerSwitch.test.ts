import { beforeEach, expect, it, vi } from "vitest";

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("../api", () => ({ apiRequest: request, ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ authHeaders: () => ({}), httpBase: () => "http://host" }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
import { prewarmChatAgent, switchProviderAndSend } from "./api";
import type { ServerConfig } from "../config";

const config = {} as ServerConfig;
beforeEach(() => request.mockReset());

// Desktop's send with a pending switch: one request that switches, then sends.
it("switches and sends in one request, keyed by the send's own id", async () => {
	request.mockResolvedValueOnce({ json: async () => ({ status: "switched", message: { turnId: "turn", duplicate: false } }) });
	const message = { text: "carry on", clientMessageId: "send-1" };
	await expect(switchProviderAndSend(config, "s 1", { targetHarness: "codex", model: "gpt-5.5" }, message)).resolves.toEqual({ turnId: "turn", duplicate: false });
	expect(request.mock.lastCall?.[1]).toBe("/api/v1/sessions/s%201/provider-switch");
	expect(JSON.parse(request.mock.lastCall?.[2].body)).toEqual({ targetHarness: "codex", model: "gpt-5.5", idempotencyKey: "send-1", message });
});

it("leaves the model to the agent when none was picked", async () => {
	request.mockResolvedValueOnce({ json: async () => ({ status: "switched" }) });
	await expect(switchProviderAndSend(config, "s", { targetHarness: "claude-code" }, { text: "hi", clientMessageId: "send-2" })).resolves.toEqual({ duplicate: true });
	expect(JSON.parse(request.mock.lastCall?.[2].body)).not.toHaveProperty("model");
});

it("warms the picked agent's chat ahead of the send", async () => {
	request.mockResolvedValueOnce({});
	await prewarmChatAgent(config, "codex");
	expect(request.mock.lastCall?.slice(1, 3)).toEqual(["/api/v1/agents/codex/chat/prewarm", { method: "POST" }]);
});
