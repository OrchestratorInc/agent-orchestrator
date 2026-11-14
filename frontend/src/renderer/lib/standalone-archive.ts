import {
	ownScratchpadWorkspaces,
	workerSessions,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";

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
