import type { QueryClient } from "@tanstack/react-query";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import type { SessionMode } from "../types/conversation";
import { OrchestratorSpawnError, spawnOrchestrator } from "./spawn-orchestrator";
import type { OrchestratorReplacementFailure } from "../stores/ui-store";

type NavigateToSession = (options: {
	to: "/projects/$projectId/sessions/$sessionId";
	params: { projectId: string; sessionId: string };
}) => unknown;

type RestartProjectOrchestratorOptions = {
	projectId: string;
	queryClient: QueryClient;
	navigate: NavigateToSession;
	setProjectRestarting: (projectId: string, restarting: boolean) => void;
	setOrchestratorReplacementError: (projectId: string, failure: OrchestratorReplacementFailure | null) => void;
	onError?: (error: unknown) => void;
	mode?: SessionMode;
	approvalMode?: "default" | "accept-edits" | "auto" | "bypass-permissions";
};

async function refreshWorkspaceState(queryClient: QueryClient) {
	try {
		await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
	} catch {
	}
}

export async function restartProjectOrchestrator({
	projectId,
	queryClient,
	navigate,
	setProjectRestarting,
	setOrchestratorReplacementError,
	onError,
	mode,
	approvalMode,
}: RestartProjectOrchestratorOptions) {
	const activeElement = document.activeElement;
	setProjectRestarting(projectId, true);
	try {
		const sessionId = await spawnOrchestrator(projectId, "restart", true, mode, approvalMode);
		await refreshWorkspaceState(queryClient);
		setOrchestratorReplacementError(projectId, null);
		if (activeElement instanceof HTMLElement) activeElement.blur();
		void navigate({
			to: "/projects/$projectId/sessions/$sessionId",
			params: { projectId, sessionId },
		});
	} catch (error) {
		await refreshWorkspaceState(queryClient);
		setOrchestratorReplacementError(projectId, {
			message: error instanceof Error ? error.message : "Could not replace orchestrator",
			...(error instanceof OrchestratorSpawnError
				? { code: error.code, requestId: error.requestId, details: error.details }
				: {}),
		});
		onError?.(error);
	} finally {
		setProjectRestarting(projectId, false);
	}
}
