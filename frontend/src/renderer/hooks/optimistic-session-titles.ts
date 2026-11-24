import type { components } from "../../api/schema";
import type { CloudCpSession } from "../lib/cloud-cp";
import { LOCAL_HOST, refKey } from "../lib/hosts";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { hasOptimisticRenames, renamedTitle } from "./optimistic-renames";

type SessionView = components["schemas"]["ControllersSessionView"];

/** A Cloud session is named on the control plane; others by the daemon of their host. */
export function sessionRenameKey(session: { id: string; hostId?: string; cloud?: unknown }): string {
	return session.cloud ? `session:cloud:${session.id}` : `session:${refKey({ host: session.hostId ?? LOCAL_HOST, id: session.id })}`;
}

/** The session with its confirmed rename; the same object when there is none. */
export function withOptimisticSessionTitle<T extends WorkspaceSession | undefined>(session: T, readStartedAt?: number): T {
	if (!session || !hasOptimisticRenames()) return session;
	const title = renamedTitle(sessionRenameKey(session), readStartedAt);
	return title === undefined || title === session.title ? session : { ...session, title };
}

/** The lists with every confirmed rename; the same array when none applies. */
export function applyOptimisticSessionTitles<T extends WorkspaceSummary[] | undefined>(workspaces: T, readStartedAt?: number): T {
	if (!workspaces || !hasOptimisticRenames()) return workspaces;
	let changed = false;
	const next = workspaces.map((workspace) => {
		let sessionsChanged = false;
		const sessions = workspace.sessions.map((session) => {
			const renamed = withOptimisticSessionTitle(session, readStartedAt);
			if (renamed !== session) sessionsChanged = true;
			return renamed;
		});
		if (!sessionsChanged) return workspace;
		changed = true;
		return { ...workspace, sessions };
	});
	return (changed ? next : workspaces) as T;
}

/**
 * Daemon session views from one host with their confirmed renames. An explicit
 * name hides the terminal program's title on the daemon, so it does here too.
 */
export function applyOptimisticSessionViewTitles<T extends SessionView>(sessions: T[], hostId: string | undefined, readStartedAt?: number): T[] {
	if (!hasOptimisticRenames()) return sessions;
	let changed = false;
	const next = sessions.map((session) => {
		const title = renamedTitle(sessionRenameKey({ id: session.id, hostId }), readStartedAt);
		if (title === undefined || (session.displayName === title && !session.terminalTitle)) return session;
		changed = true;
		return { ...session, displayName: title, terminalTitle: undefined };
	});
	return changed ? next : sessions;
}

/** Control-plane sessions with their confirmed renames. */
export function applyOptimisticCloudSessionTitles<T extends CloudCpSession>(sessions: T[], readStartedAt?: number): T[] {
	if (!hasOptimisticRenames()) return sessions;
	let changed = false;
	const next = sessions.map((session) => {
		const renamed = withOptimisticCloudSessionTitle(session, readStartedAt);
		if (renamed !== session) changed = true;
		return renamed;
	});
	return changed ? next : sessions;
}

export function withOptimisticCloudSessionTitle<T extends CloudCpSession | undefined>(session: T, readStartedAt?: number): T {
	if (!session || !hasOptimisticRenames()) return session;
	const title = renamedTitle(sessionRenameKey({ id: session.id, cloud: true }), readStartedAt);
	return title === undefined || title === session.displayName ? session : { ...session, displayName: title };
}
