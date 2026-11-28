import type { DashboardSession } from "./api";
import { kanbanColumnOf } from "./agentsView";

/**
 * What leads a worker row, the desktop sidebar's rule (`getSessionRowIndicator`
 * in the renderer's session-presentation): the working lattice while the agent
 * works, then an icon only for what the user has something to do about, and a
 * quiet ring for everything else.
 *
 *  - "attention", red alert: needs you now (input, blocked, CI failing, merge
 *    conflicts, changes requested)
 *  - "review", orange alert: a person's review is next
 *  - "ready", green pull request: ready to merge
 *  - "merged", dimmed purple merge: landed, nothing left to do
 */
export type WorkerRowMarker = "attention" | "ready" | "review" | "merged";
export type WorkerRowIndicator = "working" | WorkerRowMarker | null;

type MarkerSession = DashboardSession;

const WORKING_STATUSES = new Set(["working", "detecting", "spawning"]);

export function workerNeedsInput(session: MarkerSession): boolean {
	return (
		session.status === "needs_input" ||
		session.activity === "waiting_input" ||
		session.activity === "blocked" ||
		session.displayStatus === "Blocked" ||
		session.displayStatus === "Needs input"
	);
}

/** Working wins over any PR state: the agent may already be on its next PR. */
export function workerRowWorking(session: MarkerSession): boolean {
	if (session.displayStatus === "Terminated" || session.isTerminated) return false;
	if (session.provisionState === "provisioning" || session.activity === "active") return true;
	// An older daemon sends no activity; its status says the same thing.
	return !session.activity && WORKING_STATUSES.has(session.status ?? "");
}

export function workerRowMarker(session: MarkerSession): WorkerRowMarker | null {
	const status = session.status ?? "";
	const display = session.displayStatus ?? undefined;
	if (display === "Terminated" || status === "terminated" || display === "Closed without merge") return null;

	if (workerNeedsInput(session)) return "attention";
	if (display === "CI failing" || display === "Merge conflicts" || display === "Changes requested") return "attention";
	if (!display && (status === "ci_failed" || status === "changes_requested")) return "attention";

	switch (kanbanColumnOf(session)) {
		case "needs_review":
			// A draft is still the author's, and a PR waiting on its stack parent is
			// waiting on another PR, so both stay quiet.
			if (display === "Waiting on base PR") return null;
			return (display ? display === "Draft" : status === "draft") ? null : "review";
		case "validating":
			// AO's own review loop needs nothing from the user unless it failed.
			return display === "Review failed" ? "review" : null;
		case "ready":
			if (display ? display === "Merged" : status === "merged") return "merged";
			return (display ? display === "Mergeable" || display === "Approved" : status === "mergeable" || status === "approved")
				? "ready"
				: null;
		default:
			return null;
	}
}

export function workerRowIndicator(session: MarkerSession): WorkerRowIndicator {
	return workerRowWorking(session) ? "working" : workerRowMarker(session);
}
