import { describe, expect, it } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { botThreadReviewers, mergeReviewers, prConversationComments, prHasReviewActivity } from "./pr-review-comments";

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
		const conversation = [{ id: "1", author: "maya", isBot: false, body: "Please update the docs." }, { id: "2", author: "x", isBot: false, body: "  " }];
		const pr = { ...base, conversation } as unknown as SessionPRSummary;
		expect(prConversationComments(pr)).toHaveLength(1);
		expect(prHasReviewActivity(pr)).toBe(true);
		expect(prHasReviewActivity(base)).toBe(false);
	});

	it("leaves out the PR author's own top-level comments", () => {
		const conversation = [{ id: "1", author: "@Aditi", isBot: false, body: "Pushed a fix." }];
		const pr = { ...base, author: "aditi", conversation } as unknown as SessionPRSummary;
		expect(prConversationComments(pr)).toEqual([]);
		expect(prHasReviewActivity(pr)).toBe(false);
	});
});

describe("mergeReviewers", () => {
	it("keeps one bucket per reviewer, with every comment from both lists", () => {
		const summary = [{ reviewerId: "coderabbitai[bot]", count: 1, isBot: true, links: [{ autoInjectReview: true, url: "https://example.com/a" }] }];
		const threads = [{ reviewerId: "coderabbitai", count: 1, isBot: true, links: [{ autoInjectReview: false, url: "https://example.com/b" }] }];
		const merged = mergeReviewers(summary, threads);
		expect(merged).toHaveLength(1);
		expect(merged[0]).toMatchObject({ reviewerId: "coderabbitai[bot]", count: 2 });
		expect(merged[0].links.map((link) => link.url)).toEqual(["https://example.com/a", "https://example.com/b"]);
		expect(summary[0].links).toHaveLength(1);
	});
});

describe("prHasReviewActivity, for the PR author", () => {
	it("does not count the author's own reviews, threads or comments", () => {
		const pr = {
			...base,
			author: "aditi",
			review: { ...base.review, reviews: [{ reviewerId: "aditi" }], unresolvedBy: [{ reviewerId: "Aditi", count: 1, isBot: false, links: [] }] },
			threads: [thread("t1", "aditi", false)],
			conversation: [{ id: "1", author: "aditi", isBot: false, body: "note" }],
		} as unknown as SessionPRSummary;
		expect(prHasReviewActivity(pr)).toBe(false);
	});

	it("counts a bot's inline comment", () => {
		const pr = { ...base, author: "aditi", threads: [thread("t1", "coderabbitai[bot]", true)] } as unknown as SessionPRSummary;
		expect(prHasReviewActivity(pr)).toBe(true);
	});
});

describe("the PR author, against a bot of the same name", () => {
	it("keeps release-helper[bot]'s comments on release-helper's PR", () => {
		const conversation = [{ id: "1", author: "release-helper[bot]", isBot: true, body: "Release notes drafted." }];
		const pr = { ...base, author: "release-helper", conversation, review: { ...base.review, reviews: [{ reviewerId: "release-helper[bot]" }] } } as unknown as SessionPRSummary;
		expect(prConversationComments(pr)).toHaveLength(1);
		expect(prHasReviewActivity(pr)).toBe(true);
	});
});

describe("prConversationComments, for bots", () => {
	const conversation = [
		{ id: "1", author: "coderabbitai", isBot: true, body: "## Walkthrough" },
		{ id: "2", author: "vercel[bot]", isBot: true, body: "Preview deployed." },
		{ id: "3", author: "codecov[bot]", isBot: true, body: "Coverage 91%." },
	];

	it("keeps a bot's comment only when that bot also reviewed the PR", () => {
		const reviewed = { ...base, conversation, review: { ...base.review, reviews: [{ reviewerId: "coderabbitai[bot]" }] } } as unknown as SessionPRSummary;
		expect(prConversationComments(reviewed).map((comment) => comment.id)).toEqual(["1"]);
		const threaded = { ...base, conversation, threads: [thread("t1", "codecov[bot]", true)] } as unknown as SessionPRSummary;
		expect(prConversationComments(threaded).map((comment) => comment.id)).toEqual(["3"]);
	});

	it("does not turn the Reviews tab on for deploy and coverage bots alone", () => {
		const pr = { ...base, conversation } as unknown as SessionPRSummary;
		expect(prConversationComments(pr)).toEqual([]);
		expect(prHasReviewActivity(pr)).toBe(false);
	});
});
