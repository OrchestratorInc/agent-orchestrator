import { describe, expect, it } from "vitest";
import { buildReviewBlocks, type ReviewComment } from "../../shared/review/review-comments";
import type { PRReviewState, ReviewRun, SessionPRSummary, SessionReviews } from "./api";
import {
	commentAge,
	commentAvatarUrl,
	commentCanResolve,
	commentLocation,
	commentPreview,
	commentReplyLabel,
	commentReplyOffered,
	commentStartsExpanded,
	isApproval,
	reviewerStatusLine,
	reviewNowAction,
	unsentOpenComments,
} from "./reviewCommentView";

const PR = "https://github.com/acme/app/pull/12";

const comment = (over: Partial<ReviewComment> = {}): ReviewComment => ({
	key: "k", sentKey: "k", kind: "inline", source: "provider", author: "dana", isBot: false, body: "Rename this.",
	resolved: false, earlier: false, replies: [], delivered: false, ...over,
});

const run = (over: Partial<ReviewRun> = {}): ReviewRun => ({
	id: "run-1", reviewId: "r", sessionId: "s", batchId: "b", harness: "codex", triggerSource: "manual", prUrl: PR,
	targetSha: "abcdef123456", status: "completed", verdict: "changes_requested", body: "## Changes requested\n\nThe guard is missing.",
	githubReviewId: "77", createdAt: "2026-11-26T10:00:00Z", autoInjectReview: false, ...over,
});

const state = (over: Partial<PRReviewState> = {}): PRReviewState => ({
	prUrl: PR, prNumber: 12, title: "Fix login", targetSha: "abcdef123456", status: "changes_requested", ...over,
});

const pr = (over: Partial<SessionPRSummary> = {}): SessionPRSummary => ({
	url: PR, htmlUrl: PR, number: 12, title: "Fix login", state: "open", provider: "github", repo: "acme/app", author: "ao-worker",
	sourceBranch: "fix", targetBranch: "main", additions: 1, deletions: 1, changedFiles: 1,
	ci: { state: "passing", failingChecks: [] },
	review: { decision: "changes_requested", hasUnresolvedHumanComments: true, unresolvedBy: [], reviews: [] },
	mergeability: { state: "mergeable", reasons: [] },
	...over,
});

describe("a comment row", () => {
	it("previews a review from past the verdict it opens by restating", () => {
		expect(commentPreview(comment({ kind: "summary", verdict: "changes_requested", body: "## Changes requested\n\nThe guard is missing." }))).toBe("The guard is missing.");
		expect(commentPreview(comment({ body: "> [!NOTE]\n> **Rename** `snake_case`." }))).toBe("Rename snake_case.");
	});

	it("names where an inline comment is, shortened while folded", () => {
		expect(commentLocation(comment({ file: "src/auth/login.ts", line: 4 }))).toBe("src/auth/login.ts:4");
		expect(commentLocation(comment({ file: "src/auth/login.ts", line: 4 }), true)).toBe("login.ts:4");
		expect(commentLocation(comment())).toBe("");
	});

	// As on the desktop: what still needs the worker starts open with Send to agent in reach.
	it("starts open only while it still asks something of the worker", () => {
		const sent = new Set(["sent"]);
		expect(commentStartsExpanded(comment(), sent)).toBe(true);
		expect(commentStartsExpanded(comment({ sentKey: "sent" }), sent)).toBe(false);
		expect(commentStartsExpanded(comment({ delivered: true }), sent)).toBe(false);
		expect(commentStartsExpanded(comment({ resolved: true }), sent)).toBe(false);
		expect(commentStartsExpanded(comment({ earlier: true }), sent)).toBe(false);
		expect(commentStartsExpanded(comment({ body: "" }), sent)).toBe(false);
		expect(commentStartsExpanded(comment({ kind: "summary", verdict: "approved" }), sent)).toBe(false);
	});

	it("sends all open comments but approvals, empty ones, and ones sent or on their way", () => {
		const list = [
			comment({ key: "a", sentKey: "a" }),
			comment({ key: "b", sentKey: "b", kind: "summary", verdict: "approved" }),
			comment({ key: "c", sentKey: "c", body: "" }),
			comment({ key: "d", sentKey: "d" }),
			comment({ key: "e", sentKey: "e" }),
			comment({ key: "f", sentKey: "f", delivered: true }),
		];
		expect(isApproval(list[1]!)).toBe(true);
		expect(unsentOpenComments(list, new Set(["d"]), new Set(["e"])).map((item) => item.key)).toEqual(["a"]);
	});

	it("offers Reply on GitHub only: in a thread, or to a person's review or comment", () => {
		const github = { provider: pr(), webUrl: PR };
		expect(commentReplyOffered(comment({ threadId: "PRRT_1" }), github)).toBe(true);
		expect(commentReplyOffered(comment(), github)).toBe(false);
		expect(commentReplyOffered(comment({ kind: "summary" }), github)).toBe(true);
		expect(commentReplyOffered(comment({ kind: "summary", source: "orchestrator" }), github)).toBe(false);
		expect(commentReplyOffered(comment({ threadId: "PRRT_1", resolved: true }), github)).toBe(false);
		expect(commentReplyOffered(comment({ threadId: "x" }), { provider: pr({ provider: "gitlab" }), webUrl: PR })).toBe(false);
	});

	it("names who a reply answers, calling the Orchestrator by name rather than its agent", () => {
		expect(commentReplyLabel(comment({ file: "src/auth/login.ts", line: 4 }))).toBe("Reply to dana · login.ts:4");
		expect(commentReplyLabel(comment({ source: "orchestrator", author: "claude-code", file: "api/filters.ts", line: 18 }))).toBe("Reply to Orchestrator · filters.ts:18");
		expect(commentReplyLabel(comment({ kind: "summary" }))).toBe("Reply to dana");
	});

	it("resolves only an open inline comment with a link", () => {
		expect(commentCanResolve(comment({ url: `${PR}#discussion_r1` }))).toBe(true);
		expect(commentCanResolve(comment())).toBe(false);
		expect(commentCanResolve(comment({ kind: "summary", url: PR }))).toBe(false);
	});

	it("draws a bot by its app's picture and says how long ago", () => {
		expect(commentAvatarUrl("coderabbitai[bot]", { provider: pr(), webUrl: PR })).toBe("https://avatars.githubusercontent.com/coderabbitai?size=64");
		expect(commentAge("2026-11-26T10:00:00Z", Date.parse("2026-11-26T12:00:00Z"))).toBe("2h ago");
		expect(commentAge("2026-11-26T10:00:00Z", Date.parse("2026-11-26T10:00:20Z"))).toBe("now");
		expect(commentAge(undefined)).toBe("");
	});
});

describe("the reviewer's status line and button", () => {
	const data = (over: Partial<SessionReviews> = {}): Pick<SessionReviews, "reviews" | "runs" | "reviewerActivityState"> => ({ reviews: [state()], runs: [], ...over });

	it("says what a running pass is doing, with its commit and how long it has run", () => {
		const live = run({ status: "running", verdict: "", body: "", startedAt: "2026-11-26T10:00:00Z" });
		const reviews = [state({ status: "running", latestRun: live })];
		expect(reviewerStatusLine(data({ reviews }), reviews[0]!, Date.parse("2026-11-26T10:01:05Z"))).toEqual({ kind: "working", label: "Reviewing #12", facts: ["abcdef1", "1m 5s"] });
		const starting = [state({ latestRun: run({ status: "launching", verdict: "", body: "" }) })];
		expect(reviewerStatusLine(data({ reviews: starting }), starting[0]!).label).toBe("Starting review");
		const stopping = [state({ latestRun: run({ status: "cancelling", verdict: "", body: "" }) })];
		expect(reviewerStatusLine(data({ reviews: stopping }), stopping[0]!).label).toBe("Stopping review");
		expect(reviewNowAction(data({ reviews: stopping }), stopping[0]!)).toBe("stop");
	});

	it("offers Retry review after a failed pass, and nothing for a PR that cannot be reviewed", () => {
		const failed = state({ latestRun: run({ status: "failed", verdict: "", body: "exited" }), previousRun: run() });
		expect(reviewerStatusLine(data({ reviews: [failed] }), failed)).toMatchObject({ kind: "failed", label: "Last review failed." });
		expect(reviewNowAction(data({ reviews: [failed] }), failed)).toBe("retry");
		const ineligible = state({ status: "ineligible" });
		expect(reviewNowAction(data({ reviews: [ineligible] }), ineligible)).toBe("none");
	});
});

describe("the shared comment model on the phone's data", () => {
	it("lists the Orchestrator's review, a thread with its reply, and a top-level comment in one list", () => {
		const provider = pr({
			detailsObservedAt: "2026-11-26T10:05:00Z",
			review: {
				decision: "changes_requested", hasUnresolvedHumanComments: true,
				reviews: [{ reviewerId: "dana", verdict: "changes_requested", body: "", submittedAt: "2026-11-26T09:00:00Z", reviewUrl: `${PR}#pullrequestreview-5`, autoInjectReview: true }],
				unresolvedBy: [{ reviewerId: "dana", count: 1, links: [{ url: `${PR}#discussion_r1`, reviewId: "5", file: "src/a.ts", line: 3, body: "Guard this.", autoInjectReview: true }] }],
			},
			threads: [{ id: "PRRT_1", path: "src/a.ts", line: 3, resolved: false, comments: [
				{ id: "c1", author: "dana", body: "Guard this.", isBot: false, url: `${PR}#discussion_r1`, createdAt: "2026-11-26T09:00:00Z" },
				{ id: "c2", author: "ao-worker", body: "Done.", isBot: false, createdAt: "2026-11-26T09:30:00Z" },
			] }],
			conversation: [{ id: "c3", author: "sam", body: "Can we ship this today?", isBot: false, url: `${PR}#issuecomment-9`, createdAt: "2026-11-26T08:00:00Z" }],
		});
		const [block] = buildReviewBlocks({ openPRs: [{ url: PR, number: 12, title: "Fix login" }], providerPRs: [provider], reviewStates: [state({ latestRun: run() })], runs: [] });
		expect(block?.comments.map((item) => [item.source, item.kind, item.author])).toEqual([
			["orchestrator", "summary", "codex"],
			["provider", "inline", "dana"],
			["provider", "comment", "sam"],
		]);
		const thread = block?.comments[1];
		expect(thread).toMatchObject({ threadId: "PRRT_1", delivered: true, sentKey: `url:${PR}#discussion_r1` });
		expect(thread?.replies.map((reply) => reply.body)).toEqual(["Done."]);
		expect(block?.openCount).toBe(3);
	});
});
