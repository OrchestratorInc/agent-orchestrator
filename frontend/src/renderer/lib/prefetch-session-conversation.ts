import type { QueryClient } from "@tanstack/react-query";
import { conversationQueryOptions } from "../hooks/useConversation";
import { cloudProjectsQueryKey, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import type { WorkspaceSummary } from "../types/workspace";

/** Unknown (including ended) sessions start reading before their detail lookup. */
export function prefetchLocalSessionConversation(client: QueryClient, sessionId: string, projectId?: string) {
	const workspaces = client.getQueryData<WorkspaceSummary[]>(workspaceQueryKey) ?? [];
	const project = projectId ? workspaces.find((workspace) => workspace.id === projectId && !workspace.hostId) : undefined;
	if (project?.kind === "cloud") return;
	if (projectId && !project && client.getQueriesData<{ id: string }[]>({ queryKey: cloudProjectsQueryKey })
		.some(([, projects]) => projects?.some((candidate) => candidate.id === projectId))) return;
	const session = workspaces.filter((workspace) => !workspace.hostId && workspace.kind !== "cloud")
		.flatMap((workspace) => workspace.sessions).find((row) => row.id === sessionId);
	if (session?.mode === "tui") return;
	void client.prefetchInfiniteQuery(conversationQueryOptions(sessionId));
}
