import { describe, expect, it } from "vitest";
import type { DashboardPR, DashboardSession, SessionPRSummary } from "./api";
import {
	collectPRs,
	comparePRs,
	mergeReasonLabel,
	prFilterCounts,
	prFilterLabel,
	prInFilter,
	prListFilters,
	prListSections,
	prLifecycle,
	prStatusAtoms,
	prReviewPromptHeadline,
	prReviewPromptStatuses,
	prRowStatus,
	prRowTitle,
	prTitle,
} from "./prView";

const pr = (over: Partial<DashboardPR> = {}): DashboardPR => ({
	number: 184,
	url: "https://github.com/o/r/pull/184",
	state: "open",
	...over,
});

const session = (projectId: string, prs: DashboardPR[], id = `${projectId}-1`): DashboardSession =>
	({ id, projectId, prs }) as DashboardSession;

describe("collectPRs", () => {
	// Regression: the key was `${owner}/${repo}#${number}`, but the board endpoint
	// never sends owner or repo — so it collapsed to "/#12" and two projects with
	// a PR #12 dropped one of each other.
	it("keeps the same PR number in two different projects", () => {
		const got = collectPRs([session("alpha", [pr({ number: 12 })]), session("beta", [pr({ number: 12 })])]);
		expect(got.map((x) => x.session.projectId)).toEqual(["alpha", "beta"]);
	});

	// Deduping still has a job: several sessions in one project can point at the
	// same PR, and it should appear once.
	it("collapses the same PR seen from two sessions in one project", () => {
		const got = collectPRs([session("alpha", [pr({ number: 12 })], "alpha-1"), session("alpha", [pr({ number: 12 })], "alpha-2")]);
		expect(got).toHaveLength(1);
	});

	it("skips placeholder PRs with no real number", () => {
		expect(collectPRs([session("alpha", [pr({ number: 0 })])])).toHaveLength(0);
	});

	it("falls back to the single `pr` field when `prs` is empty", () => {
		const s = { id: "a-1", projectId: "alpha", prs: [], pr: pr({ number: 7 }) } as unknown as DashboardSession;
		expect(collectPRs([s]).map((x) => x.pr.number)).toEqual([7]);
	});
});

describe("prTitle", () => {
	it("prefers the PR's own title", () => {
		expect(prTitle(pr({ title: "Fix auth timeouts" }), "session title")).toBe("Fix auth timeouts");
	});

	// The regression this exists for: the list endpoint never sends PR titles, so
	// every card in the tab read "Pull request #N" and looked identical.
	it("falls back to the session title when the daemon sent none", () => {
		expect(prTitle(pr(), "Fix auth timeouts on refresh")).toBe("Fix auth timeouts on refresh");
	});

	it("falls back to the PR number only when there is nothing else", () => {
		expect(prTitle(pr())).toBe("Pull request #184");
		expect(prTitle(pr(), "")).toBe("Pull request #184");
		expect(prTitle(pr(), null)).toBe("Pull request #184");
	});

	it("ignores a blank title rather than rendering an empty card", () => {
		expect(prTitle(pr({ title: "   " }), "from session")).toBe("from session");
	});
});

describe("prLifecycle", () => {
	// mapPR folds the wire's "draft" into state:"open" and keeps isDraft, so the
	// flag is the only surviving signal — and nothing rendered it before.
	it("reports a draft even though its state is open", () => {
		expect(prLifecycle(pr({ state: "open", isDraft: true }))).toBe("draft");
	});

	it("reports merged and closed over draft", () => {
		expect(prLifecycle(pr({ state: "merged", isDraft: true }))).toBe("merged");
		expect(prLifecycle(pr({ state: "closed", isDraft: true }))).toBe("closed");
	});

	it("defaults to open", () => {
		expect(prLifecycle(pr())).toBe("open");
	});
});

describe("mergeReasonLabel", () => {
	it("humanises the reasons desktop knows", () => {
		expect(mergeReasonLabel("behind_base")).toBe("branch behind base");
		expect(mergeReasonLabel("ci_failing")).toBe("CI failing");
		expect(mergeReasonLabel("blocked_by_provider")).toBe("provider blocked");
	});

	it("degrades an unknown reason into readable words", () => {
		expect(mergeReasonLabel("some_new_reason")).toBe("some new reason");
	});
});

describe("prStatusAtoms", () => {
	it("collapses a decided PR to one atom", () => {
		// "Merged · CI passing" is noise about a PR nobody can act on.
		// Purple, matching the lifecycle badge above the title. The green in this
		// slot is for "CI passing · Mergeable" — states you can still act on — and
		// merged borrowed it, so the same word wore two colours on one card.
		expect(prStatusAtoms({ state: "merged", ci: { state: "passing" } })).toEqual([
			{ text: "Merged", tone: "merged" },
		]);
		expect(prStatusAtoms({ state: "closed" })).toEqual([{ text: "Closed", tone: "passive" }]);
	});

	it("reports CI, merge and review in that order", () => {
		const atoms = prStatusAtoms({
			state: "open",
			ci: { state: "passing" },
			mergeability: { state: "mergeable" },
			review: { decision: "approved" },
		});
		expect(atoms.map((a) => a.text)).toEqual(["CI passing", "Mergeable", "Approved"]);
	});

	// The daemon reports "unknown" while it is still looking; a card that said
	// "CI checking · Merge checking" on every fresh PR would be pure noise.
	it("omits anything the daemon has not determined", () => {
		expect(prStatusAtoms({ state: "open", ci: { state: "unknown" }, mergeability: { state: "unknown" } })).toEqual([]);
	});

	it("colours failures and blockers correctly", () => {
		const atoms = prStatusAtoms({
			state: "open",
			ci: { state: "failing" },
			mergeability: { state: "conflicting" },
			review: { decision: "changes_requested" },
		});
		expect(atoms.map((a) => a.tone)).toEqual(["error", "error", "warning"]);
	});

	it("surfaces unresolved comments when there is no formal decision", () => {
		const atoms = prStatusAtoms({ state: "open", review: { decision: "none", hasUnresolvedHumanComments: true } });
		expect(atoms).toEqual([{ text: "Unresolved comments", tone: "warning" }]);
	});
});

describe("PR composer prompt statuses", () => {
	it("shows CI, mergeability, and review as separate status facts", () => {
		const summary = {
			state: "open",
			url: "https://github.com/o/r/pull/184",
			number: 184,
			title: "Test pull request",
			repo: "o/r",
			author: "octocat",
			sourceBranch: "feature",
			targetBranch: "main",
			additions: 0,
			deletions: 0,
			changedFiles: 0,
			ci: { state: "passing", failingChecks: [] },
			mergeability: { state: "mergeable", reasons: [] },
			review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] },
	} as SessionPRSummary;

		expect(prReviewPromptStatuses(pr(), summary).map(({ text }) => text)).toEqual([
			"CI passing",
			"Mergeable",
			"Approved",
		]);
		expect(prReviewPromptHeadline(pr(), summary)).toEqual({ text: "Ready to merge", tone: "success" });
	});

	it("colors conflicts and failing checks as errors", () => {
		const summary = {
			state: "open",
			url: "https://github.com/o/r/pull/184",
			number: 184,
			title: "Test pull request",
			repo: "o/r",
			author: "octocat",
			sourceBranch: "feature",
			targetBranch: "main",
			additions: 0,
			deletions: 0,
			changedFiles: 0,
			ci: { state: "failing", failingChecks: [] },
			mergeability: { state: "conflicting", reasons: [] },
			review: { decision: "review_required", hasUnresolvedHumanComments: false, unresolvedBy: [] },
	} as SessionPRSummary;

		expect(prReviewPromptStatuses(pr(), summary).map(({ tone }) => tone)).toEqual(["error", "error", "neutral"]);
		expect(prReviewPromptHeadline(pr(), summary)).toEqual({ text: "Has conflicts", tone: "error" });
	});

	it("uses the available session facts while the rich PR summary is loading", () => {
		const pullRequest = pr({ ciStatus: "passing", mergeability: { mergeable: true }, reviewDecision: "pending" });
		expect(prReviewPromptStatuses(pullRequest).map(({ text }) => text)).toEqual([
			"CI passing",
			"Mergeable",
			"Review pending",
		]);
		expect(prReviewPromptHeadline(pullRequest)).toEqual({ text: "Mergeable", tone: "success" });
	});
});

describe("comparePRs", () => {
	const sorted = (list: DashboardPR[]) => [...list].sort(comparePRs).map((p) => p.number);

	it("orders open before draft before merged before closed", () => {
		const closed = pr({ number: 1, state: "closed" });
		const merged = pr({ number: 2, state: "merged" });
		const draft = pr({ number: 3, isDraft: true });
		const open = pr({ number: 4 });
		expect(sorted([closed, merged, draft, open])).toEqual([4, 3, 2, 1]);
	});

	it("floats a ready-to-merge PR to the top of the open bucket", () => {
		const plain = pr({ number: 10 });
		const ready = pr({ number: 2, reviewDecision: "approved", mergeability: { mergeable: true } });
		expect(sorted([plain, ready])[0]).toBe(2);
	});

	it("puts PRs needing attention above quiet ones", () => {
		const quiet = pr({ number: 9 });
		const failing = pr({ number: 3, ciStatus: "failing" });
		const changes = pr({ number: 5, reviewDecision: "changes_requested" });
		expect(sorted([quiet, changes, failing])).toEqual([3, 5, 9]);
	});

	it("breaks ties with the newest PR first", () => {
		expect(sorted([pr({ number: 4 }), pr({ number: 12 }), pr({ number: 7 })])).toEqual([12, 7, 4]);
	});
});

describe("prListSections", () => {
	const item = (over: Partial<DashboardPR>) => ({
		pr: pr(over),
		session: session("alpha", []),
	});

	it("groups open pull requests by the next useful action", () => {
		const sections = prListSections([
			item({ number: 1, reviewDecision: "pending" }),
			item({ number: 2, ciStatus: "failing" }),
			item({ number: 3, reviewDecision: "approved", mergeability: { mergeable: true } }),
			item({ number: 4, isDraft: true }),
		], "open");

		expect(sections.map((section) => [section.key, section.data.map(({ pr }) => pr.number)])).toEqual([
			["ready", [3]],
			["attention", [2]],
			["review", [1]],
			["draft", [4]],
		]);
	});

	it("keeps merged and closed history out of the open board", () => {
		const items = [item({ number: 5, state: "merged" }), item({ number: 6, state: "closed" })];
		expect(prListSections(items, "open")).toEqual([]);
		expect(prListSections(items, "merged").map((section) => section.key)).toEqual(["merged"]);
		expect(prListSections(items, "all").map((section) => section.key)).toEqual(["merged", "closed"]);
	});

	it("orders newer pull requests first within a section", () => {
		const sections = prListSections([item({ number: 7 }), item({ number: 12 }), item({ number: 4 })], "open");
		expect(sections[0]?.data.map(({ pr }) => pr.number)).toEqual([12, 7, 4]);
	});
});

describe("PR list filters", () => {
	const item = (over: Partial<DashboardPR>) => ({ pr: pr(over), session: session("alpha", []) });

	// Drafts are open pull requests; closed ones are history, under All only.
	it("counts each filter the way its sections list it", () => {
		const items = [
			item({ number: 1 }),
			item({ number: 2, isDraft: true }),
			item({ number: 3, state: "merged" }),
			item({ number: 4, state: "closed" }),
		];
		expect(prFilterCounts(items)).toEqual({ open: 2, merged: 1, all: 4 });
		for (const filter of prListFilters) {
			const listed = prListSections(items, filter).flatMap((section) => section.data).length;
			expect(listed).toBe(prFilterCounts(items)[filter]);
		}
		expect(prInFilter("open", "draft")).toBe(true);
		expect(prInFilter("merged", "closed")).toBe(false);
	});

	it("names each segment with its count", () => {
		expect(prListFilters.map((filter) => prFilterLabel(filter, 3))).toEqual(["Open 3", "Merged 3", "All 3"]);
	});
});

describe("prRowTitle", () => {
	// The kind of change moves to the line under the title, so the title starts
	// with what the pull request does.
	it("splits a conventional-commit prefix off and capitalises what is left", () => {
		expect(prRowTitle("fix(mobile): stop Android crashing when a machine is renamed")).toEqual({
			title: "Stop Android crashing when a machine is renamed",
			scope: "mobile",
		});
		expect(prRowTitle("feat!: drop the v1 API")).toEqual({ title: "Drop the v1 API", scope: undefined });
	});

	it("leaves a title that is not a conventional commit as it is", () => {
		expect(prRowTitle("Note: this is a plain title")).toEqual({ title: "Note: this is a plain title" });
		expect(prRowTitle("  Upgrade Electron  ")).toEqual({ title: "Upgrade Electron" });
		expect(prRowTitle("fix: ")).toEqual({ title: "fix:" });
	});
});

describe("prRowStatus", () => {
	const rich = (over: Partial<SessionPRSummary> = {}): SessionPRSummary => ({
		url: "u", number: 1, title: "t", state: "open", repo: "o/r", author: "a", sourceBranch: "b", targetBranch: "main",
		additions: 0, deletions: 0, changedFiles: 0,
		ci: { state: "passing", failingChecks: [] },
		review: { decision: "review_required", hasUnresolvedHumanComments: false, unresolvedBy: [] },
		mergeability: { state: "blocked", reasons: ["review_required"] },
		...over,
	});

	// Waiting for review is every open PR's normal state: saying it on each row,
	// in amber, is what made the old list read as one long alarm.
	it("says nothing for an open PR that is only waiting for review", () => {
		expect(prRowStatus(pr(), rich())).toBeNull();
		expect(prRowStatus(pr({ reviewDecision: "pending", ciStatus: "passing" }))).toBeNull();
	});

	it("names the failing check, or counts them", () => {
		expect(prRowStatus(pr(), rich({ ci: { state: "failing", failingChecks: [{ name: "android-build" }] } }))).toEqual({ text: "android-build failed", tone: "error" });
		expect(prRowStatus(pr(), rich({ ci: { state: "failing", failingChecks: [{ name: "a" }, { name: "b" }] } }))).toEqual({ text: "2 checks failed", tone: "error" });
		expect(prRowStatus(pr({ ciStatus: "failing" }))).toEqual({ text: "Checks failed", tone: "error" });
	});

	it("puts conflicts first, then failures, then review feedback", () => {
		const conflicted = rich({ mergeability: { state: "conflicting", reasons: [] }, ci: { state: "failing", failingChecks: [] } });
		expect(prRowStatus(pr(), conflicted)).toEqual({ text: "Merge conflicts", tone: "error" });
		const both = rich({ ci: { state: "failing", failingChecks: [] }, review: { decision: "changes_requested", hasUnresolvedHumanComments: true, unresolvedBy: [] } });
		expect(prRowStatus(pr(), both)?.tone).toBe("error");
		expect(prRowStatus(pr(), rich({ review: { decision: "changes_requested", hasUnresolvedHumanComments: false, unresolvedBy: [] } }))).toEqual({ text: "Changes requested", tone: "warning" });
		expect(prRowStatus(pr({ unresolvedThreads: 2 }))).toEqual({ text: "Unresolved comments", tone: "warning" });
	});

	it("reports running checks quietly and a mergeable approval in green", () => {
		expect(prRowStatus(pr(), rich({ ci: { state: "pending", failingChecks: [] } }))).toEqual({ text: "Checks running", tone: "neutral" });
		expect(prRowStatus(pr({ mergeability: { state: "mergeable", mergeable: true } }), rich({ review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] }, mergeability: { state: "mergeable", reasons: [] } }))).toEqual({ text: "Ready to merge", tone: "success" });
		expect(prRowStatus(pr({ reviewDecision: "approved" }))).toEqual({ text: "Approved", tone: "success" });
	});

	// The board re-polls every few seconds and places the row in its section;
	// the summary is fetched once per visit. A stale summary must not contradict
	// the section the board just moved the row into.
	it("follows the board when the summary is stale", () => {
		const staleRunning = rich({ ci: { state: "pending", failingChecks: [] }, review: { decision: "approved", hasUnresolvedHumanComments: false, unresolvedBy: [] }, mergeability: { state: "blocked", reasons: [] } });
		const boardReady = pr({ ciStatus: "passing", reviewDecision: "approved", mergeability: { state: "mergeable", mergeable: true } });
		expect(prRowStatus(boardReady, staleRunning)).toEqual({ text: "Ready to merge", tone: "success" });

		const staleFailing = rich({ ci: { state: "failing", failingChecks: [{ name: "android-build" }] } });
		expect(prRowStatus(pr({ ciStatus: "passing", reviewDecision: "pending", mergeability: { state: "blocked" } }), staleFailing)).toBeNull();
		expect(prRowStatus(pr({ state: "merged" }), staleFailing)).toBeNull();
	});

	// The board carries no check names: they come from the summary, and only
	// while the board still says the checks fail.
	it("takes the failing check names from the summary only while the board says failing", () => {
		const failing = rich({ ci: { state: "failing", failingChecks: [{ name: "android-build" }] } });
		expect(prRowStatus(pr({ ciStatus: "failing" }), failing)).toEqual({ text: "android-build failed", tone: "error" });
		expect(prRowStatus(pr({ ciStatus: "failing" }), rich())).toEqual({ text: "Checks failed", tone: "error" });
	});

	// A repo that requires up-to-date branches: another PR merges, GitHub says
	// BEHIND, the board turns "unknown" and not mergeable, and the row leaves
	// Ready to merge. It must not keep saying so from the summary.
	it("says Approved, not Ready to merge, once the board finds the branch behind", () => {
		const approved = { decision: "approved" as const, hasUnresolvedHumanComments: false, unresolvedBy: [] };
		const staleMergeable = rich({ review: approved, mergeability: { state: "mergeable", reasons: [] } });
		const behind = pr({ ciStatus: "passing", reviewDecision: "approved", mergeability: { state: "unknown", mergeable: false } });
		expect(prRowStatus(behind, staleMergeable)).toEqual({ text: "Approved", tone: "success" });
	});

	it("falls back to a summary fact only where the board has none", () => {
		const conflicted = rich({ mergeability: { state: "conflicting", reasons: [] } });
		// The board's "unknown" is a fact: the summary derives its state from the
		// same source, so a different answer there is only older.
		expect(prRowStatus(pr({ mergeability: { state: "unknown", mergeable: false } }), conflicted)).toBeNull();
		expect(prRowStatus(pr({ mergeability: { state: "blocked" } }), conflicted)).toBeNull();
		expect(prRowStatus(pr(), conflicted)).toEqual({ text: "Merge conflicts", tone: "error" });
		// Cloud's board rows carry no unresolved-thread count.
		const commented = rich({ review: { decision: "review_required", hasUnresolvedHumanComments: true, unresolvedBy: [] } });
		expect(prRowStatus(pr(), commented)).toEqual({ text: "Unresolved comments", tone: "warning" });
		expect(prRowStatus(pr({ unresolvedThreads: 0 }), commented)).toBeNull();
	});

	// Their section already says merged or closed.
	it("says nothing for a merged or closed PR", () => {
		expect(prRowStatus(pr({ state: "merged", ciStatus: "failing" }))).toBeNull();
		expect(prRowStatus(pr(), rich({ state: "closed" }))).toBeNull();
	});
});
