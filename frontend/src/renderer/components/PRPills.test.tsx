import { describe, expect, it } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { noPRDetails } from "../lib/pr-display";
import { prStatusDot, sortSessionPRs } from "./PRPills";

const pr = (number: number, overrides: Partial<SessionPRSummary> = {}): SessionPRSummary => ({
	...noPRDetails(),
	url: `https://github.com/acme/repo/pull/${number}`,
	number,
	title: `PR ${number}`,
	state: "open",
	provider: "github",
	repo: "acme/repo",
	author: "ada",
	sourceBranch: `feat/${number}`,
	targetBranch: "main",
	headSha: `sha-${number}`,
	additions: 1,
	deletions: 0,
	changedFiles: 1,
	ci: { autoInjectCI: true, state: "passing", failingChecks: [], checks: [] },
	review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
	mergeability: { state: "mergeable", reasons: [], prUrl: "", conflictFiles: [] },
	updatedAt: "2026-11-01T00:00:00Z",
	...overrides,
});

describe("PR pills", () => {
	it("orders open PRs, then drafts, then merged and closed, newest first in each", () => {
		const sorted = sortSessionPRs([pr(1, { state: "merged" }), pr(2, { state: "draft" }), pr(3), pr(4), pr(5, { state: "closed" })]);
		expect(sorted.map((item) => item.number)).toEqual([4, 3, 2, 5, 1]);
	});

	it("dots the one fact that matters, the blocker before the review before readiness", () => {
		expect(prStatusDot(pr(1, { mergeability: { state: "conflicting", reasons: [], prUrl: "", conflictFiles: [] }, ci: { autoInjectCI: true, state: "failing", failingChecks: [], checks: [] } })).labelKey).toBe("pr.card.mergeConflict");
		expect(prStatusDot(pr(2, { ci: { autoInjectCI: true, state: "failing", failingChecks: [], checks: [] } })).labelKey).toBe("pr.card.checksFailing");
		expect(prStatusDot(pr(3, { review: { decision: "changes_requested", hasUnresolvedHumanComments: false, unresolvedBy: [] } })).labelKey).toBe("pr.review.changesRequested");
		expect(prStatusDot(pr(4)).labelKey).toBe("pr.card.readyToMerge");
		expect(prStatusDot(pr(5, { ci: { autoInjectCI: true, state: "pending", failingChecks: [], checks: [] } }))).toMatchObject({ labelKey: "pr.card.checksPending", breathe: true });
		expect(prStatusDot(pr(6, { state: "merged" })).labelKey).toBe("pr.state.merged");
	});
});
