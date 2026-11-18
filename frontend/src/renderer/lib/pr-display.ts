import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { sortedPRs, type PRState, type PullRequestFacts, type SessionBranchState, type WorkspaceSession } from "../types/workspace";
import { appI18n, type PluralMessageKey } from "../i18n";
import type {
	PRCardPresentation,
	PRCardStatus,
	PRCheckState,
	PRChecksTally,
	PRDisplayTone,
	PRNoun,
	PRStatusRow,
	PRSummaryLink,
	PRSummaryPart,
} from "@aoagents/product-ui";

export type {
	PRCardPresentation,
	PRCardStatus,
	PRDisplayTone,
	PRNoun,
	PRSummaryLink,
	PRSummaryPart,
	PRSummaryPartKey,
} from "@aoagents/product-ui";

function detectProviderFromUrl(url: string): "github" | "gitlab" {
	if (url.includes("/-/merge_requests/")) return "gitlab";
	return "github";
}

const prStateRank: Record<PRState, number> = { open: 0, draft: 1, merged: 2, closed: 3 };
const ciStates = new Set<SessionPRSummary["ci"]["state"]>(["unknown", "pending", "passing", "failing"]);
const reviewDecisions = new Set<SessionPRSummary["review"]["decision"]>([
	"none",
	"approved",
	"changes_requested",
	"review_required",
]);
const mergeabilityStates = new Set<SessionPRSummary["mergeability"]["state"]>([
	"unknown",
	"mergeable",
	"conflicting",
	"blocked",
	"unstable",
]);

export const prNounKeys: Record<PRNoun, PluralMessageKey> = {
	check: "pr.noun.check",
	comment: "pr.noun.comment",
	file: "pr.noun.file",
	line: "pr.noun.line",
	reason: "pr.noun.reason",
	reviewer: "pr.noun.reviewer",
};

export function comparePRDisplaySummaries(a: SessionPRSummary, b: SessionPRSummary): number {
	return prStateRank[a.state] - prStateRank[b.state] || a.number - b.number;
}

export function prBrowserUrl(pr: SessionPRSummary): string {
	return prBaseUrl(pr) ?? pr.htmlUrl ?? pr.url;
}

/**
 * Whether a session's worktree holds this PR's branch, so its own diff shows
 * what a review comment is on. Not yet observed counts as holding it. The
 * session's branch name can't answer this: it keeps the name the session
 * started with after the agent moves to another branch.
 */
export function prCheckedOut(pr: SessionPRSummary, branch: SessionBranchState | undefined): boolean {
	if (!branch) return true;
	// remoteBranch names the remote first: "origin/feat/x".
	const pushed = branch.remoteBranch?.slice(branch.remoteBranch.indexOf("/") + 1);
	return branch.head === pr.headSha || Boolean(pr.sourceBranch && pushed === pr.sourceBranch);
}

/** A review comment on the provider's diff, where GitHub's own link lands on the conversation. */
export function prThreadDiffUrl(url: string): string {
	return url.replace(/#discussion_r(\d+)$/, "/files#r$1");
}

export function prChecksUrl(pr: SessionPRSummary): string | undefined {
	try {
		const url = new URL(prBrowserUrl(pr));
		if (url.hostname.toLowerCase() !== "github.com" || !/\/pull\/\d+\/?$/.test(url.pathname)) return undefined;
		return `${url.origin}${url.pathname.replace(/\/$/, "")}/checks`;
	} catch {
		return undefined;
	}
}

// Older daemons and cloud responses can omit the provider's explanation list.
// Keep the display layer tolerant of that wire-format drift because the PR
// card is rendered as soon as the inspector tab opens.
function mergeReasons(pr: SessionPRSummary): string[] {
	return pr.mergeability.reasons ?? [];
}

/**
 * Canonical identity key for deduplication. Reuses the URL normalization
 * from `prURL` (which normalizes GitHub issues→pull, strips query/fragment,
 * and handles GitLab MR paths). When the URL is absent or unrecognized
 * (e.g. API URLs), falls back to `number:${number}` so the enriched summary
 * still matches the session fact.
 */
function canonicalKey(url: string, number: number): string {
	return canonicalURL(url) || `number:${number}`;
}

/** The key that tells two observations of one pull request apart from others. */
export function prDisplayKey(pr: { url: string; number: number }): string {
	return canonicalKey(pr.url, pr.number);
}

function canonicalURL(rawUrl: string): string | undefined {
	return prURL({ url: rawUrl, htmlUrl: rawUrl, mergeability: { prUrl: rawUrl } } as SessionPRSummary);
}

export function sessionPRDisplaySummaries(
	session: WorkspaceSession,
	summaries: SessionPRSummary[] = [],
): SessionPRSummary[] {
	// Key by canonical web URL so a GitHub PR #7 and a GitLab MR #7 (or any
	// same-numbered PRs across providers/repos) both appear instead of one
	// hiding the other. The key normalizes known URL shapes (e.g. GitHub
	// issues/7 → pull/7, query params/fragments stripped) so the same PR
	// observed under different URL variants collapses to one card.
	// Non-standard URLs (API URLs, etc.) fall back to `number:${number}` so
	// the enriched summary still matches the session fact.
	const summariesByUrl = new Map<string, SessionPRSummary>();
	for (const s of summaries) {
		const key = canonicalKey(s.url, s.number);
		if (!summariesByUrl.has(key)) summariesByUrl.set(key, s);
	}
	const seen = new Set<string>();
	const fromFactsMap = new Map<string, SessionPRSummary>();
	for (const pr of sortedPRs(session)) {
		const key = canonicalKey(pr.url, pr.number);
		seen.add(key);
		if (!fromFactsMap.has(key)) {
			fromFactsMap.set(key, summariesByUrl.get(key) ?? sessionPRFactToSummary(session, pr));
		}
	}
	const summaryOnly = [...summariesByUrl.values()].filter((s) => !seen.has(canonicalKey(s.url, s.number)));
	return [...fromFactsMap.values(), ...summaryOnly].sort(comparePRDisplaySummaries);
}

/**
 * The open PR the worktree's checked-out branch belongs to, which is where new
 * commits and pushes land. A session can list PRs from other branches, so it is
 * matched by the pushed branch name, then by the tip commit. The first open PR
 * stands in only while the daemon has not reported the branch.
 */
export function sessionBranchPR(session: WorkspaceSession, prs: SessionPRSummary[]): SessionPRSummary | undefined {
	const open = prs.filter((pr) => pr.state === "open" || pr.state === "draft");
	const state = session.branchState;
	if (!state) return open[0];
	// remoteBranch names the remote first: "origin/feat/x".
	const pushed = state.remoteBranch?.slice(state.remoteBranch.indexOf("/") + 1);
	return (pushed ? open.find((pr) => pr.sourceBranch === pushed) : undefined)
		?? (state.head ? open.find((pr) => pr.headSha === state.head) : undefined);
}

// The view-only PR fields (labels, requested reviewers, commits) for a summary
// no provider details were observed for: AO Cloud summaries, a PR known only
// from session facts, and fixtures. detailsObservedAt stays absent, so a view
// can tell "not loaded" from "this PR has none".
export function noPRDetails(): Pick<SessionPRSummary, "labels" | "requestedReviewers" | "commits" | "commitCount"> {
	return { labels: [], requestedReviewers: [], commits: [], commitCount: 0 };
}

function sessionPRFactToSummary(session: WorkspaceSession, pr: PullRequestFacts): SessionPRSummary {
	return {
		...noPRDetails(),
		url: pr.url,
		htmlUrl: pr.url,
		number: pr.number,
		title: session.title,
		state: pr.state,
		provider: detectProviderFromUrl(pr.url),
		repo: session.workspaceName,
		author: "",
		sourceBranch: session.branch ?? "",
		targetBranch: "",
		headSha: "",
		additions: 0,
		deletions: 0,
		changedFiles: 0,
		ci: {
			autoInjectCI: true,
			state: toCIState(pr.ci),
			failingChecks: pr.failingChecks ?? [],
			// Only the daemon's summary lists every check; session facts know the failing ones.
			checks: [],
		},
		review: {
			decision: toReviewDecision(pr.review),
			hasUnresolvedHumanComments: pr.reviewComments,
			unresolvedBy: [],
		},
		mergeability: {
			state: toMergeabilityState(pr.mergeability),
			reasons: [],
			prUrl: pr.url,
			conflictFiles: [],
		},
		updatedAt: pr.updatedAt,
		observedAt: pr.updatedAt,
		ciObservedAt: pr.updatedAt,
		reviewObservedAt: pr.updatedAt,
	};
}

export function prStatusRows(pr: SessionPRSummary): PRStatusRow[] {
	return prSummaryParts(pr).map((part) => ({
		key: part.key,
		label: part.label,
		value: part.status,
		detail: part.key === "merge" ? formatDiffSummary(pr) : undefined,
		tone: part.tone,
	}));
}

/**
 * Reduces provider facts to one next-action headline for compact PR cards.
 * The detailed three-row presentation remains available for dense reports,
 * but cards should not repeat the same blocker under Merge and Review.
 * `noChecks` (see prHasNoChecks) stops a PR that nothing checks from
 * waiting on checks; the classic card leaves it unset.
 */
export function prCardPresentation(pr: SessionPRSummary, options: { noChecks?: boolean } = {}): PRCardPresentation {
	// CI "unknown" means checks are still being read, unless the PR has none.
	const awaitingChecks = pr.ci.state === "unknown" && !options.noChecks;
	let primary: PRCardStatus;
	if (pr.state === "merged") {
		primary = cardStatus("lifecycle", "pr.card.merged", "success");
	} else if (pr.state === "closed") {
		primary = cardStatus("lifecycle", "pr.card.closed", "passive");
	} else if (pr.ci.state === "failing") {
		primary = cardStatus("ci", "pr.card.checksFailing", "error", ciSummary(pr), ciLinks(pr));
	} else if (pr.mergeability.state === "conflicting") {
		primary = cardStatus("merge", "pr.card.mergeConflict", "error", mergeSummary(pr), mergeLinks(pr));
	} else if (pr.review.decision === "changes_requested" || pr.review.hasUnresolvedHumanComments) {
		primary = cardStatus("review", "pr.card.changesRequested", "warning", reviewSummary(pr), reviewLinks(pr));
	} else if (pr.review.decision === "review_required") {
		primary = cardStatus("review", "pr.card.reviewRequired", "review", appI18n.t("pr.card.reviewRequiredDetail"));
	} else if (pr.ci.state === "pending") {
		primary = cardStatus("ci", "pr.card.checksPending", "neutral", undefined, [], prChecksUrl(pr), true);
	} else if (ciCancelled(pr)) {
		primary = cardStatus("ci", "pr.card.checksCancelled", "passive", undefined, [], prChecksUrl(pr));
	} else if (awaitingChecks) {
		primary = cardStatus("ci", "pr.card.checksLoading", "passive", undefined, [], prChecksUrl(pr), true);
	} else if (pr.mergeability.state === "blocked" || pr.mergeability.state === "unstable") {
		primary = cardStatus(
			"merge",
			visibleMergeReasons(pr).length === 0 ? "pr.card.mergeUnavailable" : "pr.card.mergeBlocked",
			"warning",
			mergeSummary(pr),
			mergeLinks(pr),
		);
	} else if (pr.state === "draft") {
		primary = cardStatus("lifecycle", "pr.card.draft", "neutral");
	} else if (pr.mergeability.state === "mergeable") {
		primary = cardStatus("merge", "pr.card.readyToMerge", "success");
	} else if (pr.review.decision === "approved") {
		primary = cardStatus("review", "pr.card.reviewApproved", "success");
	} else {
		primary = cardStatus("lifecycle", "pr.card.open", "neutral");
	}

	const supporting: PRCardStatus[] = [];
	if (pr.state === "open" && primary.key !== "ci") {
		if (pr.ci.state === "passing") {
			supporting.push(cardStatus("ci", "pr.card.checksPassing", "success", undefined, [], prChecksUrl(pr)));
		} else if (pr.ci.state === "pending") {
			supporting.push(cardStatus("ci", "pr.card.checksPending", "neutral", undefined, [], prChecksUrl(pr), true));
		} else if (ciCancelled(pr)) {
			supporting.push(cardStatus("ci", "pr.card.checksCancelled", "passive", undefined, [], prChecksUrl(pr)));
		} else if (awaitingChecks) {
			supporting.push(cardStatus("ci", "pr.card.checksLoading", "passive", undefined, [], prChecksUrl(pr), true));
		}
	}
	if (pr.state === "open") {
		const statusRows: PRCardStatus[] = [];
		if (pr.mergeability.state === "conflicting") {
			statusRows.push(cardStatus("merge", "pr.card.mergeConflict", "error", "Resolve before merging", mergeLinks(pr)));
		}
		if (pr.ci.state === "passing") {
			statusRows.push(cardStatus("ci", "pr.card.checksPassing", "success", undefined, [], prChecksUrl(pr)));
		} else if (pr.ci.state === "failing") {
			statusRows.push(cardStatus("ci", "pr.card.checksFailing", "error", undefined, [], prChecksUrl(pr)));
		} else if (mergeReasons(pr).includes("github_checks_unavailable")) {
			statusRows.push(cardStatus("ci", "pr.card.checksUnavailable", "warning", undefined, [], prChecksUrl(pr)));
		} else if (ciCancelled(pr)) {
			statusRows.push(cardStatus("ci", "pr.card.checksCancelled", "neutral", undefined, [], prChecksUrl(pr)));
		} else if (pr.ci.state === "pending" || awaitingChecks) {
			statusRows.push(cardStatus("ci", pr.ci.state === "pending" ? "pr.card.checksPending" : "pr.card.checksLoading", "neutral", undefined, [], prChecksUrl(pr), true));
		}
		statusRows.push(cardStatus("review", "pr.card.reviewStatus", reviewTone(pr.review.decision, pr.review.hasUnresolvedHumanComments), reviewStatusDetail(pr)));
		const readinessRows = mergeReadinessRows(pr, options);
		const mergeable = prCanMerge(pr, options);
		if (mergeReasons(pr).includes("github_checks_unavailable")) {
			return { primary, supporting, statusRows, readinessRows, readiness: {
				label: appI18n.t("pr.merge.accessLost"),
				detail: appI18n.t("pr.merge.checksUnavailableDetail"),
				tone: "warning",
			} };
		}
		if (hasLostGitHubAccess(pr)) {
			return { primary, supporting, statusRows, readinessRows, readiness: {
				label: appI18n.t("pr.merge.accessLost"),
				detail: appI18n.t("pr.merge.accessLostDetail"),
				tone: "warning",
			} };
		}
		if (ciCancelled(pr)) {
			return { primary, supporting, statusRows, readiness: {
				label: appI18n.t("pr.card.checksCancelled"),
				detail: appI18n.t("pr.merge.checksCancelledDetail"),
				tone: "neutral",
			} };
		}
		const checkingReadiness = pr.ci.state === "pending" || awaitingChecks || pr.mergeability.state === "unknown";
		// A failing check or a conflict says it can't merge, and a green Merge button says it can.
		// A passing row ("No conflicts") doesn't say why readiness is unknown.
		const readinessExplained = checkingReadiness
			? readinessRows.some((row) => row.tone !== "passive" && row.tone !== "success")
			: mergeable || readinessRows.some((row) => row.tone === "error" || row.tone === "warning" || row.tone === "review");
		return { primary, supporting, statusRows, readinessRows, readinessExplained, readiness: {
			label: appI18n.t(checkingReadiness ? "pr.merge.checkingReadiness" : mergeable ? "pr.merge.mergeable" : "pr.merge.notMergeableYet"),
			detail: checkingReadiness ? appI18n.t("pr.merge.checkingDetail") : mergeReadinessDetail(pr),
			tone: checkingReadiness ? "neutral" : mergeable ? "success" : "error",
		} };
	}
	return { primary, supporting };
}

/**
 * What the merge-readiness line is made of: conflicts, checks and review,
 * always in that order so each keeps its place.
 */
function mergeReadinessRows(pr: SessionPRSummary, options: { noChecks?: boolean }): PRCardStatus[] {
	const merge = pr.mergeability.state;
	const conflictsKnown = merge !== "unknown";
	const conflicts = (value: "pr.merge.conflictsWithBase" | "pr.fact.none" | "pr.fact.unknown", state: PRCheckState) =>
		({ title: appI18n.t("pr.fact.conflicts"), value: appI18n.t(value), state });
	const rows: PRCardStatus[] = [
		merge === "conflicting"
			? { ...cardStatus("merge", "pr.card.mergeConflict", "error", undefined, mergeLinks(pr)), fact: conflicts("pr.merge.conflictsWithBase", "failed") }
			: { ...cardStatus("merge", conflictsKnown ? "pr.merge.noConflicts" : "pr.merge.conflictsUnknown", conflictsKnown ? "success" : "passive"), fact: conflicts(conflictsKnown ? "pr.fact.none" : "pr.fact.unknown", conflictsKnown ? "passed" : "pending") },
	];
	// The row opens to the checks, each with its own link, so it carries no
	// link of its own; it says the counts at each state in place of its label.
	const checks = prChecksTally(pr);
	const withChecks = (status: PRCardStatus): PRCardStatus => (checks ? { ...status, checks } : status);
	if (mergeReasons(pr).includes("github_checks_unavailable")) {
		rows.push(cardStatus("ci", "pr.card.checksUnavailable", "warning"));
	} else if (pr.ci.state === "passing") {
		rows.push(withChecks(cardStatus("ci", "pr.card.checksPassing", "success")));
	} else if (pr.ci.state === "failing") {
		rows.push(withChecks(cardStatus("ci", "pr.card.checksFailing", "error")));
	} else if (pr.ci.state === "pending") {
		rows.push(withChecks(cardStatus("ci", "pr.card.checksPending", "neutral", undefined, [], undefined, true)));
	} else {
		rows.push(cardStatus("ci", options.noChecks && pr.ci.state === "unknown" ? "pr.tab.noChecks" : "pr.card.checksUnknown", "passive"));
	}
	rows.push(reviewReadinessRow(pr));
	return rows;
}

type PRCheck = SessionPRSummary["ci"]["checks"][number];

/** Where each provider status stands: a cancelled run counts as failed, a queued or unreported one as pending. */
export const prCheckState: Record<PRCheck["status"], PRCheckState> = {
	failed: "failed",
	cancelled: "failed",
	in_progress: "running",
	queued: "pending",
	unknown: "pending",
	passed: "passed",
	skipped: "skipped",
};

/** Check states from worst to best: the order of the checks row's counts and of its groups. */
export const prCheckStates: PRCheckState[] = ["failed", "running", "pending", "passed", "skipped"];

/**
 * How many of a PR's checks stand at each state, worst first, with the counts
 * in words. Undefined until its checks are listed.
 */
export function prChecksTally(pr: SessionPRSummary): PRChecksTally | undefined {
	const checks = pr.ci.checks ?? [];
	if (checks.length === 0) return undefined;
	const counts = prCheckStates
		.map((state) => {
			const count = checks.filter((check) => prCheckState[check.status] === state).length;
			return { state, count, label: appI18n.t(`pr.checks.count.${state}`, { count }) };
		})
		.filter((entry) => entry.count > 0);
	return { title: appI18n.t("pr.tab.checks"), counts };
}

/**
 * The review row says what happened on the PR. With no review rule GitHub's
 * decision is empty, and "No review required" would only describe the repo,
 * so that case shows the reviews themselves, or that there are none yet.
 */
function reviewReadinessRow(pr: SessionPRSummary): PRCardStatus {
	const { approvedBy, changesRequestedBy } = latestReviewers(pr.review.reviews);
	const unresolved = pr.review.hasUnresolvedHumanComments
		? appI18n.t("inspector.unresolvedCount", { count: pr.review.unresolvedBy.reduce((sum, reviewer) => sum + reviewer.count, 0) })
		: undefined;
	// The label is the whole fact in words; `value` is what follows "Review" on a row that splits it.
	const row = (label: string, tone: PRDisplayTone, detail: string | undefined, state: PRCheckState, value = label): PRCardStatus =>
		({ key: "review", label, detail, links: [], tone, fact: { title: appI18n.t("pr.fact.review"), value, state } });
	const decision = pr.review.decision;
	if (decision === "changes_requested" || (decision === "none" && changesRequestedBy.length > 0)) {
		return row(
			changesRequestedBy.length > 0 ? appI18n.t("pr.review.changesRequestedBy", { names: changesRequestedBy.join(", ") }) : appI18n.t("pr.review.changesRequested"),
			"warning",
			unresolved,
			"failed",
		);
	}
	if (decision === "review_required") {
		// A partial approval still counts for something when more reviews are needed.
		return row(appI18n.t("pr.card.reviewRequired"), "review", approvedBy.length > 0 ? appI18n.t("pr.review.approvedBy", { names: approvedBy.join(", ") }) : unresolved, "pending", appI18n.t("pr.fact.required"));
	}
	if (decision === "approved" || approvedBy.length > 0) {
		return row(
			approvedBy.length > 0 ? appI18n.t("pr.review.approvedBy", { names: approvedBy.join(", ") }) : appI18n.t("pr.review.approved"),
			unresolved ? "warning" : "success",
			unresolved,
			"passed",
		);
	}
	// No rule and no verdict: unresolved comments still block the merge.
	return unresolved ? row(unresolved, "warning", undefined, "pending") : row(appI18n.t("pr.review.noReviewsYet"), "passive", undefined, "skipped");
}

/** Who currently approves or blocks the PR: each reviewer's newest verdict, a later comment-only review not counting. */
function latestReviewers(reviews: SessionPRSummary["review"]["reviews"] = []): { approvedBy: string[]; changesRequestedBy: string[] } {
	const latest = new Map<string, NonNullable<SessionPRSummary["review"]["reviews"]>[number]>();
	for (const review of reviews) {
		if (review.verdict !== "approved" && review.verdict !== "changes_requested") continue;
		const seen = latest.get(review.reviewerId);
		if (!seen || Date.parse(review.submittedAt) >= Date.parse(seen.submittedAt)) latest.set(review.reviewerId, review);
	}
	const names = (verdict: string) => [...latest.values()].filter((review) => review.verdict === verdict).map((review) => review.reviewerId);
	return { approvedBy: names("approved"), changesRequestedBy: names("changes_requested") };
}

function cardStatus(
	key: PRCardStatus["key"],
	labelKey:
		| "pr.card.merged"
		| "pr.card.closed"
		| "pr.card.checksFailing"
		| "pr.card.mergeConflict"
		| "pr.card.changesRequested"
		| "pr.card.reviewRequired"
		| "pr.card.mergeBlocked"
		| "pr.card.mergeUnavailable"
		| "pr.card.checksPending"
		| "pr.card.checksLoading"
		| "pr.card.checksUnavailable"
		| "pr.card.checksUnknown"
		| "pr.tab.noChecks"
		| "pr.merge.noConflicts"
		| "pr.merge.conflictsUnknown"
		| "pr.card.checksCancelled"
		| "pr.card.draft"
		| "pr.card.readyToMerge"
		| "pr.card.reviewApproved"
		| "pr.card.open"
		| "pr.card.checksPassing"
		| "pr.card.reviewStatus",
	tone: PRDisplayTone,
	detail?: string,
	links: PRSummaryLink[] = [],
	href?: string,
	breathe = false,
): PRCardStatus {
	return { key, label: appI18n.t(labelKey), detail, href, breathe, links, tone };
}

export type PRReviewer = { login: string; bot?: boolean; status: "pending" | "approved" | "changes_requested" | "commented" };

/**
 * Who a PR's review rests with, as a PR page's sidebar lists them: the people
 * asked (waiting on them, even if they reviewed before being asked again), then
 * those who reviewed, each by their latest verdict. A plain comment doesn't
 * replace a verdict, and the author's own comments aren't a review.
 */
export function prReviewers(pr: SessionPRSummary): PRReviewer[] {
	const reviewers = new Map<string, PRReviewer>();
	const author = pr.author.replace(/^@/, "").toLowerCase();
	const reviews = [...(pr.review.reviews ?? [])].sort((a, b) => a.submittedAt.localeCompare(b.submittedAt));
	for (const review of reviews) {
		if (review.reviewerId.toLowerCase() === author) continue;
		const verdict = review.verdict === "approved" || review.verdict === "changes_requested" ? review.verdict : undefined;
		const status = verdict ?? reviewers.get(review.reviewerId)?.status ?? "commented";
		reviewers.set(review.reviewerId, { login: review.reviewerId, bot: review.isBot, status });
	}
	for (const { login } of pr.requestedReviewers ?? []) reviewers.set(login, { login, status: "pending" });
	return [...reviewers.values()].sort((a, b) => Number(a.status === "pending") - Number(b.status === "pending"));
}

/** Why a PR can't merge, most fundamental first. */
export type PRBlockReason = "draft" | "conflict" | "checks" | "cancelled" | "checking" | "changes" | "review" | "threads";

/**
 * The first thing stopping these facts merging; undefined when none is known.
 * `noChecks` says CI "unknown" means the PR has no checks, not checks unread.
 */
export function prBlockReason(facts: { state: string; ci: string; mergeability: string; review: string; threads?: boolean; cancelled?: boolean; noChecks?: boolean }): PRBlockReason | undefined {
	if (facts.state === "draft") return "draft";
	if (facts.mergeability === "conflicting") return "conflict";
	if (facts.ci === "failing") return "checks";
	// A cancelled run with no replacement is settled: it needs a re-run, not a wait.
	if (facts.cancelled) return "cancelled";
	if (facts.ci === "pending" || (facts.ci === "unknown" && !facts.noChecks) || facts.mergeability === "unknown") return "checking";
	if (facts.review === "changes_requested") return "changes";
	if (facts.review === "review_required") return "review";
	if (facts.threads) return "threads";
	return undefined;
}

export const prSummaryBlockReason = (pr: SessionPRSummary) => prBlockReason({
	state: pr.state,
	ci: pr.ci.state,
	mergeability: pr.mergeability.state,
	review: pr.review.decision,
	threads: pr.review.hasUnresolvedHumanComments,
	cancelled: ciCancelled(pr),
	noChecks: prHasNoChecks(pr),
});

/**
 * How long after a PR last changed its checks get to start before "no checks
 * reported" means nothing checks it. The daemon waits as long from the push
 * (its noChecksSettle) before it lets such a PR merge; the PR changes as the
 * daemon sees the push, so waiting from that never shows a Merge it refuses.
 */
export const PR_NO_CHECKS_SETTLE_MS = 2 * 60_000;

/**
 * Whether a PR's loaded details show no checks at all, settled. GitHub reports
 * no status rollup for a commit nothing checks, which reaches here as CI
 * "unknown", the same as checks not read yet. Only details that have loaded
 * and list no checks tell the two apart, and only once the PR has been still
 * for PR_NO_CHECKS_SETTLE_MS: a commit pushed seconds ago has no rollup either.
 */
export function prHasNoChecks(pr: SessionPRSummary, now = Date.now()): boolean {
	const settlesAt = noChecksSettlesAt(pr);
	return settlesAt !== undefined && now >= settlesAt;
}

/** When a PR with an empty rollup counts as having no checks, if it has one. See usePRNoChecksClock. */
export function noChecksSettlesAt(pr: SessionPRSummary): number | undefined {
	if (pr.ci.state !== "unknown" || !pr.detailsObservedAt || (pr.ci.checks ?? []).length > 0 || pr.ci.failingChecks.length > 0) return undefined;
	const changedAt = Date.parse(pr.updatedAt);
	return Number.isNaN(changedAt) ? undefined : changedAt + PR_NO_CHECKS_SETTLE_MS;
}

// A run cancelled with no replacement: the backend reports unknown and lists the
// cancelled checks. It is settled, so it must not read as still loading.
function ciCancelled(pr: SessionPRSummary): boolean {
	return pr.ci.state === "unknown" && pr.ci.failingChecks.some((check) => check.status === "cancelled");
}

/**
 * Whether the PR can merge now. `noChecks` (see prHasNoChecks) lets a PR that
 * nothing checks merge on mergeability and review alone; the classic card
 * leaves it unset and keeps waiting for checks.
 */
export function prCanMerge(pr: SessionPRSummary, options: { noChecks?: boolean } = {}): boolean {
	return (
		pr.state === "open" &&
		(pr.ci.state === "passing" || (pr.ci.state === "unknown" && options.noChecks === true)) &&
		pr.mergeability.state === "mergeable" &&
		reviewAllowsMerge(pr.review.decision) &&
		!pr.review.hasUnresolvedHumanComments
	);
}

function reviewAllowsMerge(decision: SessionPRSummary["review"]["decision"]): boolean {
	return decision === "approved" || decision === "none";
}

function reviewStatusDetail(pr: SessionPRSummary): string {
	switch (pr.review.decision) {
		case "approved": return appI18n.t("pr.review.requirementSatisfied");
		case "changes_requested": return appI18n.t("pr.review.changesActive");
		case "review_required": return appI18n.t("pr.review.requiredNotSubmitted");
		// Unresolved comments hold a PR back (the row is a warning) even with no review required.
		case "none": return appI18n.t(pr.review.hasUnresolvedHumanComments ? "pr.review.unresolvedComments" : "pr.review.notRequired");
	}
}

function mergeReadinessDetail(pr: SessionPRSummary): string {
	if (pr.mergeability.state === "conflicting") return appI18n.t("pr.merge.reasonConflict");
	// Review first: the checks-failing text states the review requirement is satisfied.
	if (!reviewAllowsMerge(pr.review.decision)) return appI18n.t("pr.merge.reasonReview");
	if (pr.ci.state === "failing") return appI18n.t("pr.merge.reasonChecksFailing");
	if (pr.review.hasUnresolvedHumanComments) return appI18n.t("pr.merge.reasonComments");
	if (pr.mergeability.state !== "mergeable") return providerBlockedDetail(pr);
	return appI18n.t("pr.merge.reasonReady");
}

function providerBlockedDetail(pr: SessionPRSummary): string {
	return appI18n.t("pr.merge.providerBlocked", { provider: pr.provider === "gitlab" ? "GitLab" : "GitHub" });
}

export function prSummaryParts(pr: SessionPRSummary): PRSummaryPart[] {
	return [
		{
			key: "ci",
			label: appI18n.t("pr.section.ci"),
			status: mergeReasons(pr).includes("github_checks_unavailable")
				? appI18n.t("pr.card.checksUnavailable")
				: ciCancelled(pr) ? appI18n.t("pr.card.checksCancelled") : ciLabel(pr.ci.state),
			summary: mergeReasons(pr).includes("github_checks_unavailable") ? appI18n.t("pr.merge.checksUnavailableDetail") : ciSummary(pr),
			links: ciLinks(pr),
			linkTotal: pr.ci.state === "failing" ? pr.ci.failingChecks.length : 0,
			overflowLabel: pr.ci.state === "failing" ? overflowLabel(pr.ci.failingChecks.length, 3, "check") : undefined,
			overflowNoun: "check",
			tone: ciTone(pr.ci.state),
		},
		{
			key: "merge",
			label: appI18n.t("pr.section.merge"),
			status: hasLostGitHubAccess(pr) ? appI18n.t("pr.merge.accessLost") : mergeabilityLabel(pr.mergeability.state),
			summary: hasLostGitHubAccess(pr) ? appI18n.t("pr.merge.accessLostDetail") : mergeSummary(pr),
			links: mergeLinks(pr),
			linkTotal: mergeLinkTotal(pr),
			overflowLabel: mergeOverflowLabel(pr),
			overflowNoun: mergeOverflowNoun(pr),
			tone: hasLostGitHubAccess(pr) ? "warning" : mergeabilityTone(pr.mergeability.state),
		},
		{
			key: "review",
			label: appI18n.t("pr.section.review"),
			status: reviewLabel(pr.review.decision),
			summary: reviewSummary(pr),
			links: reviewLinks(pr),
			linkTotal: reviewLinkTotal(pr),
			overflowLabel:
				pr.state === "draft" || pr.review.decision === "review_required"
					? undefined
					: overflowLabel(pr.review.unresolvedBy.length, 3, "reviewer"),
			overflowNoun: "reviewer",
			tone: reviewTone(pr.review.decision, pr.review.hasUnresolvedHumanComments),
		},
	];
}

export function prDiffSummary(pr: SessionPRSummary): string | undefined {
	const parts: string[] = [];
	if (pr.changedFiles > 0) {
		parts.push(`${pr.changedFiles} ${pluralize("file", pr.changedFiles)}`);
	}
	const lineDelta = formatLineDelta(pr.additions, pr.deletions);
	if (lineDelta) {
		parts.push(lineDelta);
	}
	return parts.length > 0 ? parts.join(" · ") : undefined;
}

function ciSummary(pr: SessionPRSummary): string | undefined {
	if (pr.ci.state === "failing") {
		return pr.ci.failingChecks.length === 0 ? appI18n.t("pr.ci.noFailingLink") : undefined;
	}
	return undefined;
}

function ciLinks(pr: SessionPRSummary): PRSummaryLink[] {
	if (pr.ci.state !== "failing") {
		return [];
	}
	return pr.ci.failingChecks.slice(0, 3).map((check) => ({
		label: check.name,
		href: check.url || undefined,
		title: check.conclusion || check.status,
	}));
}

function reviewSummary(pr: SessionPRSummary): string | undefined {
	if (pr.state === "merged" || pr.state === "closed") {
		return undefined;
	}
	if (pr.state === "draft") {
		return appI18n.t("pr.review.draftNotReady");
	}
	if (pr.review.decision === "changes_requested" || pr.review.hasUnresolvedHumanComments) {
		return reviewLinks(pr).length === 0 ? appI18n.t("pr.review.changesActive") : undefined;
	}
	if (pr.review.decision === "review_required") {
		return appI18n.t("pr.review.requiredNotSubmitted");
	}
	return undefined;
}

function reviewLinks(pr: SessionPRSummary): PRSummaryLink[] {
	if (pr.state === "merged" || pr.state === "closed" || pr.state === "draft") {
		return [];
	}
	if (pr.review.decision !== "changes_requested" && !pr.review.hasUnresolvedHumanComments) {
		return [];
	}
	const links = pr.review.unresolvedBy.slice(0, 3).map((reviewer) => reviewAttentionLink(pr, reviewer));
	if (links.length === 0 && pr.review.decision === "changes_requested") {
		links.push({
			label: appI18n.t("pr.short"),
			href: prBrowserUrl(pr),
			title: appI18n.t("pr.review.openPR"),
		});
	}
	return links;
}

function mergeSummary(pr: SessionPRSummary): string | undefined {
	if (pr.state === "merged" || pr.state === "closed") {
		return formatDiffSummary(pr);
	}
	if (pr.mergeability.state === "conflicting") {
		return mergeLinks(pr).length === 0 ? appI18n.t("pr.merge.conflictsWithBase") : undefined;
	}
	if (pr.mergeability.state === "blocked" || pr.mergeability.state === "unstable") {
		return mergeLinks(pr).length === 0 ? providerBlockedDetail(pr) : undefined;
	}
	return formatDiffSummary(pr);
}

function mergeLinks(pr: SessionPRSummary): PRSummaryLink[] {
	if (pr.state === "merged" || pr.state === "closed") {
		return [];
	}
	if (pr.mergeability.state === "conflicting") {
		return mergeAttentionLinks(pr, "merge_conflict");
	}
	if (pr.mergeability.state === "blocked" || pr.mergeability.state === "unstable") {
		return mergeAttentionLinks(pr, "merge_blocked");
	}
	return [];
}

function mergeOverflowLabel(pr: SessionPRSummary): string | undefined {
	if (pr.state === "merged" || pr.state === "closed") {
		return undefined;
	}
	const hasFileLinks = (pr.mergeability.conflictFiles ?? []).length > 0;
	if (hasFileLinks) {
		return overflowLabel(pr.mergeability.conflictFiles?.length ?? 0, 3, "file");
	}
	if (pr.mergeability.state === "blocked" || pr.mergeability.state === "unstable") {
		return overflowLabel(visibleMergeReasons(pr).length, 3, "reason");
	}
	return undefined;
}

function mergeLinkTotal(pr: SessionPRSummary): number {
	if (pr.state === "merged" || pr.state === "closed") {
		return 0;
	}
	if (pr.mergeability.state === "conflicting") {
		const conflictFileCount = pr.mergeability.conflictFiles?.length ?? 0;
		return conflictFileCount > 0 ? conflictFileCount : mergeLinks(pr).length;
	}
	if (pr.mergeability.state === "blocked" || pr.mergeability.state === "unstable") {
		return visibleMergeReasons(pr).length;
	}
	return 0;
}

function mergeOverflowNoun(pr: SessionPRSummary): PRNoun {
	return (pr.mergeability.conflictFiles ?? []).length > 0 ? "file" : "reason";
}

function reviewLinkTotal(pr: SessionPRSummary): number {
	if (pr.state === "merged" || pr.state === "closed" || pr.state === "draft") {
		return 0;
	}
	if (pr.review.decision !== "changes_requested" && !pr.review.hasUnresolvedHumanComments) {
		return 0;
	}
	return pr.review.unresolvedBy.length > 0 ? pr.review.unresolvedBy.length : reviewLinks(pr).length;
}

function toCIState(value: string): SessionPRSummary["ci"]["state"] {
	return ciStates.has(value as SessionPRSummary["ci"]["state"])
		? (value as SessionPRSummary["ci"]["state"])
		: "unknown";
}

function toReviewDecision(value: string): SessionPRSummary["review"]["decision"] {
	return reviewDecisions.has(value as SessionPRSummary["review"]["decision"])
		? (value as SessionPRSummary["review"]["decision"])
		: "none";
}

function toMergeabilityState(value: string): SessionPRSummary["mergeability"]["state"] {
	return mergeabilityStates.has(value as SessionPRSummary["mergeability"]["state"])
		? (value as SessionPRSummary["mergeability"]["state"])
		: "unknown";
}

function ciLabel(state: SessionPRSummary["ci"]["state"]): string {
	switch (state) {
		case "passing":
			return appI18n.t("pr.ci.passing");
		case "failing":
			return appI18n.t("pr.ci.failing");
		case "pending":
			return appI18n.t("pr.ci.pending");
		case "unknown":
			return appI18n.t("pr.ci.checking");
	}
}

function ciTone(state: SessionPRSummary["ci"]["state"]): PRDisplayTone {
	switch (state) {
		case "passing":
			return "success";
		case "failing":
			return "error";
		case "pending":
			return "neutral";
		case "unknown":
			return "passive";
	}
}

function reviewLabel(decision: SessionPRSummary["review"]["decision"]): string {
	switch (decision) {
		case "approved":
			return appI18n.t("pr.review.approved");
		case "changes_requested":
			return appI18n.t("pr.review.changesRequested");
		case "review_required":
			return appI18n.t("pr.review.pending");
		case "none":
			return appI18n.t("pr.review.none");
	}
}

function reviewTone(
	decision: SessionPRSummary["review"]["decision"],
	hasUnresolvedHumanComments: boolean,
): PRDisplayTone {
	switch (decision) {
		case "approved":
			return "success";
		case "changes_requested":
			return "warning";
		case "review_required":
			return "review";
		case "none":
			return hasUnresolvedHumanComments ? "warning" : "passive";
	}
}

function mergeabilityLabel(state: SessionPRSummary["mergeability"]["state"]): string {
	switch (state) {
		case "mergeable":
			return appI18n.t("pr.merge.mergeable");
		case "conflicting":
			return appI18n.t("pr.merge.conflict");
		case "blocked":
			return appI18n.t("pr.merge.blocked");
		case "unstable":
			return appI18n.t("pr.merge.unstable");
		case "unknown":
			return appI18n.t("pr.merge.checking");
	}
}

function hasLostGitHubAccess(pr: SessionPRSummary): boolean {
	return mergeReasons(pr).includes("github_access_lost");
}

function mergeabilityTone(state: SessionPRSummary["mergeability"]["state"]): PRDisplayTone {
	switch (state) {
		case "mergeable":
			return "success";
		case "conflicting":
			return "error";
		case "blocked":
		case "unstable":
			return "warning";
		case "unknown":
			return "passive";
	}
}

function formatDiffSummary(pr: SessionPRSummary): string | undefined {
	if (pr.changedFiles > 0) {
		return `${pr.changedFiles} ${pluralize("file", pr.changedFiles)}`;
	}
	const changedLines = pr.additions + pr.deletions;
	if (changedLines > 0) {
		return `${changedLines} ${pluralize("line", changedLines)}`;
	}
	return undefined;
}

function formatLineDelta(additions: number, deletions: number): string | undefined {
	const parts: string[] = [];
	if (additions > 0) {
		parts.push(`+${additions}`);
	}
	if (deletions > 0) {
		parts.push(`-${deletions}`);
	}
	return parts.length > 0 ? parts.join(" ") : undefined;
}

function mergeAttentionLinks(pr: SessionPRSummary, kind: "merge_conflict" | "merge_blocked"): PRSummaryLink[] {
	const href =
		kind === "merge_conflict" ? mergeConflictUrl(pr) : pr.mergeability.prUrl || pr.htmlUrl || pr.url || undefined;
	const openConflicts = appI18n.t("pr.merge.openConflicts");
	const fileLinks = (pr.mergeability.conflictFiles ?? []).slice(0, 3).map((file) => ({
		label: file.path,
		href: file.url || href,
		title: kind === "merge_conflict" ? openConflicts : undefined,
	}));
	const reasonLinks =
		fileLinks.length > 0 || kind === "merge_conflict"
			? []
			: visibleMergeReasons(pr).slice(0, 3).map((reason) => ({
					label: mergeReasonLabel(reason),
					href,
				}));
	const fallbackLink =
		kind === "merge_conflict" && href
			? [{ label: appI18n.t("pr.merge.conflicts"), href, title: openConflicts }]
			: [];
	return fileLinks.length > 0 ? fileLinks : reasonLinks.length > 0 ? reasonLinks : fallbackLink;
}

// `blocked_by_provider` is an internal fallback for a host verdict AO cannot
// explain more precisely. It is not an actionable reason, so cards summarize
// it as merge availability instead of exposing implementation terminology.
function visibleMergeReasons(pr: SessionPRSummary): string[] {
	return mergeReasons(pr).filter((reason) => reason !== "blocked_by_provider");
}

// GitHub's and GitLab's `/conflicts` subpages only exist when the host judges the
// conflict resolvable in its web editor; otherwise they 404. The PR page always
// exists and shows the conflict banner, so link there instead.
function mergeConflictUrl(pr: SessionPRSummary): string | undefined {
	return prURL(pr) ?? pr.mergeability.prUrl ?? prBrowserUrl(pr);
}

function prBaseUrl(pr: SessionPRSummary): string | undefined {
	return prURL(pr);
}

function prURL(pr: SessionPRSummary): string | undefined {
	const raw = pr.htmlUrl || pr.mergeability.prUrl || pr.url;
	if (!raw) {
		return undefined;
	}
	try {
		const url = new URL(raw);
		// GitHub: /owner/repo/pull/N or /issues/N
		const ghMatch = url.pathname.match(/^(\/[^/]+\/[^/]+)\/(?:pull|issues)\/(\d+)(?:\/.*)?$/);
		if (ghMatch) {
			url.pathname = `${ghMatch[1]}/pull/${ghMatch[2]}`;
			url.search = "";
			url.hash = "";
			return url.toString();
		}
		// GitLab: /owner/repo/-/merge_requests/N
		const glMatch = url.pathname.match(/^(\/[^/]+\/[^/]+)\/-\/merge_requests\/(\d+)(?:\/.*)?$/);
		if (glMatch) {
			url.pathname = `${glMatch[1]}/-/merge_requests/${glMatch[2]}`;
			url.search = "";
			url.hash = "";
			return url.toString();
		}
		return undefined;
	} catch {
		return undefined;
	}
}

function reviewerLabel(reviewer: SessionPRSummary["review"]["unresolvedBy"][number]): string {
	const name = reviewerDisplayName(reviewer);
	if (reviewer.count <= 1) {
		return name;
	}
	return `${name} +${reviewer.count - 1}`;
}

function reviewerDisplayName(reviewer: SessionPRSummary["review"]["unresolvedBy"][number]): string {
	if (!reviewer.isBot) return reviewer.reviewerId;
	return appI18n.t("pr.botSuffix", { name: reviewer.reviewerId });
}

function reviewAttentionLink(
	pr: SessionPRSummary,
	reviewer: SessionPRSummary["review"]["unresolvedBy"][number],
): PRSummaryLink {
	const name = reviewerDisplayName(reviewer);
	const inlineURL = reviewer.links.find((link) => link.url)?.url;
	if (reviewer.reviewUrl) {
		return {
			label: reviewerLabel(reviewer),
			href: reviewer.reviewUrl,
			title: appI18n.t("pr.openReviewFrom", { name }),
		};
	}
	if (inlineURL) {
		return {
			label: reviewerLabel(reviewer),
			href: inlineURL,
				title:
					reviewer.count > 0
						? appI18n.t("pr.unresolvedComments", {
								count: reviewer.count,
								name,
							})
					: appI18n.t("pr.openCommentsFrom", { name }),
		};
	}
	return {
		label: reviewerLabel(reviewer),
		href: prBrowserUrl(pr),
		title: appI18n.t("pr.openPRFor", { name }),
	};
}

function mergeReasonLabel(reason: string): string {
	switch (reason) {
		case "behind_base":
			return appI18n.t("pr.reason.behindBase");
		case "ci_failing":
			return appI18n.t("pr.reason.ciFailing");
		case "changes_requested":
			return appI18n.t("pr.reason.changesRequested");
		case "review_required":
			return appI18n.t("pr.reason.reviewRequired");
		case "blocked_by_provider":
			return appI18n.t("pr.reason.providerBlocked");
		default:
			return reason.replaceAll("_", " ");
	}
}

function overflowLabel(total: number, shown: number, noun: PRNoun): string | undefined {
	const extra = total - shown;
	if (extra <= 0) {
		return undefined;
	}
	return appI18n.t("pr.overflow", { n: extra, noun: pluralize(noun, extra) });
}

function pluralize(noun: PRNoun, count: number): string {
	return appI18n.t(prNounKeys[noun], { count });
}

/**
 * owner/name of the repository a pull request URL points at (GitHub
 * /owner/name/pull/N or GitLab /owner/name/-/merge_requests/N), or undefined.
 */
export function prRepositoryFromUrl(raw: string | undefined): string | undefined {
	if (!raw) return undefined;
	try {
		const match = new URL(raw).pathname.match(/^\/([^/]+\/[^/]+)\/(?:pull|issues|-\/merge_requests)\/\d+/);
		return match?.[1];
	} catch {
		return undefined;
	}
}

/**
 * Whether a session's pull requests span more than one repository (a
 * multi-repository cloud session can open one per repository), in which case
 * each one is labelled with its repository.
 */
/** A repository's name without its owner: "acme/app" is "app". */
export function prRepositoryShortName(repository: string): string {
	return repository.slice(repository.lastIndexOf("/") + 1) || repository;
}

/**
 * What to call each repository beside a PR number when a session's PRs span
 * several: its short name, or its full name when two owners share that name
 * (a fork and its upstream), keyed by the full name.
 */
export function prRepositoryLabels(prs: ReadonlyArray<{ url?: string; htmlUrl?: string; repo?: string }>): Map<string, string> {
	const owners = new Map<string, Set<string>>();
	const repositories = prs.map((pr) => pr.repo || prRepositoryFromUrl(pr.htmlUrl || pr.url) || "").filter(Boolean);
	for (const repository of repositories) {
		const short = prRepositoryShortName(repository).toLowerCase();
		owners.set(short, (owners.get(short) ?? new Set()).add(repository.toLowerCase()));
	}
	return new Map(repositories.map((repository) => {
		const short = prRepositoryShortName(repository);
		return [repository, (owners.get(short.toLowerCase())?.size ?? 0) > 1 ? repository : short];
	}));
}

export function prsSpanRepositories(prs: ReadonlyArray<{ url?: string; htmlUrl?: string; repo?: string }>): boolean {
	const repositories = new Set(prs.map((pr) => (pr.repo || prRepositoryFromUrl(pr.htmlUrl || pr.url) || "").toLowerCase()).filter(Boolean));
	return repositories.size > 1;
}
