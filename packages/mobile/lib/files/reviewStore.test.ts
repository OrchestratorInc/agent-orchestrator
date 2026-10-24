import { describe, expect, it } from "vitest";
import { addReviewComment, clearCloudReviewsForAccount, cloudReviewKey, markReviewCommentsSent, queuedComments, removeReviewComments, reviewComments, reviewKey } from "./reviewStore";

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

	it("isolates Cloud reviews by account, workspace, repository, and session", () => {
		const key = cloudReviewKey('["https://cloud.test","one","org"]', "same", "repo-one");
		addReviewComment(key, { label: "Cloud", feedback: "private", target });
		for (const other of [
			cloudReviewKey('["https://cloud.test","two","org"]', "same", "repo-one"),
			cloudReviewKey('["https://cloud.test","one","other-org"]', "same", "repo-one"),
			cloudReviewKey('["https://cloud.test","one","org"]', "same", "repo-two"),
			cloudReviewKey('["https://cloud.test","one","org"]', "other", "repo-one"),
			reviewKey("org", "same"),
		]) expect(reviewComments(other)).toEqual([]);
		expect(reviewComments(key)).toHaveLength(1);
	});

	it("clears only the signed-out Cloud account's reviews across its workspaces", () => {
		const retired = [cloudReviewKey('["https://cloud.test","cleanup","a"]', "same"), cloudReviewKey('["https://cloud.test","cleanup","b"]', "same")];
		const kept = [cloudReviewKey('["https://other.test","cleanup","a"]', "same"), cloudReviewKey('["https://cloud.test","other","a"]', "same"), reviewKey("cleanup", "same")];
		for (const key of [...retired, ...kept]) addReviewComment(key, { label: "x", feedback: "retain", target });
		clearCloudReviewsForAccount("https://cloud.test/", "cleanup");
		for (const key of retired) expect(reviewComments(key)).toEqual([]);
		for (const key of kept) expect(reviewComments(key)).toHaveLength(1);
	});
});
