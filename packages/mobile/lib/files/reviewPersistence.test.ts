import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const store = vi.hoisted(() => ({
	items: new Map<string, string>(),
	pendingRead: null as null | { resolve(value: string | null): void },
	/** How many of the next writes fail. */
	failWrites: 0,
	writes: 0,
}));
const appState = vi.hoisted(() => ({
	currentState: "active",
	listeners: [] as Array<(state: string) => void>,
	set(state: string) {
		this.currentState = state;
		for (const listener of this.listeners) listener(state);
	},
}));
vi.mock("@react-native-async-storage/async-storage", () => {
	const failing = () => {
		store.writes++;
		if (store.failWrites > 0) {
			store.failWrites--;
			throw new Error("storage full");
		}
	};
	return { default: {
		// The first read waits until the test lets it answer.
		getItem: (key: string) => new Promise<string | null>((resolve) => {
			store.pendingRead = { resolve: (value) => resolve(value ?? store.items.get(key) ?? null) };
		}),
		setItem: async (key: string, value: string) => { failing(); store.items.set(key, value); },
		removeItem: async (key: string) => { failing(); store.items.delete(key); },
	} };
});
vi.mock("react-native", () => ({ AppState: {
	get currentState() { return appState.currentState; },
	addEventListener: (_: string, listener: (state: string) => void) => {
		appState.listeners.push(listener);
		return { remove: () => {} };
	},
} }));

const target = { path: "a.ts", side: "new" as const, line: 3 };

beforeEach(() => {
	vi.resetModules();
	vi.useFakeTimers();
	store.items.clear();
	store.pendingRead = null;
	store.failWrites = 0;
	store.writes = 0;
	appState.currentState = "active";
	appState.listeners = [];
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

	// Left during the read, before anything listened for leaving: no delay, the
	// phone may close the app before a timer would fire.
	it("writes a comment made during the read at once when the read lands with the app in the background", async () => {
		const { REVIEWS_STORAGE_KEY, startReviewPersistence } = await import("./reviewPersistence");
		const { addReviewComment, reviewKey } = await import("./reviewStore");
		const started = startReviewPersistence();
		addReviewComment(reviewKey("host", "s1"), { label: "x", feedback: "written at launch", target });
		appState.currentState = "background";
		store.pendingRead!.resolve(null);
		await started;
		// Settles promises only: no timer is allowed to run.
		await vi.advanceTimersByTimeAsync(0);
		expect(store.items.has(REVIEWS_STORAGE_KEY)).toBe(true);
	});

	it("keeps a save that failed and tries it again", async () => {
		const { REVIEWS_STORAGE_KEY, startReviewPersistence } = await import("./reviewPersistence");
		const { addReviewComment, reviewKey } = await import("./reviewStore");
		const started = startReviewPersistence();
		store.pendingRead!.resolve(null);
		await started;
		store.failWrites = 1;
		addReviewComment(reviewKey("host", "s1"), { label: "x", feedback: "kept", target });
		await vi.advanceTimersByTimeAsync(300);
		expect(store.writes).toBe(1);
		expect(store.items.has(REVIEWS_STORAGE_KEY)).toBe(false);
		await vi.advanceTimersByTimeAsync(1_000);
		expect(store.writes).toBe(2);
		expect(store.items.has(REVIEWS_STORAGE_KEY)).toBe(true);
	});

	it("writes a save that kept failing again when the app leaves the foreground", async () => {
		const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
		const { REVIEWS_STORAGE_KEY, startReviewPersistence } = await import("./reviewPersistence");
		const { addReviewComment, reviewKey } = await import("./reviewStore");
		const started = startReviewPersistence();
		store.pendingRead!.resolve(null);
		await started;
		store.failWrites = 4;
		addReviewComment(reviewKey("host", "s1"), { label: "x", feedback: "kept", target });
		await vi.advanceTimersByTimeAsync(60_000);
		// The first try and three retries, then reported and no more timers.
		expect(store.writes).toBe(4);
		expect(warn).toHaveBeenCalled();
		appState.set("background");
		await vi.advanceTimersByTimeAsync(0);
		expect(store.writes).toBe(5);
		expect(store.items.has(REVIEWS_STORAGE_KEY)).toBe(true);
		warn.mockRestore();
	});

	it("flushes a waiting change, and resolves only once it is on the phone", async () => {
		const { REVIEWS_STORAGE_KEY, flushReviewPersistence, startReviewPersistence } = await import("./reviewPersistence");
		const { addReviewComment, forgetHostReviews, reviewKey } = await import("./reviewStore");
		const started = startReviewPersistence();
		store.pendingRead!.resolve(null);
		await started;
		addReviewComment(reviewKey("host", "s1"), { label: "x", feedback: "a", target });
		await flushReviewPersistence();
		expect(store.items.has(REVIEWS_STORAGE_KEY)).toBe(true);
		forgetHostReviews("host");
		await flushReviewPersistence();
		expect(store.items.has(REVIEWS_STORAGE_KEY)).toBe(false);
		// A flush whose write fails says so, and the next one writes it.
		addReviewComment(reviewKey("host", "s2"), { label: "x", feedback: "b", target });
		store.failWrites = 1;
		await expect(flushReviewPersistence()).rejects.toThrow("storage full");
		await flushReviewPersistence();
		expect(store.items.has(REVIEWS_STORAGE_KEY)).toBe(true);
	});
	// #8367: forgetting resolves once the removal is on the phone, even while
	// the saved reviews are still being read back at launch.
	it("waits for the launch read before flushing, then writes the removal", async () => {
		const { REVIEWS_STORAGE_KEY, flushReviewPersistence, startReviewPersistence } = await import("./reviewPersistence");
		const { forgetHostReviews, reviewKey } = await import("./reviewStore");
		store.items.set(REVIEWS_STORAGE_KEY, JSON.stringify({ v: 1, reviews: [
			{ key: reviewKey("machine-a", "s1"), changedAt: Date.now(), comments: [{ id: "c1", label: "x", feedback: "for-a", target, status: "queued" }] },
		] }));
		void startReviewPersistence();
		forgetHostReviews("machine-a");
		let flushed = false;
		const flushing = flushReviewPersistence().then(() => { flushed = true; });
		await vi.advanceTimersByTimeAsync(0);
		expect(flushed).toBe(false);
		store.pendingRead!.resolve(null);
		await flushing;
		expect(store.items.has(REVIEWS_STORAGE_KEY)).toBe(false);
	});

	it("resolves at once before persistence started", async () => {
		const { flushReviewPersistence } = await import("./reviewPersistence");
		await expect(flushReviewPersistence()).resolves.toBeUndefined();
	});

	it("rejects a flush when the save already running fails", async () => {
		const { flushReviewPersistence, startReviewPersistence } = await import("./reviewPersistence");
		const { addReviewComment, reviewKey } = await import("./reviewStore");
		const started = startReviewPersistence();
		store.pendingRead!.resolve(null);
		await started;
		store.failWrites = 1;
		addReviewComment(reviewKey("host", "s1"), { label: "x", feedback: "a", target });
		// The delayed save starts, but has not finished, when the flush runs.
		vi.advanceTimersByTime(300);
		await expect(flushReviewPersistence()).rejects.toThrow("storage full");
	});
});
