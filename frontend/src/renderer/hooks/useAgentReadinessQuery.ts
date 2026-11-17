import { apiErrorException } from "../lib/api-error-exception";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { AgentCheckError } from "../lib/agent-check-error";
import { defaultProjectAgents, preferredAgentNeedsChecks, isPendingAgent, type ProjectAgentDefaults } from "../lib/agent-select-options";
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

/** An open local standalone view follows daemon recovery without forcing probes
 * on every poll. The daemon continues its own work after this view leaves. */
export function useStandaloneAgentReadiness(agentId: string, pending: boolean, enabled: boolean, accountId?: string) {
	const queryClient = useQueryClient();
	const [daemonGeneration, setDaemonGeneration] = useState(0);
	const scope = useMemo(() => ({}), [agentId, accountId, enabled, daemonGeneration, queryClient]);
	const [retryRequest, setRetryRequest] = useState<{ scope: object }>();
	const [settledRetry, setSettledRetry] = useState<typeof retryRequest>();
	const consumedRetry = useRef<typeof retryRequest>(undefined);
	const retrying = Boolean(enabled && retryRequest?.scope === scope && settledRetry !== retryRequest);
	// A cached rejection may end `pending` before a fresh probe finishes. The
	// explicit retry owns its lifetime until its normalized result is read.
	const recovering = enabled && (pending || retrying);
	const request = useMemo(() => ({}), [scope, recovering, retryRequest]);
	const currentRequest = useRef(request);
	currentRequest.current = request;
	const [expired, setExpired] = useState<{ request: object; scope: object; retry?: typeof retryRequest; snapshot?: AgentReadiness }>();
	const retryLocked = useRef(false);
	useEffect(() => {
		if (!recovering) return;
		let active = true;
		let poll: ReturnType<typeof setTimeout> | undefined;
		const isCurrent = () => active && currentRequest.current === request;
		const explicitRetry = retryRequest?.scope === scope && consumedRetry.current !== retryRequest;
		retryLocked.current = explicitRetry;
		const timeout = setTimeout(() => {
			if (!isCurrent()) return;
			active = false;
			clearTimeout(poll);
			retryLocked.current = false;
			if (explicitRetry) setSettledRetry(retryRequest);
			setExpired({ request, scope, retry: explicitRetry ? retryRequest : undefined, snapshot: queryClient.getQueryData<AgentReadiness>(agentReadinessQueryKey) });
		}, 60_000);
		// The supervisor removes the local snapshot when daemon identity changes,
		// including a replacement on the same port. Discard the old request's
		// completions and follow the replacement's startup instead.
		const unsubscribe = queryClient.getQueryCache().subscribe((event) => {
			if (event.type !== "removed" || event.query.queryKey.length !== 1 || event.query.queryKey[0] !== agentReadinessQueryKey[0]) return;
			active = false;
			clearTimeout(timeout);
			clearTimeout(poll);
			setDaemonGeneration((generation) => generation + 1);
		});
		const read = async () => {
			try {
				const next = await fetchAgentReadiness();
				if (isCurrent()) {
					cacheAgentReadiness(queryClient, next);
					if (explicitRetry) {
						retryLocked.current = false;
						setSettledRetry(retryRequest);
					}
				}
			} catch { /* A transient transport error can recover on the next cached read. */ }
			if (isCurrent()) poll = setTimeout(() => void read(), 2_000);
		};
		const start = async () => {
			try {
				if (explicitRetry) {
					consumedRetry.current = retryRequest;
					// Fresh profiles may have no selected agent until discovery settles.
					// Probe the installed pending candidates without changing the pick.
					const targets = agentId ? [agentId] : (await fetchAgentReadiness()).agents
						.filter((agent) => agent.installation.state === "installed" && (isPendingAgent(agent) || agent.authentication.state === "unauthorized"))
						.map((agent) => agent.id);
					for (const target of targets) {
						if (!isCurrent()) return;
						try {
							const { error, response } = await apiClient.POST("/api/v1/agents/{agent}/probe", { params: { path: { agent: target } } });
							if (error) throw new AgentCheckError("Readiness refresh", error, response?.status);
						} catch { /* Still check other candidates after an individual failure. */ }
					}
				} else {
					const next = await ensureAgentReadiness(agentId ? [agentId] : [], "display");
					if (isCurrent()) cacheAgentReadiness(queryClient, next);
				}
			} catch { /* Cached polling still observes daemon-owned recovery. */ }
			finally {
				if (isCurrent()) {
					// Do not let an earlier cached GET cancel the probe. Its final read
					// also normalizes the legacy probe response before enabling Send.
					if (explicitRetry) await read();
					else retryLocked.current = false;
				}
			}
		};
		void start();
		if (!explicitRetry) void read();
		return () => {
			active = false;
			unsubscribe();
			clearTimeout(timeout);
			clearTimeout(poll);
			retryLocked.current = false;
		};
	}, [agentId, retryRequest, recovering, queryClient, request, scope]);
	const retry = useCallback(() => {
		if (!enabled || retryLocked.current) return;
		retryLocked.current = true;
		setRetryRequest({ scope });
	}, [enabled, scope]);
	// An older cached rejection can end `pending` while a retry is in flight.
	// Keep its expired Retry offer until the target changes, the user retries,
	// or a relevant observation replaces the one held at the deadline. Usage
	// updates and another agent's checks cannot settle this expired attempt.
	const cached = queryClient.getQueryData<AgentReadiness>(agentReadinessQueryKey);
	const expiredRetry = expired?.retry !== undefined && expired.retry === retryRequest && expired.scope === scope
		&& expired.snapshot !== undefined && cached !== undefined
		&& expired.snapshot.agents.filter((agent) => agentId ? agent.id === agentId : agent.installation.state === "installed").every((agent) => {
			const current = cached.agents.find((candidate) => candidate.id === agent.id);
			return current?.authentication === agent.authentication && current?.installation === agent.installation;
		});
	return {
		timedOut: enabled && (Boolean(pending && expired?.request === request) || expiredRetry),
		retrying: retrying && expired?.request !== request,
		retry,
	};
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
