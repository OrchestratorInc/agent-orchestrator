import { useEffect, useCallback } from "react";
import { queryOptions, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { foldClaudeAliasDefault } from "../lib/agent-model-choices";
import { clientForHost } from "../lib/host-clients";

export type AgentModelCatalog = components["schemas"]["AgentModelsResponse"];

const MODEL_CATALOG_VALIDATION_INTERVAL_MS = 10 * 60 * 1_000;

export const agentModelsQueryPrefix = (agentId: string) => ["agent-models", agentId] as const;

export const agentModelsQueryKey = (agentId: string, projectId: string, hostId?: string) =>
	hostId ? (["agent-models", hostId, agentId, projectId] as const) : ([...agentModelsQueryPrefix(agentId), projectId] as const);

async function requestAgentModels(
	agentId: string,
	projectId: string,
	mode: "cached" | "refresh" | "revalidate",
	hostId?: string,
	signal?: AbortSignal,
): Promise<AgentModelCatalog> {
	const client = hostId ? clientForHost(hostId) : apiClient;
	const path = { agent: agentId };
	const result =
		mode === "cached"
			? await client.GET("/api/v1/agents/{agent}/models", {
					signal,
					params: { path, query: { projectId: projectId || undefined } },
				})
			: await client.POST("/api/v1/agents/{agent}/models/refresh", {
					signal,
					params: {
						path,
						query: {
							projectId: projectId || undefined,
							revalidate: mode === "revalidate" || undefined,
						},
					},
				});
	if (result.error) throw new Error(apiErrorMessage(result.error));
	const catalog = result.data as AgentModelCatalog;
	return agentId === "claude-code" ? { ...catalog, models: foldClaudeAliasDefault(catalog.models) } : catalog;
}

export function agentModelsQueryOptions(agentId: string, projectId: string, hostId?: string) {
	return queryOptions({
		queryKey: agentModelsQueryKey(agentId, projectId, hostId),
		queryFn: ({ signal }) => requestAgentModels(agentId, projectId, "cached", hostId, signal),
		gcTime: Number.POSITIVE_INFINITY,
		enabled: agentId !== "",
		staleTime: MODEL_CATALOG_VALIDATION_INTERVAL_MS,
	});
}

// All consumers share one validation job for the exact catalog revision and scope.
export function useAgentModels(agentId: string, projectId: string, hostId?: string) {
	const queryClient = useQueryClient();
	const query = useQuery(agentModelsQueryOptions(agentId, projectId, hostId));
	const validation = useQuery({
		queryKey: ["agent-model-revalidation", ...agentModelsQueryKey(agentId, projectId, hostId), query.data?.validatedAt ?? ""],
		queryFn: ({ signal }) => requestAgentModels(agentId, projectId, "revalidate", hostId, signal),
		enabled: agentId !== "" && query.data?.refreshRecommended === true,
		staleTime: Number.POSITIVE_INFINITY,
		retry: false,
	});
	useEffect(() => {
		const validated = validation.data;
		if (validated)
			queryClient.setQueryData<AgentModelCatalog>(agentModelsQueryKey(agentId, projectId, hostId), (current) => {
				const timestamp = (catalog: AgentModelCatalog) => Date.parse(catalog.validatedAt || catalog.fetchedAt || "") || 0;
				return current && timestamp(current) > timestamp(validated) ? current : validated;
			});
	}, [agentId, projectId, hostId, queryClient, validation.data]);
	const refresh = useCallback(async () => {
		await queryClient.cancelQueries({ queryKey: agentModelsQueryKey(agentId, projectId, hostId), exact: true });
		const catalog = await queryClient.fetchQuery({
			queryKey: ["agent-model-refresh", ...agentModelsQueryKey(agentId, projectId, hostId)],
			queryFn: ({ signal }) => requestAgentModels(agentId, projectId, "refresh", hostId, signal),
			staleTime: 0,
			retry: false,
		});
		queryClient.setQueryData(agentModelsQueryKey(agentId, projectId, hostId), catalog);
	}, [agentId, projectId, hostId, queryClient]);
	const cause = validation.error ?? query.error;
	return {
		...query,
		refresh,
		warning: cause instanceof Error ? cause.message : query.data?.warning,
	};
}
