import { useQuery, type QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import { LOCAL_HOST } from "../lib/hosts";

export type SessionUsageSummary = components["schemas"]["CompactSessionUsageResponse"];

export const sessionUsageQueryRoot = ["session-usage"] as const;
export const sessionUsageQueryKey = (projectId?: string, hostId?: string) =>
	[...sessionUsageQueryRoot, hostId ?? LOCAL_HOST, projectId ?? "all"] as const;

export async function fetchSessionUsageSummaries(projectId?: string, hostId?: string): Promise<SessionUsageSummary[]> {
	const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).GET("/api/v1/usage/sessions", {
		params: { query: projectId ? { projectId } : {} },
	});
	if (error) throw error;
	return data?.sessions ?? [];
}

export function sessionUsageQueryOptions(projectId?: string, hostId?: string) {
	return {
		queryKey: sessionUsageQueryKey(projectId, hostId),
		queryFn: () => fetchSessionUsageSummaries(projectId, hostId),
		retry: 1,
		// Keep summaries across board visits: a collected entry would make every
		// later visit paint its cards before their cost again.
		gcTime: Number.POSITIVE_INFINITY,
		...(hostId ? { refetchInterval: 15_000 } : {}),
		select: summariesById,
	};
}

// Warms each board's usage once the shell knows its projects, so a board's
// first frame already has cost badges. Fire-and-forget: usage is supplementary
// and must never hold navigation, and a cached entry is not refetched here
// (event invalidation and the mounted query keep it current).
export function warmSessionUsageSummaries(queryClient: QueryClient, projectIds: readonly string[]): void {
	for (const projectId of projectIds) {
		void queryClient.ensureQueryData({ ...sessionUsageQueryOptions(projectId), retry: false }).catch(() => undefined);
	}
}

// Module-level so the Map keeps its identity between renders.
function summariesById(items: SessionUsageSummary[]) {
	return new Map(items.map((item) => [item.sessionId, item] as const));
}

export function useSessionUsageSummaries(projectId?: string, hostId?: string) {
	return useQuery(sessionUsageQueryOptions(projectId, hostId));
}
