import { describe, expect, it } from "vitest";
import { addReviewComment, markReviewCommentsSent, queuedComments, removeReviewComments, restoreReviews, REVIEW_KEEP_COMMENTS, REVIEW_KEEP_MS, reviewComments, reviewKey, storedReviews } from "./reviewStore";

const target = { path: "a.ts", side: "new" as const, line: 3 };

describe("review comments", () => {
	it("keeps sent comments on their lines and queues only unsent ones", () => {
		const key = reviewKey("host", "store-test");
		const first = addReviewComment(key, { label: "a.ts · Line 3", feedback: "rename", target, anchor: { side: "new", line: 3 } });
		const second = addReviewComment(key, { label: "a.ts · Line 9", feedback: "why?", target });
		expect(first).toMatchObject({ status: "queued" });

		markReviewCommentsSent(key, [first.id]);
		expect(reviewComments(key).map((comment) => comment.status)).toEqual(["sent", "queued"]);
		expect(queuedComments(reviewComments(key)).map((comment) => comment.id)).toEqual([second.id]);

		removeReviewComments(key, [first.id, second.id]);
		expect(reviewComments(key)).toEqual([]);
	});

	it("records a comment sent straight away as already sent", () => {
		const key = reviewKey(undefined, "store-sent");
		expect(addReviewComment(key, { label: "x", feedback: "now", target, status: "sent" })).toMatchObject({ status: "sent" });
		expect(queuedComments(reviewComments(key))).toEqual([]);
	});
});

describe("review comments on the phone", () => {
	it("come back after the app restarts, with what was written since launch after them", () => {
		const key = reviewKey("host", "restore-test");
		const written = addReviewComment(key, { label: "a.ts · Line 3", feedback: "rename this", target });
		const saved = JSON.parse(JSON.stringify(storedReviews()));
		removeReviewComments(key, [written.id]); // the app restarts: memory is empty again
		const sinceLaunch = addReviewComment(key, { label: "b.ts", feedback: "and this", target });

		restoreReviews(saved);
		expect(reviewComments(key).map((comment) => comment.feedback)).toEqual(["rename this", "and this"]);
		// Restoring twice does not double them.
		restoreReviews(saved);
		expect(reviewComments(key)).toHaveLength(2);
		removeReviewComments(key, [written.id, sinceLaunch.id]);
	});

	it("never collide with new comments' ids after a restart", () => {
		const key = reviewKey("host", "id-test");
		const a = addReviewComment(key, { label: "a", feedback: "a", target });
		const b = addReviewComment(key, { label: "b", feedback: "b", target });
		expect(a.id).not.toBe(b.id);
		expect(a.id).not.toBe("c1");
		removeReviewComments(key, [a.id, b.id]);
	});

	it("drop a review left alone for a week, and anything unreadable", () => {
		const now = Date.now();
		const stale = { key: "host|stale", changedAt: now - REVIEW_KEEP_MS, comments: [{ id: "x", label: "x", feedback: "x", target, status: "queued" }] };
		const broken = { key: "host|broken", changedAt: now, comments: [{ id: 1 }] };
		restoreReviews({ v: 1, reviews: [stale, broken] }, now);
		restoreReviews("garbage", now);
		expect(reviewComments("host|stale")).toEqual([]);
		expect(reviewComments("host|broken")).toEqual([]);
	});

	it("keep only the newest comments when there are too many", () => {
		const key = reviewKey("host", "cap-test");
		const ids = Array.from({ length: REVIEW_KEEP_COMMENTS + 5 }, (_, n) => addReviewComment(key, { label: `${n}`, feedback: `${n}`, target }).id);
		const kept = storedReviews().reviews.find((review) => review.key === key)!;
		expect(kept.comments).toHaveLength(REVIEW_KEEP_COMMENTS);
		expect(kept.comments.at(-1)?.feedback).toBe(`${REVIEW_KEEP_COMMENTS + 4}`);
		removeReviewComments(key, ids);
	});
});
