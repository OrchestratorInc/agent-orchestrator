import { beforeEach, expect, it, vi } from "vitest";

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("../api", () => ({ apiRequest: request, ApiError: class extends Error {}, ATTACHMENT_REQUEST_TIMEOUT_MS: 1 }));
vi.mock("../config", () => ({ authHeaders: () => ({}), httpBase: () => "http://host" }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));
import { getConversationPage, sendConversationMessage } from "./api";
import type { ServerConfig } from "../config";

const config = {} as ServerConfig;
beforeEach(() => request.mockReset());
it("posts the same excerpts envelope as desktop while keeping the reply separate", async () => {
	request.mockResolvedValueOnce({ json: async () => ({ controller: "ready", supportsExcerpts: true }) });
	request.mockResolvedValueOnce({ json: async () => ({ turnId: "turn" }) });
	const input = { text: "Explain this", clientMessageId: "stable-id", excerpts: [{ conversationId: "conversation", messageId: "source", revision: 2, text: "Title\n• Bullet" }] };
	await expect(sendConversationMessage(config, "session", input)).resolves.toMatchObject({ turnId: "turn" });
	expect(JSON.parse(request.mock.lastCall?.[2].body)).toEqual(input);
	expect(request.mock.lastCall?.[1]).toBe("/api/v1/sessions/session/conversation/messages");
});

it("retains server-verified excerpt summaries from desktop messages for mobile rendering", async () => {
	const content = [{ type: "excerpt", text: "selected text", sourceMessageId: "source", sourceRevision: 2 }];
	request.mockResolvedValueOnce({ json: async () => ({ controller: "ready", latestSequence: 1, messages: [{ id: "reply", text: "and these?", content }] }) });
	const snapshot = await getConversationPage(config, "session");
	expect(snapshot.items[0]).toMatchObject({ kind: "message", text: "and these?", content });
});


it("refuses quoted sends to an older host before any message is submitted", async () => {
    request.mockResolvedValueOnce({ json: async () => ({ controller: "ready", capabilities: ["images"] }) });
    const input = { text: "Explain this", clientMessageId: "saved-retry", excerpts: [{ conversationId: "conversation", messageId: "source", revision: 2, text: "Important context" }] };
    await expect(sendConversationMessage(config, "session", input)).rejects.toThrow("Update Orchestrator on this machine");
    expect(request.mock.calls.filter((call) => call[2]?.method === "POST")).toEqual([]);
    expect(input.excerpts[0].text).toBe("Important context");
});

it("keeps ordinary messages compatible without an excerpt-support probe", async () => {
    request.mockResolvedValueOnce({ json: async () => ({ turnId: "ordinary" }) });
    await expect(sendConversationMessage(config, "session", { text: "Hello", clientMessageId: "ordinary-id" })).resolves.toMatchObject({ turnId: "ordinary" });
    expect(request.mock.calls).toHaveLength(1);
    expect(request.mock.lastCall?.[2].method).toBe("POST");
});
