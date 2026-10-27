import { queryOptions, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo, useSyncExternalStore } from "react";
import { useCloudProjectsQuery, useCloudSessionsQuery, remoteWorkspaceQueryKey, toWorkspaceSession, toCloudWorkspace, useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { useUiStore } from "../stores/ui-store";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSummary } from "../types/workspace";
import { apiClient, apiErrorCode } from "./api-client";
import { LOCAL_HOST } from "./hosts";
import { useNavigateToSession } from "./navigate-to-session";
import { artifactLink, artifactLinkForPath, artifactPathCandidates, parseArtifactLink, parseSessionLink, resolveSessionLink } from "./session-links";

export type SessionLinkSource = {
	ready: boolean;
	isLoading: boolean;
	isError: boolean;
	workspaces: WorkspaceSummary[];
};

function localLinkSessionOptions(sessionId: string, workspaces: WorkspaceSummary[]) {
	return queryOptions({
		queryKey: ["session", sessionId],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}", { params: { path: { sessionId } } });
			if (error) throw error;
			if (!data?.session) throw new Error("Session is unavailable");
			const projectId = data.session.projectId || STANDALONE_WORKSPACE_ID;
			const project = workspaces.find((workspace) => workspace.id === projectId) ?? { id: projectId, name: "" };
			return toWorkspaceSession(data.session, project);
		},
		staleTime: 10_000,
	});
}

/** Resolve only the session IDs named by inline artifact paths, never archive pages. */
export function useArtifactPathLink(value: string, workspaces: WorkspaceSummary[], sourceHostId?: string, sourceKind?: "cloud") {
	const listed = artifactLinkForPath(value, workspaces);
	const explicitArtifactPath = value.replace(/\\/g, "/").includes("/artifacts/");
	const candidates = listed ? [] : artifactPathCandidates(value).filter((candidate) => explicitArtifactPath || /^.+-[1-9][0-9]*$/.test(candidate.sessionId));
	const queryClient = useQueryClient();
	const local = (!sourceHostId || sourceHostId === LOCAL_HOST) && sourceKind !== "cloud"
		&& workspaces.some((workspace) => (workspace.archivedCount ?? 0) > 0);
	const ids = [...new Set(candidates.map((candidate) => candidate.sessionId))];
	const reads = useQueries({ queries: ids.map((id) => queryOptions({
		queryKey: ["artifact-path-session", id],
		queryFn: async () => {
			try {
				return await queryClient.fetchQuery({ ...localLinkSessionOptions(id, workspaces), retry: false });
			} catch (error) {
				if (apiErrorCode(error) === "SESSION_NOT_FOUND") return null;
				throw error;
			}
		},
		// A confirmed missing speculative target must not refetch with workspace CDC.
		enabled: (query) => local && query.state.data !== null,
		retry: false,
		staleTime: 10_000,
	})) });
	if (listed || !local) return listed;
	for (const candidate of candidates) {
		const session = reads.find((read) => read.data?.id === candidate.sessionId)?.data;
		if (session && workspaces.some((workspace) => workspace.id === session.workspaceId)
			&& session.artifactFiles?.some((file) => file.path === candidate.path)) {
			return artifactLink(session.workspaceId, session.id, candidate.path);
		}
	}
	return undefined;
}

function canLookUpLocalLink(target: { projectId: string }, workspaces: WorkspaceSummary[]) {
	return workspaces.some((workspace) => workspace.archivedCount !== undefined)
		&& workspaces.some((workspace) => workspace.id === target.projectId);
}

function useLocalLinkSource(_hostId: string, href?: string): SessionLinkSource {
	const query = useWorkspaceQuery({ includeCloud: false });
	const workspaces = query.data ?? [];
	const target = href ? parseSessionLink(href) ?? parseArtifactLink(href) : undefined;
	const needsDetail = Boolean(href && target && !resolveSessionLink(href, workspaces) && canLookUpLocalLink(target, workspaces));
	const detail = useQuery({ ...localLinkSessionOptions(target?.sessionId ?? "", workspaces), enabled: needsDetail });
	const data = useMemo(() => {
		const session = detail.data;
		if (!needsDetail || !session || session.workspaceId !== target?.projectId) return workspaces;
		return workspaces.map((workspace) => workspace.id === session.workspaceId
			? { ...workspace, sessions: [...workspace.sessions, session] } : workspace);
	}, [workspaces, detail.data, needsDetail, target?.projectId]);
	return {
		ready: query.isSuccess && (!needsDetail || detail.data !== undefined),
		isLoading: query.isLoading || (needsDetail && detail.isLoading),
		isError: query.isError || (needsDetail && detail.isError),
		workspaces: data,
	};
}

function useCloudLinkSource(_hostId: string, _href?: string): SessionLinkSource {
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

function useRemoteLinkSource(hostId: string, _href?: string): SessionLinkSource {
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

export function useSessionLinkSource(sourceHostId?: string, sourceKind?: "cloud", href?: string): SessionLinkSource {
	const remoteHostId = sourceHostId && sourceHostId !== LOCAL_HOST ? sourceHostId : undefined;
	const useSource = remoteHostId ? useRemoteLinkSource : sourceKind === "cloud" ? useCloudLinkSource : useLocalLinkSource;
	return useSource(remoteHostId ?? "", href);
}

export function useSessionLinkNavigation(sourceHostId?: string, sourceKind?: "cloud"): (url: string) => boolean {
	const remoteHostId = sourceHostId && sourceHostId !== LOCAL_HOST ? sourceHostId : undefined;
	const source = useSessionLinkSource(sourceHostId, sourceKind);
	const queryClient = useQueryClient();
	const navigateToSession = useNavigateToSession();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const requestWorkspaceFileOpen = useUiStore((state) => state.requestWorkspaceFileOpen);
	return useCallback((url: string) => {
		const artifact = parseArtifactLink(url);
		const target = parseSessionLink(url) ?? artifact;
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
		const unavailable = () => showGlobalToast("That session is missing or is not accessible in this AO workspace.", undefined, {
			tone: "error", placement: "top-center", dismissible: true, dedupeKey: "session-link:error",
		});
		const openResolved = (resolved: NonNullable<ReturnType<typeof resolveSessionLink>>) => {
			// A terminated session's artifacts are still served, so its artifact links still open.
			if (resolved.isTerminated && !artifact) {
				showGlobalToast(`Session ${resolved.sessionId} is terminated`, undefined, {
					placement: "top-center",
					dismissible: true,
					durationMs: 5_000,
					dedupeKey: `session-link:${remoteHostId ? `${remoteHostId}:` : ""}${resolved.projectId}:${resolved.sessionId}`,
				});
				return false;
			}
			if (artifact) requestWorkspaceFileOpen(resolved.sessionId, artifact.path, remoteHostId, "artifact");
			if (remoteHostId) navigateToSession(resolved.projectId, resolved.sessionId, remoteHostId);
			else navigateToSession(resolved.projectId, resolved.sessionId);
			return true;
		};
		const resolved = resolveSessionLink(url, source.workspaces);
		if (resolved) return openResolved(resolved);
		if (!remoteHostId && sourceKind !== "cloud" && canLookUpLocalLink(target, source.workspaces)) {
			void queryClient.fetchQuery(localLinkSessionOptions(target.sessionId, source.workspaces)).then((session) => {
				if (session.workspaceId !== target.projectId) { unavailable(); return; }
				openResolved({ ...target, isTerminated: session.isTerminated === true || session.status === "terminated" });
			}).catch(unavailable);
			return true;
		}
		unavailable();
		return false;
	}, [queryClient, navigateToSession, remoteHostId, requestWorkspaceFileOpen, showGlobalToast, source, sourceKind]);
}
