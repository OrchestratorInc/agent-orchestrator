import { expect, it } from "vitest";
import { cloudPRSummary, cloudSessionReviews } from "./pullRequests";
import type { CloudPullRequestSummary, CloudReviewRun } from "./types";

const PR_URL = "https://github.com/acme/web/pull/7";

const summary: CloudPullRequestSummary = {
	url: PR_URL, htmlUrl: PR_URL, number: 7, title: "Fix login", state: "open", provider: "github", repository: "acme/web", author: "ao-bot",
	sourceBranch: "ao/fix", targetBranch: "main", headSha: "abc123", additions: 10, deletions: 2, changedFiles: 3,
	ci: { state: "failing", failingChecks: [{ name: "test", status: "failed", conclusion: "failure", url: "https://ci/1" }] },
	review: { decision: "changes_requested", hasUnresolvedHumanComments: true, unresolvedBy: [{ reviewerId: "dana", count: 2, links: [] }] },
	mergeability: { state: "blocked", reasons: ["CI is failing"], pullRequestUrl: PR_URL, conflictFiles: [] },
	updatedAt: "2026-11-08T10:00:00Z",
};

const run: CloudReviewRun = {
	id: "run-1", reviewId: "run-1", sessionId: "s1", batchId: "run-1", harness: "codex", triggerSource: "manual", pullRequestUrl: PR_URL,
	targetSha: "abc123", status: "complete", verdict: "changes_requested", body: "Tighten the check.", providerReviewId: "gh-9",
	createdAt: "2026-11-08T10:01:00Z", autoInjectReview: true,
};

// The control plane's names for the daemon's fields; everything else is the same.
it("reads a cloud pull request as the daemon's summary", () => {
	expect(cloudPRSummary(summary)).toMatchObject({
		repo: "acme/web",
		headSha: "abc123",
		ci: { state: "failing", failingChecks: [{ name: "test" }] },
		review: { decision: "changes_requested", unresolvedBy: [{ reviewerId: "dana", count: 2 }] },
		mergeability: { state: "blocked", reasons: ["CI is failing"], prUrl: PR_URL },
	});
	expect(cloudPRSummary({ ...summary, headSha: "" }).headSha).toBeUndefined();
});

it("reads cloud reviews as the daemon's, with a conversation for a reviewer and no session handle", () => {
	const reviews = cloudSessionReviews({
		sessionId: "s1",
		reviewerHarness: "codex",
		availableReviewerHarnesses: ["codex", "claude-code"],
		reviews: [{ pullRequestUrl: PR_URL, pullRequestNumber: 7, title: "Fix login", targetSha: "abc123", status: "changes_requested", latestRun: run }],
		runs: [run],
		reviewerSurface: { mode: "chat", reviewId: "run-1", harness: "codex" },
	});
	expect(reviews.reviewerHandleId).toBe("");
	expect(reviews.reviews[0]).toMatchObject({ prUrl: PR_URL, prNumber: 7, latestRun: { prUrl: PR_URL, githubReviewId: "gh-9", deliveredAt: null } });
	expect(reviews.runs[0]?.verdict).toBe("changes_requested");
	expect(reviews.reviewerSurface).toEqual({ mode: "chat", reviewId: "run-1", harness: "codex" });
	expect(reviews.availableReviewerHarnesses).toEqual(["codex", "claude-code"]);
	expect(reviews.reviewerActivityState).toBe("idle");
});

// Seen on the Pixel: the row said "Not started" beside a review in progress
// and after it approved, as the control plane reports no reviewer state.
it("says the reviewer is working while a review runs, idle once one has, and not started before", () => {
	const state = (status?: CloudReviewRun["status"]) => cloudSessionReviews({ sessionId: "s1", reviews: [], runs: status ? [{ ...run, status }] : [] }).reviewerActivityState;
	expect(state("running")).toBe("active");
	expect(state("delivered")).toBe("idle");
	expect(state()).toBeUndefined();
});

it("leaves the model out of the reviewer section's footer where Cloud cannot choose one", async () => {
	const { reviewerSectionFooter } = await import("../reviewer-picker.types");
	const reviewer = { reviewers: [], selectedReviewer: "", effectiveReviewer: "codex", onSelectReviewer() {}, models: [], modelTitle: "Model" as const, selectedModel: "", onSelectModel() {}, busy: false };
	expect(reviewerSectionFooter({ ...reviewer, modelChoice: false })).toBe("Choose who runs the next Orchestrator review.");
	expect(reviewerSectionFooter(reviewer)).toContain("which model it uses");
});
