import { useQuery, type QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import { LOCAL_HOST } from "../lib/hosts";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSummary } from "../types/workspace";

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

// Warms every board's usage before it is opened, so a board's first frame
// already has its cost badges. One host-wide request per host is split into
// each project's board cache using the sessions the workspace list already
// knows. Never awaited by navigation: usage is supplementary. Boards that
// already have data are left alone (event invalidation and the mounted query
// keep them current), and a failed warm-up is not retried here; the board's
// own query fetches normally when it opens.
export async function warmSessionUsageSummaries(
	queryClient: QueryClient,
	workspaces: readonly WorkspaceSummary[],
): Promise<void> {
	const coldByHost = new Map<string | undefined, WorkspaceSummary[]>();
	for (const workspace of workspaces) {
		if (workspace.id === STANDALONE_WORKSPACE_ID) continue;
		if (queryClient.getQueryData(sessionUsageQueryKey(workspace.id, workspace.hostId)) !== undefined) continue;
		coldByHost.set(workspace.hostId, [...(coldByHost.get(workspace.hostId) ?? []), workspace]);
	}
	await Promise.all([...coldByHost].map(async ([hostId, cold]) => {
		const hostOptions = sessionUsageQueryOptions(undefined, hostId);
		let summaries: SessionUsageSummary[];
		try {
			summaries = await queryClient.fetchQuery({ ...hostOptions, retry: false });
		} catch {
			return;
		}
		const updatedAt = queryClient.getQueryState(hostOptions.queryKey)?.dataUpdatedAt;
		const bySession = new Map(summaries.map((summary) => [summary.sessionId, summary] as const));
		for (const workspace of cold) {
			const key = sessionUsageQueryKey(workspace.id, hostId);
			// A board that opened mid-warm-up has fresher data of its own.
			if (queryClient.getQueryData(key) !== undefined) continue;
			const projectSummaries = workspace.sessions.flatMap((session) => bySession.get(session.id) ?? []);
			queryClient.setQueryData(key, projectSummaries, { updatedAt });
		}
	}));
}

// Module-level so the Map keeps its identity between renders.
function summariesById(items: SessionUsageSummary[]) {
	return new Map(items.map((item) => [item.sessionId, item] as const));
}

export function useSessionUsageSummaries(projectId?: string, hostId?: string) {
	return useQuery(sessionUsageQueryOptions(projectId, hostId));
}
