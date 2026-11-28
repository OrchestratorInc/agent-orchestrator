import type { DashboardPR, DashboardSession, SessionPRSummary } from "./api";
import { prResolvedLifecycle, prReviewPromptHeadline, type PRLifecycle, type Tone } from "./prView";
import { leadOrdered, PR_GLYPHS, PR_TONES, workerRowPullRequest, type WorkerRowPullRequest } from "./workerRowPullRequest";

/**
 * Every pull request a session raised, for its chip and the list the chip
 * opens. The order and tones are the Workers badge's (workerRowPullRequest),
 * so the chip's lead is the PR the board's pill names.
 */

/** One row of the session's pull request list. */
export type PullRequestRow = {
	pr: DashboardPR;
	number: number;
	/** The PR's own title once its summary loads; "Pull request" until then (the row shows `#N` beside it). */
	title: string;
	lifecycle: PRLifecycle;
	glyph: WorkerRowPullRequest["glyph"];
	/** The lifecycle's tone, for the glyph: open green, draft quiet, merged purple, closed red. */
	glyphTone: Tone;
	/** The one status that decides what to do next, as the composer's chip says it. */
	status: { text: string; tone: Tone };
	accessibilityLabel: string;
};

/** What the composer needs for a session with any pull request. */
export type SessionPullRequests = {
	lead: DashboardPR;
	leadSummary?: SessionPRSummary;
	/** The badge's view: how many more, and the `+N`'s tone and words. */
	badge: WorkerRowPullRequest;
	rows: PullRequestRow[];
	/** A merge of `lead` started from its page is still running (see prMergeTracker). */
	leadMerging?: boolean;
};

type SummaryFor = (number: number) => SessionPRSummary | undefined;
/** A merge the phone started on this pull request, if any (see prMergeTracker). */
type MergeOf = (pr: DashboardPR) => "merging" | "merged" | "failed" | undefined;

const MERGING: PullRequestRow["status"] = { text: "Merging…", tone: "merged" };

/** The session's real pull requests (a number above zero), in lead order. */
export function sessionPullRequests(session: Pick<DashboardSession, "pr" | "prs">): DashboardPR[] {
	const list: DashboardPR[] = session.prs?.length ? session.prs : session.pr ? [session.pr] : [];
	return leadOrdered(list.filter((pr) => pr?.number > 0));
}

export function pullRequestRows(prs: readonly DashboardPR[], summaryFor: SummaryFor): PullRequestRow[] {
	return prs.map((pr) => {
		const summary = summaryFor(pr.number);
		// The same state the status reads, so the icon and the status never disagree.
		const lifecycle = prResolvedLifecycle(pr, summary);
		const loaded = summary?.title?.trim();
		// The row draws `#N` beside the title, so until the title loads it reads `#N Pull request`.
		const title = loaded || "Pull request";
		const status = prReviewPromptHeadline(pr, summary);
		return {
			pr,
			number: pr.number,
			title,
			lifecycle,
			glyph: PR_GLYPHS[lifecycle],
			glyphTone: PR_TONES[lifecycle],
			status,
			accessibilityLabel: loaded ? `Pull request ${pr.number}, ${loaded}, ${status.text}` : `Pull request ${pr.number}, ${status.text}`,
		};
	});
}

/**
 * Undefined for a session with no pull request, which shows no chip. A merge
 * the phone started shows before the board or the summaries agree: one running
 * says "Merging…", and a finished one reads merged at once.
 */
export function sessionPullRequestModel(session: Pick<DashboardSession, "pr" | "prs">, summaryFor: SummaryFor, mergeOf: MergeOf = () => undefined): SessionPullRequests | undefined {
	const list: DashboardPR[] = session.prs?.length ? session.prs : session.pr ? [session.pr] : [];
	const merged = new Set(list.filter((pr) => pr && pr.state !== "merged" && pr.state !== "closed" && mergeOf(pr) === "merged").map((pr) => pr.number));
	const tracked = merged.size ? { prs: list.map((pr) => merged.has(pr.number) ? { ...pr, state: "merged" as const } : pr) } : session;
	const trackedSummaryFor: SummaryFor = merged.size ? (number) => {
		const summary = summaryFor(number);
		return summary && merged.has(number) ? { ...summary, state: "merged" } : summary;
	} : summaryFor;
	const prs = sessionPullRequests(tracked);
	const badge = workerRowPullRequest(tracked);
	if (prs.length === 0 || !badge) return undefined;
	const rows = pullRequestRows(prs, trackedSummaryFor).map((row) => mergeOf(row.pr) === "merging"
		? { ...row, status: MERGING, accessibilityLabel: row.title === "Pull request" ? `Pull request ${row.number}, ${MERGING.text}` : `Pull request ${row.number}, ${row.title}, ${MERGING.text}` }
		: row);
	const model: SessionPullRequests = { lead: badge.lead, leadSummary: trackedSummaryFor(badge.lead.number), badge, rows };
	return mergeOf(badge.lead) === "merging" ? { ...model, leadMerging: true } : model;
}

/** What the session's ⋯ menu "Pull requests" does: one PR's review, or the list. */
export function pullRequestMenuAction(count: number): "review" | "list" | "none" {
	return count === 0 ? "none" : count === 1 ? "review" : "list";
}
