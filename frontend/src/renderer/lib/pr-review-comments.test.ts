import { describe, expect, it } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { botThreadReviewers, prConversationComments, prHasReviewActivity } from "./pr-review-comments";

const base = {
	number: 7,
	url: "https://github.com/acme/repo/pull/7",
	review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [], reviews: [] },
} as unknown as SessionPRSummary;

const thread = (id: string, author: string, isBot: boolean, resolved = false, url = `https://example.com/${id}`) => ({
	id,
	path: "src/a.ts",
	line: 3,
	resolved,
	comments: [{ id: `${id}-c`, author, isBot, body: `${id} body`, url }],
});

describe("botThreadReviewers", () => {
	it("groups bot threads under the bot's review name, open and resolved apart, leaving people's threads to the summary", () => {
		const pr = {
			...base,
			review: { ...base.review, reviews: [{ reviewerId: "coderabbitai", verdict: "none", autoInjectReview: true, reviewUrl: "https://example.com/review", submittedAt: "" }] },
			threads: [thread("t1", "coderabbitai[bot]", true), thread("t2", "coderabbitai[bot]", true, true), thread("t3", "maya", false)],
		} as unknown as SessionPRSummary;
		const { unresolved, resolved } = botThreadReviewers(pr);
		expect(unresolved).toEqual([{
			count: 1,
			isBot: true,
			reviewerId: "coderabbitai",
			reviewUrl: "https://example.com/review",
			links: [{ autoInjectReview: false, body: "t1 body", file: "src/a.ts", line: 3, url: "https://example.com/t1" }],
		}]);
		expect(resolved.map((reviewer) => reviewer.links.map((link) => link.url))).toEqual([["https://example.com/t2"]]);
	});

	it("does not repeat a comment the summary already lists", () => {
		const pr = {
			...base,
			review: { ...base.review, unresolvedBy: [{ reviewerId: "coderabbitai", count: 1, isBot: true, links: [{ autoInjectReview: true, url: "https://example.com/t1" }] }] },
			threads: [thread("t1", "coderabbitai[bot]", true)],
		} as unknown as SessionPRSummary;
		expect(botThreadReviewers(pr).unresolved).toEqual([]);
	});
});

describe("prHasReviewActivity", () => {
	it("counts a PR with only a top-level comment, but not an empty one", () => {
		const conversation = [{ id: "1", author: "coderabbitai", isBot: true, body: "## Walkthrough" }, { id: "2", author: "x", isBot: false, body: "  " }];
		const pr = { ...base, conversation } as unknown as SessionPRSummary;
		expect(prConversationComments(pr)).toHaveLength(1);
		expect(prHasReviewActivity(pr)).toBe(true);
		expect(prHasReviewActivity(base)).toBe(false);
	});
});
