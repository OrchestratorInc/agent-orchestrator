import type { QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { appI18n } from "../i18n";
import { beginOptimisticRename, hasLiveOptimisticRename, queueRenameWrite, settleOptimisticRename } from "../hooks/optimistic-renames";
import { sessionRenameKey } from "../hooks/optimistic-session-titles";
import {
	cloudSessionsQueryKey,
	refreshCloudSessionLifecycle,
	remoteSessionsQueryKey,
	remoteWorkspaceQueryKey,
	workspaceQueryKey,
	workspaceQueryKeyForHost,
} from "../hooks/useWorkspaceQuery";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import type { CloudCpSession } from "./cloud-cp";
import { renameCloudSession, renameSession } from "./rename-session";

export type SessionRenameTarget = Pick<WorkspaceSession, "id" | "title" | "hostId" | "cloud">;

type SessionView = components["schemas"]["ControllersSessionView"];

function renamedInWorkspaces(workspaces: WorkspaceSummary[] | undefined, target: SessionRenameTarget, title: string) {
	if (!workspaces) return workspaces;
	let changed = false;
	const next = workspaces.map((workspace) => {
		if (!workspace.sessions.some((session) => session.id === target.id && session.title !== title)) return workspace;
		changed = true;
		return { ...workspace, sessions: workspace.sessions.map((session) => (session.id === target.id ? { ...session, title } : session)) };
	});
	return changed ? next : workspaces;
}

/**
 * The names a session's cached lists held before its first rename still in
 * flight. The shown title can be a program's title or a fallback (an issue id,
 * the session id); a rollback with nothing else in flight puts these back
 * rather than writing that title as a given name.
 */
type NamesBefore = { view?: Pick<SessionView, "displayName" | "terminalTitle">; cloudDisplayName?: string };

const namesBeforeRename = new Map<string, NamesBefore>();

function readNamesBefore(queryClient: QueryClient, target: SessionRenameTarget): NamesBefore {
	if (target.cloud) {
		const listed = queryClient.getQueriesData<CloudCpSession[]>({ queryKey: cloudSessionsQueryKey })
			.flatMap(([, sessions]) => (Array.isArray(sessions) ? sessions : []))
			.find((session) => session.id === target.id)
			?? queryClient.getQueriesData<CloudCpSession | undefined>({ queryKey: ["cloud-session"] })
				.map(([, session]) => session)
				.find((session) => session?.id === target.id);
		return listed ? { cloudDisplayName: listed.displayName } : {};
	}
	if (!target.hostId) return {};
	const view = queryClient.getQueryData<SessionView[]>(remoteSessionsQueryKey(target.hostId))?.find((session) => session.id === target.id);
	return view ? { view: { displayName: view.displayName, terminalTitle: view.terminalTitle } } : {};
}

/**
 * Puts `title` on the session in every cached list that shows it. An explicit
 * name hides the terminal program's title on the daemon, so it does here too.
 * With `before`, the lists' own name fields go back to those instead.
 */
function writeSessionTitle(queryClient: QueryClient, target: SessionRenameTarget, title: string, before?: NamesBefore) {
	if (target.cloud) {
		const displayName = before?.cloudDisplayName ?? title;
		queryClient.setQueriesData<CloudCpSession[]>({ queryKey: cloudSessionsQueryKey }, (current) =>
			current?.some((session) => session.id === target.id && session.displayName !== displayName)
				? current.map((session) => (session.id === target.id ? { ...session, displayName } : session))
				: current);
		queryClient.setQueriesData<CloudCpSession | undefined>({ queryKey: ["cloud-session"] }, (current) =>
			current?.id === target.id && current.displayName !== displayName ? { ...current, displayName } : current);
		return;
	}
	if (target.hostId) {
		const names = before?.view ?? { displayName: title, terminalTitle: undefined };
		queryClient.setQueryData<WorkspaceSummary[]>(remoteWorkspaceQueryKey(target.hostId), (current) => renamedInWorkspaces(current, target, title));
		queryClient.setQueryData<SessionView[]>(remoteSessionsQueryKey(target.hostId), (current) =>
			current?.some((session) => session.id === target.id)
				? current.map((session) => (session.id === target.id ? { ...session, ...names } : session))
				: current);
		return;
	}
	// The local lists hold only the shown title, so writing it back is exact.
	queryClient.setQueryData<WorkspaceSummary[]>(workspaceQueryKey, (current) => renamedInWorkspaces(current, target, title));
	queryClient.setQueryData<WorkspaceSession | undefined>(["session", target.id], (current) =>
		current && current.title !== title ? { ...current, title } : current);
}

function refreshSessionLists(queryClient: QueryClient, target: SessionRenameTarget): Promise<unknown> {
	return target.cloud
		? refreshCloudSessionLifecycle(queryClient)
		: queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(target.hostId) });
}

export type CommitSessionRenameOptions = {
	/** Reads the caller's lists again after the rename lands, in place of the default (a Cloud session's lists are always read). */
	refresh?: () => unknown;
};

/**
 * Renames a session optimistically: the new title shows in every list and tab
 * from the commit that calls this (synchronously, before any await), stays
 * through slow answers and reads begun earlier, and a newer rename always wins
 * over an older one's answer (the requests go out in order). A failure rolls
 * back only its own title and says so in a toast. Resolves true once this rename is stored.
 */
export async function commitSessionRename(
	queryClient: QueryClient | null,
	session: SessionRenameTarget,
	name: string,
	options: CommitSessionRenameOptions = {},
): Promise<boolean> {
	const displayName = name.trim();
	if (!displayName || displayName === session.title) return false;
	const key = sessionRenameKey(session);
	if (queryClient && !hasLiveOptimisticRename(key)) namesBeforeRename.set(key, readNamesBefore(queryClient, session));
	const token = beginOptimisticRename(key, displayName, session.title);
	if (queryClient) writeSessionTitle(queryClient, session, displayName);
	try {
		// After any earlier rename of this session, so the newest is the one stored.
		const stored = await queueRenameWrite(key, async () => {
			if (session.cloud) {
				if (!queryClient) throw new Error("Renaming a Cloud session needs the app's query cache.");
				return renameCloudSession(queryClient, { id: session.id, cloud: session.cloud }, displayName);
			}
			if (session.hostId) await renameSession(session.id, displayName, session.hostId);
			else await renameSession(session.id, displayName);
			return displayName;
		});
		// The name the server kept covers older reads and is the rollback point.
		const settled = settleOptimisticRename(key, token, true, stored);
		// A stored name is now the rollback point: it is a given name, written as one.
		if (settled) namesBeforeRename.delete(key);
		if (settled?.latest) {
			if (queryClient && stored !== displayName) writeSessionTitle(queryClient, session, stored);
			// A Cloud title lives in the control plane's lists, whatever else the caller reads again.
			await Promise.all([
				options.refresh?.(),
				queryClient && (!options.refresh || session.cloud) ? refreshSessionLists(queryClient, session) : undefined,
			]);
		}
		return true;
	} catch (error) {
		console.error("Failed to rename session:", error);
		const settled = settleOptimisticRename(key, token, false);
		// A newer rename still showing makes this failure moot for what is on screen.
		if (settled?.latest) {
			// Nothing else in flight: the lists go back to exactly the names they held.
			const live = hasLiveOptimisticRename(key);
			const before = live ? undefined : namesBeforeRename.get(key);
			if (!live) namesBeforeRename.delete(key);
			if (queryClient) {
				writeSessionTitle(queryClient, session, settled.title, before);
				void refreshSessionLists(queryClient, session);
			}
			useUiStore.getState().showGlobalToast(
				appI18n.t("shell.renameFailed", { name: displayName }),
				error instanceof Error ? error.message : undefined,
				"error",
			);
		}
		return false;
	}
}
