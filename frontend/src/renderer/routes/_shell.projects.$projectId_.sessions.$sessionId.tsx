import type { QueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { loadSessionView } from "../lib/session-view-chunk";
import { RouteErrorCapture } from "../components/RouteErrorCapture";
import { conversationQueryKey, conversationQueryOptions } from "../hooks/useConversation";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { hasTrustedApiBaseUrl } from "../lib/api-client";
import type { WorkspaceSummary } from "../types/workspace";

function prefetchChat(queryClient: QueryClient, sessionId: string) {
	if (!hasTrustedApiBaseUrl() || queryClient.getQueryData(conversationQueryKey(sessionId))) return;
	const session = queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKey)
		?.flatMap((workspace) => workspace.sessions)
		.find((candidate) => candidate.id === sessionId);
	if (!session || session.mode !== "chat" || session.cloud || session.needsResume) return;
	void queryClient.prefetchInfiniteQuery(conversationQueryOptions(sessionId));
}

// SessionView renders from LocalSessionHost in the shell so switching between
// project sessions and scratchpads reuses one mounted instance. The route only
// loads its chunk: with no pending screen the previous page stays up until the
// session can paint, and the host owns the error panel if the chunk fails.
export const Route = createFileRoute("/_shell/projects/$projectId_/sessions/$sessionId")({
	loader: async ({ context, params, preload }) => {
		if (!preload) prefetchChat(context.queryClient, params.sessionId);
		await loadSessionView();
	},
	pendingMs: Number.POSITIVE_INFINITY,
	errorComponent: RouteErrorCapture,
});
