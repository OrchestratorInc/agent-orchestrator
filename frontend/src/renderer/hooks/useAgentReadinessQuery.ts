import { apiErrorException } from "../lib/api-error-exception";
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { AgentCheckError } from "../lib/agent-check-error";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import { isLocal, type HostId } from "../lib/hosts";

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

export function agentReadinessQueryOptionsForHost(hostId?: string) {
	return {
		...agentReadinessQueryOptions,
		queryKey: agentReadinessQueryKeyForHost(hostId),
		queryFn: () => fetchAgentReadiness(hostId),
	};
}

export function useAgentReadinessQuery(enabled = true, hostId?: string) {
	return useQuery({ ...agentReadinessQueryOptionsForHost(hostId), enabled });
}

const NO_HOSTS: ReadonlySet<HostId> = new Set();

/**
 * useEnsureAgentReadiness for several hosts at once: the hosts whose display
 * check has finished. Each host keeps its own check, so a host connecting or
 * leaving never reopens the gate of another.
 */
export function useEnsuredReadinessHosts(hostIds: readonly HostId[], { enabled = true, retryOnError = false }: { enabled?: boolean; retryOnError?: boolean } = {}): ReadonlySet<HostId> {
	const queryClient = useQueryClient();
	const hostsKey = [...new Set(hostIds)].sort().join("\u0000");
	const running = useRef(new Map<HostId, () => void>());
	const [ensured, setEnsured] = useState<ReadonlySet<HostId>>(NO_HOSTS);
	useEffect(() => {
		const wanted = new Set(enabled && hostsKey !== "" ? hostsKey.split("\u0000") : []);
		const drop = (hostId: HostId) => setEnsured((current) => {
			if (!current.has(hostId)) return current;
			const next = new Set(current);
			next.delete(hostId);
			return next;
		});
		for (const [hostId, stop] of running.current) {
			if (wanted.has(hostId)) continue;
			stop();
			running.current.delete(hostId);
			drop(hostId);
		}
		for (const hostId of wanted) {
			if (running.current.has(hostId)) continue;
			const target = isLocal(hostId) ? undefined : hostId;
			let active = true;
			let retryTimer: ReturnType<typeof setTimeout> | undefined;
			const ensure = () => void ensureAgentReadiness([], "display", target)
				.then((next) => {
					if (!active) return;
					cacheAgentReadiness(queryClient, next, target);
					setEnsured((current) => current.has(hostId) ? current : new Set([...current, hostId]));
				})
				.catch(() => {
					// Keep cached display data, but never report a failed check as done.
					if (active && retryOnError) retryTimer = setTimeout(ensure, 30_000);
				});
			running.current.set(hostId, () => {
				active = false;
				clearTimeout(retryTimer);
			});
			ensure();
		}
	}, [enabled, hostsKey, queryClient, retryOnError]);
	useEffect(() => () => {
		for (const stop of running.current.values()) stop();
		running.current.clear();
	}, []);
	return enabled ? ensured : NO_HOSTS;
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
