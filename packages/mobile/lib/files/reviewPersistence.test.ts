import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const store = vi.hoisted(() => ({
	items: new Map<string, string>(),
	pendingRead: null as null | { resolve(value: string | null): void },
}));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	// The first read waits until the test lets it answer.
	getItem: (key: string) => new Promise<string | null>((resolve) => {
		store.pendingRead = { resolve: (value) => resolve(value ?? store.items.get(key) ?? null) };
	}),
	setItem: async (key: string, value: string) => { store.items.set(key, value); },
	removeItem: async (key: string) => { store.items.delete(key); },
} }));
vi.mock("react-native", () => ({ AppState: { addEventListener: () => ({ remove: () => {} }) } }));

const target = { path: "a.ts", side: "new" as const, line: 3 };

beforeEach(() => {
	vi.resetModules();
	vi.useFakeTimers();
	store.items.clear();
	store.pendingRead = null;
});
afterEach(() => vi.useRealTimers());

describe("saving review comments", () => {
	it("saves a comment written while the saved reviews are still being read, with them", async () => {
		const { REVIEWS_STORAGE_KEY, startReviewPersistence } = await import("./reviewPersistence");
		const { addReviewComment, reviewKey } = await import("./reviewStore");
		const savedKey = reviewKey("host", "saved");
		store.items.set(REVIEWS_STORAGE_KEY, JSON.stringify({ v: 1, reviews: [{ key: savedKey, changedAt: Date.now(), comments: [{ id: "c-old", label: "a.ts · Line 1", feedback: "from before", target, status: "queued" }] }] }));
		const started = startReviewPersistence();
		// Written before the read answers.
		addReviewComment(reviewKey("host", "new"), { label: "a.ts · Line 3", feedback: "written at launch", target });
		await vi.advanceTimersByTimeAsync(1_000);
		// Nothing written yet: that would have replaced the saved review.
		expect(JSON.parse(store.items.get(REVIEWS_STORAGE_KEY)!).reviews).toHaveLength(1);
		store.pendingRead!.resolve(null);
		await started;
		await vi.advanceTimersByTimeAsync(1_000);
		const saved = JSON.parse(store.items.get(REVIEWS_STORAGE_KEY)!) as { reviews: Array<{ key: string; comments: Array<{ feedback: string }> }> };
		expect(saved.reviews.flatMap((review) => review.comments.map((comment) => comment.feedback)).sort()).toEqual(["from before", "written at launch"]);
	});

	it("forgets a machine's reviews, on the phone too", async () => {
		const { REVIEWS_STORAGE_KEY, startReviewPersistence } = await import("./reviewPersistence");
		const { addReviewComment, forgetHostReviews, reviewComments, reviewKey } = await import("./reviewStore");
		const started = startReviewPersistence();
		store.pendingRead!.resolve(null);
		await started;
		addReviewComment(reviewKey("machine-a", "s1"), { label: "x", feedback: "a's", target });
		addReviewComment(reviewKey("machine-b", "s1"), { label: "x", feedback: "b's", target });
		await vi.advanceTimersByTimeAsync(1_000);
		forgetHostReviews("machine-a");
		await vi.advanceTimersByTimeAsync(1_000);
		expect(reviewComments(reviewKey("machine-a", "s1"))).toEqual([]);
		const saved = JSON.parse(store.items.get(REVIEWS_STORAGE_KEY)!) as { reviews: Array<{ key: string }> };
		expect(saved.reviews.map((review) => review.key)).toEqual([reviewKey("machine-b", "s1")]);
	});

	// Forgotten while the launch was still reading the saved reviews: the read
	// landing must not put that machine's comments back, in memory or on the phone.
	it("does not bring back a machine forgotten while the saved reviews were being read", async () => {
		const { REVIEWS_STORAGE_KEY, startReviewPersistence } = await import("./reviewPersistence");
		const { forgetHostReviews, reviewComments, reviewKey } = await import("./reviewStore");
		const comment = (id: string) => ({ id, label: "a.ts · Line 1", feedback: id, target, status: "queued" });
		store.items.set(REVIEWS_STORAGE_KEY, JSON.stringify({ v: 1, reviews: [
			{ key: reviewKey("machine-a", "s1"), changedAt: Date.now(), comments: [comment("for-a")] },
			{ key: reviewKey("machine-b", "s1"), changedAt: Date.now(), comments: [comment("for-b")] },
		] }));
		const started = startReviewPersistence();
		forgetHostReviews("machine-a");
		store.pendingRead!.resolve(null);
		await started;
		await vi.advanceTimersByTimeAsync(1_000);
		expect(reviewComments(reviewKey("machine-a", "s1"))).toEqual([]);
		expect(reviewComments(reviewKey("machine-b", "s1"))).toHaveLength(1);
		const saved = JSON.parse(store.items.get(REVIEWS_STORAGE_KEY)!) as { reviews: Array<{ key: string }> };
		expect(saved.reviews.map((review) => review.key)).toEqual([reviewKey("machine-b", "s1")]);
	});

	// Nothing in memory to forget yet, so no change was told: what was saved
	// must still be written over once the read lands.
	it("writes over the saved reviews of a machine forgotten before any were restored", async () => {
		const { REVIEWS_STORAGE_KEY, startReviewPersistence } = await import("./reviewPersistence");
		const { forgetHostReviews, reviewKey } = await import("./reviewStore");
		store.items.set(REVIEWS_STORAGE_KEY, JSON.stringify({ v: 1, reviews: [
			{ key: reviewKey("machine-a", "s1"), changedAt: Date.now(), comments: [{ id: "c1", label: "x", feedback: "for-a", target, status: "queued" }] },
		] }));
		const started = startReviewPersistence();
		forgetHostReviews("machine-a");
		store.pendingRead!.resolve(null);
		await started;
		await vi.advanceTimersByTimeAsync(1_000);
		expect(store.items.has(REVIEWS_STORAGE_KEY)).toBe(false);
	});
});
