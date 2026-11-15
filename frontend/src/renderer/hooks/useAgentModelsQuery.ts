import { apiErrorException } from "../lib/api-error-exception";
import { queryOptions, type QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { foldClaudeAliasDefault } from "../lib/agent-model-choices";
import { cacheAgentModels, clearCachedAgentModels, readCachedAgentModels, withCachedModelUsage } from "../lib/agent-model-cache";
import { modelCatalogScope } from "../lib/cloud-agents";
import { clientForHost } from "../lib/host-clients";
import { LOCAL_HOST } from "../lib/hosts";

export type AgentModelCatalog = components["schemas"]["AgentModelsResponse"];

const MODEL_CATALOG_VALIDATION_INTERVAL_MS = 10 * 60 * 1_000;
const MODEL_CATALOG_REFRESH_POLL_MS = 3_000;

export function isRefreshingCatalog(catalog: Pick<AgentModelCatalog, "refreshState"> | undefined): boolean {
	return catalog?.refreshState === "refreshing" || catalog?.refreshState === "queued";
}

export const agentModelsQueryPrefix = (agentId: string) =>
	["agent-models", agentId] as const;

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
	if (result.error) throw apiErrorException(result.error);
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
		queryKey: catalogOnly ? [...key, "catalog"] as const : key,
		initialData: () => readCachedAgentModels(agentId, catalogScope, hostId),
		initialDataUpdatedAt: 0,
		queryFn: async ({ client, signal }): Promise<AgentModelCatalog> => {
			if (catalogOnly) return requestAgentModels(agentId, catalogScope, "cached", hostId, true, signal);
			const catalogKey = [...key, "catalog"] as const;
			if (!client.getQueryData<AgentModelCatalog>(key)) {
				const cached = client.getQueryData<AgentModelCatalog>(catalogKey);
				if (cached) client.setQueryData(key, cached);
			}
			const beforeDBRead = client.getQueryData<AgentModelCatalog>(key);
			const catalog = await client.fetchQuery({
				queryKey: catalogKey,
				queryFn: ({ signal: catalogSignal }) => requestAgentModels(agentId, catalogScope, "cached", hostId, true, catalogSignal),
				staleTime: 0,
			});
			signal.throwIfAborted();
			const afterDBRead = client.getQueryData<AgentModelCatalog>(key);
			if (afterDBRead && afterDBRead !== beforeDBRead) {
				cacheAgentModels(afterDBRead, agentId, catalogScope, hostId);
				return afterDBRead;
			}
			const withUsage = withCachedModelUsage(catalog, client.getQueryData<AgentModelCatalog>(key));
			cacheAgentModels(withUsage, agentId, catalogScope, hostId);
			const published = client.setQueryData(key, withUsage);
			const enriched = await requestAgentModels(agentId, catalogScope, "cached", hostId, false, signal);
			const current = client.getQueryData<AgentModelCatalog>(key);
			const latest = current && current !== published ? current : enriched;
			cacheAgentModels(latest, agentId, catalogScope, hostId);
			return latest;
		},
		enabled: agentId !== "",
		staleTime: (query) => (query.state.data?.warning ? 0 : MODEL_CATALOG_VALIDATION_INTERVAL_MS),
		refetchInterval: (query) => (isRefreshingCatalog(query.state.data) ? MODEL_CATALOG_REFRESH_POLL_MS : false),
	});
}

export type ModelCatalogAuthIssue = "required" | "expired";

const AUTH_WARNING_PATTERN = /not signed in|sign in to load|rejected the credential|access token has expired|re-?authenticate|unauthori[sz]ed|\b401\b/i;

export function modelCatalogAuthIssue(catalog: Pick<AgentModelCatalog, "warning" | "warningCode"> | undefined): ModelCatalogAuthIssue | undefined {
	if (catalog?.warningCode === "auth_expired") return "expired";
	if (catalog?.warningCode === "auth_required") return "required";
	if (!catalog?.warningCode && catalog?.warning && AUTH_WARNING_PATTERN.test(catalog.warning)) return "required";
	return undefined;
}

export async function invalidateAgentModelCatalogs(queryClient: QueryClient, agentId: string, hostId?: string): Promise<unknown> {
	const queryKey = hostId ? ["agent-models", hostId, agentId] : agentModelsQueryPrefix(agentId);
	await queryClient.cancelQueries({ queryKey });
	clearCachedAgentModels(agentId, hostId);
	return Promise.all([
		queryClient.invalidateQueries({ queryKey }),
		queryClient.invalidateQueries({
			predicate: ({ queryKey: [root, first, second] }) => {
				if (root !== "agent-model-revalidation") return false;
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
