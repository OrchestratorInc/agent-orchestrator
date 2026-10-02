import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { apiErrorCode, apiErrorMessage } from "../lib/api-client";
import { refKey, sessionUiKey } from "../lib/hosts";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { useShellMaybe } from "../lib/shell-context";
import { openRemoteOrchestrator } from "../lib/remote-orchestrator";
import { isChatPreflightCode } from "../lib/spawn-orchestrator";
import { useUiStore } from "../stores/ui-store";
import { hasConfiguredOrchestratorAgent, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";
import { remoteWorkspaceQueryKey } from "./useWorkspaceQuery";

export function useRemoteProjectBoardActions({ hostId, projectId, project, orchestrator, connected }: {
	hostId?: string;
	projectId?: string;
	project?: WorkspaceSummary;
	orchestrator?: WorkspaceSession;
	connected: boolean;
}) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const shell = useShellMaybe();
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	const setProjectRestarting = useUiStore((state) => state.setProjectRestarting);
	const setStartupError = useUiStore((state) => state.setOrchestratorStartupError);
	const projectKey = projectId ? sessionUiKey(projectId, hostId) : undefined;
	const isProjectRestarting = useUiStore((state) => projectKey ? state.restartingProjectIds.has(projectKey) : false);
	const isProvisioning = useUiStore((state) => projectKey ? state.provisioningProjectIds.has(projectKey) : false);
	const startupError = useUiStore((state) => projectKey ? state.orchestratorStartupErrors[projectKey] : undefined);
	const [spawnError, setSpawnError] = useState("");
	const [spawnErrorCode, setSpawnErrorCode] = useState<string>();
	const [spawningRoute, setSpawningRoute] = useState<string | null>(null);
	const launchingRoutes = useRef(new Set<string>());
	const routeKey = hostId && projectId ? refKey({ host: hostId, id: projectId }) : null;
	const isSpawning = spawningRoute === routeKey;
	const activeRoute = useRef<string | null>(routeKey);
	activeRoute.current = routeKey;
	useEffect(() => {
		activeRoute.current = routeKey;
		setSpawnError("");
		setSpawnErrorCode(undefined);
		return () => { activeRoute.current = null; };
	}, [routeKey]);

	const launch = async (mode?: "tui", clean = false) => {
		if (!connected || !hostId || !projectId || !project || isProvisioning || isProjectRestarting) return;
		const actionRouteKey = refKey({ host: hostId, id: projectId });
		if (launchingRoutes.current.has(actionRouteKey)) return;
		setSpawnError("");
		setSpawnErrorCode(undefined);
		setStartupError(projectId, null, hostId);
		if (!orchestrator && !hasConfiguredOrchestratorAgent(project)) {
			shell?.openRemoteProjectSettings(hostId, projectId);
			return;
		}
		launchingRoutes.current.add(actionRouteKey);
		setSpawningRoute(actionRouteKey);
		if (clean) setProjectRestarting(projectId, true, hostId);
		try {
			const sessionId = await openRemoteOrchestrator(hostId, projectId, orchestrator, mode, clean);
			await queryClient.invalidateQueries({ queryKey: remoteWorkspaceQueryKey(hostId) });
			if (activeRoute.current === routeKey) void navigate(sessionNavigateTarget(projectId, sessionId, hostId));
		} catch (error) {
			setStartupError(projectId, error instanceof Error ? error.message : apiErrorMessage(error, t("shell.couldNotSpawn")), hostId);
			if (activeRoute.current === routeKey) {
				setSpawnErrorCode(apiErrorCode(error));
				setSpawnError(error instanceof Error ? error.message : apiErrorMessage(error, t("shell.couldNotSpawn")));
			}
		} finally {
			launchingRoutes.current.delete(actionRouteKey);
			if (clean) setProjectRestarting(projectId, false, hostId);
			setSpawningRoute((current) => current === actionRouteKey ? null : current);
		}
	};

	return {
		orchestrator,
		isSpawning,
		isProjectRestarting,
		isProvisioning,
		spawnError: spawnError || startupError || "",
		canCreateAsTui: isChatPreflightCode(spawnErrorCode),
		openNewTask: () => { if (hostId && projectId && connected && !isProjectRestarting && !isProvisioning) requestNewTask(projectId, hostId); },
		openOrchestrator: (mode?: "tui") => { void launch(mode); },
		restartOrchestrator: () => launch(undefined, true),
	};
}
