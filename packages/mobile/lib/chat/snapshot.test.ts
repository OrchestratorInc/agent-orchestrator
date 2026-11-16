import { describe, expect, it } from "vitest";
import * as snapshotModule from "./snapshot";

const { cachedConversationState, createConversationPageCache, discardHistoricalPages, mergeConversationPages, olderPageFits, withLivePage } = snapshotModule;
type ConversationPage = snapshotModule.ConversationPage;

function page(overrides: Partial<ConversationPage> = {}): ConversationPage {
	return {
		conversationId: "conv-1",
		sessionId: "session-1",
		harness: "codex",
		mode: "chat",
		controller: { state: "ready" },
		latestSequence: 3,
		oldestSequence: 1,
		hasMoreBefore: false,
		turns: [],
		items: [],
		settings: {},
		...overrides,
	};
}

describe("mobile conversation pagination", () => {
	it("keeps chronological order and the oldest page cursor", () => {
		const merged = mergeConversationPages([
			page({ oldestSequence: 3, hasMoreBefore: true, items: [{ kind: "message", id: "m3", sequence: 3, revision: 1, role: "assistant", origin: "provider", text: "new", streaming: false, createdAt: "2026-01-01" }] }),
			page({ oldestSequence: 1, hasMoreBefore: false, items: [{ kind: "message", id: "m1", sequence: 1, revision: 1, role: "user", origin: "human", text: "old", streaming: false, createdAt: "2026-01-01" }] }),
		]);
		expect(merged?.items.map((item) => item.id)).toEqual(["m1", "m3"]);
		expect(merged?.oldestSequence).toBe(1);
		expect(merged?.hasMoreBefore).toBe(false);
	});

	it("lets the live page replace an overlapping streaming revision", () => {
		const historical = { kind: "message" as const, id: "m2", sequence: 2, revision: 1, role: "assistant" as const, origin: "provider" as const, text: "hel", streaming: true, createdAt: "2026-01-01" };
		const live = { ...historical, revision: 2, text: "hello", streaming: false };
		const merged = mergeConversationPages([page({ items: [live] }), page({ items: [historical] })]);
		expect(merged?.items).toEqual([live]);
	});

	it("drops stale historical rows before a rollback projection is reloaded", () => {
		const live = page({ oldestSequence: 3, hasMoreBefore: true });
		const historical = page({ oldestSequence: 1, hasMoreBefore: false });
		expect(discardHistoricalPages([live, historical])).toEqual([live]);
	});
});

describe("live page and branch changes", () => {
	const message = (id: string, sequence: number) => ({ kind: "message" as const, id, sequence, revision: 1, role: "user" as const, origin: "human" as const, text: id, streaming: false, createdAt: "2026-01-01" });

	it("keeps loaded history across refreshes of the same branch", () => {
		const live = page({ activeBranchId: "root", items: [message("new", 5)] });
		const older = page({ activeBranchId: "root", items: [message("old", 1)] });
		const refreshed = page({ activeBranchId: "root", items: [message("new", 5)] });
		expect(withLivePage([live, older], refreshed)).toEqual([refreshed, older]);
	});

	it("drops source-branch history once an edit, even an uncertain one, activates another branch", () => {
		const live = page({ activeBranchId: "root", items: [message("source-new", 5)] });
		const older = page({ activeBranchId: "root", items: [message("source-old", 1)] });
		const replacement = page({ activeBranchId: "child", items: [message("replacement", 6)] });
		const pages = withLivePage([live, older], replacement);
		expect(mergeConversationPages(pages)?.items.map((item) => item.id)).toEqual(["replacement"]);
	});

	it("starts over for another conversation and keeps history when an older host omits the branch", () => {
		const older = page({ items: [message("old", 1)] });
		expect(withLivePage([page(), older], page({ conversationId: "conv-2" }))).toHaveLength(1);
		expect(withLivePage([page(), older], page())).toHaveLength(2);
	});

	it("refuses an older page that loaded for a branch no longer active", () => {
		expect(olderPageFits([page({ activeBranchId: "child" })], page({ activeBranchId: "root" }))).toBe(false);
		expect(olderPageFits([page({ activeBranchId: "child" })], page({ activeBranchId: "child" }))).toBe(true);
		expect(olderPageFits([], page())).toBe(false);
	});
});

describe("mobile conversation reopen cache", () => {
	it("retains multiple lightweight sessions within the mobile memory budget", () => {
		const createMobileCache = (
			snapshotModule as unknown as {
				createMobileConversationPageCache?: (weigh: (page: ConversationPage) => number) => ReturnType<typeof createConversationPageCache>;
			}
		).createMobileConversationPageCache;
		const cache = createMobileCache?.(() => 1);
		for (let index = 1; index <= 5; index += 1) {
			cache?.set(`session-${index}`, [page({ sessionId: `session-${index}` })]);
		}

		expect(cache?.get("session-1")?.[0]?.sessionId).toBe("session-1");
		expect(cache?.get("session-5")?.[0]?.sessionId).toBe("session-5");
	});

	it("starts with cached content visible while the live page revalidates", () => {
		const cache = createConversationPageCache(2);
		const live = page({ items: [{ kind: "message", id: "m1", sequence: 1, revision: 1, role: "assistant", origin: "provider", text: "Cached", streaming: false, createdAt: "2026-01-01" }] });
		cache.set("server/session-1", [live]);

		expect(cachedConversationState(cache, "server/session-1")).toEqual({
			pages: [live],
			loading: false,
		});
	});

	it("reuses only the live page instead of retaining expanded history", () => {
		const cache = createConversationPageCache(2);
		const live = page({ oldestSequence: 50, hasMoreBefore: true });
		const history = page({ oldestSequence: 1, hasMoreBefore: false });

		cache.set("server/session-1", [live, history]);

		expect(cache.get("server/session-1")).toEqual([live]);
	});

	it("evicts the least recently used session when bounded", () => {
		const cache = createConversationPageCache(2);
		cache.set("session-1", [page({ sessionId: "session-1" })]);
		cache.set("session-2", [page({ sessionId: "session-2" })]);
		cache.get("session-1");
		cache.set("session-3", [page({ sessionId: "session-3" })]);

		expect(cache.get("session-1")?.[0]?.sessionId).toBe("session-1");
		expect(cache.get("session-2")).toBeUndefined();
		expect(cache.get("session-3")?.[0]?.sessionId).toBe("session-3");
	});

	it("evicts retained pages by memory weight before the entry limit", () => {
		const cache = createConversationPageCache(3, 10, (value) => value.sessionId === "small" ? 4 : 7);
		cache.set("large", [page({ sessionId: "large" })]);
		cache.set("small", [page({ sessionId: "small" })]);

		expect(cache.get("large")).toBeUndefined();
		expect(cache.get("small")?.[0]?.sessionId).toBe("small");
	});

	it("does not retain a single page larger than the memory budget", () => {
		const cache = createConversationPageCache(3, 5, () => 6);
		cache.set("oversized", [page({ sessionId: "oversized" })]);

		expect(cache.get("oversized")).toBeUndefined();
	});
});
