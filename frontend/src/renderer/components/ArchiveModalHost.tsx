import { useCallback, useMemo, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useConnectedHosts } from "../hooks/useHostConnection";
import { useRemoteHostWorkspacesQuery, useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { LOCAL_HOST, refKey } from "../lib/hosts";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { recordManualWorkerOpen } from "../lib/session-management-telemetry";
import { isArchivedSession } from "../lib/standalone-archive";
import { useUiStore, type ArchiveModalRequest } from "../stores/ui-store";
import { workerSessions, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";
import { SessionArchiveModal } from "./SessionArchiveModal";

/**
 * The archive modal, mounted once by the shell so it opens over whatever page
 * is showing: a session, Artifacts, settings or the board. The board's Archive
 * button, a sidebar project's menu and Cmd+K open it through
 * `useUiStore().openArchive`; none of them navigates. Only opening or
 * restoring a session from inside it moves the user, and that closes it.
 */
export function ArchiveModalHost() {
	const request = useUiStore((state) => state.archiveModal);
	// The last request outlives the close, so the closing animation keeps its
	// cards. Nothing mounts until the first open.
	const [shown, setShown] = useState(request);
	if (request && request !== shown) setShown(request);
	if (!shown) return null;
	return <ArchiveModal open={request !== null} request={shown} />;
}

function ArchiveModal({ open, request }: { open: boolean; request: ArchiveModalRequest }) {
	const navigate = useNavigate();
	const { hostId, projectId } = request;
	const connectedHosts = useConnectedHosts();
	const connected = !hostId || connectedHosts.includes(hostId);
	// Subscribed only while open: closed (or closing), workspace polls leave
	// this alone. The shell keeps both lists fresh either way.
	const local = useWorkspaceQuery({ subscribed: open && !hostId });
	const remote = useRemoteHostWorkspacesQuery(hostId ?? "", { enabled: open && Boolean(hostId), subscribed: open && Boolean(hostId) });
	const workspaces = hostId ? remote.data : local.data;

	const sessions = useMemo(() => {
		const scoped = projectId ? (workspaces ?? []).filter((workspace) => workspace.id === projectId) : workspaces ?? [];
		return archivedNewestFirst(scoped);
	}, [projectId, workspaces]);

	const openSession = useCallback((session: WorkspaceSession) => {
		if (session.kind === "worker") recordManualWorkerOpen(session.id, hostId);
		void navigate(sessionNavigateTarget(session.workspaceId, session.id, hostId));
	}, [hostId, navigate]);

	const onOpenChange = useCallback((next: boolean) => {
		if (!next) useUiStore.getState().closeArchive();
	}, []);

	const returnFocus = request.returnFocus;
	const onCloseAutoFocus = useMemo(() => returnFocus ? (event: Event) => {
		// The element that opened the modal is gone (Cmd+K's search), so hand
		// focus back to where the opener came from. Two frames later, so a page
		// the modal navigated to (a session's composer) claims it first.
		event.preventDefault();
		requestAnimationFrame(() => requestAnimationFrame(() => {
			const active = document.activeElement;
			if ((!active || active === document.body) && returnFocus.isConnected) returnFocus.focus();
		}));
	} : undefined, [returnFocus]);

	return (
		<SessionArchiveModal
			connected={connected}
			hostId={hostId}
			initialQuery={request.query}
			onCloseAutoFocus={onCloseAutoFocus}
			onOpenChange={onOpenChange}
			onOpenSession={openSession}
			open={open}
			requestId={request.nonce}
			resetKey={refKey({ host: hostId ?? LOCAL_HOST, id: projectId ?? "all" })}
			sessions={sessions}
		/>
	);
}

function archivedNewestFirst(workspaces: WorkspaceSummary[]): WorkspaceSession[] {
	return workspaces
		.flatMap((workspace) => workerSessions(workspace.sessions))
		.filter(isArchivedSession)
		.sort((left, right) => right.updatedAt.localeCompare(left.updatedAt));
}
