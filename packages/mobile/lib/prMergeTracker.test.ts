import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PR_MERGE_HOLD_MS, prMergeEntry, prMergeKey, resetPRMerges, subscribePRMerges, trackPRMerge } from "./prMergeTracker";

const KEY = prMergeKey("dev", "https://github.com/acme/repo/pull/12");

function deferred() {
	let resolve!: () => void;
	let reject!: (error: unknown) => void;
	const promise = new Promise<void>((yes, no) => { resolve = yes; reject = no; });
	return { promise, resolve, reject };
}

beforeEach(() => { vi.useFakeTimers(); resetPRMerges(); });
afterEach(() => { vi.useRealTimers(); });

describe("trackPRMerge", () => {
	it("holds the merge as merging until the call returns, then as merged", async () => {
		const call = deferred();
		const tracked = trackPRMerge(KEY, () => call.promise);
		expect(prMergeEntry(KEY)).toEqual({ status: "merging" });
		call.resolve();
		await tracked;
		expect(prMergeEntry(KEY)).toMatchObject({ status: "merged" });
	});

	it("keeps a failure, with its error, for a page opened after the call failed", async () => {
		const error = new Error("head changed");
		await expect(trackPRMerge(KEY, () => Promise.reject(error))).rejects.toBe(error);
		expect(prMergeEntry(KEY)).toMatchObject({ status: "failed", error });
	});

	it("lets the outcome go after the hold, so a daemon that never agrees is believed again", async () => {
		await trackPRMerge(KEY, () => Promise.resolve());
		vi.advanceTimersByTime(PR_MERGE_HOLD_MS - 1);
		expect(prMergeEntry(KEY)?.status).toBe("merged");
		vi.advanceTimersByTime(1);
		expect(prMergeEntry(KEY)).toBeUndefined();
	});

	it("does not start a second merge of a pull request already merging", async () => {
		const call = deferred();
		const first = trackPRMerge(KEY, () => call.promise);
		const second = vi.fn(() => Promise.resolve());
		const repeat = trackPRMerge(KEY, second);
		expect(second).not.toHaveBeenCalled();
		call.resolve();
		await Promise.all([first, repeat]);
		expect(prMergeEntry(KEY)?.status).toBe("merged");
	});

	it("tells subscribers about every change, and keeps one snapshot between them", async () => {
		const listener = vi.fn();
		const unsubscribe = subscribePRMerges(listener);
		const call = deferred();
		const tracked = trackPRMerge(KEY, () => call.promise);
		expect(listener).toHaveBeenCalledTimes(1);
		expect(prMergeEntry(KEY)).toBe(prMergeEntry(KEY));
		call.resolve();
		await tracked;
		expect(listener).toHaveBeenCalledTimes(2);
		vi.advanceTimersByTime(PR_MERGE_HOLD_MS);
		expect(listener).toHaveBeenCalledTimes(3);
		unsubscribe();
	});

	it("reads one pull request under each spelling of its URL", () => {
		expect(prMergeKey("dev", "https://github.com/Acme/Repo/pull/12/")).toBe(KEY);
	});

	it("keys each machine's pull request apart", () => {
		expect(prMergeKey("dev", "https://github.com/acme/repo/pull/12")).not.toBe(prMergeKey("laptop", "https://github.com/acme/repo/pull/12"));
	});
});
