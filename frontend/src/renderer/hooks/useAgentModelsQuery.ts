import { queryOptions, type QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";

export type AgentModelCatalog = components["schemas"]["AgentModelsResponse"];

export const MODEL_CATALOG_VALIDATION_INTERVAL_MS = 10 * 60 * 1_000;

export const agentModelsQueryPrefix = (agentId: string) =>
	["agent-models", agentId] as const;

export const agentModelsQueryKey = (agentId: string, projectId: string, hostId?: string) =>
	hostId ? ["agent-models", hostId, agentId, projectId] as const : [...agentModelsQueryPrefix(agentId), projectId] as const;

async function requestAgentModels(
	agentId: string,
	projectId: string,
	mode: "cached" | "refresh" | "revalidate" | "expand",
	hostId?: string,
): Promise<AgentModelCatalog> {
	const client = hostId ? clientForHost(hostId) : apiClient;
	const path = { agent: agentId };
	const result =
		mode === "cached"
			? await client.GET("/api/v1/agents/{agent}/models", {
					params: { path, query: { projectId: projectId || undefined } },
				})
			: mode === "expand"
				? await client.POST("/api/v1/agents/{agent}/models/expand", { params: { path, query: { projectId: projectId || undefined } } })
				: await client.POST("/api/v1/agents/{agent}/models/refresh", {
					params: {
						path,
						query: { projectId: projectId || undefined, revalidate: mode === "revalidate" || undefined },
					},
				});
	if (result.error) throw new Error(apiErrorMessage(result.error));
	return result.data as AgentModelCatalog;
}

export function agentModelsQueryOptions(agentId: string, projectId: string, hostId?: string) {
	return queryOptions({
		queryKey: agentModelsQueryKey(agentId, projectId, hostId),
		queryFn: () => requestAgentModels(agentId, projectId, "cached", hostId),
		enabled: agentId !== "",
		staleTime: MODEL_CATALOG_VALIDATION_INTERVAL_MS,
		gcTime: 24 * 60 * 60 * 1_000,
	});
}

export function refreshAgentModels(agentId: string, projectId: string, hostId?: string) {
	return requestAgentModels(agentId, projectId, "refresh", hostId);
}

export function revalidateAgentModels(agentId: string, projectId: string, hostId?: string) {
	return requestAgentModels(agentId, projectId, "revalidate", hostId);
}

export function agentModelsRevalidationQueryOptions(
	agentId: string,
	projectId: string,
	catalog: AgentModelCatalog | undefined,
	hostId?: string,
) {
	return queryOptions({
		queryKey: ["agent-model-revalidation", hostId ?? "", agentId, projectId, catalog?.validatedAt ?? ""] as const,
		queryFn: () => revalidateAgentModels(agentId, projectId, hostId),
		enabled: agentId !== "" && catalog?.refreshRecommended === true,
		staleTime: Number.POSITIVE_INFINITY,
		retry: false,
	});
}

export async function resetAgentModels(queryClient: QueryClient, agentId: string, hostId?: string) {
	await Promise.all([
		queryClient.resetQueries({
			queryKey: hostId ? ["agent-models", hostId, agentId] : agentModelsQueryPrefix(agentId),
			predicate: (query) => query.queryKey.length === (hostId ? 4 : 3),
		}),
		queryClient.resetQueries({ queryKey: ["agent-model-revalidation", hostId ?? "", agentId] }),
	]);
}

export async function expandAgentModels(queryClient: QueryClient, agentId: string, projectId: string, hostId?: string) {
	await requestAgentModels(agentId, projectId, "expand", hostId);
	await queryClient.invalidateQueries({ queryKey: agentModelsQueryKey(agentId, projectId, hostId), exact: true });
}
