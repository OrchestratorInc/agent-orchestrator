import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useRouter } from "@tanstack/react-router";
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
	// Enabled and subscribed only while open. This stays mounted after the first
	// open, and an enabled observer would keep the cloud lists' own poll timers
	// (5 s, 15 s) running beside the shell's for the rest of the app's life.
	const local = useWorkspaceQuery({ enabled: open && !hostId, subscribed: open && !hostId });
	const remote = useRemoteHostWorkspacesQuery(hostId ?? "", { enabled: open && Boolean(hostId), subscribed: open && Boolean(hostId) });
	const workspaces = hostId ? remote.data : local.data;

	const liveSessions = useMemo(() => {
		const scoped = projectId ? (workspaces ?? []).filter((workspace) => workspace.id === projectId) : workspaces ?? [];
		return archivedNewestFirst(scoped);
	}, [projectId, workspaces]);
	// Closing keeps the last list on screen: disabled, the cloud part of the
	// workspace list can drop out, and the exit animation would show it empty.
	const shownSessionsRef = useRef(liveSessions);
	if (open) shownSessionsRef.current = liveSessions;
	const sessions = shownSessionsRef.current;

	const openSession = useCallback((session: WorkspaceSession) => {
		if (session.kind === "worker") recordManualWorkerOpen(session.id, hostId);
		void navigate(sessionNavigateTarget(session.workspaceId, session.id, hostId));
	}, [hostId, navigate]);

	const onOpenChange = useCallback((next: boolean) => {
		if (!next) useUiStore.getState().closeArchive();
	}, []);

	// Opened over a page, it belongs to that page: any move to another one (a
	// shortcut, history, a notification) closes it, so a restore still running
	// cannot take the user anywhere once they have left. Without a router (a
	// component test), there is nothing to follow.
	const router = useRouter({ warn: false });
	useEffect(() => {
		if (!open || !router) return;
		return router.subscribe("onBeforeNavigate", (event) => {
			if (event.pathChanged) useUiStore.getState().closeArchive();
		});
	}, [open, router]);

	const returnFocus = request.returnFocus;
	const onCloseAutoFocus = useMemo(() => returnFocus ? (event: Event) => {
		// Radix would focus the dialog's trigger, and the archive has none: it
		// opens from a menu, a button on another surface or Cmd+K. Hand focus
		// back to the opener instead, two frames later, so a page the modal
		// navigated to (a session's composer) claims it first.
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
