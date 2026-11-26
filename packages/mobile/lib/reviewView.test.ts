import { describe, expect, it } from "vitest";
import type { DashboardSession, PRReviewState, ReviewRun, SessionPRSummary, SessionReviews } from "./api";
import { latestAutoReviewFailure, previousRouteIsWorker, pullRequestSummaryForURL, reviewBatchAction, reviewerControls, reviewerDestination, reviewForPullRequest, reviewPrimaryAction, reviewPrimaryActionLabel, reviewRouteForSession, reviewRunMeta, reviewRunSendable, reviewStatusLabel, rerequestableReviewers, reviewStatusVisual, reviewVerdictLabel, sessionPRReadyForReview, shortCommit, workerRouteForSession } from "./reviewView";

const run = (over: Partial<ReviewRun> = {}): ReviewRun => ({
	id: "run-1", reviewId: "review-1", sessionId: "worker-1", batchId: "", harness: "codex",
	triggerSource: "manual", prUrl: "https://github.com/acme/repo/pull/12", targetSha: "abcdef123456",
	status: "completed", verdict: "approved", body: "Looks good", githubReviewId: "", createdAt: "2026-09-22T00:00:00Z",
	autoInjectReview: true, ...over,
});

const state = (over: Partial<PRReviewState> = {}): PRReviewState => ({
	prUrl: "https://github.com/acme/repo/pull/12", prNumber: 12, title: "Improve mobile review",
	targetSha: "new-head", status: "needs_review", previousRun: run({ targetSha: "old-head" }), ...over,
});

describe("mobile review presentation", () => {
	it("opens the worker's chat on the PR's machine", () => {
		expect(workerRouteForSession({ id: "worker-1", projectId: "repo" }, "host-a")).toEqual({
			pathname: "/session/[id]",
			params: { id: "worker-1", projectId: "repo", hostId: "host-a" },
		});
		expect(workerRouteForSession({ id: "worker-1", projectId: "repo" }).params).not.toHaveProperty("hostId");
	});

	it("goes back to the worker only when its own chat is directly under the PR page", () => {
		const tabs = { name: "(tabs)" };
		const chat = (id: string) => ({ name: "session/[id]", params: { id } });
		const page = { name: "review/[sessionId]", params: { sessionId: "worker-1" } };

		expect(previousRouteIsWorker([tabs, chat("worker-1"), page], 2, "worker-1")).toBe(true);
		// From the PR tab, or from another worker's chat, the worker is opened instead.
		expect(previousRouteIsWorker([tabs, page], 1, "worker-1")).toBe(false);
		expect(previousRouteIsWorker([tabs, chat("worker-2"), page], 2, "worker-1")).toBe(false);
		expect(previousRouteIsWorker([page], 0, "worker-1")).toBe(false);
	});

	it("offers the first open PR in a session and skips closed or merged PRs", () => {
		const merged = { number: 10, url: "merged", state: "merged" };
		const open = { number: 12, url: "open", state: "open" };
		const session = { id: "worker-1", pr: merged, prs: [merged, open] } as DashboardSession;

		expect(sessionPRReadyForReview(session)).toBe(open);
		expect(sessionPRReadyForReview({ id: "worker-2", pr: merged } as DashboardSession)).toBeUndefined();
	});

	it("builds a review route from the session's current pull request", () => {
		const session = { id: "worker-1", pr: { number: 12, url: "https://github.com/acme/repo/pull/12" } } as DashboardSession;
		expect(reviewRouteForSession(session)).toEqual({
			pathname: "/review/[sessionId]",
			params: { sessionId: "worker-1", prNumber: "12", prUrl: "https://github.com/acme/repo/pull/12" },
		});
	});

	it("qualifies review and reviewer links with their owning host", () => {
		const session = { id: "worker-1", pr: { number: 12, url: "https://github.com/acme/repo/pull/12" } } as DashboardSession;
		expect(reviewRouteForSession(session, "host-b")?.params).toMatchObject({ hostId: "host-b", sessionId: "worker-1" });
		const review = state();
		const data = { reviewerSurface: { mode: "chat", reviewId: "review-1", harness: "codex" } } as SessionReviews;
		expect(reviewerDestination(data, review, session.id, "host-b")?.params).toMatchObject({ hostId: "host-b", sessionId: "worker-1" });
	});

	it("prefers the first PR in the current list and omits sessions without one", () => {
		const session = {
			id: "worker-1",
			pr: { number: 10, url: "legacy" },
			prs: [{ number: 12, url: "current" }, { number: 13, url: "other" }],
		} as DashboardSession;
		expect(reviewRouteForSession(session)?.params).toMatchObject({ prNumber: "12", prUrl: "current" });
		expect(reviewRouteForSession({ id: "worker-2" } as DashboardSession)).toBeUndefined();
	});

	it("matches the exact PR URL before falling back to its number", () => {
		const reviews = [state({ prUrl: "other", prNumber: 12 }), state()];
		expect(reviewForPullRequest(reviews, state().prUrl, 12)?.prUrl).toBe(state().prUrl);
	});

	it("matches aliases by repository and number without selecting another PR", () => {
		const prs = [
			{ url: "https://github.com/acme/legacy-name/pull/12", htmlUrl: "https://github.com/acme/legacy-name/pull/12", repo: "acme/repo", number: 12 },
			{ url: "https://github.com/acme/repo/pull/13", htmlUrl: "https://github.com/acme/repo/pull/13", repo: "acme/repo", number: 13 },
		] as SessionPRSummary[];

		expect(pullRequestSummaryForURL(prs, prs[1].url)).toBe(prs[1]);
		expect(pullRequestSummaryForURL(prs, "https://github.com/acme/repo/pull/12")).toBe(prs[0]);
		expect(pullRequestSummaryForURL(prs, "https://github.com/acme/old-repo/pull/12")).toBeUndefined();
	});

	it("keeps an earlier verdict in previousRun instead of treating it as current", () => {
		const review = state();
		expect(review.latestRun).toBeUndefined();
		expect(review.previousRun?.verdict).toBe("approved");
		expect(reviewStatusLabel(review.status)).toBe("Needs review");
	});

	it("labels terminal outcomes and shortens commit ids", () => {
		expect(reviewVerdictLabel(run({ verdict: "changes_requested" }))).toBe("Changes requested");
		expect(reviewVerdictLabel(run({ status: "cancelled", verdict: "" }))).toBe("Review cancelled");
		expect(shortCommit("abcdef123456")).toBe("abcdef12");
	});

	it("does not present pending or unavailable reviews as successful", () => {
		expect(reviewStatusVisual("needs_review")).toEqual({ icon: "clock", tone: "muted" });
		expect(reviewStatusVisual("ineligible")).toEqual({ icon: "alert-circle", tone: "muted" });
		expect(reviewStatusVisual("up_to_date")).toEqual({ icon: "check-circle", tone: "green" });
	});

	it("only offers actions supported by the current review state", () => {
		expect(reviewPrimaryAction(state({ status: "needs_review" }))).toBe("start");
		expect(reviewPrimaryAction(state({ status: "running" }))).toBe("cancel");
		expect(reviewPrimaryAction(state({ status: "up_to_date" }))).toBe("review_again");
		expect(reviewPrimaryAction(state({ status: "ineligible" }))).toBe("none");
		expect(reviewPrimaryActionLabel("review_again")).toBe("Review again");
		expect(reviewPrimaryActionLabel("cancel", true)).toBe("Cancel running reviews");
	});

	it("prioritizes cancelling an active session batch over starting another", () => {
		const selected = state({ status: "needs_review" });
		expect(reviewBatchAction(selected, [selected, state({ prUrl: "other", status: "running" })])).toBe("cancel");
	});

	it("surfaces only the newest written automatic-review failure while automation is enabled", () => {
		const oldFailure = run({ id: "old", triggerSource: "auto", status: "failed", body: "old failure", createdAt: "2026-09-21T00:00:00Z" });
		const newestFailure = run({ id: "new", triggerSource: "auto", status: "failed", body: "reviewer crashed", createdAt: "2026-09-23T00:00:00Z" });
		const reviews = [state({ latestRun: oldFailure }), state({ prUrl: "other", latestRun: newestFailure })];
		expect(latestAutoReviewFailure(reviews, true)).toBe(newestFailure);
		expect(latestAutoReviewFailure(reviews, false)).toBeUndefined();
	});

	it("does not elevate manual, successful, or empty automatic-review results as failures", () => {
		expect(latestAutoReviewFailure([state({ latestRun: run({ status: "failed", triggerSource: "manual" }) })], true)).toBeUndefined();
		expect(latestAutoReviewFailure([state({ latestRun: run({ status: "completed", triggerSource: "auto" }) })], true)).toBeUndefined();
		expect(latestAutoReviewFailure([state({ latestRun: run({ status: "failed", triggerSource: "auto", body: "  " }) })], true)).toBeUndefined();
	});

	it("routes each reviewer surface to its native mobile experience", () => {
		const review = state();
		const base = { reviewerHandleId: "fallback", reviews: [review], runs: [] };
		expect(reviewerDestination({ ...base, reviewerSurface: { mode: "chat", reviewId: "review-1", harness: "codex" } }, review, "worker-1")).toMatchObject({ pathname: "/reviewer/[reviewId]", params: { reviewId: "review-1" } });
		expect(reviewerDestination({ ...base, reviewerSurface: { mode: "tui", reviewId: "review-1", harness: "codex" } }, review, "worker-1")).toMatchObject({ pathname: "/shell/[handleId]", params: { handleId: "fallback", kind: "reviewer" } });
		expect(reviewerDestination(base, review, "worker-1")).toBeUndefined();
	});
	it("only offers finished reviews with findings to the worker", () => {
		expect(reviewRunSendable(run())).toBe(true);
		expect(reviewRunSendable(run({ status: "superseded" }))).toBe(false);
		expect(reviewRunSendable(run({ status: "running", body: "" }))).toBe(false);
		expect(reviewRunSendable(run({ status: "running", body: "partial" }))).toBe(false);
		expect(reviewRunSendable(run({ status: "failed", verdict: "", body: "reviewer process exited before submitting a result" }))).toBe(false);
		expect(reviewRunSendable(run({ status: "cancelled", verdict: "" }))).toBe(false);
		expect(reviewRunSendable(run({ body: "  " }))).toBe(false);
	});

	it("says delivered once", () => {
		expect(reviewRunMeta(run({ deliveredAt: "2026-09-30T00:00:00Z" }))).toBe("codex · manual · completed · delivered");
		expect(reviewRunMeta(run())).toBe("codex · manual · completed");
		expect(reviewRunMeta(run({ status: "running" }))).toBe("codex · manual · running");
	});
	it("only offers Open when there is a reviewer surface to open", () => {
		const base = { reviewerHandleId: "", reviews: [state()], runs: [run()] } as SessionReviews;
		const tui = { mode: "tui", reviewId: "review-1", harness: "claude-code" } as SessionReviews["reviewerSurface"];
		// Stopped terminal reviewer: surface kept, handle gone -> Restore, not a dead Open.
		expect(reviewerControls({ ...base, reviewerSurface: tui, reviewerActivityState: "active" } as SessionReviews, state(), "worker-1")).toEqual({ restore: true, stop: false });
		// Live terminal reviewer.
		expect(reviewerControls({ ...base, reviewerHandleId: "review-worker-1", reviewerSurface: { ...tui!, handleId: "review-worker-1" }, reviewerActivityState: "idle" } as SessionReviews, state(), "worker-1")).toEqual({ open: "terminal", restore: false, stop: true });
		// Exited reviewer with a stale handle: Restore only, never Restore and Stop together.
		expect(reviewerControls({ ...base, reviewerHandleId: "review-worker-1", reviewerSurface: { ...tui!, handleId: "review-worker-1" }, reviewerActivityState: "exited" } as SessionReviews, state(), "worker-1")).toEqual({ restore: true, stop: false });
		// Chat reviewer opens by review id.
		expect(reviewerControls({ ...base, reviewerSurface: { mode: "chat", reviewId: "review-1", harness: "codex" }, reviewerActivityState: "active" } as SessionReviews, state(), "worker-1")).toEqual({ open: "chat", restore: false, stop: false });
		// No reviewer and no history: nothing to show.
		expect(reviewerControls({ reviewerHandleId: "", reviews: [state()], runs: [] } as SessionReviews, state(), "worker-1")).toEqual({ restore: false, stop: false });
	});
});

describe("reviewers to ask again", () => {
	const PR = "https://github.com/acme/repo/pull/12";
	const entry = (reviewerId: string, over: Partial<NonNullable<SessionPRSummary["review"]["reviews"]>[number]> = {}) => ({
		reviewerId, verdict: "changes_requested" as const, body: "", submittedAt: "2026-11-20T10:00:00Z", autoInjectReview: true, ...over,
	});
	const pr = (over: Partial<SessionPRSummary> = {}): SessionPRSummary => ({
		url: PR, htmlUrl: PR, number: 12, title: "Improve mobile review", state: "open", repo: "acme/repo", author: "ao-worker",
		sourceBranch: "feat", targetBranch: "main", additions: 1, deletions: 1, changedFiles: 1,
		ci: { state: "passing", failingChecks: [] },
		review: { decision: "changes_requested", hasUnresolvedHumanComments: true, unresolvedBy: [], reviews: [] },
		mergeability: { state: "mergeable", reasons: [] },
		...over,
	});
	const ids = (value: SessionPRSummary, reviews?: Pick<SessionReviews, "reviews" | "runs">) => rerequestableReviewers(value, reviews).map((reviewer) => reviewer.reviewerId);

	it("offers a person who asked for changes, with their inline comment count", () => {
		const value = pr({
			review: {
				decision: "changes_requested", hasUnresolvedHumanComments: true, reviews: [entry("dana")],
				unresolvedBy: [{ reviewerId: "dana", count: 2, links: [
					{ url: `${PR}#discussion_r1`, file: "a.ts", line: 1, body: "One", autoInjectReview: true },
					{ url: `${PR}#discussion_r2`, file: "b.ts", line: 2, body: "Two", autoInjectReview: true },
				] }],
			},
		});
		expect(rerequestableReviewers(value, undefined)).toEqual([{ reviewerId: "dana", count: 2 }]);
	});

	// GitHub rejects review requests for apps, so a bot is never offered.
	it("never offers a bot", () => {
		expect(ids(pr({ review: { decision: "changes_requested", hasUnresolvedHumanComments: false, unresolvedBy: [], reviews: [entry("coderabbitai[bot]", { isBot: true, body: "Walkthrough" })] } }))).toEqual([]);
	});

	it("leaves out a reviewer who approved, one already asked again, and the PR's author", () => {
		const value = pr({
			author: "Ao-Worker[bot]",
			review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [], reviews: [
				entry("erin", { verdict: "approved" }),
				entry("sam"),
				entry("ao-worker"),
			] },
			requestedReviewers: [{ login: "sam" }],
		});
		expect(ids(value)).toEqual([]);
	});

	it("leaves out the review the Orchestrator posted", () => {
		const value = pr({ review: { decision: "changes_requested", hasUnresolvedHumanComments: false, unresolvedBy: [], reviews: [
			entry("prasad", { reviewUrl: `${PR}#pullrequestreview-77`, body: "Changes requested" }),
		] } });
		expect(ids(value)).toEqual(["prasad"]);
		expect(ids(value, { reviews: [], runs: [run({ prUrl: PR, githubReviewId: "77", status: "completed", body: "Changes requested" })] })).toEqual([]);
	});
});
