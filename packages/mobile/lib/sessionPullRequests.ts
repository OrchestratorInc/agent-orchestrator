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
};

type SummaryFor = (number: number) => SessionPRSummary | undefined;

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

/** Undefined for a session with no pull request, which shows no chip. */
export function sessionPullRequestModel(session: Pick<DashboardSession, "pr" | "prs">, summaryFor: SummaryFor): SessionPullRequests | undefined {
	const prs = sessionPullRequests(session);
	const badge = workerRowPullRequest(session);
	if (prs.length === 0 || !badge) return undefined;
	return { lead: badge.lead, leadSummary: summaryFor(badge.lead.number), badge, rows: pullRequestRows(prs, summaryFor) };
}

/** What the session's ⋯ menu "Pull requests" does: one PR's review, or the list. */
export function pullRequestMenuAction(count: number): "review" | "list" | "none" {
	return count === 0 ? "none" : count === 1 ? "review" : "list";
}
