import type { ClientEvent } from "@aoagents/cloud-client";
import { describe, expect, it } from "vitest";
import { createCloudConversationReplayCache } from "./conversationReplay";
const event = (sequence: number, text = "one"): ClientEvent => ({ sessionId: "s1", sequence, type: "chat.user_message", createdAt: "2026-10-13T00:00:00Z", payload: { text } });
describe("Cloud replay cache", () => {
	it("deduplicates overlap and separates account scopes", () => {
		const cache = createCloudConversationReplayCache();
		cache.apply("account-a", "s1", [event(1)], 1); cache.apply("account-a", "s1", [event(1)], 1);
		expect(cache.get("account-a", "s1")?.events).toHaveLength(1); expect(cache.get("account-b", "s1")).toBeUndefined();
	});
	it("keeps the cursor monotonic with its matching history despite out-of-order responses", () => {
		const cache = createCloudConversationReplayCache();
		cache.apply("a", "s1", [event(3), event(1)], 3); cache.apply("a", "s1", [event(2), event(1, "stale")], 2);
		expect(cache.get("a", "s1")).toMatchObject({ cursor: 3, events: [{ sequence: 1, payload: { text: "one" } }, { sequence: 2 }, { sequence: 3 }] });
	});
	it("does not store a cursor beyond the events actually adopted", () => {
		const cache = createCloudConversationReplayCache(); cache.apply("a", "s1", [event(1)], 100);
		expect(cache.get("a", "s1")?.cursor).toBe(1);
	});
	it("evicts history and cursor together, retaining a pinned active transcript", () => {
		const cache = createCloudConversationReplayCache(1);
		const unpin = cache.pin("a", "s1"); cache.apply("a", "s1", [event(1, "large ".repeat(400_000))], 1);
		expect(cache.get("a", "s1")?.events).toHaveLength(1);
		unpin(); expect(cache.get("a", "s1")).toBeUndefined();
		cache.apply("a", "s1", [event(1)], 1); cache.apply("a", "s2", [{ ...event(2), sessionId: "s2" }], 2);
		expect(cache.get("a", "s1")).toBeUndefined(); expect(cache.get("a", "s2")?.cursor).toBe(2);
	});
	it("clears only one scope without exposing mutable internal arrays", () => {
		const cache = createCloudConversationReplayCache(); cache.apply("a", "s1", [event(1)], 1); cache.apply("b", "s1", [event(1)], 1);
		cache.get("a", "s1")!.events.push(event(2)); expect(cache.get("a", "s1")?.events).toHaveLength(1);
		cache.clearScope("a"); expect(cache.get("a", "s1")).toBeUndefined(); expect(cache.get("b", "s1")).toBeDefined();
	});
});
