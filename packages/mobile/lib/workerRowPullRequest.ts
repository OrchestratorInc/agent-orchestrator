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
	/**
	 * The `+N`'s own tone, from the pull requests it counts: red when any of
	 * them closed, purple when all merged, green when one is still open, quiet
	 * for drafts. Null with no others.
	 */
	moreTone: Tone | null;
	/** What the `+N` counts, by lifecycle, in the lead order. Empty with no others. */
	moreLifecycles: { lifecycle: PRLifecycle; count: number }[];
	/** The board card's tones (`prLine`): open green, merged purple, closed red, a draft quiet. */
	tone: Tone;
	glyph: "git-pull-request" | "git-pull-request-draft" | "git-merge" | "git-pull-request-closed";
};

// The live one leads: a worker that closed #12 and opened #15 is on #15.
const LEAD_ORDER: Record<PRLifecycle, number> = { open: 0, draft: 1, merged: 2, closed: 3 };
export const PR_TONES: Record<PRLifecycle, Tone> = { open: "success", draft: "passive", merged: "merged", closed: "error" };
export const PR_GLYPHS: Record<PRLifecycle, WorkerRowPullRequest["glyph"]> = {
	open: "git-pull-request",
	draft: "git-pull-request-draft",
	merged: "git-merge",
	closed: "git-pull-request-closed",
};

/** Open first, then drafts, merged and closed; the newest within each. A sorted copy. */
export function leadOrdered(prs: readonly DashboardPR[]): DashboardPR[] {
	return [...prs].sort((a, b) => LEAD_ORDER[prLifecycle(a)] - LEAD_ORDER[prLifecycle(b)] || b.number - a.number);
}

/** Null for a worker with no pull request. */
export function workerRowPullRequest(session: Pick<DashboardSession, "pr" | "prs">): WorkerRowPullRequest | null {
	const list: DashboardPR[] = session.prs?.length ? session.prs : session.pr ? [session.pr] : [];
	const real = list.filter((pr) => pr?.number > 0);
	if (real.length === 0) return null;
	const lead = leadOrdered(real)[0];
	const lifecycle = prLifecycle(lead);
	const more = real.length - 1;
	const counts = new Map<PRLifecycle, number>();
	for (const pr of real) {
		if (pr === lead) continue;
		const life = prLifecycle(pr);
		counts.set(life, (counts.get(life) ?? 0) + 1);
	}
	const moreLifecycles = [...counts.entries()]
		.map(([life, count]) => ({ lifecycle: life, count }))
		.sort((a, b) => LEAD_ORDER[a.lifecycle] - LEAD_ORDER[b.lifecycle]);
	return {
		label: more > 0 ? `#${lead.number} +${more}` : `#${lead.number}`,
		lead,
		number: lead.number,
		lifecycle,
		more,
		moreTone: moreToneOf(counts),
		moreLifecycles,
		tone: PR_TONES[lifecycle],
		glyph: PR_GLYPHS[lifecycle],
	};
}

// A closed one is the thing to notice, so it wins; merged only when every one is.
function moreToneOf(counts: Map<PRLifecycle, number>): Tone | null {
	if (counts.size === 0) return null;
	if (counts.has("closed")) return PR_TONES.closed;
	if (counts.has("open")) return PR_TONES.open;
	if (counts.has("draft")) return PR_TONES.draft;
	return PR_TONES.merged;
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
	if (pr) parts.push(`Pull request ${pr.number}, ${pr.lifecycle}${pullRequestMoreWords(pr)}`);
	// One sentence for where it runs: "web on Studio", not "web. on Studio".
	parts.push(machine ? `${project} on ${machine}` : project);
	if (updated) parts.push(`Updated ${spokenAge(updated)}`);
	return `${parts.join(". ")}.${unreachable ? " Last known; its machine can't be reached." : ""}`;
}

/** `, and 3 more merged`, or `, and 3 more: 2 merged, 1 closed`; the colour alone says nothing to a screen reader. */
export function pullRequestMoreWords(pr: WorkerRowPullRequest): string {
	const groups = pr.moreLifecycles;
	if (groups.length === 0) return "";
	if (groups.length === 1) return `, and ${pr.more} more ${groups[0].lifecycle}`;
	return `, and ${pr.more} more: ${groups.map((g) => `${g.count} ${g.lifecycle}`).join(", ")}`;
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
