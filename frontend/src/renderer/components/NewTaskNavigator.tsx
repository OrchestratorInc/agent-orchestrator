import { useQueryClient } from "@tanstack/react-query";
import { useMatchRoute, useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { useCloudGate } from "../hooks/useCloudGate";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { useCloudSession } from "../lib/cloud-session";
import { useCloudProjectsQuery, useSharedCloudWorkspacesQuery, workspaceQueryKey, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { CODER_PROVIDER, projectSandboxProvider } from "../lib/cloud-offering";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { useUiStore } from "../stores/ui-store";
import { CLOUD_STANDALONE_WORKSPACE_ID, STANDALONE_WORKSPACE_ID, type WorkspaceSummary } from "../types/workspace";
import { NewTaskDialog } from "./NewTaskDialog";

export const CLOUD_CLASSIFY_WAIT_MS = 4000;

export function NewTaskNavigator() {
	const navigate = useNavigate();
	const matchRoute = useMatchRoute();
	const queryClient = useQueryClient();
	const newTaskRequest = useUiStore((state) => state.newTaskRequest);
	const lastNonce = useRef(newTaskRequest?.nonce ?? 0);
	const [dialog, setDialog] = useState<{ open: boolean; projectId?: string; hostId?: string }>({ open: false });
	const cloudProjectsQuery = useCloudProjectsQuery();
	const sharedWorkspacesQuery = useSharedCloudWorkspacesQuery();
	const cloudProjects = cloudProjectsQuery.data;
	const sharedWorkspaces = sharedWorkspacesQuery.data;
	const { status: cloudStatus } = useCloudSession();
	const cloudGate = useCloudGate();
	const { error: cloudOrgError } = useCloudOrg();
	const cloudInPlay = cloudGate.loading || cloudStatus === "loading" || (cloudStatus === "authenticated" && cloudGate.cloudEnabled);
	const listsAnswered = (cloudProjectsQuery.status !== "pending" || cloudOrgError !== undefined) &&
		sharedWorkspacesQuery.status !== "pending";
	const classifying = cloudInPlay && !listsAnswered;
	const waiting = useRef<{ nonce: number; expired: boolean; timer?: ReturnType<typeof setTimeout> } | null>(null);
	const [waitExpired, setWaitExpired] = useState(0);
	useEffect(() => () => clearTimeout(waiting.current?.timer), []);
	const cloudOffering = useCloudOffering();

	useEffect(() => {
		if (!newTaskRequest || newTaskRequest.nonce === lastNonce.current) return;
		const { projectId, hostId, report } = newTaskRequest;
		const cloudProject = hostId ? undefined : cloudProjects?.find((project) => project.id === projectId);
		const localProject = !hostId && Boolean(queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKey)?.some((workspace) => workspace.id === projectId));
		if (!hostId && projectId !== STANDALONE_WORKSPACE_ID && !cloudProject && !localProject && classifying) {
			const nonce = newTaskRequest.nonce;
			if (waiting.current?.nonce !== nonce) {
				clearTimeout(waiting.current?.timer);
				const wait: { nonce: number; expired: boolean; timer?: ReturnType<typeof setTimeout> } = { nonce, expired: false };
				wait.timer = setTimeout(() => {
					wait.expired = true;
					setWaitExpired(nonce);
				}, CLOUD_CLASSIFY_WAIT_MS);
				waiting.current = wait;
			}
			if (!waiting.current.expired) return;
		}
		clearTimeout(waiting.current?.timer);
		waiting.current = null;
		lastNonce.current = newTaskRequest.nonce;
		const sharedAgent = !hostId && !cloudProject &&
			Boolean(sharedWorkspaces?.some((workspace) => workspace.id === projectId && workspace.standaloneAgent));
		const target = cloudProject?.standaloneAgent ? CLOUD_STANDALONE_WORKSPACE_ID : sharedAgent ? STANDALONE_WORKSPACE_ID : projectId;
		if (cloudProject && !cloudProject.standaloneAgent && projectSandboxProvider(cloudProject.config, cloudOffering) === CODER_PROVIDER) {
			setDialog((current) => current.open ? current : { open: true, projectId, hostId });
			return;
		}
		void navigate({
			to: "/new",
			search: {
				project: target === STANDALONE_WORKSPACE_ID ? undefined : target,
				host: hostId,
				report,
			},
			replace: Boolean(matchRoute({ to: "/new" })),
		});
	}, [classifying, cloudOffering, cloudProjects, matchRoute, navigate, newTaskRequest, queryClient, sharedWorkspaces, waitExpired]);

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
