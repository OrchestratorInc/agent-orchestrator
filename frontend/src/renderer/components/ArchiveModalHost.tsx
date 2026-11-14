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

export function ArchiveModalHost() {
	const request = useUiStore((state) => state.archiveModal);
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
	const local = useWorkspaceQuery({ enabled: open && !hostId, subscribed: open && !hostId });
	const remote = useRemoteHostWorkspacesQuery(hostId ?? "", { enabled: open && Boolean(hostId), subscribed: open && Boolean(hostId) });
	const workspaces = hostId ? remote.data : local.data;

	const liveSessions = useMemo(() => {
		const scoped = projectId ? (workspaces ?? []).filter((workspace) => workspace.id === projectId) : workspaces ?? [];
		return archivedNewestFirst(scoped);
	}, [projectId, workspaces]);
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

	const router = useRouter({ warn: false });
	useEffect(() => {
		if (!open || !router) return;
		return router.subscribe("onBeforeNavigate", (event) => {
			if (event.pathChanged) useUiStore.getState().closeArchive();
		});
	}, [open, router]);

	const returnFocus = request.returnFocus;
	const onCloseAutoFocus = useMemo(() => returnFocus ? (event: Event) => {
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
