import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNavigate, useParams } from "@tanstack/react-router";
import { Archive, LayoutDashboard, Plus } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useProjectOrchestratorAction } from "../hooks/useProjectOrchestratorAction";
import {
	isOrchestratorSession,
	resolveNextNavigationAfterSessionKill,
	sessionIsActive,
	sessionCueTargetAvailable,
	CLOUD_PROJECT_KIND,
	STANDALONE_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
	toProjectKind,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";
import { useWorkspaceScope, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { sessionUiKey } from "../lib/hosts";
import { projectNavigateTarget, sessionNavigateTarget } from "../lib/navigate-to-session";
import {
	clearTerminateSessionState,
	useProjectTerminateSessionStates,
	useTerminateSession,
	useTerminateSessionState,
} from "../hooks/useTerminateSession";
import { inspectorIsOpen, useUiStore } from "../stores/ui-store";
import { OrchestratorIcon } from "./icons";
import { CoderWorkspaceTopbarButton } from "./CoderWorkspaceLinkButton";
import { useSessionShortcut } from "../hooks/useAppShortcut";
import { isMacPlatform } from "../lib/platform";
import { PageTopbarActions } from "./PageTopbar";
import { TopbarActionError, TopbarButton } from "./TopbarButton";
import { SessionArchiveDialog, type SessionArchiveChoice } from "./SessionArchiveDialog";
import { TopbarOpenEditorButton } from "./TopbarOpenEditorButton";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { CueRunMenu } from "./chat/CueRunMenu";
import { SessionOwnerTag } from "./SessionOwnerTag";

const noDragStyle = isMacPlatform() ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties) : undefined;

// The action cluster a session's tab strip hosts: open-in-editor, cues, archive,
// and the orchestrator / board / new-task launchers. The page frame itself is
// SessionTabsTopbar (PageTopbar.tsx); this renders only the actions, so it has
// no frame of its own. Orchestrators open the Kanban board and workers open
// their orchestrator.
export function SessionTopbarActions({
	sessionAction,
	compactActions = false,
	startingOrchestrator = false,
}: {
	sessionAction?: ReactNode;
	compactActions?: boolean;
	startingOrchestrator?: boolean;
} = {}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const navigate = useNavigate();
	const params = useParams({ strict: false }) as { hostId?: string; projectId?: string; sessionId?: string };
	const hostId = params.hostId;
	const currentSessionId = startingOrchestrator ? undefined : params.sessionId;
	const workspaceQuery = useWorkspaceScope(params.projectId, params.sessionId, hostId);
	const workspaceScope = workspaceQuery.data;
	const session = startingOrchestrator ? undefined : workspaceScope?.session;
	const isOrchestrator = startingOrchestrator || (session ? isOrchestratorSession(session) : false);
	const isInspectorOpen = useUiStore((state) =>
		currentSessionId ? inspectorIsOpen(state.inspectorSessions, sessionUiKey(currentSessionId, hostId)) : false,
	);
	// The session's workspace wins over the route param so the cross-project
	// /sessions/$sessionId route still resolves its project.
	const projectId = session?.workspaceId ?? params.projectId;
	const project = workspaceScope?.project;
	const supportsLocalCues = Boolean(!hostId && project && toProjectKind(project.kind));
	const orchestrator = workspaceScope?.orchestrator;
	const supportsProjectActions = project?.kind !== STANDALONE_PROJECT_KIND && projectId !== STANDALONE_WORKSPACE_ID;
	const projectActions = useProjectOrchestratorAction({
		projectId: supportsProjectActions ? projectId : undefined,
		project: supportsProjectActions ? project : undefined,
		orchestrator: supportsProjectActions ? orchestrator : undefined,
		source: "topbar",
		sessionId: currentSessionId,
		hostId,
	});
	const { isSpawning, isProjectRestarting, isProvisioning, openNewTask, openOrchestrator } = projectActions;
	// ⌘⇧O / Ctrl+Shift+G does what the Orchestrator button does for the
	// project in scope: open, resume, or spawn. Only one topbar is mounted at
	// a time, so the shortcut fires once. No-op without a project (root board).
	useSessionShortcut("open-orchestrator", () => {
		if (!supportsProjectActions || startingOrchestrator) return;
		openOrchestrator();
	});
	const orchestratorTooltip = isProjectRestarting ? t("shell.restarting") : isSpawning
		? t("shell.spawning") : orchestrator ? t("shell.openOrchestrator") : t("shell.spawnOrchestrator");

	const openBoard = () => void navigate(projectId ? projectNavigateTarget(projectId, hostId) : { to: "/" });

	return (
		<PageTopbarActions
			data-compact-actions={compactActions ? "true" : "false"}
			data-testid="workspace-topbar-actions"
		>
			{isOrchestrator ? <ProjectTerminationFeedback projectId={projectId} hostId={hostId} /> : null}
			{isOrchestrator ? (
				<>
					{sessionAction ? (
						<div className="inline-flex shrink-0 items-center" style={noDragStyle}>
							{sessionAction}
						</div>
					) : null}
					<Tooltip>
						<TooltipTrigger asChild>
							<span className="inline-flex" style={noDragStyle}>
								<TopbarButton
									aria-label={t("shell.newTask")}
									className="topbar-control--labeled"
									data-priority="primary"
									disabled={startingOrchestrator || isProjectRestarting || isProvisioning}
									onClick={openNewTask}
									variant="primary"
								>
									<Plus className="size-icon-md" aria-hidden="true" />
									<span data-compact-label>{t("newTask.task")}</span>
								</TopbarButton>
							</span>
						</TooltipTrigger>
						<TooltipContent side="bottom">{t("shell.newTask")}</TooltipContent>
					</Tooltip>
					<Tooltip>
						<TooltipTrigger asChild>
							<TopbarButton
								aria-label={t("shell.openKanban")}
								disabled={startingOrchestrator}
								className="topbar-control--labeled"
								data-priority="secondary"
								onClick={openBoard}
								style={noDragStyle}
								variant="action"
							>
								<LayoutDashboard className="size-icon-md" aria-hidden="true" />
								<span data-compact-label>{t("shell.openKanban")}</span>
							</TopbarButton>
						</TooltipTrigger>
						<TooltipContent side="bottom">{t("shell.openKanban")}</TooltipContent>
					</Tooltip>
				</>
			) : null}
			{/* Open-in-editor leads the session actions: it is the only
			    non-destructive one, and it must sit left of Kill. Kept outside
			    the local-actions group because Electron main independently
			    reports whether this session has a live workspace. Cloud sessions
			    have no local workspace to hand off to an editor: the local daemon
			    has never heard of them, so querying it just surfaces its 404 as a
			    confusing "Unknown session" error (see workspace.ts's `kind` doc). */}
			{session && !hostId && project?.kind !== CLOUD_PROJECT_KIND ? (
				// Keyed per session so a stale launch error does not carry over
				// when switching sessions. The prefix keeps it distinct from the
				// kill button's key: identical sibling keys make React duplicate
				// the nodes.
				<TopbarOpenEditorButton
					key={`open-workspace-${session.id}`}
					sessionId={session.id}
					projectId={session.workspaceId}
					sessionCreatedAt={session.createdAt}
					sessionTerminated={session.isTerminated}
					style={noDragStyle}
				/>
			) : null}
			{/* A bring-your-own Coder session names the workspace it runs in, so
			    the customer can find it in their own Coder. */}
			{session?.cloud?.coderWorkspaceUrl ? (
				<CoderWorkspaceTopbarButton
					key={`coder-workspace-${session.id}`}
					url={session.cloud.coderWorkspaceUrl}
					style={noDragStyle}
				/>
			) : null}
			{/* Cues run from the topbar into the selected session. */}
			{session && supportsLocalCues ? (
				<span className="inline-flex" style={noDragStyle}>
					<CueRunMenu
						projectId={session.workspaceId}
						sessionId={session.id}
						disabled={!sessionCueTargetAvailable(session)}
					/>
				</span>
			) : null}
			{/* Local worker actions share one tight control group. Navigation
			    remains a separate visual target in the outer top-bar row. */}
			{!isOrchestrator &&
				(sessionAction || (session && !session.cloud && sessionIsActive(session))) ? (
				<div
					className="inline-flex shrink-0 items-center gap-(--gap-topbar-control)"
					data-testid="session-local-actions"
					style={noDragStyle}
				>
					{sessionAction ? <div className="inline-flex shrink-0 items-center">{sessionAction}</div> : null}
					{session && !session.cloud && sessionIsActive(session) ? (
						<TopbarArchiveButton
							key={session.id}
							session={session}
							orchestratorId={orchestrator?.id}
							onKilled={(workspaceId) => {
								const workspaces = queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKeyForHost(hostId)) ?? [];
								const fullWorkspace = workspaces.find((w: WorkspaceSummary) => w.id === workspaceId);
								const nextRoute = resolveNextNavigationAfterSessionKill(fullWorkspace, session.id);
								if (nextRoute.target === "session") {
									void navigate(sessionNavigateTarget(workspaceId, nextRoute.sessionId, hostId));
									return;
								}
								if (workspaceId === STANDALONE_WORKSPACE_ID) {
									void navigate({ to: "/" });
									return;
								}
								void navigate(projectNavigateTarget(workspaceId, hostId));
							}}
						/>
					) : null}
				</div>
			) : null}
			{!isOrchestrator && supportsProjectActions ? (
				<Tooltip>
					<TooltipTrigger asChild>
						<span className="inline-flex" style={noDragStyle}>
							<TopbarButton
								aria-label={t("shell.openOrchestrator")}
								className="topbar-control--labeled"
								data-priority="secondary"
								disabled={isSpawning || isProjectRestarting || isProvisioning}
								onClick={() => void openOrchestrator()}
								variant="primary"
							>
								<OrchestratorIcon className="size-icon-md" aria-hidden="true" />
								<span data-compact-label>{t("shell.orchestrator")}</span>
							</TopbarButton>
						</span>
					</TooltipTrigger>
					<TooltipContent side="bottom">{orchestratorTooltip}</TooltipContent>
				</Tooltip>
			) : null}
			{/* The pinned controls are owned by SessionView so they stay at the
			    window's right edge. Reserve their width only when the rail is closed. */}
			{startingOrchestrator ? null : <div
				className="session-pinned-actions-reserve"
				data-state={isInspectorOpen ? "collapsed" : "expanded"}
				data-testid="session-pinned-actions-reserve"
				aria-hidden="true"
			/>}
		</PageTopbarActions>
	);
}

// Confirmation is modal, but teardown progress is not: confirming closes the
// dialog and returns to the project's orchestrator while the daemon finishes.
// The control archives rather than deletes, so it carries the Archive icon and
// the neutral icon treatment instead of the danger-red kill affordance.
// Mutation-cache state is filtered by worker ID so rapid route switches never
// carry another worker's Archiving/error state into the current topbar.
export function TopbarArchiveButton({
	session,
	orchestratorId,
	onKilled,
}: {
	session: WorkspaceSession;
	orchestratorId?: string;
	onKilled: (workspaceId: string, orchestratorId?: string) => void;
}) {
	const { t } = useTranslation();
	const [confirmOpen, setConfirmOpen] = useState(false);
	const queryClient = useQueryClient();
	const kill = useTerminateSession();
	const { error, isPending } = useTerminateSessionState(session.id, session.hostId);

	const confirmKill = ({ deleteArtifacts }: SessionArchiveChoice) => {
		setConfirmOpen(false);
		kill.mutate({ ...session, deleteArtifacts });
		onKilled(session.workspaceId, orchestratorId);
	};
	// ⌘⇧A opens the same confirmation the button does.
	useSessionShortcut("archive-session", () => {
		if (isPending) return;
		clearTerminateSessionState(queryClient, session.id, session.hostId);
		setConfirmOpen(true);
	});

	return (
		<div className="inline-flex items-center gap-1.5" style={noDragStyle}>
			<Tooltip>
				<TooltipTrigger asChild>
					<span className="inline-flex">
						<SessionArchiveDialog
							onConfirm={confirmKill}
							onOpenChange={setConfirmOpen}
							open={confirmOpen}
							session={session}
							trigger={
								<TopbarButton
									aria-label={isPending ? t("shell.archiving") : t("shell.archiveSession")}
									disabled={isPending}
									onClick={() => {
										clearTerminateSessionState(queryClient, session.id, session.hostId);
										// Always open the confirm; the modal owns its own dismissal.
										setConfirmOpen(true);
									}}
									variant="icon"
								>
									<Archive className="size-icon-md" aria-hidden="true" />
								</TopbarButton>
							}
						/>
					</span>
				</TooltipTrigger>
				<TooltipContent side="bottom">{t("shell.archiveSession")}</TooltipContent>
			</Tooltip>
			{error ? <TopbarActionError>{error}</TopbarActionError> : null}
		</div>
	);
}

export function ProjectTerminationFeedback({ projectId, hostId }: { projectId: string | undefined; hostId?: string }) {
	const { t } = useTranslation();
	const states = useProjectTerminateSessionStates(projectId, hostId);
	if (states.length === 0) return null;

	return (
		<div aria-label={t("shell.sessionTerminationStatus")} className="flex max-w-content-max items-center gap-2">
			{states.map((state) =>
				state.error ? (
					<TopbarActionError className="max-w-48 truncate" key={state.session.id} title={`${state.session.title}: ${state.error}`}>
						{state.error}
					</TopbarActionError>
				) : (
					<span
						className="max-w-40 truncate text-caption text-muted-foreground"
						key={state.session.id}
						role="status"
						title={t(state.cleanupPending ? "shell.workspaceCleanupPending" : "shell.archivingNamed", { title: state.session.title })}
					>
						{t(state.cleanupPending ? "shell.workspaceCleanupPending" : "shell.archivingNamed", { title: state.session.title })}
					</span>
				),
			)}
		</div>
	);
}
