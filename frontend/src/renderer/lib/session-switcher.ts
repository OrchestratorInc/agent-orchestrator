import {
	newestActiveOrchestrator,
	sortedWorkerSessions,
	type WorkspaceSession,
} from "../types/workspace";

/** Workers shown in the switcher. The orchestrator, when live, is an extra row. */
export const SESSION_SWITCHER_WORKER_LIMIT = 7;

export type SessionSwitcherRole = "orchestrator" | "worker";

export type SessionSwitcherEntry = {
	id: string;
	role: SessionSwitcherRole;
};

/**
 * Eligible workers match the sidebar: non-orchestrator sessions that are not
 * terminated (`isTerminated !== true`), ordered by sidebar activity
 * (`sortedWorkerSessions`: last interaction, else last user message, else creation).
 * Manual sidebar drag order is not used. The list is frozen by the caller
 * while the popup is open.
 *
 * At most seven workers. If the current worker is among the latest seven, those
 * seven are shown. Otherwise the current worker plus the latest six, still in
 * activity order. Kanban (no current worker) shows the latest seven.
 * The live orchestrator is always first and does not consume a worker slot.
 */
export function buildSessionSwitcherEntries(
	sessions: readonly WorkspaceSession[],
	currentSessionId?: string,
): SessionSwitcherEntry[] {
	const orchestrator = newestActiveOrchestrator([...sessions]);
	const workers = sortedWorkerSessions([...sessions]).filter((session) => session.isTerminated !== true);
	const currentIndex = workers.findIndex((session) => session.id === currentSessionId);
	const shown = currentIndex >= SESSION_SWITCHER_WORKER_LIMIT
		? workers.filter((_, index) => index < SESSION_SWITCHER_WORKER_LIMIT - 1 || index === currentIndex)
		: workers.slice(0, SESSION_SWITCHER_WORKER_LIMIT);
	const entries: SessionSwitcherEntry[] = [];
	if (orchestrator) entries.push({ id: orchestrator.id, role: "orchestrator" });
	for (const session of shown) entries.push({ id: session.id, role: "worker" });
	return entries;
}

/** First press highlights the current row, or the orchestrator on the project board. */
export function initialSessionSwitcherIndex(
	entries: readonly SessionSwitcherEntry[],
	currentSessionId: string | undefined,
	kanban: boolean,
): number {
	if (!kanban && currentSessionId) {
		const current = entries.findIndex((entry) => entry.id === currentSessionId);
		if (current >= 0) return current;
	}
	if (kanban) {
		const orchestrator = entries.findIndex((entry) => entry.role === "orchestrator");
		if (orchestrator >= 0) return orchestrator;
	}
	return 0;
}

export function stepSessionSwitcherIndex(index: number, length: number, direction: -1 | 1): number {
	if (length <= 0) return 0;
	return (index + direction + length) % length;
}

/** Ids that may still be committed: sidebar-eligible workers plus the live orchestrator. */
export function sessionSwitcherEligibleIds(sessions: readonly WorkspaceSession[]): Set<string> {
	const ids = new Set(
		sortedWorkerSessions([...sessions])
			.filter((session) => session.isTerminated !== true)
			.map((session) => session.id),
	);
	const orchestrator = newestActiveOrchestrator([...sessions]);
	if (orchestrator) ids.add(orchestrator.id);
	return ids;
}

/**
 * Release commits the highlighted id when the interaction was not cancelled
 * and that id is still eligible. Same-session selection does not navigate.
 */
export function sessionSwitcherCommitId(
	selectedId: string | undefined,
	eligibleIds: ReadonlySet<string>,
	currentSessionId: string | undefined,
	cancelled: boolean,
): string | undefined {
	if (cancelled || !selectedId) return undefined;
	if (!eligibleIds.has(selectedId)) return undefined;
	if (selectedId === currentSessionId) return undefined;
	return selectedId;
}
