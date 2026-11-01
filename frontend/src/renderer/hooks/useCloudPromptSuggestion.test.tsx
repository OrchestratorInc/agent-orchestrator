import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { chatFixtureEmpty } from "../lib/chat-fixture";
import type { ConversationItem, ConversationSnapshot } from "../types/conversation";
import type { WorkspaceSession } from "../types/workspace";

const { suggestChatPrompt } = vi.hoisted(() => ({ suggestChatPrompt: vi.fn() }));
vi.mock("./useCloudCp", () => ({
	useCloudCp: () => ({ client: { suggestChatPrompt }, ready: true, baseUrl: "https://cp.test", userId: "user-1" }),
}));
import { promptSuggestionMessages, useCloudPromptSuggestion } from "./useCloudPromptSuggestion";

function message(id: string, role: "user" | "assistant", text: string, sequence: number): ConversationItem {
	return { kind: "message", id, role, origin: role === "user" ? "human" : "provider", text, sequence, revision: 1, streaming: false, createdAt: "" };
}

const settled: ConversationSnapshot = {
	...chatFixtureEmpty,
	conversationId: "cloud:session-1",
	sessionId: "session-1",
	controller: { state: "ready" },
	turns: [{ id: "turn-1", state: "completed", requestedAt: "2026-11-01T00:00:00Z" }],
	items: [message("prompt-1", "user", "Fix the race.", 1), message("reply-1", "assistant", "Fixed it.", 2)],
};

const session = { id: "session-1", runtimeConnected: true, cloud: { orgId: "org-1" } } as WorkspaceSession;

function wrapper({ children }: { children: ReactNode }) {
	return <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>;
}

beforeEach(() => {
	suggestChatPrompt.mockReset().mockResolvedValue({ turnId: "turn-1", prompt: " Add a regression test. " });
});

describe("promptSuggestionMessages", () => {
	it("keeps the newest eight text messages, oldest first, and skips activity and empty text", () => {
		const items: ConversationItem[] = Array.from({ length: 10 }, (_, index) =>
			message(`m-${index}`, index % 2 ? "assistant" : "user", `text ${index}`, index));
		items.splice(5, 0, { kind: "activity", id: "a", sequence: 5, revision: 1, activityKind: "command", status: "completed", summary: "ls", createdAt: "" });
		items.push(message("blank", "assistant", "  ", 11));
		const messages = promptSuggestionMessages({ ...settled, items });
		expect(messages).toHaveLength(8);
		expect(messages[0]).toEqual({ role: "user", text: "text 2" });
		expect(messages.at(-1)).toEqual({ role: "assistant", text: "text 9" });
	});
});

describe("useCloudPromptSuggestion", () => {
	it("asks the control plane with the settled turn and recent transcript", async () => {
		const { result } = renderHook(() => useCloudPromptSuggestion(session, settled, true), { wrapper });
		await waitFor(() => expect(result.current?.text).toBe("Add a regression test."));
		expect(suggestChatPrompt).toHaveBeenCalledWith("org-1", "session-1", {
			turnId: "turn-1",
			messages: [{ role: "user", text: "Fix the race." }, { role: "assistant", text: "Fixed it." }],
		}, expect.objectContaining({ signal: expect.any(AbortSignal) }));
	});

	it("shows nothing when the worker cannot suggest or is not connected", async () => {
		suggestChatPrompt.mockRejectedValue(new Error("unsupported worker transport request"));
		const { result } = renderHook(() => useCloudPromptSuggestion(session, settled, true), { wrapper });
		await waitFor(() => expect(suggestChatPrompt).toHaveBeenCalled());
		expect(result.current).toBeUndefined();
		suggestChatPrompt.mockClear();
		renderHook(() => useCloudPromptSuggestion({ ...session, runtimeConnected: false }, settled, true), { wrapper });
		expect(suggestChatPrompt).not.toHaveBeenCalled();
	});

	it("drops an answer for another turn", async () => {
		suggestChatPrompt.mockResolvedValue({ turnId: "turn-0", prompt: "Old" });
		const { result } = renderHook(() => useCloudPromptSuggestion(session, settled, true), { wrapper });
		await waitFor(() => expect(suggestChatPrompt).toHaveBeenCalled());
		expect(result.current).toBeUndefined();
	});
});
