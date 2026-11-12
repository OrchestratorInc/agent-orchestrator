import { OrchestratorStartingChat } from "./chat/OrchestratorStartingChat";
import { useCallback, useEffect, useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
	SessionsBoardGridView,
	chipTone,
	largestSession,
	type ChipTone,
} from "@aoagents/product-ui";
import { AlertTriangle, Archive, RotateCw, WifiOff } from "@/components/app-icons";
import {
	CLOUD_PROJECT_KIND,
	STANDALONE_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
	type WorkspaceSession,
	isOrchestratorSession,
	newestActiveOrchestrator,
	orchestratorHealth,
	workerSessions,
	hasConfiguredOrchestratorAgent,
} from "../types/workspace";
import { isArchivedSession } from "../lib/standalone-archive";
import {
	boardKanbanColumnOrder,
	getKanbanColumnView,
	type KanbanColumnView,
} from "../lib/session-presentation";
import { useSessionMenuAvailable } from "../hooks/useAppMenuCommand";
import { useSessionShortcut } from "../hooks/useAppShortcut";
import { useTerminateSession } from "../hooks/useTerminateSession";
import { useCloudCp } from "../hooks/useCloudCp";
import { useSessionParticipants } from "../hooks/useSessionParticipants";
import { useCloudProjectsQuery, useRemoteProjectQuery, useWorkspaceQuery, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { FREESTYLE_PROVIDER, projectSandboxProvider } from "../lib/cloud-offering";
import { BoardWelcome, ProjectBoardEmpty } from "./BoardEmptyStates";
import { ProjectImportOffer } from "./ProjectImportOffer";
import { noDragStyle, PageTopbar, PageTopbarTitle } from "./PageTopbar";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { formatTimeCompact } from "../lib/format-time";
import { restartProjectOrchestrator } from "../lib/restart-orchestrator";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { demoBoardSessions } from "../lib/demo-board-sessions";
import { useUiStore } from "../stores/ui-store";
import { DaemonStartupLoader } from "./DaemonStartupLoader";
import { useBoardPresentation } from "../hooks/useBoardPresentation";
import { useProjectOrchestratorAction } from "../hooks/useProjectOrchestratorAction";
import { openRemoteOrchestrator } from "../lib/remote-orchestrator";
import { labelForHost } from "../lib/host-clients";
import type { RemoteHost } from "../hooks/useRemoteHosts";
import { cn } from "../lib/utils";
import { useConnectedHosts } from "../hooks/useHostConnection";
import { LOCAL_HOST, refKey, sessionUiKey } from "../lib/hosts";
import { useShellMaybe } from "../lib/shell-context";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { ProjectTerminationFeedback } from "./SessionTopbarActions";
import { ProjectBoardActions } from "./ProjectBoardActions";
import { useDiagnosticsEnabled, usePressureState, useSessionMemory } from "../hooks/useSessionMemory";
import { AppMemoryIndicator, toSessionFacts } from "./SessionMemoryPanel";
import { Button } from "./ui/button";
import { recordManualWorkerOpen } from "../lib/session-management-telemetry";
import {
	BoardSessionCardAdapter,
	sessionsBoardLabels,
} from "./SessionsBoardAdapters";

type SessionsBoardProps = {
	/** When set, the board shows only this project's sessions. */
	projectId?: string;
	hostId?: string;
};

export function SessionsBoard({ projectId, hostId }: SessionsBoardProps) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const shell = useShellMaybe();
	const connected = useConnectedHosts().includes(hostId ?? "");
	const hostOffline = Boolean(hostId) && !connected;
	const savedHost = hostId ? shell?.remoteHosts?.find((host) => host.hostId === hostId) : undefined;
	const hostLabel = hostId ? labelForHost(hostId) ?? savedHost?.label ?? hostId : undefined;
	const scopeKey = refKey({ host: hostId ?? LOCAL_HOST, id: projectId ?? "all" });
	// Lanes follow the daemon's delivery order: building -> validating ->
	// in review -> ready. The middle two are one review-feedback loop, split by
	// whose turn it is.
	const columns: KanbanColumnView[] = boardKanbanColumnOrder.map((column) => getKanbanColumnView(column, t));
	const localWorkspaceQuery = useWorkspaceQuery();
	const remoteProjectQuery = useRemoteProjectQuery(hostId ?? "", projectId ?? "");
	const all = localWorkspaceQuery.data ?? [];
	const workspaces = hostId
		? remoteProjectQuery.data ? [remoteProjectQuery.data] : []
		: projectId ? all.filter((candidate) => candidate.id === projectId) : all;
	const workspace = projectId ? workspaces[0] : undefined;
	// A Freestyle cloud project's orchestrator starts in place (see
	// OrchestratorStartingChat); its session does not exist yet, so the
	// project's provider decides, as the control plane's does.
	// Only this machine's cloud projects start in place: a remote host's board
	// never reads this machine's cloud settings (as useCloudCp below).
	const localCloudBoard = !hostId && workspace?.kind === CLOUD_PROJECT_KIND;
	const cloudProjects = useCloudProjectsQuery({ enabled: localCloudBoard });
	const offering = useCloudOffering(localCloudBoard);
	const cloudProject = cloudProjects.data?.find((candidate) => candidate.id === projectId);
	const startsInPlace = localCloudBoard && cloudProject !== undefined &&
		projectSandboxProvider(cloudProject.config, offering) === FREESTYLE_PROVIDER;
	// Board chrome stays route-oriented; project context remains in the sidebar.
	const boardLabel = t("shell.board");
	const liveSessions = workspaces.flatMap((workspace) => workerSessions(workspace.sessions)).filter((session) => !session.needsResume || isArchivedSession(session));
	const demoWorkspaceId = projectId ?? workspaces[0]?.id;
	const sessions = !hostId && usesPreviewWorkspaceData && demoWorkspaceId && liveSessions.length === 0
		? demoBoardSessions(demoWorkspaceId)
		: liveSessions;
	const orchestrator = projectId ? newestActiveOrchestrator(workspaces[0]?.sessions ?? []) : undefined;
	const projectActions = useProjectOrchestratorAction({
		projectId,
		project: workspace,
		orchestrator,
		source: "board",
		hostId,
	});
	const { isProjectRestarting, isProvisioning } = projectActions;
	// A Freestyle cloud project just created starts its orchestrator on its own
	// (only those are flagged, see CreateProjectFlow), so it opens on the
	// orchestrator starting in place rather than on an empty board.
	const autoStartFlagged = useUiStore((state) => !hostId && projectId !== undefined && state.autoStartOrchestratorProjectIds.has(projectId));
	const autoStartPending = autoStartFlagged && !orchestrator;
	const { openOrchestrator } = projectActions;
	useEffect(() => {
		if (!autoStartFlagged || !projectId) return;
		if (orchestrator) {
			useUiStore.getState().setAutoStartOrchestrator(projectId, false);
			return;
		}
		if (workspace?.kind !== CLOUD_PROJECT_KIND || !startsInPlace) return;
		// Read the store, not this render: the flag is consumed exactly once.
		const ui = useUiStore.getState();
		if (!ui.autoStartOrchestratorProjectIds.has(projectId)) return;
		ui.setAutoStartOrchestrator(projectId, false);
		openOrchestrator();
	}, [autoStartFlagged, openOrchestrator, orchestrator, projectId, startsInPlace, workspace?.kind]);
	useSessionShortcut("open-orchestrator", () => {
		if (!projectId || projectId === STANDALONE_WORKSPACE_ID || workspace?.kind === STANDALONE_PROJECT_KIND || workspace?.shared) return;
		projectActions.openOrchestrator();
	}, Boolean(projectId) && !projectActions.isSpawning && !isProvisioning);
	useSessionMenuAvailable(
		"open-orchestrator",
		Boolean(projectId) && projectId !== STANDALONE_WORKSPACE_ID && workspace?.kind !== STANDALONE_PROJECT_KIND && !workspace?.shared && !projectActions.isSpawning && !isProvisioning,
	);
	const setProjectRestarting = useUiStore((state) => state.setProjectRestarting);
	const setOrchestratorReplacementError = useUiStore((state) => state.setOrchestratorReplacementError);
	const setOrchestratorStartupError = useUiStore((state) => state.setOrchestratorStartupError);
	const health = workspace ? orchestratorHealth(workspace, isProjectRestarting) : { state: "ok" as const };
	// Someone this project was shared with works through what was shared and
	// the owner's orchestrator: they can't start tasks or an orchestrator
	// here, so the board offers neither and doesn't ask them to.
	const guest = Boolean(workspace?.shared);
	// The archive itself is the shell's modal; the board only shows its count.
	const archivedCount = sessions.filter(isArchivedSession).length;
	const activeSessions = sessions.filter((candidate) => !isArchivedSession(candidate));
	// Who is in each shared cloud session, read without joining any of them.
	const participantsBySession = useSessionParticipants(activeSessions, !hostId);
	const { userId: cloudUserId } = useCloudCp(!hostId && activeSessions.some((session) => Boolean(session.cloud)));
	const boardSessions = activeSessions.map((session) =>
		session.status === "no_signal" || session.displayStatus === "No signal"
			? { ...session, kanbanColumn: "building" as const }
			: session,
	);
	const boardLabels = sessionsBoardLabels(t);
	const presentation = useBoardPresentation({
		projectId,
		isSuccess: localWorkspaceQuery.isSuccess,
		isError: localWorkspaceQuery.isError,
		hasProjects: workspaces.length > 0,
		hasWorkerSessions: liveSessions.length > 0,
	});
	const showStartup = !hostId && presentation.showStartup;
	const showWelcome = !hostId && presentation.showWelcome;
	const showProjectEmpty = hostId
		? connected && remoteProjectQuery.isSuccess && Boolean(workspace) && liveSessions.length === 0
		: presentation.showProjectEmpty;
	// Memory and CPU monitoring is a developer tool: the light, the card chips
	// and the window behind them only exist with Diagnostics on in Developer mode.
	// They read this machine, so another machine's board shows none of them.
	const diagnostics = useDiagnosticsEnabled();
	// The light leads the board's actions, before the Task button, not in the archive bar.
	const memoryLight = diagnostics && !hostId ? <AppMemoryIndicator /> : null;
	// Per-session readings feed each card's resource chip. Chips are grey
	// unless the machine is tight and the card is part of the fix (idle, or
	// the single largest).
	const sessionMemory = useSessionMemory(projectId, !hostId).data;
	const memoryBySession = diagnostics ? sessionMemory : undefined;
	const pressure = usePressureState(!hostId);
	const chipToneOf = useMemo(() => {
		const now = Date.now();
		const facts = sessions
			.filter((session) => session.isTerminated !== true && !isOrchestratorSession(session))
			.map((session) => toSessionFacts(session, memoryBySession?.get(session.id), now));
		const largest = largestSession(facts);
		return (session: WorkspaceSession): ChipTone =>
			chipTone(pressure ?? "fine", facts.find((f) => f.id === session.id) ?? toSessionFacts(session, memoryBySession?.get(session.id), now), largest);
	}, [sessions, memoryBySession, pressure]);
	const hasArchive = archivedCount > 0;
	const terminateSession = useTerminateSession();
	const activeScopeRef = useRef(scopeKey);
	activeScopeRef.current = scopeKey;
	const openSession = useCallback((session: WorkspaceSession) => {
		if (session.kind === "worker") recordManualWorkerOpen(session.id, hostId);
		void navigate(sessionNavigateTarget(session.workspaceId, session.id, hostId));
	}, [navigate, hostId]);
	const openSessionArtifact = useCallback((session: WorkspaceSession, path: string) => {
		useUiStore.getState().requestWorkspaceFileOpen(session.id, path, hostId, "artifact");
		openSession(session);
	}, [openSession, hostId]);
	const openSessionArtifacts = useCallback((session: WorkspaceSession) => {
		const uiKey = sessionUiKey(session.id, hostId);
		useUiStore.getState().initializeInspectorSession(uiKey, Boolean(session.previewUrl), true);
		useUiStore.getState().setInspectorView(uiKey, "artifacts");
		useUiStore.getState().setInspectorOpen(uiKey, true);
		openSession(session);
	}, [openSession, hostId]);

	const restartOrchestrator = async () => {
		if (!projectId || isProjectRestarting || isProvisioning) return;
		if (hostId) {
			if (!connected) return;
			setProjectRestarting(projectId, true, hostId);
			setOrchestratorStartupError(projectId, null, hostId);
			try {
				const sessionId = await openRemoteOrchestrator(hostId, projectId, orchestrator, undefined, true, "restart");
				await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
				if (activeScopeRef.current === scopeKey) void navigate(sessionNavigateTarget(projectId, sessionId, hostId));
			} catch (error) {
				setOrchestratorStartupError(projectId, error instanceof Error ? error.message : t("shell.couldNotSpawn"), hostId);
			} finally {
				setProjectRestarting(projectId, false, hostId);
			}
			return;
		}
		await restartProjectOrchestrator({
			projectId,
			queryClient,
			navigate,
			setProjectRestarting,
			setOrchestratorReplacementError,
		});
	};

	const actions = projectId && (!hostId || connected) ? (
		<>
			<ProjectTerminationFeedback projectId={projectId} hostId={hostId} />
			{memoryLight}
			{guest ? null : <ProjectBoardActions actions={projectActions} placement="header" quiet={showProjectEmpty} cloud={workspace?.kind === CLOUD_PROJECT_KIND} />}
		</>
	) : hostOffline ? (
		<HostOfflineStatus host={savedHost} label={hostLabel ?? ""} updatedAt={workspace ? remoteProjectQuery.dataUpdatedAt : undefined} />
	) : memoryLight ?? undefined;

	if (projectId && (isProvisioning || projectActions.isSpawning || autoStartPending)) {
		return <OrchestratorStartingChat
			cloud={workspace?.kind === CLOUD_PROJECT_KIND || autoStartPending}
			freestyle={startsInPlace || autoStartPending}
			coderWorkspaceUrl={workspace?.sessions.find((session) => session.kind === "orchestrator")?.cloud?.coderWorkspaceUrl}
			harness={workspace?.orchestratorAgent ?? workspace?.sessions.find((session) => session.kind === "orchestrator")?.provider}
		/>;
	}

	return (
		<div className="relative flex h-full min-h-0 flex-col bg-background text-foreground" data-testid="board" data-host-id={hostId} data-project-id={projectId}>
			<PageTopbar
				lead={showWelcome ? undefined : (
					<PageTopbarTitle
						data-testid="board-topbar-label"
					>
						{boardLabel}
						{hostLabel ? <span className="truncate text-muted-foreground">· {hostLabel}</span> : null}
					</PageTopbarTitle>
				)}
				actions={showWelcome ? undefined : actions}
			/>
			{connected && remoteProjectQuery.isError ? <p role="alert" className="px-4 py-3 text-sm text-destructive">{t("shell.couldNotLoadProjects")}</p> : null}
			{hostId && remoteProjectQuery.isSuccess && !workspace ? <p role="alert" className="px-4 py-3 text-sm text-destructive">{t("session.notFound")}</p> : null}
			{hostId && projectId && connected && workspace && !orchestrator && !hasConfiguredOrchestratorAgent(workspace) ? (
				<div className="mx-3 my-3 flex items-center gap-3 rounded-md border border-border bg-surface px-3 py-2 text-xs text-muted-foreground">
					<span className="min-w-0 flex-1">{t("remote.configureOrchestratorFirst", { label: labelForHost(hostId) ?? hostId, defaultValue: "Choose an orchestrator agent on {{label}} to start one." })}</span>
					<Button type="button" variant="ghost" size="xs" className="font-medium" onClick={() => shell?.openRemoteProjectSettings(hostId, projectId)}>{t("restoreUnavailable.configureOrchestrator")}</Button>
				</div>
			) : null}

			<div className={cn("min-h-0 flex-1 overflow-hidden", hostOffline && workspace && "opacity-75")} data-host-offline={hostOffline && workspace ? "" : undefined}>
				{projectId && !guest && health.state !== "ok" && (!hostId || connected && (health.state !== "missing" || hasConfiguredOrchestratorAgent(workspace))) ? (
					<div className="mx-3 my-3 flex items-center gap-3 rounded-md border border-border bg-surface px-3 py-2 text-xs text-muted-foreground">
						<AlertTriangle className="size-icon-base shrink-0 text-warning" aria-hidden="true" />
						<span className="min-w-0 flex-1">{health.message}</span>
						{health.state === "restart_needed" || health.state === "duplicates" ? (
							<Button type="button" size="sm" disabled={isProjectRestarting} onClick={() => void restartOrchestrator()} variant="primary">
								<RotateCw className="size-3.5" aria-hidden="true" />
								{t("shell.restart")}
							</Button>
						) : null}
					</div>
				) : null}
			{workspace?.folderMissing ? (
				<div className="mx-3 my-3 flex items-center gap-3 rounded-md border border-border bg-surface px-3 py-2 text-xs text-muted-foreground">
					<AlertTriangle className="size-icon-base shrink-0 text-warning" aria-hidden="true" />
					<span className="min-w-0 flex-1">{t("home.folderMissing")}</span>
				</div>
			) : null}
			{projectId && isProvisioning ? (
				<div
					className="mx-3 my-3 flex items-center gap-3 rounded-md border border-border bg-surface px-3 py-2 text-xs text-muted-foreground"
					role="status"
				>
					<span
						className="size-icon-base shrink-0 animate-spin rounded-full border-2 border-current border-r-transparent"
						aria-hidden="true"
					/>
					<span className="min-w-0 flex-1">
						{t("shell.provisioning", { defaultValue: "Setting up the project — starting the orchestrator…" })}
					</span>
				</div>
			) : null}
				{!hostId && (presentation.workspaceStartupState === "error" || localWorkspaceQuery.isError) ? (
					<p className="py-10 text-center text-xs text-passive">{t("shell.couldNotLoadSessions")}</p>
				) : hostOffline && !workspace ? (
					<div className="flex h-full min-h-0 items-center justify-center overflow-y-auto" data-testid="board-host-offline-empty">
						<div className="flex w-full max-w-xl flex-col items-center px-4 pb-empty-offset-y text-center">
							<h2 className="text-subtitle font-semibold tracking-tight text-foreground">{hostLabel}</h2>
							<p className="mt-2 text-md-sm leading-relaxed text-muted-foreground">{t(hostOfflineReasonKey(savedHost))}</p>
						</div>
					</div>
				) : hostId && !workspace ? null : showWelcome ? (
				<BoardWelcome />
				) : showProjectEmpty ? (
					<ProjectBoardEmpty
						actions={guest ? null : <ProjectBoardActions actions={projectActions} placement="empty" />}
						offer={projectId && !hostId ? <ProjectImportOffer className="mt-8 w-full text-left" projectId={projectId} /> : null}
					/>
				) : (
					<SessionsBoardGridView
						columns={columns}
						key={scopeKey}
						labels={boardLabels}
						renderSessionCard={(session) => (
							<BoardSessionCardAdapter
								participants={participantsBySession.get(session.id)}
								me={cloudUserId}
								memory={memoryBySession?.get(session.id)}
								memoryTone={chipToneOf(session)}
								onOpen={() => openSession(session)}
								onOpenArtifact={(path) => openSessionArtifact(session, path)}
								onOpenArtifacts={() => openSessionArtifacts(session)}
								onTerminate={!hostId || connected ? ({ deleteArtifacts }) => terminateSession.mutate({ ...session, deleteArtifacts }) : undefined}
								session={session}
							/>
						)}
						sessions={boardSessions}
					/>
				)}
			</div>

			{hasArchive ? (
				// Even 4px gap to the divider and the panel's bottom and left edges; the
				// 30px button puts its text on the same line as the sidebar's Sign in.
				<div className="flex shrink-0 items-center border-t border-border p-1">
					<Button
						aria-haspopup="dialog"
						aria-label={t("shell.archiveSessionsAria", { count: archivedCount })}
						data-testid="board-archive-button"
						// The shell's archive modal, the same one the sidebar and Cmd+K open.
						onClick={(event) => useUiStore.getState().openArchive({ projectId, hostId, returnFocus: event.currentTarget })}
						// The xs button at 30px (no Button size is 30).
						className="h-7.5 gap-1 px-2 text-xs"
						size="none"
						type="button"
						variant="quiet"
					>
						<Archive className="size-icon-sm" aria-hidden="true" />
						{t("shell.archive")}
						<span className="tabular-nums text-passive">{archivedCount}</span>
					</Button>
				</div>
			) : null}
			{showStartup ? <DaemonStartupLoader /> : null}
		</div>
	);
}

function hostOfflineReasonKey(host: RemoteHost | undefined) {
	return host?.failureReason === "unauthorized" ? "remote.hostUnauthorized" as const
		: host?.failureReason === "incompatible" ? "remote.hostIncompatible" as const
		: "remote.hostOffline" as const;
}

function HostOfflineStatus({ host, label, updatedAt }: { host: RemoteHost | undefined; label: string; updatedAt?: number }) {
	const { t } = useTranslation();
	const shell = useShellMaybe();
	const action = host?.status === "connecting"
		? <Button type="button" size="sm" variant="secondary" disabled>{t("terminal.connecting")}</Button>
		: host?.failureReason === "unauthorized"
			? <Button type="button" size="sm" variant="secondary" onClick={() => useUiStore.getState().openGlobalSettings("remoteHosts", { preserveProject: true })}>{t("remote.editHost", { label })}</Button>
			: <Button type="button" size="sm" variant="secondary" aria-label={t("remote.retryHost", { label })} disabled={!shell?.retryRemoteHosts} onClick={() => shell?.retryRemoteHosts?.()}>
				<RotateCw className="size-3.5" aria-hidden="true" />
				{t("remote.retry")}
			</Button>;
	return (
		<div className="flex min-w-0 items-center gap-2 text-xs" data-testid="board-host-offline" role="status" style={noDragStyle}>
			<Tooltip>
				<TooltipTrigger asChild>
					<span className="inline-flex shrink-0 items-center gap-1.5 text-warning" tabIndex={0}>
						<WifiOff className="size-icon-sm" aria-hidden="true" />
						{t("remote.offline")}
					</span>
				</TooltipTrigger>
				<TooltipContent className="max-w-72">{t(hostOfflineReasonKey(host))}{updatedAt ? ` ${t("remote.offlineCopy")}` : ""}</TooltipContent>
			</Tooltip>
			{updatedAt ? <span className="truncate text-muted-foreground">· {t("shell.updatedAt", { time: formatTimeCompact(new Date(updatedAt).toISOString()) })}</span> : null}
			{action}
		</div>
	);
}
