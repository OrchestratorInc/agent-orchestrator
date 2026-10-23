import { OrchestratorStartingChat } from "./chat/OrchestratorStartingChat";
import { memo, useCallback, useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
	SessionsArchiveView,
	SessionsBoardGridView,
	archiveToggleOffsetClassName,
	chipTone,
	largestSession,
	type ChipTone,
} from "@aoagents/product-ui";
import { AlertTriangle, LayoutDashboard, RotateCw } from "lucide-react";
import {
	CLOUD_PROJECT_KIND,
	type WorkspaceSession,
	type WorkspaceSummary,
	isOrchestratorSession,
	newestActiveOrchestrator,
	orchestratorHealth,
	workerSessions,
	hasConfiguredOrchestratorAgent,
} from "../types/workspace";
import {
	boardKanbanColumnOrder,
	getKanbanColumnView,
	type KanbanColumnView,
} from "../lib/session-presentation";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { useTerminateSession } from "../hooks/useTerminateSession";
import { useRemoteProjectQuery, useWorkspaceQuery, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { NotificationCenter } from "./NotificationCenter";
import { BoardWelcome, ProjectBoardEmpty } from "./BoardEmptyStates";
import { TopbarButton, topbarProjectLabelClass } from "./TopbarButton";
import { restartProjectOrchestrator } from "../lib/restart-orchestrator";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { demoBoardSessions } from "../lib/demo-board-sessions";
import { isLinuxPlatform, isMacPlatform, usesBoardActionsInPanel } from "../lib/platform";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { RestoreUnavailableDialog } from "./RestoreUnavailableDialog";
import { DaemonStartupLoader } from "./DaemonStartupLoader";
import { useBoardPresentation } from "../hooks/useBoardPresentation";
import { useProjectOrchestratorAction } from "../hooks/useProjectOrchestratorAction";
import { openRemoteOrchestrator } from "../lib/remote-orchestrator";
import { labelForHost } from "../lib/host-clients";
import { useConnectedHosts } from "../hooks/useHostConnection";
import { LOCAL_HOST, refKey, sessionUiKey } from "../lib/hosts";
import { useShellMaybe } from "../lib/shell-context";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { ProjectTerminationFeedback } from "./ShellTopbar";
import { ProjectBoardActions } from "./ProjectBoardActions";
import { useDiagnosticsEnabled, usePressureState, useSessionMemory } from "../hooks/useSessionMemory";
import { AppMemoryIndicator, toSessionFacts, useHasAppMemory } from "./SessionMemoryPanel";
import { recordManualWorkerOpen } from "../lib/session-management-telemetry";
import {
	ArchivedSessionCardAdapter,
	BoardSessionCardAdapter,
	sessionsBoardLabels,
} from "./SessionsBoardAdapters";

type SessionsBoardProps = {
	/** When set, the board shows only this project's sessions. */
	projectId?: string;
	hostId?: string;
	remoteWorkspaces?: WorkspaceSummary[];
	remoteLoading?: boolean;
	failedHostIds?: string[];
	onRetryRemoteHosts?: () => void;
};

const boardSessionKey = (session: WorkspaceSession) => refKey({ host: session.hostId ?? LOCAL_HOST, id: session.id });

function sessionIsConnected(session: WorkspaceSession, connectedHosts: string[], boardHost?: string): boolean {
	const host = session.hostId ?? boardHost;
	return !host || connectedHosts.includes(host);
}


// Live merged sessions remain in-flow. A terminated runtime is archived even
// when its SCM outcome remains `merged`, which is exactly what the daemon's
// `archive` column means.
function isArchivedSession(session: WorkspaceSession): boolean {
	return (
		session.kanbanColumn === "archive" ||
		session.isTerminated === true ||
		session.status === "terminated"
	);
}

const isMac = isMacPlatform();
const dragStyle = isMac ? ({ WebkitAppRegion: "drag" } as React.CSSProperties) : undefined;
const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties) : undefined;

export function SessionsBoard({ projectId, hostId, remoteWorkspaces = [], remoteLoading = false, failedHostIds = [], onRetryRemoteHosts }: SessionsBoardProps) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const shell = useShellMaybe();
	const connectedHosts = useConnectedHosts();
	const connected = connectedHosts.includes(hostId ?? "");
	const scopeKey = refKey({ host: hostId ?? LOCAL_HOST, id: projectId ?? "all" });
	// Lanes follow the daemon's delivery order: building -> validating ->
	// in review -> ready. The middle two are one review-feedback loop, split by
	// whose turn it is.
	const columns: KanbanColumnView[] = boardKanbanColumnOrder.map((column) => getKanbanColumnView(column, t));
	const localWorkspaceQuery = useWorkspaceQuery();
	const remoteProjectQuery = useRemoteProjectQuery(hostId ?? "", projectId ?? "");
	// Evaluated at render so platform mocks in tests can flip the in-panel chrome.
	const boardActionsInPanel = usesBoardActionsInPanel();
	/** Bell lives in the board action row when the shell topbar does not host it. */
	const boardOwnsNotificationCenter = isLinuxPlatform() || boardActionsInPanel;
	const all = localWorkspaceQuery.data ?? [];
	const workspaces = hostId
		? remoteProjectQuery.data ? [remoteProjectQuery.data] : []
		: projectId ? all.filter((candidate) => candidate.id === projectId) : [...all, ...remoteWorkspaces];
	const workspace = projectId ? workspaces[0] : undefined;
	const boardLabel = t(projectId ? "shell.board" : "shell.allSessions");
	const liveSessions = workspaces.flatMap((workspace) => workerSessions(workspace.sessions));
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
	const setProjectRestarting = useUiStore((state) => state.setProjectRestarting);
	const setOrchestratorReplacementError = useUiStore((state) => state.setOrchestratorReplacementError);
	const setOrchestratorStartupError = useUiStore((state) => state.setOrchestratorStartupError);
	const health = workspace ? orchestratorHealth(workspace, isProjectRestarting) : { state: "ok" as const };
	const archived = sessions
		.filter(isArchivedSession)
		.sort((left, right) => right.updatedAt.localeCompare(left.updatedAt));
	const activeSessions = sessions.filter((candidate) => !isArchivedSession(candidate));
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
	const showWelcome = !hostId && presentation.showWelcome && !remoteLoading && failedHostIds.length === 0;
	const showProjectEmpty = hostId
		? connected && remoteProjectQuery.isSuccess && Boolean(workspace) && liveSessions.length === 0
		: presentation.showProjectEmpty;
	// Memory and CPU monitoring is a developer tool: the light, the card chips
	// and the window behind them only exist with Diagnostics on in Developer mode.
	// They read this machine, so another machine's board shows none of them.
	const diagnostics = useDiagnosticsEnabled();
	const hasMemory = useHasAppMemory(!hostId) && diagnostics && !hostId;
	// Per-session readings feed each card's resource chip. Chips are grey
	// unless the machine is tight and the card is part of the fix (idle, or
	// the single largest).
	const sessionMemory = useSessionMemory(projectId, !hostId).data;
	const memoryBySession = diagnostics ? sessionMemory : undefined;
	const pressure = usePressureState(!hostId);
	const chipToneOf = useMemo(() => {
		const now = Date.now();
		const facts = sessions
			.filter((session) => !session.hostId && session.isTerminated !== true && !isOrchestratorSession(session))
			.map((session) => toSessionFacts(session, memoryBySession?.get(session.id), now));
		const largest = largestSession(facts);
		return (session: WorkspaceSession): ChipTone =>
			chipTone(pressure ?? "fine", facts.find((f) => f.id === session.id) ?? toSessionFacts(session, memoryBySession?.get(session.id), now), largest);
	}, [sessions, memoryBySession, pressure]);
	// The bar hosts the memory indicator too, so it stays up with an empty archive.
	const hasArchive = archived.length > 0 || hasMemory;
	const terminateSession = useTerminateSession();
	const activeScopeRef = useRef(scopeKey);
	activeScopeRef.current = scopeKey;

	const openSession = useCallback((session: WorkspaceSession) => {
		const sessionHost = session.hostId ?? hostId;
		if (session.kind === "worker") recordManualWorkerOpen(session.id, sessionHost);
		void navigate(sessionNavigateTarget(session.workspaceId, session.id, sessionHost));
	}, [navigate, hostId]);
	const openSessionArtifact = useCallback((session: WorkspaceSession, path: string) => {
		useUiStore.getState().requestWorkspaceFileOpen(session.id, path, session.hostId ?? hostId, "artifact");
		openSession(session);
	}, [openSession, hostId]);
	const openSessionArtifacts = useCallback((session: WorkspaceSession) => {
		const uiKey = sessionUiKey(session.id, session.hostId ?? hostId);
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
			<ProjectBoardActions actions={projectActions} placement="header" quiet={showProjectEmpty} cloud={workspace?.kind === CLOUD_PROJECT_KIND} />
			{boardOwnsNotificationCenter ? (
				<>
					<NotificationCenter />
				</>
			) : null}
		</>
	) : boardOwnsNotificationCenter ? (
		<NotificationCenter />
	) : undefined;

	if (projectId && (isProvisioning || projectActions.isSpawning)) {
		return <OrchestratorStartingChat
			cloud={workspace?.kind === CLOUD_PROJECT_KIND}
			coderWorkspaceUrl={workspace?.sessions.find((session) => session.kind === "orchestrator")?.cloud?.coderWorkspaceUrl}
		/>;
	}

	return (
		<div className="relative flex h-full min-h-0 flex-col bg-background text-foreground" data-testid="board" data-host-id={hostId} data-project-id={projectId}>
			{/* macOS: shell topbar is hidden on board routes, so the project/"Board"
			    crumb + New task / Orchestrator / bell live in this in-panel row.
			    Win/Linux keep the crumb and actions in the framed ShellTopbar.
			    The global board keeps its identity above the import chooser. */}
			{boardActionsInPanel && (boardLabel || actions) ? (
				<div
					className="workspace-topbar-container center-panel-titlebar flex h-toolbar shrink-0 items-center gap-2 border-b border-border-strong pr-1"
					style={dragStyle}
				>
					{boardLabel ? (
						<span
							className={cn(topbarProjectLabelClass, "inline-flex items-center gap-1.5")}
							data-testid="board-topbar-label"
						>
							<LayoutDashboard aria-hidden="true" className="size-icon-md" />
							{boardLabel}
							{hostId ? <span className="truncate text-muted-foreground">· {labelForHost(hostId) ?? hostId}</span> : null}
						</span>
					) : null}
					<div className="min-w-0 flex-1" />
					{actions ? (
						<div className="workspace-topbar-actions flex shrink-0 items-center" style={noDragStyle}>
							{actions}
						</div>
					) : null}
				</div>
			) : null}
			{hostId && !connected ? <p role="alert" className="px-4 py-3 text-sm text-destructive">{t("remote.hostOffline")}</p> : null}
			{hostId && remoteProjectQuery.isError ? <p role="alert" className="px-4 py-3 text-sm text-destructive">{t("shell.couldNotLoadProjects")}</p> : null}
			{hostId && remoteProjectQuery.isSuccess && !workspace ? <p role="alert" className="px-4 py-3 text-sm text-destructive">{t("session.notFound")}</p> : null}
			{!projectId && localWorkspaceQuery.isError ? (
				<div className="flex items-center gap-3 px-4 py-3" role="alert">
					<p className="text-xs text-destructive">{t("shell.couldNotLoadSessions")}</p>
					<TopbarButton disabled={localWorkspaceQuery.isFetching} onClick={() => void localWorkspaceQuery.refetch()}>{t("shell.retry")}</TopbarButton>
				</div>
			) : null}
			{!projectId && failedHostIds.map((failedHostId) => (
				<div className="flex items-center gap-3 px-4 py-3" key={failedHostId} role="alert">
					<p className="text-xs text-destructive">{labelForHost(failedHostId) ?? failedHostId}: {t("shell.couldNotLoadSessions")}</p>
					<TopbarButton onClick={onRetryRemoteHosts}>{t("shell.retry")}</TopbarButton>
				</div>
			))}
			{remoteLoading ? <p className="px-4 py-3 text-xs text-muted-foreground" role="status">{t("shell.loadingSessions")}</p> : null}
			{hostId && projectId && connected && workspace && !orchestrator && !hasConfiguredOrchestratorAgent(workspace) ? (
				<div className="mx-3 my-3 flex items-center gap-3 rounded-md border border-border bg-surface px-3 py-2 text-xs text-muted-foreground">
					<span className="min-w-0 flex-1">{t("remote.configureOrchestratorFirst", { label: labelForHost(hostId) ?? hostId, defaultValue: "Choose an orchestrator agent on {{label}} to start one." })}</span>
					<button type="button" className="shrink-0 rounded-md px-2 py-1 font-medium text-foreground hover:bg-interactive-hover focus-visible:outline-2 focus-visible:outline-ring" onClick={() => shell?.openRemoteProjectSettings(hostId, projectId)}>{t("restoreUnavailable.configureOrchestrator")}</button>
				</div>
			) : null}

			{/* Reserve only the collapsed archive bar. Expanded archive overlays the
			    board so lane height (and Needs You scrollbars) stay stable. */}
			<div className={cn("min-h-0 flex-1 overflow-hidden", hasArchive && archiveToggleOffsetClassName)}>
				{projectId && health.state !== "ok" && (!hostId || connected && (health.state !== "missing" || hasConfiguredOrchestratorAgent(workspace))) ? (
					<div className="mx-3 my-3 flex items-center gap-3 rounded-md border border-border bg-surface px-3 py-2 text-xs text-muted-foreground">
						<AlertTriangle className="size-icon-base shrink-0 text-warning" aria-hidden="true" />
						<span className="min-w-0 flex-1">{health.message}</span>
						{health.state === "restart_needed" || health.state === "duplicates" ? (
							<TopbarButton disabled={isProjectRestarting} onClick={() => void restartOrchestrator()} variant="primary">
								<RotateCw className="size-3.5" aria-hidden="true" />
								{t("shell.restart")}
							</TopbarButton>
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
				{!hostId && projectId && (presentation.workspaceStartupState === "error" || localWorkspaceQuery.isError) ? (
					<div className="flex flex-col items-center gap-3 py-10" role="alert">
						<p className="text-xs text-passive">{t("shell.couldNotLoadSessions")}</p>
					</div>
				) : hostId && !workspace ? null : showWelcome ? (
				<BoardWelcome />
				) : showProjectEmpty ? (
					<ProjectBoardEmpty actions={<ProjectBoardActions actions={projectActions} placement="empty" />} />
				) : (
					<SessionsBoardGridView
						columns={columns}
						key={scopeKey}
						labels={boardLabels}
						sessionKey={boardSessionKey}
						renderSessionCard={(session) => (
							<BoardSessionCardAdapter
								memory={!session.hostId ? memoryBySession?.get(session.id) : undefined}
								memoryTone={!session.hostId ? chipToneOf(session) : undefined}
								onOpen={() => openSession(session)}
								onOpenArtifact={(path) => openSessionArtifact(session, path)}
								onOpenArtifacts={() => openSessionArtifacts(session)}
								onTerminate={sessionIsConnected(session, connectedHosts, hostId) ? ({ deleteArtifacts }) => terminateSession.mutate({ ...session, deleteArtifacts }) : undefined}
								session={session}
								showWorkspace={!projectId}
							/>
						)}
						sessions={boardSessions}
					/>
				)}
			</div>

			{hasArchive ? (
				<BoardArchivePanel
					activeScopeRef={activeScopeRef}
					hostId={hostId}
					scopeKey={scopeKey}
					connectedHosts={connectedHosts}
					sessions={archived}
					showWorkspace={!projectId}
				/>
			) : null}
			{showStartup ? <DaemonStartupLoader /> : null}
		</div>
	);
}

/**
 * Restore state lives here so expand/collapse in SessionsArchiveView does not
 * re-render the kanban columns. In-flight restores are invalidated on project
 * change or unmount so completion cannot navigate after the user left.
 */
const BoardArchivePanel = memo(function BoardArchivePanel({
	activeScopeRef,
	hostId,
	scopeKey,
	connectedHosts,
	sessions,
	showWorkspace,
}: {
	activeScopeRef: React.MutableRefObject<string>;
	hostId?: string;
	scopeKey: string;
	connectedHosts: string[];
	sessions: WorkspaceSession[];
	showWorkspace: boolean;
}) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const diagnostics = useDiagnosticsEnabled();
	const restoreSessionById = useRestoreSession();
	const [restoringSessionKey, setRestoringSessionKey] = useState<string | undefined>();
	const [restoreErrors, setRestoreErrors] = useState<Record<string, string>>({});
	const [restoreUnavailableSession, setRestoreUnavailableSession] = useState<WorkspaceSession | undefined>();
	const restoreGenerationRef = useRef(0);

	useEffect(() => {
		setRestoringSessionKey(undefined);
		setRestoreErrors({});
		setRestoreUnavailableSession(undefined);
		restoreGenerationRef.current += 1;
	}, [scopeKey]);

	useEffect(() => {
		const generation = restoreGenerationRef.current;
		return () => {
			// Invalidate in-flight restores if this panel unmounts (e.g. project with
			// no archive) so completion cannot navigate after the user left.
			if (restoreGenerationRef.current === generation) {
				restoreGenerationRef.current += 1;
			}
		};
	}, []);

	const restoreArchivedSession = async (event: MouseEvent<HTMLButtonElement>, session: WorkspaceSession) => {
		event.stopPropagation();
		if (restoringSessionKey) return;
		const sessionHost = session.hostId ?? hostId;
		const sessionKey = boardSessionKey(session);
		const restoreScopeKey = scopeKey;
		const generation = restoreGenerationRef.current;
		const isStillActiveProject = () =>
			generation === restoreGenerationRef.current &&
			activeScopeRef.current === restoreScopeKey;
		setRestoringSessionKey(sessionKey);
		setRestoreErrors((current) => {
			const next = { ...current };
			delete next[sessionKey];
			return next;
		});
		try {
			const result = await restoreSessionById(session.id, sessionHost);
			if (!isStillActiveProject()) return;
			if (result.status === "success") {
				void navigate(sessionNavigateTarget(session.workspaceId, session.id, sessionHost));
				return;
			}
			if (result.status === "not_resumable") {
				setRestoreUnavailableSession(session);
				return;
			}
			setRestoreErrors((current) => ({ ...current, [sessionKey]: result.message }));
		} finally {
			if (isStillActiveProject()) {
				setRestoringSessionKey(undefined);
			}
		}
	};

	return (
		<>
			<SessionsArchiveView
				labels={{
					archive: t("shell.archive"),
					archiveAria: t("shell.archiveSessionsAria", { count: sessions.length }),
					archivedSessions: t("shell.archivedSessions"),
				}}
				trailing={diagnostics && !hostId ? <AppMemoryIndicator /> : undefined}
				sessionKey={boardSessionKey}
				renderSessionCard={(session) => (
					<ArchivedSessionCardAdapter
						isRestoreDisabled={!sessionIsConnected(session, connectedHosts, hostId) || restoringSessionKey !== undefined}
						isRestoring={restoringSessionKey === boardSessionKey(session)}
						restoreAction={(event) => void restoreArchivedSession(event, session)}
						restoreError={restoreErrors[boardSessionKey(session)]}
						session={session}
						showWorkspace={showWorkspace}
					/>
				)}
				resetKey={scopeKey}
				sessions={sessions}
			/>
			{restoreUnavailableSession ? (
				<RestoreUnavailableDialog
					open={true}
					session={restoreUnavailableSession}
					hostId={restoreUnavailableSession.hostId ?? hostId}
					onOpenChange={(open) => {
						if (!open) setRestoreUnavailableSession(undefined);
					}}
					onRecreated={async () => {
						await queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(restoreUnavailableSession.hostId ?? hostId) });
					}}
				/>
			) : null}
		</>
	);
});
