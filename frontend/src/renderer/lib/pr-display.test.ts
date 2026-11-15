import { describe, expect, it } from "vitest";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import type { PullRequestFacts, SessionBranchState, WorkspaceSession } from "../types/workspace";
import {
	noPRDetails,
	prBrowserUrl,
	prCanMerge,
	prCardPresentation,
	prChecksTally,
	prCheckedOut,
	prDiffSummary,
	prHasNoChecks,
	noChecksSettlesAt,
	PR_NO_CHECKS_SETTLE_MS,
	prSummaryBlockReason,
	prThreadDiffUrl,
	prStatusRows,
	prSummaryParts,
	sessionBranchPR,
	sessionPRDisplaySummaries,
} from "./pr-display";

const summary = (overrides: Partial<SessionPRSummary> = {}): SessionPRSummary => ({
	...noPRDetails(),
	url: "https://github.com/acme/repo/pull/7",
	htmlUrl: "https://github.com/acme/repo/pull/7",
	number: 7,
	title: "Fix dashboard",
	state: "open",
	provider: "github",
	repo: "acme/repo",
	author: "ada",
	sourceBranch: "fix/dashboard",
	targetBranch: "main",
	headSha: "abc123",
	additions: 10,
	deletions: 3,
	changedFiles: 2,
	ci: { autoInjectCI: true, state: "passing", failingChecks: [], checks: [] },
	review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] },
	mergeability: { state: "mergeable", reasons: [], prUrl: "https://github.com/acme/repo/pull/7" },
	updatedAt: "2026-06-15T00:00:00Z",
	observedAt: "2026-06-15T00:00:00Z",
	ciObservedAt: "2026-06-15T00:00:00Z",
	reviewObservedAt: "2026-06-15T00:00:00Z",
	...overrides,
});

const session = (prs: WorkspaceSession["prs"]): WorkspaceSession => ({
	id: "sess-1",
	workspaceId: "ws-1",
	workspaceName: "repo",
	title: "Fix timing",
	provider: "codex",
	branch: "feat/timing",
	status: "review_pending",
	updatedAt: "2026-06-15T12:00:00Z",
	prs,
});

function sessionWith(overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id: "sess-1",
		workspaceId: "ws-1",
		workspaceName: "my-app",
		title: "fix-bug",
		provider: "claude-code",
		branch: "feat/x",
		status: "working",
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		...overrides,
	};
}

describe("sessionPRDisplaySummaries", () => {
	it("keeps GitHub PR #7 and GitLab MR #7 distinct in the same session", () => {
		// Both providers have a PR/MR numbered 7. Keying by number alone collapses
		// them into one summary (the second wins), hiding the other. Keying by the
		// canonical web URL keeps both visible.
		const githubPR: PullRequestFacts = {
			url: "https://github.com/acme/repo/pull/7",
			number: 7,
			state: "open",
			ci: "passing",
			review: "approved",
			mergeability: "mergeable",
			reviewComments: false,
			updatedAt: "2026-06-15T00:00:00Z",
		};
		const gitlabMR: PullRequestFacts = {
			url: "https://gitlab.com/acme/repo/-/merge_requests/7",
			number: 7,
			state: "open",
			ci: "passing",
			review: "approved",
			mergeability: "mergeable",
			reviewComments: false,
			updatedAt: "2026-06-15T00:00:00Z",
		};
		const session = sessionWith({ prs: [githubPR, gitlabMR] });
		const githubSummary = summary({
			url: "https://github.com/acme/repo/pull/7",
			htmlUrl: "https://github.com/acme/repo/pull/7",
			number: 7,
			provider: "github",
			repo: "acme/repo",
			mergeability: {
				state: "mergeable",
				reasons: [],
				prUrl: "https://github.com/acme/repo/pull/7",
			},
		});
		const gitlabSummary = summary({
			url: "https://gitlab.com/acme/repo/-/merge_requests/7",
			htmlUrl: "https://gitlab.com/acme/repo/-/merge_requests/7",
			number: 7,
			provider: "gitlab",
			repo: "acme/repo",
			mergeability: {
				state: "mergeable",
				reasons: [],
				prUrl: "https://gitlab.com/acme/repo/-/merge_requests/7",
			},
		});

		const result = sessionPRDisplaySummaries(session, [githubSummary, gitlabSummary]);

		// Both summaries appear — not deduped to one.
		expect(result).toHaveLength(2);
		const urls = result.map((r) => r.url).sort();
		expect(urls).toEqual(["https://github.com/acme/repo/pull/7", "https://gitlab.com/acme/repo/-/merge_requests/7"]);
		// Each retains its own provider/repo, no summary is hidden.
		const github = result.find((r) => r.url === "https://github.com/acme/repo/pull/7");
		const gitlab = result.find((r) => r.url === "https://gitlab.com/acme/repo/-/merge_requests/7");
		expect(github?.provider).toBe("github");
		expect(gitlab?.provider).toBe("gitlab");
	});

	it("does not drop summaries whose url is empty, falling back to number:${number}", () => {
		// Two facts with empty urls and distinct numbers must both surface. The
		// fallback key `number:${number}` keeps them distinct within one provider
		// even when the canonical URL is not yet populated.
		const session = sessionWith({
			prs: [
				{
					url: "",
					number: 7,
					state: "open",
					ci: "passing",
					review: "approved",
					mergeability: "mergeable",
					reviewComments: false,
					updatedAt: "2026-06-15T00:00:00Z",
				},
				{
					url: "",
					number: 8,
					state: "open",
					ci: "passing",
					review: "approved",
					mergeability: "mergeable",
					reviewComments: false,
					updatedAt: "2026-06-15T00:00:00Z",
				},
			],
		});
		const summaries: SessionPRSummary[] = [
			summary({
				url: "",
				htmlUrl: undefined,
				number: 7,
			}),
			summary({
				url: "",
				htmlUrl: undefined,
				number: 8,
			}),
		];

		const result = sessionPRDisplaySummaries(session, summaries);

		expect(result.map((r) => r.number).sort((a, b) => a - b)).toEqual([7, 8]);
	});
});

describe("sessionBranchPR", () => {
	const quickNav = summary({ number: 4, sourceBranch: "ao/quick-nav", headSha: "5b20471" });
	const faq = summary({ number: 5, state: "draft", sourceBranch: "ao/faq-questions", headSha: "614aaa3" });
	const branch = (state: Partial<NonNullable<WorkspaceSession["branchState"]>>) =>
		sessionWith({ branchState: { commits: 3, unpushed: 0, behind: 0, ...state } });

	it("picks the PR whose branch is checked out, not the first open one", () => {
		expect(sessionBranchPR(branch({ remoteBranch: "origin/ao/faq-questions", head: "614aaa3" }), [quickNav, faq])?.number).toBe(5);
	});

	it("keeps the PR while local commits are not pushed yet", () => {
		expect(sessionBranchPR(branch({ remoteBranch: "origin/ao/faq-questions", head: "new0001", unpushed: 1 }), [quickNav, faq])?.number).toBe(5);
	});

	it("matches the tip commit when the branch has no upstream", () => {
		expect(sessionBranchPR(branch({ head: "614aaa3" }), [quickNav, faq])?.number).toBe(5);
	});

	it("finds none when the checked-out branch has no open PR", () => {
		expect(sessionBranchPR(branch({ remoteBranch: "origin/ao/other", head: "fff0001" }), [quickNav, faq])).toBeUndefined();
		expect(sessionBranchPR(branch({ remoteBranch: "origin/ao/faq-questions" }), [quickNav, { ...faq, state: "merged" }])).toBeUndefined();
	});

	it("falls back to the first open PR before the daemon reports the branch", () => {
		expect(sessionBranchPR(sessionWith(), [quickNav, faq])?.number).toBe(4);
	});
});

describe("prStatusRows", () => {
	it("shows unavailable checks without a loading indicator when cloud cannot read CI", () => {
		const pr = summary({ ci: { autoInjectCI: true, state: "unknown", failingChecks: [], checks: [] }, mergeability: {
			state: "mergeable", reasons: ["github_checks_unavailable"], prUrl: "https://github.com/acme/repo/pull/7",
		} });
		const card = prCardPresentation(pr);
		expect(card.statusRows?.find((row) => row.key === "ci")).toMatchObject({ label: "Checks unavailable", breathe: false });
		expect(card.readiness?.label).toBe("Status unavailable");
		expect(card.readiness?.detail).toContain("Checks and Commit statuses read access");
	});
	it("shows a run cancelled with no replacement as settled, not loading", () => {
		const pr = summary({ ci: { autoInjectCI: true, checks: [], state: "unknown", failingChecks: [
			{ name: "lint", status: "cancelled", conclusion: "cancelled" },
		] } });
		const card = prCardPresentation(pr);
		expect(card.statusRows?.find((row) => row.key === "ci")).toMatchObject({ label: "Checks cancelled", breathe: false });
		expect(card.readiness?.label).toBe("Checks cancelled");
		expect(prStatusRows(pr).find((row) => row.key === "ci")?.value).toBe("Checks cancelled");
	});
	it("shows lost GitHub App access instead of checking merge status", () => {
		const pr = summary({ mergeability: {
			state: "unknown", reasons: ["github_access_lost"], prUrl: "https://github.com/acme/repo/pull/7",
		} });
		const merge = prStatusRows(pr).find((row) => row.key === "merge");
		expect(merge?.value).toBe("Status unavailable");
		expect(prCardPresentation(pr).readiness?.detail).toBe("GitHub access to this repository is unavailable, so merge readiness cannot be checked.");
	});

	it("formats the three PR states without exposing raw unknown", () => {
		const rows = prStatusRows(
			summary({
				ci: { autoInjectCI: true, state: "unknown", failingChecks: [], checks: [] },
				review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
				mergeability: { state: "unknown", reasons: [], prUrl: "https://github.com/acme/repo/pull/7" },
			}),
		);

		expect(rows.map((row) => `${row.label}:${row.value}`)).toEqual(["CI:Checking", "Merge:Checking", "Review:None"]);
	});

	it("includes minimal diff detail on the merge row", () => {
		const rows = prStatusRows(summary({ changedFiles: 4, additions: 25, deletions: 2 }));
		expect(rows.find((row) => row.key === "merge")?.detail).toBe("4 files");
	});
});

describe("prDiffSummary", () => {
	it("formats file and line delta metadata", () => {
		expect(prDiffSummary(summary({ changedFiles: 6, additions: 42, deletions: 8 }))).toBe("6 files · +42 -8");
	});

	it("omits the diff label when no diff metadata is available", () => {
		expect(prDiffSummary(summary({ changedFiles: 0, additions: 0, deletions: 0 }))).toBeUndefined();
	});
});

describe("prCardPresentation", () => {
	it("renders when the provider returns null merge reasons", () => {
		const malformed = summary({
			mergeability: { state: "blocked", reasons: null, prUrl: "https://github.com/acme/repo/pull/7" } as unknown as SessionPRSummary["mergeability"],
		});

		expect(() => prCardPresentation(malformed)).not.toThrow();
	});

	const priorityCases: Array<[string, Partial<SessionPRSummary>, string]> = [
		["conflict + passing + approval", { mergeability: { state: "conflicting", reasons: [], prUrl: "" } }, "Not mergeable yet"],
		["clean + passing + approval", { mergeability: { state: "mergeable", reasons: [], prUrl: "" } }, "Mergeable"],
		["clean + failing + approval", { ci: { autoInjectCI: true, state: "failing", failingChecks: [], checks: [] }, mergeability: { state: "mergeable", reasons: [], prUrl: "" } }, "Not mergeable yet"],
	];
	it.each(priorityCases)("renders the priority stack for %s", (_name, overrides, readiness) => {
		const presentation = prCardPresentation(summary(overrides));
		expect(presentation.statusRows?.map((status) => status.label)).toEqual(
			readiness === "Mergeable" ? ["Checks passing", "Review status"] : overrides.mergeability?.state === "conflicting" ? ["Merge conflict", "Checks passing", "Review status"] : ["Checks failing", "Review status"],
		);
		expect(presentation.readiness?.label).toBe(readiness);
	});

	it("shows a required review once instead of repeating it as a merge blocker", () => {
		const presentation = prCardPresentation(
			summary({
				review: { decision: "review_required", hasUnresolvedHumanComments: false, unresolvedBy: [] },
				mergeability: {
					state: "blocked",
					reasons: ["review_required"],
					prUrl: "https://github.com/acme/repo/pull/7",
				},
			}),
		);

		expect(presentation.primary).toMatchObject({
			key: "review",
			label: "Review required",
			detail: "Merge blocked until a required review is submitted.",
			tone: "review",
		});
		expect(presentation.supporting.map((status) => status.label)).toEqual(["Checks passing"]);
	});

	it("treats a PR that needs no review as mergeable", () => {
		const presentation = prCardPresentation(
			summary({ review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] } }),
		);

		expect(presentation.readiness).toMatchObject({
			label: "Mergeable",
			detail: "No merge conflict, checks are passing, and the review requirement is satisfied.",
			tone: "success",
		});
		expect(presentation.statusRows?.find((status) => status.key === "review")?.detail).toBe("No review required");
	});

	it("names unresolved comments on a PR that needs no review, which its warning stands for", () => {
		const presentation = prCardPresentation(
			summary({ review: { decision: "none", hasUnresolvedHumanComments: true, unresolvedBy: [] } }),
		);

		expect(presentation.statusRows?.find((status) => status.key === "review")).toMatchObject({
			detail: "Unresolved review comments",
			tone: "warning",
		});
	});

	it.each(["review_required", "changes_requested"] as const)("does not call a %s PR mergeable", (decision) => {
		const presentation = prCardPresentation(
			summary({ review: { decision, hasUnresolvedHumanComments: false, unresolvedBy: [] } }),
		);

		expect(presentation.readiness?.label).toBe("Not mergeable yet");
	});

	it.each([
		["review_required", "A required review must be completed before this PR can merge."],
		["approved", "The review requirement is satisfied, but failing checks still block this PR."],
	] as const)("only says the review is satisfied on a failing %s PR when it is", (decision, detail) => {
		const presentation = prCardPresentation(
			summary({
				ci: { autoInjectCI: true, checks: [], state: "failing", failingChecks: [] },
				review: { decision, hasUnresolvedHumanComments: false, unresolvedBy: [] },
			}),
		);

		expect(presentation.readiness).toMatchObject({ label: "Not mergeable yet", detail });
	});

	it.each(["blocked", "unstable"] as const)("does not call a %s PR mergeable", (state) => {
		const presentation = prCardPresentation(
			summary({ mergeability: { state, reasons: [], prUrl: "https://github.com/acme/repo/pull/7" } }),
		);

		expect(presentation.readiness).toMatchObject({
			label: "Not mergeable yet",
			detail: "GitHub currently reports this pull request can't be merged.",
		});
	});

	it.each(["approved", "none"] as const)("does not call a %s PR with unresolved review comments mergeable", (decision) => {
		const presentation = prCardPresentation(
			summary({ review: { decision, hasUnresolvedHumanComments: true, unresolvedBy: [] } }),
		);

		expect(presentation.readiness).toMatchObject({
			label: "Not mergeable yet",
			detail: "Unresolved review comments must be resolved before this PR can merge.",
		});
	});

	it("keeps the verdict only when no readiness row explains it", () => {
		const explained = (overrides: Parameters<typeof summary>[0]) => prCardPresentation(summary(overrides)).readinessExplained;

		expect(explained({})).toBe(true);
		expect(explained({ ci: { autoInjectCI: true, state: "failing", failingChecks: [], checks: [] } })).toBe(true);
		expect(explained({ ci: { autoInjectCI: true, state: "pending", failingChecks: [], checks: [] } })).toBe(true);
		expect(explained({ review: { decision: "review_required", hasUnresolvedHumanComments: false, unresolvedBy: [] } })).toBe(true);
		// GitHub blocks it for a reason no row names, such as a branch rule.
		expect(explained({ mergeability: { state: "blocked", reasons: [], prUrl: "https://github.com/acme/repo/pull/7" } })).toBe(false);
		// A passing row doesn't say why the checks are unknown.
		expect(explained({
			ci: { autoInjectCI: true, state: "unknown", failingChecks: [], checks: [] },
			mergeability: { state: "mergeable", reasons: [], prUrl: "https://github.com/acme/repo/pull/7" },
			review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
		})).toBe(false);
	});

	it("shows checking merge readiness while provider state is pending", () => {
		const presentation = prCardPresentation(
			summary({
				ci: { autoInjectCI: true, state: "pending", failingChecks: [], checks: [] },
				mergeability: { state: "unknown", reasons: [], prUrl: "https://github.com/acme/repo/pull/7" },
			}),
		);

		expect(presentation.readiness?.label).toBe("Checking merge readiness");
		expect(presentation.readiness?.detail).toBe("Waiting for the latest checks and review state.");
	});

	it("prioritizes failing checks over lower-priority review and merge facts", () => {
		const presentation = prCardPresentation(
			summary({
				ci: {
					autoInjectCI: true,
					checks: [],
					state: "failing",
					failingChecks: [{ name: "unit", status: "failed", conclusion: "failure", url: "https://ci/unit" }],
				},
				review: { decision: "review_required", hasUnresolvedHumanComments: false, unresolvedBy: [] },
				mergeability: {
					state: "blocked",
					reasons: ["review_required"],
					prUrl: "https://github.com/acme/repo/pull/7",
				},
			}),
		);

		expect(presentation.primary.label).toBe("Checks failing");
		expect(presentation.primary.links[0]).toMatchObject({ label: "unit", href: "https://ci/unit" });
		expect(presentation.supporting).toEqual([]);
	});

	it("retains running checks as linked supporting state when review is the primary action", () => {
		const presentation = prCardPresentation(
			summary({
				ci: { autoInjectCI: true, state: "pending", failingChecks: [], checks: [] },
				review: { decision: "review_required", hasUnresolvedHumanComments: false, unresolvedBy: [] },
				mergeability: {
					state: "blocked",
					reasons: ["review_required"],
					prUrl: "https://github.com/acme/repo/pull/7",
				},
			}),
		);

		expect(presentation.primary.label).toBe("Review required");
		expect(presentation.supporting[0]).toMatchObject({
			label: "Checks running",
			href: "https://github.com/acme/repo/pull/7/checks",
			breathe: true,
		});
	});

	it("shows running checks instead of an internal provider blocker", () => {
		const presentation = prCardPresentation(
			summary({
				ci: { autoInjectCI: true, state: "pending", failingChecks: [], checks: [] },
				mergeability: {
					state: "unstable",
					reasons: ["blocked_by_provider"],
					prUrl: "https://github.com/acme/repo/pull/7",
				},
			}),
		);

		expect(presentation.primary).toMatchObject({
			key: "ci",
			label: "Checks running",
			href: "https://github.com/acme/repo/pull/7/checks",
			breathe: true,
		});
		expect(presentation.supporting).toEqual([]);
	});

	it("uses customer-facing copy for an unexplained merge blocker", () => {
		const presentation = prCardPresentation(
			summary({
				mergeability: {
					state: "blocked",
					reasons: ["blocked_by_provider"],
					prUrl: "https://github.com/acme/repo/pull/7",
				},
			}),
		);

		expect(presentation.primary).toMatchObject({
			key: "merge",
			label: "Merge unavailable",
			detail: "GitHub currently reports this pull request can't be merged.",
			links: [],
		});
	});

	it("names GitLab when a merge request is blocked", () => {
		const url = "https://gitlab.com/acme/repo/-/merge_requests/7";
		const presentation = prCardPresentation(
			summary({
				url,
				htmlUrl: url,
				provider: "gitlab",
				mergeability: { state: "blocked", reasons: ["blocked_by_provider"], prUrl: url },
			}),
		);

		expect(presentation.primary.detail).toBe("GitLab currently reports this pull request can't be merged.");
		expect(presentation.readiness?.detail).toBe("GitLab currently reports this pull request can't be merged.");
	});
});

describe("merge readiness rows", () => {
	const labels = (pr: SessionPRSummary) => prCardPresentation(pr).readinessRows?.map((row) => row.label);
	const reviewEntry = (reviewerId: string, verdict: "approved" | "changes_requested", submittedAt = "2026-06-15T00:00:00Z") => ({
		autoInjectReview: false,
		reviewerId,
		submittedAt,
		verdict,
	});

	it("splits the conflicts and review rows the way the checks row reads: what it is about, then where it stands", () => {
		const facts = (pr: SessionPRSummary) => prCardPresentation(pr).readinessRows?.filter((row) => row.key !== "ci").map((row) => row.fact);
		expect(facts(summary({ review: { decision: "review_required", hasUnresolvedHumanComments: false, unresolvedBy: [] } }))).toEqual([
			{ title: "Conflicts", value: "None", state: "passed" },
			{ title: "Review", value: "Required", state: "pending" },
		]);
		expect(facts(summary({
			mergeability: { state: "conflicting", reasons: [], prUrl: "", conflictFiles: [] },
			review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [], reviews: [reviewEntry("grace", "approved")] },
		}))).toEqual([
			{ title: "Conflicts", value: "Conflicts with the base branch", state: "failed" },
			{ title: "Review", value: "Approved by grace", state: "passed" },
		]);
		expect(facts(summary({
			mergeability: { state: "unknown", reasons: [], prUrl: "", conflictFiles: [] },
			review: { decision: "changes_requested", hasUnresolvedHumanComments: false, unresolvedBy: [], reviews: [reviewEntry("linus", "changes_requested")] },
		}))).toEqual([
			{ title: "Conflicts", value: "Unknown", state: "pending" },
			{ title: "Review", value: "Changes requested by linus", state: "failed" },
		]);
	});

	it("always shows conflicts, checks and review, saying no reviews yet instead of no review required", () => {
		const pr = summary({ review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] } });
		expect(labels(pr)).toEqual(["No conflicts", "Checks passing", "No reviews yet"]);
		expect(prCardPresentation(pr).readinessRows?.[2]).toMatchObject({ tone: "passive" });
	});

	it("says what is still unknown while GitHub is computing", () => {
		const pr = summary({
			ci: { autoInjectCI: true, state: "unknown", failingChecks: [], checks: [] },
			mergeability: { state: "unknown", reasons: [], prUrl: "https://github.com/acme/repo/pull/7" },
			review: { decision: "review_required", hasUnresolvedHumanComments: false, unresolvedBy: [] },
		});
		expect(labels(pr)).toEqual(["Conflicts unknown", "Checks unknown", "Review required"]);
		// The row opens to the checks, each with its own link, so it has none of its own.
		expect(prCardPresentation(pr).readinessRows?.[1]?.href).toBeUndefined();
	});

	it("counts the checks at each state, worst first, for the checks row", () => {
		const check = (name: string, status: "passed" | "failed" | "skipped" | "cancelled" | "queued" | "in_progress" | "unknown") => ({ name, status });
		const running = summary({
			ci: { autoInjectCI: true, state: "pending", failingChecks: [], checks: [check("test", "passed"), check("lint", "skipped"), check("build", "in_progress"), check("e2e", "queued")] },
		});
		expect(prChecksTally(running)).toEqual({
			title: "Checks",
			counts: [
				{ state: "running", count: 1, label: "1 running" },
				{ state: "pending", count: 1, label: "1 pending" },
				{ state: "passed", count: 1, label: "1 passed" },
				{ state: "skipped", count: 1, label: "1 skipped" },
			],
		});
		const row = prCardPresentation(running, {}).readinessRows?.find((status) => status.key === "ci");
		expect(row?.checks?.counts.map((count) => count.label)).toEqual(["1 running", "1 pending", "1 passed", "1 skipped"]);
		// A cancelled run counts as failed, and a check GitHub hasn't reported waits with the queued ones.
		const failing = summary({
			ci: { autoInjectCI: true, state: "failing", failingChecks: [], checks: [check("test", "passed"), check("format", "failed"), check("e2e", "cancelled"), check("docs", "unknown")] },
		});
		expect(prChecksTally(failing)?.counts.map((count) => [count.state, count.count])).toEqual([["failed", 2], ["pending", 1], ["passed", 1]]);
		expect(prChecksTally(summary({ ci: { autoInjectCI: true, state: "unknown", failingChecks: [], checks: [] } }))).toBeUndefined();
	});

	it("names reviewers by their newest verdict", () => {
		const pr = summary({
			review: {
				decision: "changes_requested",
				hasUnresolvedHumanComments: false,
				unresolvedBy: [],
				reviews: [reviewEntry("ada", "changes_requested", "2026-06-14T00:00:00Z"), reviewEntry("ada", "approved"), reviewEntry("lin", "changes_requested")],
			},
		});
		expect(labels(pr)).toEqual(["No conflicts", "Checks passing", "Changes requested by lin"]);
	});

	it("keeps a voluntary review when no review is required", () => {
		const pr = summary({
			review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [], reviews: [reviewEntry("ada", "approved")] },
		});
		expect(labels(pr)?.at(-1)).toBe("Approved by ada");
	});

	it("shows unresolved comments, which still block the merge without a review rule", () => {
		const pr = summary({
			review: { decision: "none", hasUnresolvedHumanComments: true, unresolvedBy: [{ count: 2, links: [], reviewerId: "ada" }] },
		});
		expect(prCardPresentation(pr).readinessRows?.at(-1)).toMatchObject({ label: "2 unresolved comments", tone: "warning" });
	});

	it("shows a merge conflict in the conflicts row", () => {
		const pr = summary({ mergeability: { state: "conflicting", reasons: [], prUrl: "https://github.com/acme/repo/pull/7" } });
		expect(prCardPresentation(pr).readinessRows?.[0]).toMatchObject({ label: "Merge conflict", tone: "error" });
	});
});

describe("prCheckedOut", () => {
	const pr = { headSha: "abc", sourceBranch: "lab/show" } as SessionPRSummary;
	const branch = (over: Partial<SessionBranchState>) => ({ behind: 0, commits: 1, conflicts: false, unpushed: 0, ...over });

	it("is true on the PR's commit or tracking its branch, even with newer local work", () => {
		expect(prCheckedOut(pr, branch({ head: "abc" }))).toBe(true);
		expect(prCheckedOut(pr, branch({ head: "def", remoteBranch: "origin/lab/show" }))).toBe(true);
	});

	it("is false on another branch, and true until the branch is observed", () => {
		expect(prCheckedOut(pr, branch({ head: "def", remoteBranch: "origin/ao/task/root" }))).toBe(false);
		// A branch whose name only ends the same way is another branch.
		expect(prCheckedOut(pr, branch({ head: "def", remoteBranch: "origin/team/lab/show" }))).toBe(false);
		expect(prCheckedOut(pr, branch({ head: "def" }))).toBe(false);
		expect(prCheckedOut(pr, undefined)).toBe(true);
	});
});

describe("prThreadDiffUrl", () => {
	it("points a GitHub review comment at the PR's files", () => {
		expect(prThreadDiffUrl("https://github.com/acme/app/pull/24#discussion_r42")).toBe("https://github.com/acme/app/pull/24/files#r42");
	});

	it("leaves any other link alone", () => {
		const url = "https://gitlab.com/acme/app/-/merge_requests/9#note_1";
		expect(prThreadDiffUrl(url)).toBe(url);
	});
});

describe("prBrowserUrl", () => {
	it("normalizes issue-shaped GitHub PR URLs to the pull request page", () => {
		expect(
			prBrowserUrl(
				summary({
					url: "https://github.com/acme/repo/issues/7",
					htmlUrl: "https://github.com/acme/repo/issues/7",
				}),
			),
		).toBe("https://github.com/acme/repo/pull/7");
	});

	it("normalizes GitLab merge request URLs", () => {
		expect(
			prBrowserUrl(
				summary({
					provider: "gitlab",
					url: "https://gitlab.com/acme/repo/-/merge_requests/7",
					htmlUrl: "https://gitlab.com/acme/repo/-/merge_requests/7",
					mergeability: {
						state: "mergeable",
						reasons: [],
						prUrl: "https://gitlab.com/acme/repo/-/merge_requests/7",
					},
				}),
			),
		).toBe("https://gitlab.com/acme/repo/-/merge_requests/7");
	});

	it("strips query params and fragments from GitLab MR URLs", () => {
		expect(
			prBrowserUrl(
				summary({
					provider: "gitlab",
					url: "https://gitlab.com/acme/repo/-/merge_requests/7/diffs?view=inline#note_123",
					htmlUrl: "https://gitlab.com/acme/repo/-/merge_requests/7/diffs?view=inline#note_123",
					mergeability: {
						state: "mergeable",
						reasons: [],
						prUrl: "https://gitlab.com/acme/repo/-/merge_requests/7",
					},
				}),
			),
		).toBe("https://gitlab.com/acme/repo/-/merge_requests/7");
	});
});

describe("sessionPRDisplaySummaries", () => {
	it("leaves lifecycle timing absent for fallback PR facts until provider times arrive", () => {
		const [fallback] = sessionPRDisplaySummaries(
			session([
				{
					url: "https://github.com/acme/repo/pull/7",
					number: 7,
					state: "open",
					ci: "passing",
					review: "none",
					mergeability: "mergeable",
					reviewComments: false,
					updatedAt: "2026-06-15T11:00:00Z",
				},
			]),
		);

		expect(fallback.updatedAt).toBe("2026-06-15T11:00:00Z");
		expect(fallback.createdAt).toBeUndefined();
		expect(fallback.stateChangedAt).toBeUndefined();
	});

	it("deduplicates repeated API summaries before rendering", () => {
		const got = sessionPRDisplaySummaries(session([]), [
			summary({ number: 7, title: "first observed PR #7" }),
			summary({ number: 7, title: "duplicate PR #7" }),
			summary({ number: 8, title: "PR #8", url: "https://github.com/acme/repo/pull/8", htmlUrl: "https://github.com/acme/repo/pull/8" }),
		]);

		expect(got.map((pr) => pr.number)).toEqual([7, 8]);
		expect(got[0].title).toBe("first observed PR #7");
	});

	it("keeps same-number PRs from different repositories distinct", () => {
		const got = sessionPRDisplaySummaries(session([]), [
			summary({
				url: "https://github.com/acme/api/pull/7",
				htmlUrl: "https://github.com/acme/api/pull/7",
				repo: "acme/api",
				number: 7,
				title: "API PR #7",
			}),
			summary({
				url: "https://github.com/acme/web/pull/7",
				htmlUrl: "https://github.com/acme/web/pull/7",
				repo: "acme/web",
				number: 7,
				title: "Web PR #7",
			}),
		]);

		expect(got.map((pr) => pr.title)).toEqual(["API PR #7", "Web PR #7"]);
	});

	it("keeps same-number PRs with the same repository basename from different owners distinct", () => {
		const got = sessionPRDisplaySummaries(session([]), [
			summary({
				url: "https://github.com/acme/repo/pull/7",
				htmlUrl: "https://github.com/acme/repo/pull/7",
				repo: "acme/repo",
				number: 7,
				title: "Acme PR #7",
				headSha: "acme-head",
			}),
			summary({
				url: "https://github.com/other/repo/pull/7",
				htmlUrl: "https://github.com/other/repo/pull/7",
				repo: "other/repo",
				number: 7,
				title: "Other PR #7",
				headSha: "other-head",
			}),
		]);

		expect(got.map((pr) => pr.title)).toEqual(["Acme PR #7", "Other PR #7"]);
	});

	it("uses the enriched summary for a unique session fact when URL identities differ", () => {
		const got = sessionPRDisplaySummaries(
			session([
				{
					url: "https://example.com/pr/8",
					number: 8,
					state: "draft",
					ci: "pending",
					review: "none",
					mergeability: "unknown",
					reviewComments: false,
					updatedAt: "2026-06-15T10:00:00Z",
				},
			]),
			[
				summary({
					number: 8,
					url: "https://api.github.com/repos/acme/repo/pulls/8",
					htmlUrl: "https://github.com/acme/repo/pull/8",
					title: "enriched PR #8",
				}),
			],
		);

		expect(got).toHaveLength(1);
		expect(got[0]).toMatchObject({
			number: 8,
			title: "enriched PR #8",
			htmlUrl: "https://github.com/acme/repo/pull/8",
		});
	});

	it("keeps a same-number summary for another repository after an exact fact match", () => {
		const got = sessionPRDisplaySummaries(
			session([
				{
					url: "https://github.com/acme/api/pull/7",
					number: 7,
					state: "open",
					ci: "passing",
					review: "approved",
					mergeability: "mergeable",
					reviewComments: false,
					updatedAt: "2026-06-15T10:00:00Z",
				},
			]),
			[
				summary({
					url: "https://github.com/acme/api/pull/7",
					htmlUrl: "https://github.com/acme/api/pull/7",
					repo: "acme/api",
					number: 7,
					title: "API PR #7",
				}),
				summary({
					url: "https://github.com/acme/web/pull/7",
					htmlUrl: "https://github.com/acme/web/pull/7",
					repo: "acme/web",
					number: 7,
					title: "Web PR #7",
				}),
			],
		);

		expect(got.map((pr) => pr.title)).toEqual(["API PR #7", "Web PR #7"]);
	});

	it("deduplicates repeated session facts and uses the enriched summary for the surviving card", () => {
		const got = sessionPRDisplaySummaries(
			session([
				{
					url: "https://github.com/acme/repo/pull/7",
					number: 7,
					state: "open",
					ci: "pending",
					review: "none",
					mergeability: "unknown",
					reviewComments: false,
					updatedAt: "2026-06-15T10:00:00Z",
				},
				{
					url: "https://github.com/acme/repo/issues/7",
					number: 7,
					state: "open",
					ci: "passing",
					review: "approved",
					mergeability: "mergeable",
					reviewComments: false,
					updatedAt: "2026-06-15T11:00:00Z",
				},
			]),
			[
				summary({ number: 7, title: "enriched PR #7", ci: { autoInjectCI: true, state: "passing", failingChecks: [], checks: [] } }),
				summary({ number: 7, title: "duplicate enriched PR #7" }),
			],
		);

		expect(got).toHaveLength(1);
		expect(got[0]).toMatchObject({ number: 7, title: "enriched PR #7", ci: { state: "passing" } });
	});

	it("keeps the next selected session's PR list independent after a duplicated list", () => {
		const firstSelection = sessionPRDisplaySummaries(
			session([
				{
					url: "https://github.com/acme/repo/pull/7",
					number: 7,
					state: "open",
					ci: "passing",
					review: "approved",
					mergeability: "mergeable",
					reviewComments: false,
					updatedAt: "2026-06-15T10:00:00Z",
				},
				{
					url: "https://github.com/acme/repo/issues/7",
					number: 7,
					state: "open",
					ci: "passing",
					review: "approved",
					mergeability: "mergeable",
					reviewComments: false,
					updatedAt: "2026-06-15T11:00:00Z",
				},
			]),
			[summary({ number: 7 }), summary({ number: 7 })],
		);
		const nextSelection = sessionPRDisplaySummaries(
			session([
				{
					url: "https://github.com/acme/repo/pull/9",
					number: 9,
					state: "open",
					ci: "passing",
					review: "approved",
					mergeability: "mergeable",
					reviewComments: false,
					updatedAt: "2026-06-15T12:00:00Z",
				},
			]),
		);

		expect(firstSelection.map((pr) => pr.number)).toEqual([7]);
		expect(nextSelection.map((pr) => pr.number)).toEqual([9]);
	});
});

describe("prSummaryParts", () => {
	it("always returns CI, Merge, and Review parts", () => {
		expect(prSummaryParts(summary()).map((part) => part.label)).toEqual(["CI", "Merge", "Review"]);
	});

	it("details active CI, merge, and review blockers under their parts", () => {
		const parts = prSummaryParts(
			summary({
				ci: {
					autoInjectCI: true,
					checks: [],
					state: "failing",
					failingChecks: [
						{ name: "copy-check", status: "failed", conclusion: "failure", url: "https://checks.example/copy" },
					],
				},
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: true,
					unresolvedBy: [
						{
							reviewerId: "alice",
							count: 6,
							links: [
								{
									url: "https://github.com/acme/repo/pull/7#discussion_r1",
									file: "main.go",
									line: 12,
									autoInjectReview: true,
								},
							],
						},
					],
				},
				mergeability: {
					state: "blocked",
					reasons: ["behind_base"],
					prUrl: "https://github.com/acme/repo/pull/7",
				},
			}),
		);

		expect(parts.map((part) => part.key)).toEqual(["ci", "merge", "review"]);
		expect(parts.find((part) => part.key === "ci")).toMatchObject({
			status: "Failing",
			summary: undefined,
			tone: "error",
		});
		expect(parts.find((part) => part.key === "ci")?.links[0]).toMatchObject({
			label: "copy-check",
			href: "https://checks.example/copy",
		});
		expect(parts.find((part) => part.key === "merge")).toMatchObject({
			status: "Blocked",
			summary: undefined,
			tone: "warning",
		});
		expect(parts.find((part) => part.key === "review")).toMatchObject({
			status: "Changes requested",
			summary: undefined,
			tone: "warning",
		});
		expect(parts.find((part) => part.key === "review")?.links[0]).toMatchObject({
			label: "alice +5",
			href: "https://github.com/acme/repo/pull/7#discussion_r1",
		});
	});

	it("links failing CI checks to their provider URLs", () => {
		const parts = prSummaryParts(
			summary({
				ci: {
					autoInjectCI: true,
					checks: [],
					state: "failing",
					failingChecks: [
						{ name: "unit", status: "failed", conclusion: "failure", url: "https://checks.example/unit" },
						{ name: "lint", status: "failed", conclusion: "failure", url: "https://checks.example/lint" },
						{ name: "build", status: "failed", conclusion: "failure", url: "https://checks.example/build" },
						{ name: "types", status: "failed", conclusion: "failure", url: "https://checks.example/types" },
					],
				},
			}),
		);

		const ciPart = parts.find((part) => part.key === "ci");
		expect(ciPart?.links).toEqual([
			{ label: "unit", href: "https://checks.example/unit", title: "failure" },
			{ label: "lint", href: "https://checks.example/lint", title: "failure" },
			{ label: "build", href: "https://checks.example/build", title: "failure" },
		]);
		expect(ciPart?.overflowLabel).toBe("+1 check");
	});

	it("prefers the submitted review summary over inline comments", () => {
		const parts = prSummaryParts(
			summary({
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: true,
					unresolvedBy: [
						{
							reviewerId: "alice",
							count: 2,
							reviewUrl: "https://github.com/acme/repo/pull/7#pullrequestreview-1",
							links: [
								{
									url: "https://github.com/acme/repo/pull/7#discussion_r1",
									file: "main.go",
									line: 12,
									autoInjectReview: true,
								},
								{
									url: "https://github.com/acme/repo/pull/7#discussion_r2",
									file: "test.go",
									line: 20,
									autoInjectReview: true,
								},
							],
						},
					],
				},
			}),
		);

		expect(parts.find((part) => part.key === "review")?.links[0]).toMatchObject({
			label: "alice +1",
			href: "https://github.com/acme/repo/pull/7#pullrequestreview-1",
			title: "Open requested-changes review from alice",
		});
	});

	it("falls back to the first inline comment when no review summary exists", () => {
		const parts = prSummaryParts(
			summary({
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: true,
					unresolvedBy: [
						{
							reviewerId: "alice",
							count: 2,
							links: [
								{
									url: "https://github.com/acme/repo/pull/7#discussion_r1",
									file: "main.go",
									line: 12,
									autoInjectReview: true,
								},
								{
									url: "https://github.com/acme/repo/pull/7#discussion_r2",
									file: "test.go",
									line: 20,
									autoInjectReview: true,
								},
							],
						},
					],
				},
			}),
		);

		expect(parts.find((part) => part.key === "review")?.links[0]).toMatchObject({
			label: "alice +1",
			href: "https://github.com/acme/repo/pull/7#discussion_r1",
			title: "2 unresolved comments from alice",
		});
	});

	it("falls back to the PR page when review summary and inline comment URLs are missing", () => {
		const parts = prSummaryParts(
			summary({
				url: "https://github.com/acme/repo/issues/7",
				htmlUrl: "https://github.com/acme/repo/issues/7",
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: true,
					unresolvedBy: [{ reviewerId: "alice", count: 1, links: [] }],
				},
			}),
		);

		expect(parts.find((part) => part.key === "review")?.links[0]).toMatchObject({
			label: "alice",
			href: "https://github.com/acme/repo/pull/7",
			title: "Open pull request for alice",
		});
	});

	it("shows bot reviewers with a bot label", () => {
		const parts = prSummaryParts(
			summary({
				review: {
					decision: "changes_requested",
					hasUnresolvedHumanComments: false,
					unresolvedBy: [
						{
							reviewerId: "copilot",
							count: 0,
							reviewUrl: "https://github.com/acme/repo/pull/7#pullrequestreview-2",
							isBot: true,
							links: [],
						},
					],
				},
			}),
		);

		expect(parts.find((part) => part.key === "review")?.links[0]).toMatchObject({
			label: "copilot bot",
			href: "https://github.com/acme/repo/pull/7#pullrequestreview-2",
			title: "Open requested-changes review from copilot bot",
		});
	});

	it("links merge conflicts to the PR page instead of the host's conflicts subpage", () => {
		const parts = prSummaryParts(
			summary({
				url: "https://github.com/acme/repo/issues/7",
				htmlUrl: "https://github.com/acme/repo/issues/7",
				mergeability: {
					state: "conflicting",
					reasons: [],
					prUrl: "https://github.com/acme/repo/issues/7",
				},
			}),
		);

		expect(parts.find((part) => part.key === "merge")).toMatchObject({
			status: "Conflict",
			summary: undefined,
		});
		expect(parts.find((part) => part.key === "merge")?.links[0]).toMatchObject({
			label: "conflicts",
			href: "https://github.com/acme/repo/pull/7",
		});
	});

	it("keeps closed or merged PR summaries to the three status parts", () => {
		const parts = prSummaryParts(
			summary({
				state: "merged",
				ci: {
					autoInjectCI: true,
					checks: [],
					state: "failing",
					failingChecks: [{ name: "unit", status: "failed", conclusion: "failure" }],
				},
				review: { decision: "changes_requested", hasUnresolvedHumanComments: true, unresolvedBy: [] },
				mergeability: { state: "conflicting", reasons: ["conflicts"], prUrl: "https://github.com/acme/repo/pull/7" },
			}),
		);

		expect(parts).toHaveLength(3);
		expect(parts.find((part) => part.key === "merge")?.links).toEqual([]);
		expect(parts.find((part) => part.key === "review")?.links).toEqual([]);
	});

	it("puts draft readiness under Review", () => {
		const parts = prSummaryParts(
			summary({ state: "draft", review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] } }),
		);

		expect(parts.find((part) => part.key === "review")).toMatchObject({
			status: "None",
			summary: "Draft PR · Not ready for review",
		});
	});
});

describe("pull request repositories", () => {
	it("reads owner/name from GitHub and GitLab pull request URLs", async () => {
		const { prRepositoryFromUrl } = await import("./pr-display");
		expect(prRepositoryFromUrl("https://github.com/acme/11x/pull/12")).toBe("acme/11x");
		expect(prRepositoryFromUrl("https://gitlab.com/acme/app/-/merge_requests/3")).toBe("acme/app");
		expect(prRepositoryFromUrl("not a url")).toBeUndefined();
	});

	it("tells when a session's pull requests span repositories", async () => {
		const { prsSpanRepositories } = await import("./pr-display");
		expect(prsSpanRepositories([{ url: "https://github.com/acme/a/pull/1" }, { url: "https://github.com/acme/a/pull/2" }])).toBe(false);
		expect(prsSpanRepositories([{ url: "https://github.com/acme/a/pull/1" }, { url: "https://github.com/acme/b/pull/1" }])).toBe(true);
	});

	it("calls a repository by its short name, and by its full name when two owners share it", async () => {
		const { prRepositoryLabels, prRepositoryShortName } = await import("./pr-display");
		expect(prRepositoryShortName("acme-platform/landing-site")).toBe("landing-site");
		expect(prRepositoryShortName("landing-site")).toBe("landing-site");
		const apart = prRepositoryLabels([{ repo: "acme-platform/landing-site" }, { repo: "acme-platform/control-plane" }]);
		expect(apart.get("acme-platform/landing-site")).toBe("landing-site");
		expect(apart.get("acme-platform/control-plane")).toBe("control-plane");
		// A fork and its upstream share a short name, so each keeps its owner.
		const fork = prRepositoryLabels([{ repo: "acme/app" }, { repo: "ada/app" }, { url: "https://github.com/acme/docs/pull/3" }]);
		expect(fork.get("acme/app")).toBe("acme/app");
		expect(fork.get("ada/app")).toBe("ada/app");
		expect(fork.get("acme/docs")).toBe("docs");
	});
});

// GitHub reports no status rollup for a head nothing checks, which reaches the
// client as CI "unknown", the same as checks not read yet.
describe("a PR nothing checks", () => {
	const unknownCI = { autoInjectCI: true, state: "unknown" as const, failingChecks: [], checks: [] };
	const unchecked = (overrides: Partial<SessionPRSummary> = {}) => summary({ ci: unknownCI, detailsObservedAt: "2026-06-15T00:00:00Z", ...overrides });

	it("is told apart from unread checks only by details that loaded with none", () => {
		expect(prHasNoChecks(unchecked())).toBe(true);
		expect(prHasNoChecks(unchecked({ detailsObservedAt: undefined }))).toBe(false);
		expect(prHasNoChecks(unchecked({ ci: { ...unknownCI, failingChecks: [{ name: "test", status: "cancelled", conclusion: "cancelled" }] } }))).toBe(false);
		expect(prHasNoChecks(summary({ detailsObservedAt: "2026-06-15T00:00:00Z" }))).toBe(false);
	});

	it("waits for checks that may still be starting, right after the PR changed", () => {
		const changed = Date.parse("2026-11-12T10:00:00Z");
		const fresh = unchecked({ updatedAt: "2026-11-12T10:00:00Z" });
		expect(prHasNoChecks(fresh, changed + 10_000)).toBe(false);
		expect(prHasNoChecks(fresh, changed + PR_NO_CHECKS_SETTLE_MS)).toBe(true);
		expect(noChecksSettlesAt(fresh)).toBe(changed + PR_NO_CHECKS_SETTLE_MS);
		expect(noChecksSettlesAt(summary({ detailsObservedAt: "2026-06-15T00:00:00Z" }))).toBeUndefined();
	});

	it("can merge from the PR view, while the classic card still waits for checks", () => {
		expect(prSummaryBlockReason(unchecked())).toBeUndefined();
		expect(prCanMerge(unchecked(), { noChecks: true })).toBe(true);
		expect(prCanMerge(unchecked())).toBe(false);
		expect(prSummaryBlockReason(unchecked({ detailsObservedAt: undefined }))).toBe("checking");
		expect(prSummaryBlockReason(unchecked({ mergeability: { state: "unknown", reasons: [], prUrl: "" } }))).toBe("checking");
	});

	it("reads as mergeable with no checks on the PR view card", () => {
		const view = prCardPresentation(unchecked(), { noChecks: true });
		expect(view.readiness?.label).toBe("Mergeable");
		expect(view.readinessRows?.map((row) => row.label)).toContain("No checks reported");
		expect(view.statusRows?.map((row) => row.label)).toEqual(["Review status"]);
		expect(prCardPresentation(unchecked()).readiness?.label).toBe("Checking merge readiness");
	});
});
