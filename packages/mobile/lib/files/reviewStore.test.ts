import { describe, expect, it } from "vitest";
import { addReviewComment, markReviewCommentsSent, queuedComments, removeReviewComments, reviewComments, reviewKey } from "./reviewStore";

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
