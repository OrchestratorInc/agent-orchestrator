import { useQueryClient } from "@tanstack/react-query";
import { useMatchRoute, useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { useRemoteWorkspaces, useWorkspaceQuery, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { useUiStore } from "../stores/ui-store";
import { CLOUD_PROJECT_KIND, STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { NewTaskDialog } from "./NewTaskDialog";

// Every New Task entry point (⌘N / Ctrl+Shift+N, sidebar, topbar, board, home,
// command palette) raises the ui-store `newTaskRequest` signal. The shell is
// mounted on every route, so this opens the new chat page from anywhere; the
// nonce lets a repeat request for the same project fire again. Cloud projects
// keep the New Task dialog for now.
export function NewTaskNavigator() {
	const navigate = useNavigate();
	const matchRoute = useMatchRoute();
	const queryClient = useQueryClient();
	const newTaskRequest = useUiStore((state) => state.newTaskRequest);
	const lastNonce = useRef(newTaskRequest?.nonce ?? 0);
	const localWorkspaces = useWorkspaceQuery().data;
	const remoteWorkspaces = useRemoteWorkspaces().data;
	const [dialog, setDialog] = useState<{ open: boolean; projectId?: string; hostId?: string }>({ open: false });

	useEffect(() => {
		if (!newTaskRequest || newTaskRequest.nonce === lastNonce.current) return;
		lastNonce.current = newTaskRequest.nonce;
		const { projectId, hostId } = newTaskRequest;
		const workspaces = (hostId ? remoteWorkspaces?.filter((workspace) => workspace.hostId === hostId) : localWorkspaces) ?? [];
		if (workspaces.some((workspace) => workspace.id === projectId && workspace.kind === CLOUD_PROJECT_KIND)) {
			// An open dialog keeps its form rather than retargeting to another project.
			setDialog((current) => current.open ? current : { open: true, projectId, hostId });
			return;
		}
		void navigate({
			to: "/new",
			search: {
				project: projectId === STANDALONE_WORKSPACE_ID ? undefined : projectId,
				host: hostId,
			},
			// A new task from the new chat page replaces it rather than stacking history.
			replace: Boolean(matchRoute({ to: "/new" })),
		});
	}, [localWorkspaces, matchRoute, navigate, newTaskRequest, remoteWorkspaces]);

	const handleCreated = async (sessionId: string) => {
		if (!dialog.projectId) return;
		await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(dialog.hostId) });
		void navigate(sessionNavigateTarget(dialog.projectId, sessionId, dialog.hostId));
	};

	return (
		<NewTaskDialog
			open={dialog.open}
			projectId={dialog.projectId}
			hostId={dialog.hostId}
			onProjectChange={(projectId) => setDialog((current) => ({ ...current, projectId }))}
			onCreated={(sessionId) => void handleCreated(sessionId)}
			onOpenChange={(open) => setDialog((current) => ({ ...current, open }))}
		/>
	);
}
