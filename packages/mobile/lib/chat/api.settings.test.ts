import { beforeEach, expect, it, vi } from "vitest";

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("../api", () => ({ apiRequest: request, ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ authHeaders: () => ({}), httpBase: () => "http://host" }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
import { getConversationPage } from "./api";
import type { ServerConfig } from "../config";

const config = {} as ServerConfig;
beforeEach(() => request.mockReset());

// The chat's settings as the daemon reports them, fast mode included: a
// snapshot that dropped it read Off, and the bolt fell back to Off once its
// save went unconfirmed.
it("keeps the chat's fast mode, and reads anything but true as off", async () => {
	const page = (settings: Record<string, unknown>) => ({ json: async () => ({ controller: "ready", latestSequence: 0, settings }) });
	request.mockResolvedValueOnce(page({ model: "opus", reasoningEffort: "high", approvalMode: "auto", fastMode: true }));
	expect((await getConversationPage(config, "session")).settings).toEqual({ model: "opus", reasoningEffort: "high", approvalMode: "auto", fastMode: true });
	request.mockResolvedValueOnce(page({ model: "opus", fastMode: false }));
	expect((await getConversationPage(config, "session")).settings.fastMode).toBeUndefined();
	request.mockResolvedValueOnce(page({ model: "opus" }));
	expect((await getConversationPage(config, "session")).settings.fastMode).toBeUndefined();
});
