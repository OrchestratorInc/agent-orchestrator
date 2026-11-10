import { useEffect } from "react";
import { queryOptions, type QueryClient } from "@tanstack/react-query";
import { useAgentReadinessQuery } from "../hooks/useAgentReadinessQuery";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { CLOUD_PROJECT_KIND, STANDALONE_WORKSPACE_ID, type WorkspaceSummary } from "../types/workspace";
import type { components } from "../../api/schema";
import { apiClient, apiErrorCode } from "./api-client";
import { apiErrorException } from "./api-error-exception";
import { captureRendererEvent } from "./telemetry";

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
		const started = Date.now();
		const response = await apiClient.GET("/api/v1/session-import", { signal });
		captureOncePerRun("scan", "ao.session_imports.scan_completed", {
			outcome: response.error ? "failed" : "succeeded",
			candidate_count: response.data?.candidates.length,
			duration_ms: Date.now() - started,
		});
		if (response.error) throw apiErrorException(response.error);
		return response.data;
	},
});

export type ImportSource = "home_folders" | "project_offer" | "project_dialog" | "settings";

const reportedThisRun = new Set<string>();

function captureOncePerRun(key: string, event: string, properties: Record<string, unknown>): void {
	if (reportedThisRun.has(key)) return;
	reportedThisRun.add(key);
	void captureRendererEvent(event, properties);
}

export function useImportOfferShown(surface: "home" | "project_board", shown: boolean): void {
	useEffect(() => {
		if (shown) captureOncePerRun(`offer:${surface}`, "ao.session_imports.offer_shown", { surface });
	}, [surface, shown]);
}

export function trackImportFirstOpen(agent: string, opening: Promise<void>): void {
	const started = Date.now();
	void opening.then(
		() => captureRendererEvent("ao.session_imports.first_opened", { agent, outcome: "succeeded", duration_ms: Date.now() - started }),
		(error: unknown) => captureRendererEvent("ao.session_imports.first_opened", {
			agent,
			outcome: apiErrorCode(error) === "SESSION_OPEN_ELSEWHERE" ? "open_elsewhere" : "failed",
			error_code: apiErrorCode(error),
			duration_ms: Date.now() - started,
		}),
	);
}

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

export async function importChats(queryClient: QueryClient, source: ImportSource, ids: string[], folders: string[] = []): Promise<ImportResult[]> {
	const results: ImportResult[] = [];
	let threw = true;
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
		threw = false;
		return results;
	} finally {
		const harness = new Map(queryClient.getQueryData(sessionImportPreviewQuery.queryKey)?.candidates.map((c) => [c.id, c.harness] as const));
		const created = results.filter((r) => r.status === "created");
		void captureRendererEvent("ao.session_imports.completed", {
			source,
			created_count: created.length,
			existing_count: results.filter((r) => r.status === "already_imported").length,
			failed_count: threw ? ids.length : results.filter((r) => r.status === "failed").length,
			folder_count: folders.length,
			agents: [...new Set(created.flatMap((r) => harness.get(r.id) ?? []))].sort().join(","),
		});
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
