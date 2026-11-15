import {
	ownScratchpadWorkspaces,
	workerSessions,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";

/**
 * Whether a session belongs in Archive (the board's, Scratchpad's, Cmd+K's
 * count). Live merged sessions stay on the lanes; a terminated runtime is
 * archived even when its SCM outcome is still `merged`, which is exactly what
 * the daemon's `archive` column means.
 */
export function isArchivedSession(session: WorkspaceSession): boolean {
	return (
		session.kanbanColumn === "archive" ||
		session.isTerminated === true ||
		session.status === "terminated"
	);
}

export function archivedStandaloneSessions(
	workspaces: readonly WorkspaceSummary[],
): WorkspaceSession[] {
	return workerSessions(ownScratchpadWorkspaces(workspaces).flatMap((workspace) => workspace.sessions))
		.filter(isArchivedSession)
		.sort((left, right) => right.updatedAt.localeCompare(left.updatedAt));
}
