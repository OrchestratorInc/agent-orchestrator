import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { clientForSessionHost } from "../lib/host-clients";
import { LOCAL_HOST } from "../lib/hosts";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import type { WorkspaceSession } from "../types/workspace";
import { useRemoteWorkspaces, useWorkspaceQuery } from "./useWorkspaceQuery";

/**
 * Where a session moved to another computer, if it did, and how to open it
 * there. open is absent when that computer is not connected.
 */
export function useSessionMovedTo(session: WorkspaceSession | undefined) {
	const navigate = useNavigate();
	const local = useWorkspaceQuery().data ?? [];
	const remote = useRemoteWorkspaces().data ?? [];
	const movedTo = useQuery({
		queryKey: ["session-moved-to", session?.hostId ?? LOCAL_HOST, session?.id],
		enabled: Boolean(session?.id) && !session?.cloud,
		staleTime: Infinity,
		queryFn: async () => {
			const res = await clientForSessionHost(session?.hostId).GET("/api/v1/sessions/{sessionId}/transfer", {
				params: { path: { sessionId: session?.id ?? "" } },
			});
			return res.data?.movedTo ?? null;
		},
	}).data;
	if (!movedTo) return undefined;
	const host = movedTo.hostId || LOCAL_HOST;
	const workspaces = host === LOCAL_HOST ? local : remote.filter((workspace) => workspace.hostId === host);
	const project = workspaces.find((workspace) => workspace.sessions.some((each) => each.id === movedTo.sessionId));
	const open = project ? () => void navigate(sessionNavigateTarget(project.id, movedTo.sessionId, host)) : undefined;
	return { ...movedTo, open };
}
