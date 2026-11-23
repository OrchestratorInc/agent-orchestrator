import type { QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { appI18n } from "../i18n";
import { beginOptimisticRename, queueRenameWrite, settleOptimisticRename } from "../hooks/optimistic-renames";
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
 * Puts `title` on the session in every cached list that shows it. An explicit
 * name hides the terminal program's title on the daemon, so it does here too.
 */
function writeSessionTitle(queryClient: QueryClient, target: SessionRenameTarget, title: string) {
	if (target.cloud) {
		queryClient.setQueriesData<CloudCpSession[]>({ queryKey: cloudSessionsQueryKey }, (current) =>
			current?.some((session) => session.id === target.id && session.displayName !== title)
				? current.map((session) => (session.id === target.id ? { ...session, displayName: title } : session))
				: current);
		queryClient.setQueriesData<CloudCpSession | undefined>({ queryKey: ["cloud-session"] }, (current) =>
			current?.id === target.id && current.displayName !== title ? { ...current, displayName: title } : current);
		return;
	}
	if (target.hostId) {
		queryClient.setQueryData<WorkspaceSummary[]>(remoteWorkspaceQueryKey(target.hostId), (current) => renamedInWorkspaces(current, target, title));
		queryClient.setQueryData<SessionView[]>(remoteSessionsQueryKey(target.hostId), (current) =>
			current?.some((session) => session.id === target.id)
				? current.map((session) => (session.id === target.id ? { ...session, displayName: title, terminalTitle: undefined } : session))
				: current);
		return;
	}
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
		const settled = settleOptimisticRename(key, token, true);
		if (settled?.latest && queryClient) {
			if (stored !== displayName) writeSessionTitle(queryClient, session, stored);
			// A Cloud title lives in the control plane's lists, whatever else the caller reads again.
			await Promise.all([
				options.refresh?.(),
				!options.refresh || session.cloud ? refreshSessionLists(queryClient, session) : undefined,
			]);
		}
		return true;
	} catch (error) {
		console.error("Failed to rename session:", error);
		const settled = settleOptimisticRename(key, token, false);
		// A newer rename still showing makes this failure moot for what is on screen.
		if (settled?.latest) {
			if (queryClient) {
				writeSessionTitle(queryClient, session, settled.title);
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
