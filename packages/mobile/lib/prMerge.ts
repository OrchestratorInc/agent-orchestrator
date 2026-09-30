import type { SessionPRSummary } from "./api";

export type MergeReadiness = {
	canMerge: boolean;
	/** Short status, desktop's `pr.merge.*` wording. */
	label: string;
	/** Why it cannot merge yet, when it cannot. */
	reason?: string;
};

type MergeFacts = Pick<SessionPRSummary, "state" | "ci" | "mergeability" | "review" | "url" | "headSha">;

/**
 * Mirrors desktop's `prCanMerge` (frontend/src/renderer/lib/pr-display.ts): an
 * open PR with passing checks, a mergeable head, a review decision that allows
 * it, and no unresolved human comments. The head sha is required because the
 * merge is fenced to it.
 */
export function mergeReadiness(pr: MergeFacts): MergeReadiness {
	if (pr.state === "merged") return { canMerge: false, label: "Merged" };
	if (pr.state === "closed") return { canMerge: false, label: "Closed" };
	if (pr.state === "draft") return { canMerge: false, label: "Not mergeable yet", reason: "Draft pull requests can't be merged." };
	if (pr.mergeability.state === "conflicting") {
		return { canMerge: false, label: "Conflict", reason: "Conflicts with the base branch" };
	}
	if (pr.ci.state === "failing") {
		return { canMerge: false, label: "Blocked", reason: "Failing checks block this PR." };
	}
	if (pr.ci.state !== "passing" || pr.mergeability.state === "unknown") {
		return { canMerge: false, label: "Checking", reason: "Waiting for the latest checks and review state." };
	}
	if (pr.review.decision === "changes_requested" || pr.review.decision === "review_required") {
		return { canMerge: false, label: "Blocked", reason: "A required review must be completed before this PR can merge." };
	}
	if (pr.review.hasUnresolvedHumanComments) {
		return { canMerge: false, label: "Blocked", reason: "Unresolved review comments must be resolved before this PR can merge." };
	}
	if (pr.mergeability.state !== "mergeable") {
		return { canMerge: false, label: pr.mergeability.state === "unstable" ? "Unstable" : "Blocked", reason: "GitHub currently reports this pull request can't be merged." };
	}
	if (!pr.url || !pr.headSha) {
		return { canMerge: false, label: "Checking", reason: "Waiting for the latest checks and review state." };
	}
	return { canMerge: true, label: "Mergeable", reason: "No merge conflict, checks are passing, and the review requirement is satisfied." };
}
