import { describe, expect, it, vi } from "vitest";
import { toSnapshot } from "../components/chat/CloudSessionChatSurface";
import type { CloudCpClientEvent } from "../lib/cloud-cp";
import type { WorkspaceSession } from "../types/workspace";
import { asChatEvents, loadReviewChatEvents } from "./useCloudReviewerConversation";

function event(sequence: number, type: string, payload: Record<string, unknown>): CloudCpClientEvent {
	return { sessionId: "session-1", sequence, type, payload, createdAt: "2026-11-01T00:00:00Z" };
}

describe("asChatEvents", () => {
	it("reads a review conversation as a chat: review.* types and reviewTurnId become chat.* and turnId", () => {
		const events = asChatEvents([
			event(1, "review.user_message", { reviewRunId: "run-1", reviewTurnId: "t-1", text: "Review this PR." }),
			event(2, "review.turn_started", { reviewRunId: "run-1", reviewTurnId: "t-1" }),
			event(3, "review.assistant_delta", { reviewRunId: "run-1", reviewTurnId: "t-1", text: "Looks good", itemId: "a" }),
			event(4, "chat.user_message", { turnId: "main", text: "not the reviewer's" }),
		]);
		expect(events.map((value) => value.type)).toEqual(["chat.user_message", "chat.turn_started", "chat.assistant_delta"]);
		expect(events[0].payload).toEqual({ reviewRunId: "run-1", turnId: "t-1", text: "Review this PR." });
		const snapshot = toSnapshot({ id: "session-1", provider: "codex" } as WorkspaceSession, events);
		expect(snapshot.turns).toEqual([expect.objectContaining({ id: "t-1", state: "running" })]);
		expect(snapshot.items.map((item) => item.kind === "message" ? [item.role, item.text] : item.kind)).toEqual([
			["user", "Review this PR."], ["assistant", "Looks good"],
		]);
	});
});

describe("loadReviewChatEvents", () => {
	it("pages after the newest event it holds", async () => {
		const listReviewChatEvents = vi.fn()
			.mockResolvedValueOnce({ events: [event(5, "review.turn_started", {})], hasMore: true, nextAfter: 5 })
			.mockResolvedValueOnce({ events: [event(6, "review.turn_completed", {})], hasMore: false, nextAfter: 6 });
		const events = await loadReviewChatEvents({ listReviewChatEvents }, "org-1", "session-1", "run-1", [event(4, "review.user_message", {})]);
		expect(events.map((value) => value.sequence)).toEqual([4, 5, 6]);
		expect(listReviewChatEvents).toHaveBeenNthCalledWith(1, "org-1", "session-1", "run-1", { after: 4, limit: 500 });
		expect(listReviewChatEvents).toHaveBeenNthCalledWith(2, "org-1", "session-1", "run-1", { after: 5, limit: 500 });
	});
});
