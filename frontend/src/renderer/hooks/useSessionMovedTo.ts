import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { clientForSessionHost } from "../lib/host-clients";
import { LOCAL_HOST } from "../lib/hosts";
import { cancelMove } from "../lib/move-session";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { useRemoteWorkspaces, useWorkspaceQuery } from "./useWorkspaceQuery";

const transferStateKey = (session: WorkspaceSession | undefined) =>
	["session-moved-to", session?.hostId ?? LOCAL_HOST, session?.id, Boolean(session?.isTerminated)] as const;

/** The session's finished or unfinished move, read only for sessions that are stopped. */
function useTransferState(session: WorkspaceSession | undefined) {
	return useQuery({
		queryKey: transferStateKey(session),
		enabled: Boolean(session?.id) && !session?.cloud,
		staleTime: Infinity,
		queryFn: async () => {
			const res = await clientForSessionHost(session?.hostId).GET("/api/v1/sessions/{sessionId}/transfer", {
				params: { path: { sessionId: session?.id ?? "" } },
			});
			return res.data ?? null;
		},
	}).data;
}

/**
 * Where a session moved to another computer, if it did, and how to open it
 * there. open is absent when that computer is not connected.
 */
export function useSessionMovedTo(session: WorkspaceSession | undefined) {
	const navigate = useNavigate();
	const local = useWorkspaceQuery().data ?? [];
	const remote = useRemoteWorkspaces().data ?? [];
	const movedTo = useTransferState(session)?.movedTo;
	if (!movedTo) return undefined;
	const host = movedTo.hostId || LOCAL_HOST;
	const workspaces = host === LOCAL_HOST ? local : remote.filter((workspace) => workspace.hostId === host);
	const project = workspaces.find((workspace) => workspace.sessions.some((each) => each.id === movedTo.sessionId));
	const open = project ? () => void navigate(sessionNavigateTarget(project.id, movedTo.sessionId, host)) : undefined;
	return { ...movedTo, open };
}

/**
 * A move that was prepared but not finished, which keeps the session from
 * resuming here. finish reopens the Move dialog; keepHere drops the move and
 * resumes the agent.
 */
export function usePendingMove(session: WorkspaceSession | undefined) {
	const queryClient = useQueryClient();
	const requestMoveSession = useUiStore((state) => state.requestMoveSession);
	const [keeping, setKeeping] = useState(false);
	const pending = useTransferState(session)?.pending;
	if (!pending || !session || session.isTerminated) return undefined;
	const source = { client: clientForSessionHost(session.hostId), hostId: session.hostId ?? LOCAL_HOST, sessionId: session.id, label: "" };
	return {
		label: pending.label,
		keeping,
		finish: () => requestMoveSession({ sessionId: session.id, hostId: session.hostId, projectId: session.workspaceId, title: session.title }),
		keepHere: async () => {
			setKeeping(true);
			try {
				await cancelMove(source, pending.transferId);
			} finally {
				setKeeping(false);
				void queryClient.invalidateQueries({ queryKey: ["session-moved-to"] });
			}
		},
	};
}
