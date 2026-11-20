import type { PRReviewState, ReviewRun, SessionPRSummary, SessionReviews } from "../api";
import type { CloudPRReviewState, CloudPullRequestSummary, CloudReviewRun, CloudSessionReviewState } from "./types";

// A cloud session's pull requests and AO reviews in the shapes the PR page,
// its cards and the review screen already read from a machine's daemon. The
// control plane serves the same fields under a few other names, which is all
// this maps, as the desktop does (useSessionScmSummary's
// cloudPRSummaryToSessionPRSummary).

export function cloudPRSummary(pr: CloudPullRequestSummary): SessionPRSummary {
	return {
		url: pr.url,
		htmlUrl: pr.htmlUrl,
		number: pr.number,
		title: pr.title,
		state: pr.state,
		repo: pr.repository,
		author: pr.author,
		headSha: pr.headSha || undefined,
		sourceBranch: pr.sourceBranch,
		targetBranch: pr.targetBranch,
		additions: pr.additions,
		deletions: pr.deletions,
		changedFiles: pr.changedFiles,
		ci: { state: pr.ci.state, failingChecks: pr.ci.failingChecks ?? [] },
		review: {
			decision: pr.review.decision,
			hasUnresolvedHumanComments: pr.review.hasUnresolvedHumanComments,
			unresolvedBy: pr.review.unresolvedBy ?? [],
			resolvedBy: pr.review.resolvedBy,
			reviews: pr.review.reviews,
		},
		mergeability: {
			state: pr.mergeability.state,
			reasons: pr.mergeability.reasons ?? [],
			prUrl: pr.mergeability.pullRequestUrl,
			conflictFiles: pr.mergeability.conflictFiles,
		},
		updatedAt: pr.updatedAt,
	};
}

export function cloudReviewRun(run: CloudReviewRun): ReviewRun {
	return {
		id: run.id,
		reviewId: run.reviewId,
		sessionId: run.sessionId,
		batchId: run.batchId,
		harness: run.harness,
		triggerSource: run.triggerSource,
		prUrl: run.pullRequestUrl,
		targetSha: run.targetSha,
		status: run.status,
		verdict: run.verdict,
		body: run.body,
		githubReviewId: run.providerReviewId,
		createdAt: run.createdAt,
		deliveredAt: run.deliveredAt ?? null,
		autoInjectReview: run.autoInjectReview,
	};
}

function cloudPRReviewState(review: CloudPRReviewState): PRReviewState {
	return {
		prUrl: review.pullRequestUrl,
		prNumber: review.pullRequestNumber,
		title: review.title,
		targetSha: review.targetSha,
		status: review.status,
		latestRun: review.latestRun ? cloudReviewRun(review.latestRun) : undefined,
		previousRun: review.previousRun ? cloudReviewRun(review.previousRun) : undefined,
	};
}

/**
 * A cloud session's reviews as the review screen reads them. Cloud reviews
 * run in Chat (an older control plane's ran in a terminal, which the phone
 * cannot open for a cloud session), and the control plane has no reviewer
 * session to stop or restore, so there is no handle here: the reviewer row
 * offers Open when there is a conversation, and nothing else. Nor does it
 * report the reviewer's activity, so the row reads it from the runs: working
 * while one runs, idle once one has, not started before.
 */
export function cloudSessionReviews(state: CloudSessionReviewState): SessionReviews & { availableReviewerHarnesses: string[] } {
	const runs = state.runs ?? [];
	return {
		reviewerHandleId: "",
		reviewerActivityState: runs.some((run) => run.status === "running") ? "active" : runs.length ? "idle" : undefined,
		reviewerHarness: state.reviewerHarness,
		reviewerSurface: state.reviewerSurface ? { mode: "chat", reviewId: state.reviewerSurface.reviewId, harness: state.reviewerSurface.harness } : undefined,
		reviews: (state.reviews ?? []).map(cloudPRReviewState),
		runs: runs.map(cloudReviewRun),
		availableReviewerHarnesses: state.availableReviewerHarnesses ?? [],
	};
}
