import { queryOptions, type QueryClient } from "@tanstack/react-query";
import { useAgentReadinessQuery } from "../hooks/useAgentReadinessQuery";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { CLOUD_PROJECT_KIND, STANDALONE_WORKSPACE_ID, type WorkspaceSummary } from "../types/workspace";
import type { components } from "../../api/schema";
import { apiClient, apiErrorCode } from "./api-client";
import { apiErrorException } from "./api-error-exception";

export type ImportCandidate = components["schemas"]["SessionImportCandidate"];
export type ImportResult = components["schemas"]["SessionImportResult"];
export type AgentBlock = "not_installed" | "signed_out";

export const MAX_IMPORT = 500;

export const sessionImportPreviewQuery = queryOptions({
	queryKey: ["session-import-preview"],
	retry: false,
	staleTime: 0,
	refetchOnWindowFocus: false,
	refetchOnReconnect: false,
	queryFn: async ({ signal }) => {
		const response = await apiClient.GET("/api/v1/session-import", { signal });
		if (response.error) throw apiErrorException(response.error);
		return response.data;
	},
});

export function countBy(keys: string[]): Map<string, number> {
	const counts = new Map<string, number>();
	for (const key of keys) counts.set(key, (counts.get(key) ?? 0) + 1);
	return counts;
}

export function folderName(path: string): string {
	return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path;
}

export function importableProject(workspace: WorkspaceSummary): boolean {
	return !workspace.hostId && !workspace.shared && workspace.kind !== CLOUD_PROJECT_KIND && workspace.id !== STANDALONE_WORKSPACE_ID;
}

export function projectChats(candidates: ImportCandidate[] | undefined, projectId: string): ImportCandidate[] {
	return (candidates ?? [])
		.filter((c) => c.projectId === projectId && !c.sessionId)
		.sort((a, b) => b.lastActivity.localeCompare(a.lastActivity));
}

export function agentBlock(agent: { installation: { state: string }; authentication: { state: string } } | undefined): AgentBlock | undefined {
	if (agent?.installation.state === "not_installed") return "not_installed";
	if (agent?.authentication.state === "unauthorized") return "signed_out";
	return undefined;
}

export function useImportedAgentBlock(
	session: { needsResume?: boolean; provider: string; cloud?: unknown } | undefined,
	hostId?: string,
): { block?: AgentBlock; checking: boolean } {
	const watch = Boolean(session?.needsResume && !session.cloud && !hostId);
	const readiness = useAgentReadinessQuery(watch);
	if (!watch) return { checking: false };
	return {
		block: agentBlock(readiness.data?.agents?.find((agent) => agent.id === session?.provider)),
		checking: readiness.isPending && readiness.fetchStatus !== "idle",
	};
}

export async function importChats(queryClient: QueryClient, ids: string[], folders: string[] = []): Promise<ImportResult[]> {
	const results: ImportResult[] = [];
	try {
		for (const path of folders) {
			const added = await apiClient.POST("/api/v1/projects", { body: { path } });
			if (added.error && apiErrorCode(added.error) !== "PATH_ALREADY_REGISTERED") throw apiErrorException(added.error);
		}
		if (folders.length) {
			await queryClient.cancelQueries({ queryKey: sessionImportPreviewQuery.queryKey });
			await queryClient.fetchQuery(sessionImportPreviewQuery);
		}
		for (let start = 0; start < ids.length; start += MAX_IMPORT) {
			try {
				const response = await apiClient.POST("/api/v1/session-import", { body: { ids: ids.slice(start, start + MAX_IMPORT) } });
				if (response.error) throw apiErrorException(response.error);
				results.push(...response.data.results);
			} catch (err) {
				if (!results.length) throw err;
				const error = err instanceof Error ? err.message : String(err);
				results.push(...ids.slice(start).map((id) => ({ id, status: "failed" as const, error })));
				break;
			}
		}
		return results;
	} finally {
		const imported = new Map(results.flatMap((r) => (r.status !== "failed" && r.sessionId ? [[r.id, r.sessionId] as const] : [])));
		if (imported.size) {
			await queryClient.cancelQueries({ queryKey: sessionImportPreviewQuery.queryKey });
			queryClient.setQueryData(sessionImportPreviewQuery.queryKey, (data) =>
				data && { ...data, candidates: data.candidates.map((c) => (imported.has(c.id) ? { ...c, sessionId: imported.get(c.id) } : c)) },
			);
		}
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
	}
}
