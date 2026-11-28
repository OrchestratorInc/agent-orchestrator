import type { ReviewRun } from "./api";

export function reviewRunUrl(run: ReviewRun): string | undefined {
	if (!run.githubReviewId || !run.prUrl) return undefined;
	return `${run.prUrl}#pullrequestreview-${run.githubReviewId}`;
}

export function formatReviewSummaryMessage(run: ReviewRun): string {
	const url = reviewRunUrl(run);
	return [
		`Review summary (${run.harness || "reviewer"}, commit ${run.targetSha.slice(0, 8)}):`,
		run.body?.trim() || "No written findings.",
		url ? `Review URL: ${url}` : undefined,
	].filter(Boolean).join("\n\n");
}
