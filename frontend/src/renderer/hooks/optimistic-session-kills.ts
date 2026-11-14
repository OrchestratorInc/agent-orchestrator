import { toKanbanColumn, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";
import { LOCAL_HOST, refKey } from "../lib/hosts";

const optimisticKillIds = new Set<string>();
let optimisticKillVersion = 0;
const optimisticKillListeners = new Set<() => void>();

function notifyOptimisticKillChange() {
	optimisticKillVersion += 1;
	for (const listener of optimisticKillListeners) listener();
}

export function subscribeOptimisticSessionKills(listener: () => void): () => void {
	optimisticKillListeners.add(listener);
	return () => optimisticKillListeners.delete(listener);
}

export function optimisticSessionKillsVersion(): number {
	return optimisticKillVersion;
}

function sessionKey(sessionId: string, hostId?: string): string {
	return refKey({ host: hostId ?? LOCAL_HOST, id: sessionId });
}

function markTerminated(sessionId: string, hostId?: string) {
	return (session: WorkspaceSession): WorkspaceSession =>
		session.id === sessionId && session.hostId === hostId
			? {
				...session,
				isTerminated: true,
				status: "terminated",
				kanbanColumn: toKanbanColumn(undefined, "terminated"),
			}
			: session;
}

export function trackOptimisticSessionKill(sessionId: string, hostId?: string): void {
	const key = sessionKey(sessionId, hostId);
	if (optimisticKillIds.has(key)) return;
	optimisticKillIds.add(key);
	notifyOptimisticKillChange();
}

export function clearOptimisticSessionKill(sessionId: string, hostId?: string): void {
	if (optimisticKillIds.delete(sessionKey(sessionId, hostId))) notifyOptimisticKillChange();
}

export function applyTerminatedSession(
	workspaces: WorkspaceSummary[] | undefined,
	sessionId: string,
	hostId?: string,
): WorkspaceSummary[] | undefined {
	return workspaces?.map((workspace) => ({
		...workspace,
		sessions: workspace.sessions.map(markTerminated(sessionId, hostId)),
	}));
}

export function applyOptimisticSessionKills(
	workspaces: WorkspaceSummary[] | undefined,
): WorkspaceSummary[] | undefined {
	if (!workspaces || optimisticKillIds.size === 0) return workspaces;
	let changed = false;
	const next = workspaces.map((workspace) => {
		let sessionsChanged = false;
		const sessions = workspace.sessions.map((session) => {
			if (!optimisticKillIds.has(sessionKey(session.id, session.hostId)) || session.isTerminated === true) return session;
			sessionsChanged = true;
			return markTerminated(session.id, session.hostId)(session);
		});
		if (!sessionsChanged) return workspace;
		changed = true;
		return { ...workspace, sessions };
	});
	return changed ? next : workspaces;
}
