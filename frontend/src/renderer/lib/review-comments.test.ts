import { describe, expect, it } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { noPRDetails } from "./pr-display";
import { buildReviewBlocks, providerReviewId, reviewCommentIsOpen, reviewerRowKey, type ReviewComment } from "./review-comments";
import type { PRReviewState, ReviewRunFacts } from "./session-reviews";

const PR_URL = "https://github.com/acme/repo/pull/3";

const run = (overrides: Partial<ReviewRunFacts> = {}): ReviewRunFacts => ({
	id: "run-1",
	reviewId: "review-1",
	sessionId: "sess-1",
	harness: "codex",
	status: "completed",
	verdict: "approved",
	body: "Looks good.",
	prUrl: PR_URL,
	targetSha: "abc123",
	createdAt: "2026-06-16T10:00:00Z",
	autoInjectReview: true,
	...overrides,
}) as ReviewRunFacts;

const state = (overrides: Partial<PRReviewState> = {}): PRReviewState => ({
	prUrl: PR_URL,
	prNumber: 3,
	title: "Reviewable change 3",
	targetSha: "abc123",
	status: "up_to_date",
	...overrides,
}) as PRReviewState;

const summary = (overrides: Partial<SessionPRSummary> = {}): SessionPRSummary => ({
	...noPRDetails(),
	url: "https://api.github.com/repos/acme/repo/pulls/3",
	htmlUrl: PR_URL,
	number: 3,
	title: "PR 3",
	state: "open",
	provider: "github",
	repo: "acme/repo",
	author: "ada",
	sourceBranch: "feat/3",
	targetBranch: "main",
	headSha: "abc123",
	additions: 1,
	deletions: 0,
	changedFiles: 1,
	ci: { autoInjectCI: true, state: "passing", failingChecks: [], checks: [] },
	review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
	mergeability: { state: "mergeable", reasons: [], prUrl: PR_URL, conflictFiles: [] },
	updatedAt: "2026-06-16T12:00:00Z",
	...overrides,
}) as SessionPRSummary;

const thread = (id: string, author: string, body: string, overrides: Partial<NonNullable<SessionPRSummary["threads"]>[number]> = {}) => ({
	id,
	path: "src/a.ts",
	line: 3,
	resolved: false,
	comments: [{ id: `${id}-1`, author, isBot: false, url: `${PR_URL}#discussion_${id}`, createdAt: "2026-06-16T11:00:00Z", body }],
	...overrides,
});

function blockFor(input: { provider?: SessionPRSummary; states?: PRReviewState[]; runs?: ReviewRunFacts[] }) {
	const [block] = buildReviewBlocks({
		openPRs: [{ url: PR_URL, number: 3 }],
		providerPRs: input.provider ? [input.provider] : [],
		reviewStates: input.states ?? [],
		runs: input.runs ?? [],
	});
	return block!;
}

const bodies = (comments: ReviewComment[]) => comments.map((comment) => comment.body);

describe("buildReviewBlocks", () => {
	it("keeps the newest Orchestrator pass open and earlier passes out of Open", () => {
		const block = blockFor({
			states: [state()],
			runs: [
				run({ id: "run-old", body: "Earlier pass.", createdAt: "2026-06-15T10:00:00Z" }),
				run({ id: "run-new", body: "Latest pass.", verdict: "changes_requested", githubReviewId: "111", triggerSource: "agent" }),
			],
		});

		expect(bodies(block.comments)).toEqual(["Latest pass.", "Earlier pass."]);
		const [latest, earlier] = block.comments;
		expect(latest).toMatchObject({ source: "orchestrator", kind: "summary", verdict: "changes_requested", earlier: false, runId: "run-new", requestedByAgent: true, url: `${PR_URL}#pullrequestreview-111` });
		expect(earlier).toMatchObject({ earlier: true, url: undefined, requestedByAgent: false });
		expect(block.openCount).toBe(1);
		expect(block.resolvedCount).toBe(1);
		expect(block.comments.filter(reviewCommentIsOpen)).toEqual([latest]);
	});

	it("ignores runs that are still running, failed, or have nothing to read", () => {
		const block = blockFor({
			states: [state({ status: "running" })],
			runs: [
				run({ id: "live", status: "running", body: "" }),
				run({ id: "failed", status: "failed", body: "reviewer crashed" }),
				run({ id: "empty", body: "  " }),
			],
		});
		expect(block.comments).toEqual([]);
	});

	it("falls back to the review state's own runs against a daemon without the runs list", () => {
		const block = blockFor({
			states: [state({ latestRun: run({ id: "latest", body: "From the state." }), previousRun: run({ id: "prev", body: "Older.", createdAt: "2026-06-01T00:00:00Z" }) })],
		});
		expect(bodies(block.comments)).toEqual(["From the state.", "Older."]);
	});

	it("leaves out provider reviews the Orchestrator posted and the PR author's own", () => {
		const block = blockFor({
			states: [state()],
			runs: [run({ githubReviewId: "111", body: "Orchestrator summary." })],
			provider: summary({
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: false,
					unresolvedBy: [],
					reviews: [
						{ reviewerId: "ada", verdict: "changes_requested", submittedAt: "2026-06-16T09:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-100`, body: "Self review.", autoInjectReview: true },
						{ reviewerId: "ada", verdict: "approved", submittedAt: "2026-06-16T10:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-111`, body: "Orchestrator summary.", autoInjectReview: true },
						{ reviewerId: "maya", verdict: "changes_requested", submittedAt: "2026-06-16T11:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-222`, body: "Please add tests.", autoInjectReview: false },
					],
				},
			}),
		});

		expect(bodies(block.comments)).toEqual(["Please add tests.", "Orchestrator summary."]);
		expect(block.comments[0]).toMatchObject({ source: "provider", author: "maya", verdict: "changes_requested", delivered: false, rerequestReviewerId: "maya" });
		expect(block.comments[1]).toMatchObject({ source: "orchestrator" });
	});

	it("keeps a reviewer's newest review open and their older ones as earlier", () => {
		const block = blockFor({
			provider: summary({
				review: {
					decision: "approved",
					hasUnresolvedHumanComments: false,
					unresolvedBy: [],
					reviews: [
						{ reviewerId: "maya", verdict: "changes_requested", submittedAt: "2026-06-15T11:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-1`, body: "First look.", autoInjectReview: true },
						{ reviewerId: "maya", verdict: "approved", submittedAt: "2026-06-16T11:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-2`, body: "Ship it.", autoInjectReview: true },
					],
				},
			}),
		});

		expect(block.comments.map((comment) => [comment.body, comment.earlier])).toEqual([["Ship it.", false], ["First look.", true]]);
		// An approval needs no second look.
		expect(block.comments[0]!.rerequestReviewerId).toBeUndefined();
		expect(block.comments[1]!.rerequestReviewerId).toBe("maya");
	});

	it("credits an inline thread to the Orchestrator by its review id, though it posts as the PR author", () => {
		const block = blockFor({
			states: [state()],
			runs: [run({ githubReviewId: "111", body: "Summary." })],
			provider: summary({
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: true,
					unresolvedBy: [{ reviewerId: "ada", count: 1, links: [{ reviewId: "111", url: `${PR_URL}#discussion_t1`, file: "src/a.ts", line: 3, autoInjectReview: false }] }],
				},
				threads: [
					thread("t1", "ada", "Guard this."),
					// The PR author's own note on a line is not feedback for the worker.
					thread("t2", "ada", "Note to self."),
					thread("t3", "maya", "Rename this.", {
						resolved: true,
						comments: [
							{ id: "t3-1", author: "maya", isBot: false, url: `${PR_URL}#discussion_t3`, createdAt: "2026-06-16T11:00:00Z", body: "Rename this." },
							{ id: "t3-2", author: "ada", isBot: false, url: `${PR_URL}#discussion_t3b`, createdAt: "2026-06-16T11:30:00Z", body: "Done." },
						],
					}),
				],
			}),
		});

		const guard = block.comments.find((comment) => comment.body === "Guard this.")!;
		expect(guard).toMatchObject({ kind: "inline", source: "orchestrator", author: "codex", harness: "codex", runId: "run-1", threadId: "t1", file: "src/a.ts", line: 3, delivered: false });
		expect(bodies(block.comments)).not.toContain("Note to self.");
		const rename = block.comments.find((comment) => comment.body === "Rename this.")!;
		expect(rename).toMatchObject({ source: "provider", resolved: true });
		expect(rename.replies).toEqual([expect.objectContaining({ author: "ada", body: "Done." })]);
		expect(reviewCommentIsOpen(rename)).toBe(false);
	});

	it("reads inline comments from the summary's links before the PR's threads are loaded", () => {
		const block = blockFor({
			provider: summary({
				threads: undefined,
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: true,
					unresolvedBy: [{ reviewerId: "maya", count: 1, links: [{ body: "Open one.", file: "src/b.ts", line: 9, url: `${PR_URL}#discussion_1`, autoInjectReview: true }] }],
					resolvedBy: [{ reviewerId: "maya", count: 1, links: [{ body: "Closed one.", file: "src/b.ts", line: 2, url: `${PR_URL}#discussion_2`, autoInjectReview: true }] }],
				},
			}),
		});

		expect(block.comments.map((comment) => [comment.body, comment.resolved, comment.delivered])).toEqual(
			expect.arrayContaining([["Open one.", false, true], ["Closed one.", true, true]]),
		);
		expect(block.comments.every((comment) => comment.kind === "inline" && comment.threadId === undefined)).toBe(true);
	});

	it("keeps one sent identity for a comment read from a link and then from its thread", () => {
		const link = { body: "Open one.", file: "src/a.ts", line: 3, url: `${PR_URL}#discussion_t1`, autoInjectReview: false };
		const review = { decision: "changes_requested" as const, hasUnresolvedHumanComments: true, unresolvedBy: [{ reviewerId: "maya", count: 1, links: [link] }] };
		const before = blockFor({ provider: summary({ threads: undefined, review }) }).comments[0]!;
		const after = blockFor({ provider: summary({ threads: [thread("t1", "maya", "Open one.")], review }) }).comments[0]!;
		expect(before.key).not.toBe(after.key);
		expect(after.sentKey).toBe(before.sentKey);
	});

	it("lists top-level PR comments, but not the author's or a deploy bot's", () => {
		const block = blockFor({
			provider: summary({
				conversation: [
					{ id: "c1", author: "maya", isBot: false, url: `${PR_URL}#issuecomment-1`, createdAt: "2026-06-16T11:00:00Z", body: "Update the docs too." },
					{ id: "c2", author: "ada", isBot: false, url: `${PR_URL}#issuecomment-2`, createdAt: "2026-06-16T11:10:00Z", body: "Pushed." },
					{ id: "c3", author: "vercel[bot]", isBot: true, url: `${PR_URL}#issuecomment-3`, createdAt: "2026-06-16T11:20:00Z", body: "Preview ready." },
				],
			}),
		});

		expect(block.comments).toEqual([expect.objectContaining({ kind: "comment", author: "maya", body: "Update the docs too.", delivered: false })]);
	});

	it("orders the newest review first, its summary before its inline comments down the files", () => {
		const block = blockFor({
			states: [state()],
			runs: [run({ githubReviewId: "111", body: "Summary.", createdAt: "2026-06-16T10:00:00Z" })],
			provider: summary({
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: true,
					unresolvedBy: [{
						reviewerId: "ada",
						count: 2,
						links: [
							{ reviewId: "111", url: `${PR_URL}#discussion_b`, autoInjectReview: true },
							{ reviewId: "111", url: `${PR_URL}#discussion_a`, autoInjectReview: true },
						],
					}],
				},
				threads: [
					thread("b", "ada", "Second file.", { path: "src/z.ts", line: 1 }),
					thread("a", "ada", "First file.", { path: "src/a.ts", line: 8 }),
					thread("m", "maya", "A later note.", {
						comments: [{ id: "m-1", author: "maya", isBot: false, url: `${PR_URL}#discussion_m`, createdAt: "2026-06-17T09:00:00Z", body: "A later note." }],
					}),
				],
			}),
		});

		expect(bodies(block.comments)).toEqual(["A later note.", "Summary.", "First file.", "Second file."]);
	});

	it("names who reviewed: the Orchestrator first, then each provider reviewer once, then those still asked", () => {
		const block = blockFor({
			states: [state({ status: "changes_requested" })],
			runs: [run({ verdict: "changes_requested" })],
			provider: summary({
				requestedReviewers: [{ login: "maya", isTeam: false }, { login: "core", isTeam: true }],
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: false,
					unresolvedBy: [],
					reviews: [
						{ reviewerId: "maya", verdict: "changes_requested", submittedAt: "2026-06-15T11:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-1`, autoInjectReview: true },
						{ reviewerId: "lee", verdict: "approved", submittedAt: "2026-06-16T11:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-2`, autoInjectReview: true },
						{ reviewerId: "lee", verdict: "changes_requested", submittedAt: "2026-06-14T11:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-3`, autoInjectReview: true },
					],
				},
			}),
		});

		expect(block.reviewers.map((reviewer) => [reviewer.id, reviewer.status, reviewer.canRerequest])).toEqual([
			["orchestrator", "changes_requested", false],
			["lee", "approved", false],
			// Asked again after reviewing: the provider waits on them now.
			["maya", "pending", false],
			["core", "pending", false],
		]);
		expect(block.reviewers[0]).toMatchObject({ orchestrator: true, harness: "codex" });
	});

	it("does not offer to ask a bot to review again", () => {
		const block = blockFor({
			provider: summary({
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: false,
					unresolvedBy: [],
					reviews: [
						{ reviewerId: "coderabbitai[bot]", isBot: true, verdict: "changes_requested", submittedAt: "2026-06-15T11:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-7`, body: "Nitpicks.", autoInjectReview: false },
						{ reviewerId: "maya", verdict: "changes_requested", submittedAt: "2026-06-16T11:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-8`, body: "Please fix.", autoInjectReview: false },
					],
				},
			}),
		});
		expect(block.reviewers.map((reviewer) => [reviewer.id, reviewer.canRerequest])).toEqual([["maya", true], ["coderabbitai[bot]", false]]);
		expect(block.comments.map((comment) => [comment.author, comment.rerequestReviewerId])).toEqual([["maya", "maya"], ["coderabbitai[bot]", undefined]]);
	});

	// An app and a person can share a base login; the newer bot review must not hide the person or replace their review.
	it("keeps a person apart from a bot with the same base login", () => {
		const block = blockFor({
			provider: summary({
				requestedReviewers: [{ login: "dana", isTeam: false }],
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: false,
					unresolvedBy: [],
					reviews: [
						{ reviewerId: "dana", verdict: "changes_requested", submittedAt: "2026-06-15T11:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-11`, body: "Please fix.", autoInjectReview: false },
						{ reviewerId: "dana[bot]", isBot: true, verdict: "changes_requested", submittedAt: "2026-06-16T11:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-12`, body: "Automated check.", autoInjectReview: false },
					],
				},
			}),
		});
		expect(block.reviewers.map((reviewer) => [reviewer.id, reviewer.isBot, reviewer.status])).toEqual([
			["dana[bot]", true, "changes_requested"],
			// Asked again: the request is for the person, not the app.
			["dana", false, "pending"],
		]);
		expect(block.comments.map((comment) => [comment.author, comment.earlier])).toEqual([["dana[bot]", false], ["dana", false]]);
	});

	// GitHub lists apps among requested reviewers (re-requesting Copilot), with no
	// flag saying so: the request must turn the bot's own row pending, not add a second.
	it("turns a re-requested bot's row pending rather than listing it twice", () => {
		const block = blockFor({
			provider: summary({
				requestedReviewers: [{ login: "copilot-pull-request-reviewer", isTeam: false }],
				review: {
					decision: "none",
					hasUnresolvedHumanComments: false,
					unresolvedBy: [],
					reviews: [
						{ reviewerId: "copilot-pull-request-reviewer[bot]", isBot: true, verdict: "none", submittedAt: "2026-06-15T11:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-21`, body: "Reviewed 3 files.", autoInjectReview: false },
					],
				},
			}),
		});
		expect(block.reviewers.map((reviewer) => [reviewer.id, reviewer.isBot, reviewer.status])).toEqual([
			["copilot-pull-request-reviewer[bot]", true, "pending"],
		]);
	});

	// The person "dana" and an app GitHub's GraphQL names "dana" share an id; their rows' keys must not.
	it("gives a person and a same-named bot different row keys", () => {
		const block = blockFor({
			provider: summary({
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: false,
					unresolvedBy: [],
					reviews: [
						{ reviewerId: "dana", verdict: "changes_requested", submittedAt: "2026-06-15T11:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-31`, body: "Please fix.", autoInjectReview: false },
						{ reviewerId: "dana", isBot: true, verdict: "changes_requested", submittedAt: "2026-06-16T11:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-32`, body: "Automated check.", autoInjectReview: false },
					],
				},
			}),
		});
		const keys = block.reviewers.map(reviewerRowKey);
		expect(block.reviewers.map((reviewer) => reviewer.id)).toEqual(["dana", "dana"]);
		expect(new Set(keys).size).toBe(keys.length);
	});

	it("titles a PR from its review state, then the provider, then its number", () => {
		expect(blockFor({ states: [state({ title: "From state" })], provider: summary() }).title).toBe("From state");
		expect(blockFor({ provider: summary({ title: "From provider" }) }).title).toBe("From provider");
		expect(blockFor({}).title).toBe("#3");
	});

	it("pairs a session PR with its provider summary by number when its URL names no repository", () => {
		const [block] = buildReviewBlocks({
			openPRs: [{ url: "https://example.com/pr/3", number: 3 }],
			providerPRs: [summary()],
			reviewStates: [],
			runs: [],
		});
		expect(block!.provider?.htmlUrl).toBe(PR_URL);
		expect(block!.webUrl).toBe(PR_URL);
	});

	it("does not pair PRs with the same number from different repositories", () => {
		const [block] = buildReviewBlocks({
			openPRs: [{ url: "https://github.com/acme/other/pull/3", number: 3 }],
			providerPRs: [summary()],
			reviewStates: [],
			runs: [],
		});
		expect(block!.provider).toBeUndefined();
	});

	it("keeps two GitLab subgroup projects' MRs with one number apart", () => {
		const mr = (path: string) => `https://gitlab.com/${path}/-/merge_requests/3`;
		const gitlab = (path: string, title: string) => summary({ url: mr(path), htmlUrl: mr(path), repo: undefined, provider: "gitlab", title });
		const blocks = buildReviewBlocks({
			openPRs: [{ url: mr("team/sub/app"), number: 3 }, { url: mr("team/sub/web"), number: 3 }],
			providerPRs: [gitlab("team/sub/app", "App MR"), gitlab("team/sub/web", "Web MR")],
			reviewStates: [],
			runs: [],
		});
		expect(blocks.map((block) => block.title)).toEqual(["App MR", "Web MR"]);
		expect(new Set(blocks.map((block) => block.key)).size).toBe(2);
		expect(blocks.map((block) => block.repository)).toEqual(["team/sub/app", "team/sub/web"]);
	});

	it("keeps a GitHub PR and a GitLab MR with one slug and number apart", () => {
		const gitlabUrl = "https://gitlab.com/acme/repo/-/merge_requests/3";
		const blocks = buildReviewBlocks({
			openPRs: [{ url: PR_URL, number: 3 }, { url: gitlabUrl, number: 3 }],
			providerPRs: [summary({ title: "GitHub PR" }), summary({ url: gitlabUrl, htmlUrl: gitlabUrl, provider: "gitlab", title: "GitLab MR" })],
			reviewStates: [],
			runs: [],
		});
		expect(blocks.map((block) => [block.title, block.webUrl])).toEqual([["GitHub PR", PR_URL], ["GitLab MR", gitlabUrl]]);
		expect(new Set(blocks.map((block) => block.key)).size).toBe(2);
	});

	it("does not pair by number alone when two PRs share it", () => {
		const [block] = buildReviewBlocks({
			openPRs: [{ url: "https://example.com/pr/3", number: 3 }],
			providerPRs: [summary(), summary({ url: "https://api.github.com/repos/acme/web/pulls/3", htmlUrl: "https://github.com/acme/web/pull/3", repo: "acme/web" })],
			reviewStates: [],
			runs: [],
		});
		expect(block!.provider).toBeUndefined();
	});

	it("does not pair by number with an unnamed PR when another repository has the number too", () => {
		const opaque = "https://example.com/pr/3";
		const [block] = buildReviewBlocks({
			openPRs: [{ url: "https://github.com/acme/app/pull/3", number: 3 }],
			providerPRs: [
				summary({ url: "https://api.github.com/repos/acme/web/pulls/3", htmlUrl: "https://github.com/acme/web/pull/3", repo: "acme/web" }),
				summary({ url: opaque, htmlUrl: opaque, repo: undefined }),
			],
			reviewStates: [],
			runs: [],
		});
		expect(block!.provider).toBeUndefined();
	});

	it("does not give a summary one PR names to another PR by its number", () => {
		const opaque = "https://other-forge.example/pr/3";
		const blocks = buildReviewBlocks({
			openPRs: [{ url: PR_URL, number: 3 }, { url: opaque, number: 3 }],
			providerPRs: [summary()],
			reviewStates: [],
			runs: [],
		});
		expect(blocks.map((block) => block.provider?.htmlUrl)).toEqual([PR_URL, undefined]);
		expect(blocks[1]!.webUrl).toBe(opaque);
	});

	it("lists a state's own completed pass when the runs list holds only other PRs' runs", () => {
		const block = blockFor({
			states: [state({ latestRun: run({ id: "embedded", body: "From the state." }) })],
			runs: [run({ id: "other", prUrl: "https://github.com/acme/repo/pull/9", body: "Another PR." })],
		});
		expect(bodies(block.comments)).toEqual(["From the state."]);
	});

	it("lets a bodyless approval replace the reviewer's older change request", () => {
		const block = blockFor({
			provider: summary({
				review: {
					decision: "approved",
					hasUnresolvedHumanComments: false,
					unresolvedBy: [],
					reviews: [
						{ reviewerId: "maya", verdict: "changes_requested", submittedAt: "2026-06-15T11:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-1`, body: "Add tests.", autoInjectReview: true },
						{ reviewerId: "maya", verdict: "approved", submittedAt: "2026-06-16T11:00:00Z", reviewUrl: `${PR_URL}#pullrequestreview-2`, body: "", autoInjectReview: true },
					],
				},
			}),
		});
		expect(block.comments.map((comment) => [comment.body, comment.earlier])).toEqual([["Add tests.", true]]);
		expect(block.openCount).toBe(0);
	});

	it("names the repository only when the session's PRs span several", () => {
		const blocks = buildReviewBlocks({
			openPRs: [{ url: PR_URL, number: 3 }, { url: "https://github.com/acme/web/pull/4", number: 4 }],
			providerPRs: [],
			reviewStates: [],
			runs: [],
		});
		expect(blocks.map((block) => block.repository)).toEqual(["acme/repo", "acme/web"]);
		expect(blockFor({}).repository).toBeUndefined();
	});
});

describe("providerReviewId", () => {
	it("reads the review id from a review URL", () => {
		expect(providerReviewId(`${PR_URL}#pullrequestreview-98765`)).toBe("98765");
		expect(providerReviewId(`${PR_URL}#discussion_r1`)).toBeUndefined();
		expect(providerReviewId(undefined)).toBeUndefined();
	});
});
