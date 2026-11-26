import type { DashboardPR, DashboardSession } from "./api";
import { prLifecycle, type PRLifecycle, type Tone } from "./prView";
import type { WorkerRowIndicator } from "./workerRowMarker";

/**
 * The pull request a worker row's pill names, and how it reads. The pill
 * speaks for the pull request alone, in its lifecycle's tone and glyph; the
 * worker's own state is the lead mark's (see workerRowMarker).
 */
export type WorkerRowPullRequest = {
	/** `#15`, or `#15 +1` with more than one. */
	label: string;
	/** The pull request itself, so the row's "Open PR" opens the one the pill names. */
	lead: DashboardPR;
	number: number;
	lifecycle: PRLifecycle;
	/** How many more pull requests the worker has. */
	more: number;
	/** The board card's tones (`prLine`): open green, merged purple, closed red, a draft quiet. */
	tone: Tone;
	glyph: "git-pull-request" | "git-pull-request-draft" | "git-merge" | "git-pull-request-closed";
};

// The live one leads: a worker that closed #12 and opened #15 is on #15.
const LEAD_ORDER: Record<PRLifecycle, number> = { open: 0, draft: 1, merged: 2, closed: 3 };
const TONES: Record<PRLifecycle, Tone> = { open: "success", draft: "passive", merged: "merged", closed: "error" };
const GLYPHS: Record<PRLifecycle, WorkerRowPullRequest["glyph"]> = {
	open: "git-pull-request",
	draft: "git-pull-request-draft",
	merged: "git-merge",
	closed: "git-pull-request-closed",
};

/** Null for a worker with no pull request. */
export function workerRowPullRequest(session: DashboardSession): WorkerRowPullRequest | null {
	const list: DashboardPR[] = session.prs?.length ? session.prs : session.pr ? [session.pr] : [];
	const real = list.filter((pr) => pr?.number > 0);
	if (real.length === 0) return null;
	// Open first, then drafts, merged and closed; the newest within each.
	const lead = real.reduce((best, pr) => {
		const rank = LEAD_ORDER[prLifecycle(pr)] - LEAD_ORDER[prLifecycle(best)];
		return rank < 0 || (rank === 0 && pr.number > best.number) ? pr : best;
	});
	const lifecycle = prLifecycle(lead);
	const more = real.length - 1;
	return {
		label: more > 0 ? `#${lead.number} +${more}` : `#${lead.number}`,
		lead,
		number: lead.number,
		lifecycle,
		more,
		tone: TONES[lifecycle],
		glyph: GLYPHS[lifecycle],
	};
}

const INDICATOR_WORDS: Record<Exclude<WorkerRowIndicator, null | "attention">, string> = {
	working: "Working",
	review: "Review next",
	ready: "Ready to merge",
	merged: "Merged",
};

/**
 * What a screen reader hears for a row: what the lead mark and pill show, in
 * words. The state follows the same source as the mark (the board's display
 * status), so a red "needs you" mark is never read out as "PR open".
 */
export function workerRowAccessibilityLabel({ title, project, indicator, displayStatus, statusLabel, pr, machine, updated, unreachable }: {
	title: string;
	project: string;
	indicator: WorkerRowIndicator;
	/** The board's own words for the state, such as "Merge conflicts". */
	displayStatus?: string | null;
	/** The raw status's label, for a quiet row the board has no words for. */
	statusLabel: string;
	pr: WorkerRowPullRequest | null;
	machine?: string;
	/** `relativeTime`'s short form, such as `5m`, or `now`. */
	updated: string;
	unreachable: boolean;
}): string {
	const state = indicator === "attention"
		? displayStatus || "Needs you"
		: indicator
			? INDICATOR_WORDS[indicator]
			: displayStatus || statusLabel;
	const parts = [title, state];
	if (pr) parts.push(`Pull request ${pr.number}, ${pr.lifecycle}${pr.more ? `, and ${pr.more} more` : ""}`);
	parts.push(project);
	if (machine) parts.push(`on ${machine}`);
	if (updated) parts.push(`Updated ${spokenAge(updated)}`);
	return `${parts.join(". ")}.${unreachable ? " Last known; its machine can't be reached." : ""}`;
}

const UNITS: Record<string, [string, string]> = { m: ["minute", "minutes"], h: ["hour", "hours"], d: ["day", "days"], w: ["week", "weeks"] };

/** `5m` as "5 minutes ago", for a screen reader that would say "5 m". */
function spokenAge(short: string): string {
	if (short === "now") return "just now";
	const match = /^(\d+)([mhdw])$/.exec(short);
	if (!match) return short;
	const count = Number(match[1]);
	const [one, many] = UNITS[match[2]];
	return `${count} ${count === 1 ? one : many} ago`;
}
