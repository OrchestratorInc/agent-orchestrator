import { apiErrorException } from "../lib/api-error-exception";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { AgentCheckError } from "../lib/agent-check-error";
import { defaultProjectAgents, hasInstalledAgent, preferredAgentNeedsChecks, type ProjectAgentDefaults } from "../lib/agent-select-options";
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

// Bounds the readiness wait so creating or starting a project never hangs on
// a daemon that accepted the request and stopped answering.
const PROJECT_AGENT_DEFAULTS_DEADLINE_MS = 5000;

// The readiness snapshot ranked for a project's default agents: the one the
// renderer already holds, or one fetch when it holds none. When it hasn't
// confirmed the agent AO prefers most, or hasn't found any agent installed (the
// cache never expires, so one may have been installed since), it waits for the
// daemon's checks (joining any already running) first. Null when it could not
// be loaded.
async function readProjectAgentSnapshot(queryClient: QueryClient, hostId?: string): Promise<AgentReadinessSnapshot[] | null> {
	let agents: AgentReadinessSnapshot[];
	try {
		agents = (await queryClient.ensureQueryData({
			...agentReadinessQueryOptions,
			queryKey: agentReadinessQueryKeyForHost(hostId),
			queryFn: () => fetchAgentReadiness(hostId),
		})).agents;
	} catch {
		return null;
	}
	const noInstalled = !hasInstalledAgent(agents);
	if (preferredAgentNeedsChecks(agents) || noInstalled) {
		try {
			// When the daemon already checked agents and cached them as not_installed or
			// unauthorized, display ensure returns that cache for 5 min. Force a refresh
			// so an agent installed or signed in moments ago is picked up immediately.
			const hasCachedFailure = noInstalled && agents.some(
				(agent) =>
					agent.installation.state === "not_installed" ||
					(agent.installation.state === "installed" && agent.authentication.state === "unauthorized"),
			);
			if (hasCachedFailure) {
				const { error } = await (hostId ? clientForHost(hostId) : apiClient).POST("/api/v1/agents/refresh");
				if (!error) {
					const refreshed = await fetchAgentReadiness(hostId);
					cacheAgentReadiness(queryClient, refreshed, hostId);
					agents = refreshed.agents;
				}
			} else {
				const checked = await ensureAgentReadiness([], "display", hostId);
				cacheAgentReadiness(queryClient, checked, hostId);
				agents = mergeAgentReadiness({ agents }, checked).agents;
			}
		} catch {
			// Rank what we have.
		}
	}
	return agents;
}

// How long to wait for the daemon's current snapshot once the checks have
// outlasted PROJECT_AGENT_DEFAULTS_DEADLINE_MS.
const LATEST_AGENT_SNAPSHOT_MS = 2000;

function withinMs<T>(work: Promise<T>, ms: number): Promise<T | null> {
	let timer: ReturnType<typeof setTimeout> | undefined;
	const deadline = new Promise<null>((resolve) => {
		timer = setTimeout(() => resolve(null), ms);
	});
	return Promise.race([work, deadline]).finally(() => clearTimeout(timer));
}

async function readProjectAgentSnapshotWithDeadline(queryClient: QueryClient, hostId?: string): Promise<AgentReadinessSnapshot[] | null> {
	const checked = await withinMs(readProjectAgentSnapshot(queryClient, hostId), PROJECT_AGENT_DEFAULTS_DEADLINE_MS);
	if (checked) return checked;
	// The checks outlasted the limit, or the first read failed. The daemon keeps
	// checking and records each agent as it finishes, so rank what it knows now
	// rather than nothing (which would fall back to Claude Code even on a machine
	// that only has Codex).
	const latest = await withinMs(fetchAgentReadiness(hostId).then((readiness) => readiness.agents, () => null), LATEST_AGENT_SNAPSHOT_MS);
	return latest ?? queryClient.getQueryData<AgentReadiness>(agentReadinessQueryKeyForHost(hostId))?.agents ?? null;
}

/**
 * The agents a new project starts on. When readiness can't be loaded in time
 * it ranks nothing, so the project falls back to AO's default agent.
 */
export async function loadProjectAgentDefaults(queryClient: QueryClient, hostId?: string): Promise<ProjectAgentDefaults> {
	return defaultProjectAgents((await readProjectAgentSnapshotWithDeadline(queryClient, hostId)) ?? []);
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

/**
 * The agents a return to the app re-checks: every agent with sessions, every
 * agent not confirmed installed (the presence check runs no process), and every
 * installed agent whose last sign-in check did not pass, since the user may have
 * just fixed it in a terminal. An installed agent that is unused and signed in,
 * or never checked, waits for a picker or a launch to ask, so focus never sets
 * off a check of every agent on the machine. The daemon's display TTL decides
 * which of these actually run.
 */
// A check that did not finish keeps the last known state (authorized, say) but
// records why, so focus still re-checks it.
const AUTH_CHECK_FAILURE_CODES = new Set(["auth_check_inconclusive", "auth_check_timeout", "auth_check_failed"]);

export function focusReadinessIds(agents: AgentReadinessSnapshot[]): string[] {
	return agents
		.filter((agent) =>
			agent.usageCount > 0 ||
			agent.installation.state !== "installed" ||
			((agent.authentication.checkedAt != null || agent.authentication.attemptedAt != null) &&
				(agent.authentication.state === "unauthorized" ||
					agent.authentication.state === "unknown" ||
					AUTH_CHECK_FAILURE_CODES.has(agent.authentication.reasonCode))),
		)
		.map((agent) => agent.id);
}

/**
 * A fresh readiness snapshot (fetching it runs no checks), so the agents a
 * return to the app re-checks reflect sessions started since the cache filled.
 */
export function loadAgentReadinessSnapshot(queryClient: QueryClient, hostId?: string): Promise<AgentReadiness> {
	return queryClient.fetchQuery({
		...agentReadinessQueryOptions,
		staleTime: 0,
		queryKey: agentReadinessQueryKeyForHost(hostId),
		queryFn: () => fetchAgentReadiness(hostId),
	});
}
