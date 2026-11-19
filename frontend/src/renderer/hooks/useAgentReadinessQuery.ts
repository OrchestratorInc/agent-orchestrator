import { apiErrorException } from "../lib/api-error-exception";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { AgentCheckError } from "../lib/agent-check-error";
import { defaultProjectAgents, preferredAgentNeedsChecks, type ProjectAgentDefaults } from "../lib/agent-select-options";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";

export type AgentReadiness = components["schemas"]["AgentReadinessResponse"];
export type AgentReadinessSnapshot = components["schemas"]["AgentReadinessSnapshot"];
export type AgentReadinessPurpose = components["schemas"]["EnsureAgentReadinessRequest"]["purpose"];

export const agentReadinessQueryKey = ["agent-readiness"] as const;
export const agentReadinessQueryKeyForHost = (hostId?: string) => hostId ? ["agent-readiness", hostId] as const : agentReadinessQueryKey;

async function fetchAgentReadiness(hostId?: string): Promise<AgentReadiness> {
	const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).GET("/api/v1/agents/readiness");
	if (error) throw apiErrorException(error);
	return data as AgentReadiness;
}

export async function ensureAgentReadiness(
	agentIds: string[] = [],
	purpose: AgentReadinessPurpose = "display",
	hostId?: string,
): Promise<AgentReadiness> {
	const { data, error, response } = await (hostId ? clientForHost(hostId) : apiClient).POST("/api/v1/agents/readiness/ensure", {
		body: { agentIds, purpose },
	});
	if (error || !data) throw new AgentCheckError("Readiness refresh", error, response?.status);
	return data as AgentReadiness;
}

export function mergeAgentReadiness(
	current: AgentReadiness | undefined,
	next: AgentReadiness,
): AgentReadiness {
	if (!current || next.agents.length === 0) return next;
	const byID = new Map(current.agents.map((agent) => [agent.id, agent]));
	for (const agent of next.agents) byID.set(agent.id, agent);
	return { agents: [...byID.values()].sort((a, b) => a.id.localeCompare(b.id)) };
}

export function cacheAgentReadiness(queryClient: QueryClient, next: AgentReadiness, hostId?: string): void {
	queryClient.setQueryData<AgentReadiness>(agentReadinessQueryKeyForHost(hostId), (current) =>
		mergeAgentReadiness(current, next),
	);
}

export const agentReadinessQueryOptions = {
	queryKey: agentReadinessQueryKey,
	queryFn: fetchAgentReadiness,
	retry: 1,
	// Freshness belongs to the daemon coordinator. React Query only retains the
	// latest display copy and must never decide whether native work is required.
	staleTime: Number.POSITIVE_INFINITY,
	// Keep the last daemon snapshot for the lifetime of the renderer so returning
	// to Harness settings can paint immediately while its silent refresh runs.
	gcTime: Number.POSITIVE_INFINITY,
};

/**
 * The agents a new project starts on, ranked from the readiness snapshot the
 * renderer already holds (one fetch when it holds none). When that snapshot
 * has not confirmed the preferred agent yet, it waits for the daemon's checks
 * (joining any already running) before ranking. A failed fetch ranks nothing,
 * so the project falls back to the daemon's default agent.
 */
export async function loadProjectAgentDefaults(queryClient: QueryClient, hostId?: string): Promise<ProjectAgentDefaults> {
	let agents: AgentReadinessSnapshot[] = [];
	try {
		agents = (await queryClient.ensureQueryData({
			...agentReadinessQueryOptions,
			queryKey: agentReadinessQueryKeyForHost(hostId),
			queryFn: () => fetchAgentReadiness(hostId),
		})).agents;
	} catch {
		// Keep the fallback below.
	}
	if (preferredAgentNeedsChecks(agents)) {
		try {
			const checked = await ensureAgentReadiness([], "display", hostId);
			cacheAgentReadiness(queryClient, checked, hostId);
			agents = mergeAgentReadiness({ agents }, checked).agents;
		} catch {
			// Rank what we have.
		}
	}
	return defaultProjectAgents(agents);
}

export function useAgentReadinessQuery(enabled = true, hostId?: string) {
	return useQuery({
		...agentReadinessQueryOptions,
		queryKey: agentReadinessQueryKeyForHost(hostId),
		queryFn: () => fetchAgentReadiness(hostId),
		enabled,
	});
}

export function useEnsureAgentReadiness({
	agentIds = [],
	enabled = true,
	purpose = "display",
	hostId,
	retryOnError = false,
}: {
	agentIds?: string[];
	enabled?: boolean;
	purpose?: AgentReadinessPurpose;
	hostId?: string;
	retryOnError?: boolean;
} = {}): boolean {
	const queryClient = useQueryClient();
	const agentIDsKey = [...new Set(agentIds.filter(Boolean))].sort().join("\u0000");
	const normalizedIDs = useMemo(
		() => (agentIDsKey === "" ? [] : agentIDsKey.split("\u0000")),
		[agentIDsKey],
	);
	// A completion belongs to this exact request, never a previous host or
	// enabled period. Cached installation data alone cannot open this gate.
	const request = useMemo(() => ({}), [enabled, hostId, normalizedIDs, purpose, queryClient, retryOnError]);
	const [completedRequest, setCompletedRequest] = useState<object | null>(null);

	useEffect(() => {
		if (!enabled) return;
		let active = true;
		let retryTimer: ReturnType<typeof setTimeout> | undefined;
		setCompletedRequest(null);
		const ensure = () => void ensureAgentReadiness(normalizedIDs, purpose, hostId)
			.then((next) => {
				if (!active) return;
				cacheAgentReadiness(queryClient, next, hostId);
				setCompletedRequest(request);
			})
			.catch(() => {
				// Keep cached display data, but never report a failed check as done.
				if (active && retryOnError) retryTimer = setTimeout(ensure, 30_000);
			});
		ensure();
		return () => {
			active = false;
			clearTimeout(retryTimer);
		};
	}, [enabled, hostId, normalizedIDs, purpose, queryClient, request, retryOnError]);
	return enabled && completedRequest === request;
}

/**
 * Keeps agent checks fresh while a create-project flow is open and returns the
 * loader for the agents a new project starts on.
 */
export function useProjectAgentDefaults(enabled: boolean, hostId?: string): () => Promise<ProjectAgentDefaults> {
	const queryClient = useQueryClient();
	useAgentReadinessQuery(enabled, hostId);
	useEnsureAgentReadiness({ enabled, hostId });
	return useCallback(() => loadProjectAgentDefaults(queryClient, hostId), [queryClient, hostId]);
}
