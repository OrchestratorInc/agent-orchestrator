import { useMemo } from "react";
import type { components } from "../../api/schema";
import { useQuery } from "@tanstack/react-query";
import { apiClient, hasTrustedApiBaseUrl } from "../lib/api-client";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSummary } from "../types/workspace";
import { toWorkspaceSession } from "./useWorkspaceQuery";

export const archivedSessionsQueryKey = ["archived-sessions"] as const;

/** Read every page only for a history consumer. Never insert ended rows into the live list. */
export async function fetchArchivedSessions(projectId?: string, signal?: AbortSignal) {
	const sessions: components["schemas"]["ControllersSessionView"][] = [];
	let cursor: string | undefined;
	const seen = new Set<string>();
	const ids = new Set<string>();
	do {
		const { data, error } = await apiClient.GET("/api/v1/sessions", {
			signal,
			params: { query: { active: false, limit: 100, cursor, project: projectId === STANDALONE_WORKSPACE_ID ? undefined : projectId } },
		});
		if (error) throw error;
		for (const session of data?.sessions ?? []) {
			if (ids.has(session.id)) continue;
			ids.add(session.id);
			sessions.push(session);
		}
		cursor = data?.nextCursor;
		if (cursor && seen.has(cursor)) throw new Error("Archive pagination did not advance");
		if (cursor) seen.add(cursor);
	} while (cursor);
	return projectId === STANDALONE_WORKSPACE_ID ? sessions.filter((session) => !session.projectId) : sessions;
}

export function useArchivedWorkspaces(workspaces: WorkspaceSummary[], enabled: boolean, projectId?: string) {
	const supported = workspaces.some((workspace) => workspace.archivedCount !== undefined && (workspace.archiveSummary?.total ?? workspace.archivedCount) > 0);
	const archive = useQuery({
		queryKey: [...archivedSessionsQueryKey, projectId ?? "all"],
		enabled: enabled && supported && hasTrustedApiBaseUrl() && !usesPreviewWorkspaceData,
		queryFn: ({ signal }) => fetchArchivedSessions(projectId, signal),
		staleTime: 10_000,
	});
	const merged = useMemo(() => {
		if (!supported || !archive.data) return workspaces;
		return workspaces.map((workspace) => {
			if (workspace.hostId || workspace.kind === "cloud") return workspace;
			const rows = (archive.data ?? []).filter((session) => (session.projectId || STANDALONE_WORKSPACE_ID) === workspace.id);
			const ids = new Set(workspace.sessions.map((session) => session.id));
			return { ...workspace, sessions: [...workspace.sessions, ...rows.filter((row) => !ids.has(row.id)).map((row) => toWorkspaceSession(row, workspace))] };
		});
	}, [workspaces, archive.data, supported]);
	return { ...archive, workspaces: merged };
}
