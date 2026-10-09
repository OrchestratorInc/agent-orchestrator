import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo, useSyncExternalStore } from "react";
import { useCloudProjectsQuery, useCloudSessionsQuery, remoteWorkspaceQueryKey, toCloudWorkspace, useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSummary } from "../types/workspace";
import { LOCAL_HOST } from "./hosts";
import { useNavigateToSession } from "./navigate-to-session";
import { parseSessionLink, resolveSessionLink } from "./session-links";

export type SessionLinkSource = {
	ready: boolean;
	isLoading: boolean;
	isError: boolean;
	workspaces: WorkspaceSummary[];
};

function useLocalLinkSource(_hostId: string): SessionLinkSource {
	const query = useWorkspaceQuery({ includeCloud: false });
	return { ready: query.isSuccess, isLoading: query.isLoading, isError: query.isError, workspaces: query.data ?? [] };
}

function useCloudLinkSource(_hostId: string): SessionLinkSource {
	const projects = useCloudProjectsQuery();
	const sessions = useCloudSessionsQuery();
	const { org, ready: orgReady } = useCloudOrg();
	const workspaces = useMemo(() => {
		if (!org?.id) return [];
		return (projects.data ?? []).map((project) => toCloudWorkspace(project, sessions.data ?? [], org.id));
	}, [org?.id, projects.data, sessions.data]);
	return {
		ready: orgReady && Boolean(org?.id) && projects.isSuccess && sessions.isSuccess,
		isLoading: !orgReady || projects.isLoading || sessions.isLoading,
		isError: projects.isError || sessions.isError,
		workspaces,
	};
}

function useRemoteLinkSource(hostId: string): SessionLinkSource {
	const queryClient = useQueryClient();
	const subscribe = useCallback((notify: () => void) => queryClient.getQueryCache().subscribe(notify), [queryClient]);
	const state = useSyncExternalStore(
		subscribe,
		() => queryClient.getQueryState<WorkspaceSummary[]>(remoteWorkspaceQueryKey(hostId)),
	);
	return {
		ready: state?.status === "success",
		isLoading: state === undefined || state.status === "pending",
		isError: state?.status === "error",
		workspaces: state?.data ?? [],
	};
}

export function useSessionLinkSource(sourceHostId?: string, sourceKind?: "cloud"): SessionLinkSource {
	const remoteHostId = sourceHostId && sourceHostId !== LOCAL_HOST ? sourceHostId : undefined;
	const useSource = remoteHostId ? useRemoteLinkSource : sourceKind === "cloud" ? useCloudLinkSource : useLocalLinkSource;
	return useSource(remoteHostId ?? "");
}

export function useSessionLinkNavigation(sourceHostId?: string, sourceKind?: "cloud"): (url: string) => boolean {
	const remoteHostId = sourceHostId && sourceHostId !== LOCAL_HOST ? sourceHostId : undefined;
	const source = useSessionLinkSource(sourceHostId, sourceKind);
	const navigateToSession = useNavigateToSession();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	return useCallback((url: string) => {
		const target = parseSessionLink(url);
		if (!target) {
			showGlobalToast("This AO session link is malformed or unsupported.", undefined, {
				tone: "error",
				placement: "top-center",
				dismissible: true,
				dedupeKey: "session-link:error",
			});
			return false;
		}
		if (!source.ready) {
			showGlobalToast(`AO could not verify that session. Check the ${remoteHostId ? "host" : sourceKind === "cloud" ? "Cloud" : "daemon"} connection and try again.`, undefined, {
				tone: "error",
				placement: "top-center",
				dismissible: true,
				dedupeKey: "session-link:error",
			});
			return false;
		}
		const resolved = resolveSessionLink(url, source.workspaces);
		if (!resolved) {
			showGlobalToast("That session is missing or is not accessible in this AO workspace.", undefined, {
				tone: "error",
				placement: "top-center",
				dismissible: true,
				dedupeKey: "session-link:error",
			});
			return false;
		}
		if (resolved.isTerminated) {
			showGlobalToast(`Session ${resolved.sessionId} is terminated`, undefined, {
				placement: "top-center",
				dismissible: true,
				durationMs: 5_000,
				dedupeKey: `session-link:${remoteHostId ? `${remoteHostId}:` : ""}${resolved.projectId}:${resolved.sessionId}`,
			});
			return false;
		}
		if (remoteHostId) navigateToSession(resolved.projectId, resolved.sessionId, remoteHostId);
		else navigateToSession(resolved.projectId, resolved.sessionId);
		return true;
	}, [navigateToSession, remoteHostId, showGlobalToast, source, sourceKind]);
}
