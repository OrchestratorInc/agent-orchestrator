import { queryOptions, type QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { foldClaudeAliasDefault } from "../lib/agent-model-choices";
import { cacheAgentModels, clearCachedAgentModels, readCachedAgentModels, withCachedModelUsage } from "../lib/agent-model-cache";
import { modelCatalogScope } from "../lib/cloud-agents";
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

// Model catalogs are device-wide, so every project shares one query per host and
// agent. `scope` only distinguishes a cloud credential scope (credentialModelScope);
// any other value, including a project id, reads the device catalog.
export const agentModelsQueryKey = (agentId: string, scope: string, hostId?: string) => {
	const catalogScope = modelCatalogScope(scope);
	return hostId ? ["agent-models", hostId, agentId, catalogScope] as const : [...agentModelsQueryPrefix(agentId), catalogScope] as const;
};

async function requestAgentModels(
	agentId: string,
	scope: string,
	mode: "cached" | "refresh" | "revalidate",
	hostId?: string,
	catalogOnly = false,
	signal?: AbortSignal,
): Promise<AgentModelCatalog> {
	const client = hostId ? clientForHost(hostId) : apiClient;
	const path = { agent: agentId };
	const catalogScope = modelCatalogScope(scope);
	const result =
		mode === "cached"
			? await client.GET("/api/v1/agents/{agent}/models", {
					params: { path, query: { projectId: catalogScope || undefined, ...(catalogOnly ? { catalogOnly: true } : {}) } },
				})
			: await client.POST("/api/v1/agents/{agent}/models/refresh", {
					params: {
						path,
						query: { projectId: catalogScope || undefined, revalidate: mode === "revalidate" || undefined, ...(catalogOnly ? { catalogOnly: true } : {}) },
					},
				});
	signal?.throwIfAborted();
	if (result.error) throw new Error(apiErrorMessage(result.error));
	const catalog = result.data as AgentModelCatalog;
	const normalized = agentId === "claude-code" ? { ...catalog, models: foldClaudeAliasDefault(catalog.models) } : catalog;
	if (mode !== "cached" || catalogOnly) {
		const snapshot = catalogOnly ? withCachedModelUsage(normalized, readCachedAgentModels(agentId, catalogScope, hostId)) : normalized;
		cacheAgentModels(snapshot, agentId, catalogScope, hostId);
	}
	return normalized;
}

export function agentModelsQueryOptions(agentId: string, scope: string, hostId?: string, catalogOnly = false) {
	const catalogScope = modelCatalogScope(scope);
	const key = agentModelsQueryKey(agentId, catalogScope, hostId);
	return queryOptions<AgentModelCatalog>({
		// Both read the shared daemon catalog; only task responses add recency.
		queryKey: catalogOnly ? [...key, "catalog"] as const : key,
		initialData: () => readCachedAgentModels(agentId, catalogScope, hostId),
		initialDataUpdatedAt: 0,
		queryFn: async ({ client, signal }): Promise<AgentModelCatalog> => {
			if (catalogOnly) return requestAgentModels(agentId, catalogScope, "cached", hostId, true, signal);
			const catalogKey = [...key, "catalog"] as const;
			if (!client.getQueryData<AgentModelCatalog>(key)) {
				// Publish the DB catalog before the task response scans session history
				// for recency. Reuse Settings' read only for this exact host and scope.
				const cached = client.getQueryData<AgentModelCatalog>(catalogKey);
				if (cached) client.setQueryData(key, cached);
			}
			// Read the DB even when the UI has a saved snapshot. Deduplicate reads
			// shared with Settings, but never wait for recency to publish the result.
			const beforeDBRead = client.getQueryData<AgentModelCatalog>(key);
			const catalog = await client.fetchQuery({
				queryKey: catalogKey,
				queryFn: ({ signal: catalogSignal }) => requestAgentModels(agentId, catalogScope, "cached", hostId, true, catalogSignal),
				staleTime: 0,
			});
			signal.throwIfAborted();
			// A manual refresh may finish while this older DB read is pending.
			// Preserve that update in both memory and the saved snapshot.
			const afterDBRead = client.getQueryData<AgentModelCatalog>(key);
			if (afterDBRead && afterDBRead !== beforeDBRead) {
				cacheAgentModels(afterDBRead, agentId, catalogScope, hostId);
				return afterDBRead;
			}
			const withUsage = withCachedModelUsage(catalog, client.getQueryData<AgentModelCatalog>(key));
			cacheAgentModels(withUsage, agentId, catalogScope, hostId);
			const published = client.setQueryData(key, withUsage);
			const enriched = await requestAgentModels(agentId, catalogScope, "cached", hostId, false, signal);
			// A background refresh may have published newer models while recency
			// was being read. Keep that catalog rather than restoring the old list.
			const current = client.getQueryData<AgentModelCatalog>(key);
			const latest = current && current !== published ? current : enriched;
			cacheAgentModels(latest, agentId, catalogScope, hostId);
			return latest;
		},
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
export async function invalidateAgentModelCatalogs(queryClient: QueryClient, agentId: string, hostId?: string): Promise<unknown> {
	const queryKey = hostId ? ["agent-models", hostId, agentId] : agentModelsQueryPrefix(agentId);
	// Cancel inactive catalog reads too, so the next task read cannot dedupe
	// onto a request made before the login changed.
	await queryClient.cancelQueries({ queryKey });
	clearCachedAgentModels(agentId, hostId);
	return Promise.all([
		queryClient.invalidateQueries({ queryKey }),
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

export function refreshAgentModels(agentId: string, scope: string, hostId?: string, catalogOnly = false) {
	return requestAgentModels(agentId, scope, "refresh", hostId, catalogOnly);
}

export function revalidateAgentModels(agentId: string, scope: string, hostId?: string) {
	return requestAgentModels(agentId, scope, "revalidate", hostId);
}
