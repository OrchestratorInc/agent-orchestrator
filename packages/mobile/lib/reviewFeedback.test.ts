import { describe, expect, it } from "vitest";
import type { ReviewRun } from "./api";
import { formatReviewSummaryMessage, reviewRunUrl } from "./reviewFeedback";

const run = (id: string, createdAt: string, prUrl = "https://github.com/ao/repo/pull/42"): ReviewRun => ({
	id, reviewId: "review", sessionId: "session", batchId: "batch", harness: "codex", triggerSource: "auto", prUrl,
	targetSha: "abcdef123456", status: "completed", verdict: "approved", body: "Looks good.", githubReviewId: "99", createdAt,
	autoInjectReview: true,
});

describe("mobile review feedback", () => {
	it("builds the exact GitHub review URL and worker summary", () => {
		expect(reviewRunUrl(run("one", "2026-01-01"))).toBe("https://github.com/ao/repo/pull/42#pullrequestreview-99");
		expect(formatReviewSummaryMessage(run("one", "2026-01-01"))).toContain("Review URL: https://github.com/ao/repo/pull/42#pullrequestreview-99");
	});
});
