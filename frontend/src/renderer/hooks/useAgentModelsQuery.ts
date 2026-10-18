import { queryOptions, type QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { foldClaudeAliasDefault } from "../lib/agent-model-choices";
import { clientForHost } from "../lib/host-clients";
import { LOCAL_HOST } from "../lib/hosts";

export type AgentModelCatalog = components["schemas"]["AgentModelsResponse"];

const MODEL_CATALOG_VALIDATION_INTERVAL_MS = 10 * 60 * 1_000;
const MODEL_CATALOG_REFRESH_POLL_MS = 3_000;

/** Whether the daemon is still refreshing this catalog in the background. */
export function isRefreshingCatalog(catalog: Pick<AgentModelCatalog, "refreshState"> | undefined): boolean {
	return catalog?.refreshState === "refreshing" || catalog?.refreshState === "queued";
}

export const agentModelsQueryPrefix = (agentId: string) =>
	["agent-models", agentId] as const;

export const agentModelsQueryKey = (agentId: string, projectId: string, hostId?: string, role?: string) => {
	if (hostId) return ["agent-models", hostId, agentId, projectId, role ?? ""] as const;
	if (role) return [...agentModelsQueryPrefix(agentId), projectId, `role:${role}`] as const;
	return [...agentModelsQueryPrefix(agentId), projectId] as const;
};

async function requestAgentModels(
	agentId: string,
	projectId: string,
	mode: "cached" | "refresh" | "revalidate",
	hostId?: string,
	role?: string,
	catalogOnly = false,
): Promise<AgentModelCatalog> {
	const client = hostId ? clientForHost(hostId) : apiClient;
	const path = { agent: agentId };
	const query = { projectId: projectId || undefined, role: role || undefined };
	const result =
		mode === "cached"
			? await client.GET("/api/v1/agents/{agent}/models", {
					params: { path, query: { ...query, ...(catalogOnly ? { catalogOnly: true } : {}) } },
				})
			: await client.POST("/api/v1/agents/{agent}/models/refresh", {
					params: {
						path,
						query: { ...query, revalidate: mode === "revalidate" || undefined, ...(catalogOnly ? { catalogOnly: true } : {}) },
					},
				});
	if (result.error) throw new Error(apiErrorMessage(result.error));
	const catalog = result.data as AgentModelCatalog;
	return agentId === "claude-code" ? { ...catalog, models: foldClaudeAliasDefault(catalog.models) } : catalog;
}

export function agentModelsQueryOptions(agentId: string, projectId: string, hostId?: string, role?: string, catalogOnly = false) {
	const key = agentModelsQueryKey(agentId, projectId, hostId, role);
	return queryOptions({
		// Both read the shared daemon catalog; only task responses add recency.
		queryKey: catalogOnly ? [...key, "catalog"] as const : key,
		queryFn: () => requestAgentModels(agentId, projectId, "cached", hostId, role, catalogOnly),
		enabled: agentId !== "",
		// A catalog that carries a warning describes a problem the user may fix
		// outside AO (a login in a terminal), so any remount re-reads it.
		staleTime: (query) => (query.state.data?.warning ? 0 : MODEL_CATALOG_VALIDATION_INTERVAL_MS),
		// The daemon serves its cached catalog at once and refreshes it in the
		// background; re-read it while that runs so the new one appears.
		refetchInterval: (query) => (isRefreshingCatalog(query.state.data) ? MODEL_CATALOG_REFRESH_POLL_MS : false),
	});
}

export type ModelCatalogAuthIssue = "required" | "expired";

// Older or remote daemons predate warningCode; their wording for the same
// conditions is stable enough to recognize.
const AUTH_WARNING_PATTERN = /not signed in|sign in to load|rejected the credential|access token has expired|re-?authenticate|unauthori[sz]ed|\b401\b/i;

/** Whether a catalog warning is an agent login problem the user can fix by logging in. */
export function modelCatalogAuthIssue(catalog: Pick<AgentModelCatalog, "warning" | "warningCode"> | undefined): ModelCatalogAuthIssue | undefined {
	if (catalog?.warningCode === "auth_expired") return "expired";
	if (catalog?.warningCode === "auth_required") return "required";
	if (!catalog?.warningCode && catalog?.warning && AUTH_WARNING_PATTERN.test(catalog.warning)) return "required";
	return undefined;
}

/**
 * Drop every cached catalog for one agent, including the background
 * revalidation results keyed off it, so mounted pickers re-read the daemon.
 * Call after anything that can change what the agent's login allows.
 */
export function invalidateAgentModelCatalogs(queryClient: QueryClient, agentId: string, hostId?: string): Promise<unknown> {
	return Promise.all([
		queryClient.invalidateQueries({ queryKey: hostId ? ["agent-models", hostId, agentId] : agentModelsQueryPrefix(agentId) }),
		queryClient.invalidateQueries({
			predicate: ({ queryKey: [root, first, second] }) => {
				if (root !== "agent-model-revalidation") return false;
				// Pickers key revalidation as [root, agent, …] or [root, host, agent, …],
				// spelling the local host as absent, "" or LOCAL_HOST.
				if (hostId) return first === hostId && second === agentId;
				return first === agentId || ((first === "" || first === LOCAL_HOST) && second === agentId);
			},
		}),
	]);
}

export function refreshAgentModels(agentId: string, projectId: string, hostId?: string, role?: string, catalogOnly = false) {
	return requestAgentModels(agentId, projectId, "refresh", hostId, role, catalogOnly);
}

export function revalidateAgentModels(agentId: string, projectId: string, hostId?: string, role?: string, catalogOnly = false) {
	return requestAgentModels(agentId, projectId, "revalidate", hostId, role, catalogOnly);
}
