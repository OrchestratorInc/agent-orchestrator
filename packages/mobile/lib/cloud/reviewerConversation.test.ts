import { expect, it, vi } from "vitest";
import { loadReviewChatEvents, reviewEventsAsChat } from "./useCloudReviewerConversation";
import type { CloudClientEvent } from "./types";

const event = (sequence: number, type: string, payload: object = {}): CloudClientEvent => ({ sessionId: "s1", sequence, type, payload, createdAt: "2026-11-08T10:00:00Z" });

// The session chat's projection reads a review conversation unchanged once
// its events carry the chat's names.
it("reads a review conversation's events as the session chat's", () => {
	const chat = reviewEventsAsChat([
		event(1, "review.turn_started", { reviewTurnId: "t1" }),
		event(2, "review.assistant_delta", { reviewTurnId: "t1", text: "Looks good" }),
		event(3, "session.status", { status: "working" }),
	]);
	expect(chat.map((item) => item.type)).toEqual(["chat.turn_started", "chat.assistant_delta"]);
	expect(chat[1]?.payload).toEqual({ turnId: "t1", text: "Looks good" });
});

it("pages through everything after what it holds", async () => {
	const listReviewChatEvents = vi.fn()
		.mockResolvedValueOnce({ events: [event(2, "review.turn_started")], hasMore: true, nextAfter: 2 })
		.mockResolvedValueOnce({ events: [event(3, "review.turn_completed")], hasMore: false, nextAfter: 3 });
	const events = await loadReviewChatEvents({ listReviewChatEvents }, "org", "s1", "run-1", [event(1, "review.user_message")]);
	expect(events.map((item) => item.sequence)).toEqual([1, 2, 3]);
	expect(listReviewChatEvents.mock.calls.map((call) => call[3].after)).toEqual([1, 2]);
});

it("stops on a cursor that does not move rather than looping", async () => {
	const listReviewChatEvents = vi.fn().mockResolvedValue({ events: [], hasMore: true, nextAfter: 0 });
	await expect(loadReviewChatEvents({ listReviewChatEvents }, "org", "s1", "run-1", [])).rejects.toThrow("did not advance");
});
