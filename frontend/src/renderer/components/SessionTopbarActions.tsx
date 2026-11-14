import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNavigate, useParams } from "@tanstack/react-router";
import { ChevronDown, Code2, LayoutDashboard, Play, Plus } from "@/components/app-icons";
import { useState, type ReactNode } from "react";
import { useProjectOrchestratorAction } from "../hooks/useProjectOrchestratorAction";
import { useOrchestratorReplyState } from "../stores/orchestrator-reply-store";
import {
	isOrchestratorSession,
	resolveNextNavigationAfterSessionKill,
	canArchiveWorkerSession,
	sessionCueTargetAvailable,
	CLOUD_PROJECT_KIND,
	isScratchpadWorkspace,
	STANDALONE_WORKSPACE_ID,
	toProjectKind,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";
import { useWorkspaceScope, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { projectNavigateTarget, sessionNavigateTarget } from "../lib/navigate-to-session";
import {
	clearTerminateSessionState,
	useProjectTerminateSessionStates,
	useTerminateSession,
	useTerminateSessionState,
} from "../hooks/useTerminateSession";
import { OrchestratorReplyIcon, orchestratorReplyLabel } from "./OrchestratorReplyIcon";
import { CoderWorkspaceTopbarButton } from "./CoderWorkspaceLinkButton";
import { useSessionMenuAvailable } from "../hooks/useAppMenuCommand";
import { useSessionShortcut } from "../hooks/useAppShortcut";
import { noDragStyle, PageTopbarActions } from "./PageTopbar";
import { Button, GHOST_PILL_CLASS } from "./ui/button";
import { TopbarActionError } from "./TopbarActionError";
import { SessionArchiveDialog, type SessionArchiveChoice } from "./SessionArchiveDialog";
import { TopbarOpenEditorButton } from "./TopbarOpenEditorButton";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { CueRunMenu } from "./chat/CueRunMenu";
import { captureTopbarAction } from "../lib/topbar-telemetry";
import { SessionOwnerTag } from "./SessionOwnerTag";

type SessionTopbarRoute = { hostId?: string; projectId?: string; sessionId?: string };
type SessionTopbarActionsProps = {
	sessionAction?: ReactNode;
	compactActions?: boolean;
	startingOrchestrator?: boolean;
	route?: SessionTopbarRoute;
};

export function SessionTopbarActions({
	sessionAction,
	compactActions = false,
	startingOrchestrator = false,
	route,
}: SessionTopbarActionsProps = {}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const navigate = useNavigate();
	const routeParams = useParams({ strict: false }) as SessionTopbarRoute;
	const params = route ?? routeParams;
	const hostId = params.hostId;
	const currentSessionId = startingOrchestrator ? undefined : params.sessionId;
	const workspaceQuery = useWorkspaceScope(params.projectId, params.sessionId, hostId);
	const workspaceScope = workspaceQuery.data;
	const session = startingOrchestrator ? undefined : workspaceScope?.session;
	const isOrchestrator = startingOrchestrator || (session ? isOrchestratorSession(session) : false);
	const projectId = session?.workspaceId ?? params.projectId;
	const project = workspaceScope?.project;
	const supportsLocalCues = Boolean(!hostId && project && toProjectKind(project.kind));
	const orchestrator = workspaceScope?.orchestrator;
	const supportsProjectActions = !isScratchpadWorkspace(project) && projectId !== STANDALONE_WORKSPACE_ID;
	const projectActions = useProjectOrchestratorAction({
		projectId: supportsProjectActions ? projectId : undefined,
		project: supportsProjectActions ? project : undefined,
		orchestrator: supportsProjectActions ? orchestrator : undefined,
		source: "topbar",
		sessionId: currentSessionId,
		hostId,
	});
	const { isSpawning, isProjectRestarting, isProvisioning, openNewTask, openOrchestrator } = projectActions;
	const orchestratorReply = useOrchestratorReplyState({
		projectId: supportsProjectActions ? projectId : undefined,
		hostId,
		orchestrator: supportsProjectActions ? orchestrator : undefined,
		viewing: isOrchestrator,
	});
	useSessionShortcut("open-orchestrator", () => {
		if (!supportsProjectActions || startingOrchestrator) return;
		openOrchestrator();
	});
	useSessionMenuAvailable("open-orchestrator", !isOrchestrator && supportsProjectActions && !isSpawning && !isProjectRestarting && !isProvisioning);
	const orchestratorTooltip = isProjectRestarting ? t("shell.restarting") : isSpawning
		? t("shell.spawning") : orchestrator ? t("shell.openOrchestrator") : t("shell.spawnOrchestrator");
	const orchestratorTooltipShortcut = !isProjectRestarting && !isSpawning && orchestrator ? "open-orchestrator" : undefined;

	const view = isOrchestrator ? "orchestrator" : "worker";
	const openBoard = () => {
		captureTopbarAction("open_board", view);
		void navigate(projectId ? projectNavigateTarget(projectId, hostId) : { to: "/" });
	};
	const archiveTarget = !isOrchestrator && session && canArchiveWorkerSession(session) ? session : undefined;

	return (
		<PageTopbarActions
			data-compact-actions={compactActions ? "true" : "false"}
			data-testid="workspace-topbar-actions"
		>
			{session ? <SessionOwnerTag session={session} /> : null}
			{isOrchestrator ? <ProjectTerminationFeedback projectId={projectId} hostId={hostId} /> : null}
			{!isOrchestrator && supportsProjectActions ? (
				<Tooltip>
					<TooltipTrigger asChild>
						<span className="inline-flex" style={noDragStyle}>
							<Button type="button" size="sm"
								aria-label={orchestratorReplyLabel(t("shell.openOrchestrator"), orchestratorReply, t)}
								data-priority="secondary"
								disabled={isSpawning || isProjectRestarting || isProvisioning}
								onClick={() => {
									captureTopbarAction("open_orchestrator", view);
									void openOrchestrator();
								}}
								variant="quiet"
							>
								<OrchestratorReplyIcon className="size-icon-md" reply={orchestratorReply} surface="topbar" />
								<span data-compact-label>{t("shell.orchestrator")}</span>
							</Button>
						</span>
					</TooltipTrigger>
					<TooltipContent shortcut={orchestratorTooltipShortcut} side="bottom">{orchestratorReplyLabel(orchestratorTooltip, orchestratorReply, t)}</TooltipContent>
				</Tooltip>
			) : null}
			{sessionAction ? (
				<div className="inline-flex shrink-0 items-center" style={noDragStyle}>
					{sessionAction}
				</div>
			) : null}
			{startingOrchestrator && !hostId ? (
				<>
					{project?.kind !== CLOUD_PROJECT_KIND ? <div aria-hidden="true" className={`${GHOST_PILL_CLASS} inline-flex items-center gap-0 rounded-md before:hidden`} style={noDragStyle}>
						<Button type="button" size="icon-sm" variant="quiet" className="before:hidden" aria-label={t("editor.chooseEditor")} disabled tabIndex={-1}>
							<Code2 className="size-icon-md" aria-hidden="true" />
						</Button>
						<Button type="button" size="icon-sm" variant="quiet" className="before:hidden" aria-label={t("editor.openOptionsAria")} disabled tabIndex={-1}>
							<ChevronDown className="size-icon-sm" aria-hidden="true" />
						</Button>
					</div> : null}
					<span className="inline-flex" style={noDragStyle}>
						<Button type="button" size="icon-sm" variant="quiet" aria-label={t("cues.run")} data-priority="secondary" disabled tabIndex={-1}>
							<Play className="size-icon-md" aria-hidden="true" />
						</Button>
					</span>
				</>
			) : null}
			{session && !hostId && project?.kind !== CLOUD_PROJECT_KIND ? (
				<TopbarOpenEditorButton
					key={`open-workspace-${session.id}`}
					sessionId={session.id}
					projectId={session.workspaceId}
					sessionCreatedAt={session.createdAt}
					sessionTerminated={session.isTerminated}
					style={noDragStyle}
					telemetryView={view}
				/>
			) : null}
			{session?.cloud?.coderWorkspaceUrl ? (
				<CoderWorkspaceTopbarButton
					key={`coder-workspace-${session.id}`}
					url={session.cloud.coderWorkspaceUrl}
					style={noDragStyle}
				/>
			) : null}
			{session && (supportsLocalCues || (!hostId && session.cloud)) ? (
				<span className="inline-flex" style={noDragStyle}>
					<CueRunMenu
						projectId={session.workspaceId}
						sessionId={session.id}
						cloudOrgId={session.cloud?.orgId}
						disabled={!sessionCueTargetAvailable(session)}
						onRun={() => captureTopbarAction("run_cue", view)}
					/>
				</span>
			) : null}
			{archiveTarget ? (
				<SessionArchiveShortcut
					key={archiveTarget.id}
					session={archiveTarget}
					orchestratorId={orchestrator?.id}
					onKilled={(workspaceId) => {
						const workspaces = queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKeyForHost(hostId)) ?? [];
						const fullWorkspace = workspaces.find((w: WorkspaceSummary) => w.id === workspaceId);
						const nextRoute = resolveNextNavigationAfterSessionKill(fullWorkspace, archiveTarget.id);
						if (nextRoute.target === "session") {
							void navigate(sessionNavigateTarget(workspaceId, nextRoute.sessionId, hostId));
							return;
						}
						if (workspaceId === STANDALONE_WORKSPACE_ID || (workspaceId === project?.id && isScratchpadWorkspace(project))) {
							void navigate({ to: "/" });
							return;
						}
						void navigate(projectNavigateTarget(workspaceId, hostId));
					}}
				/>
			) : null}
			{isOrchestrator ? (
				<>
					<Tooltip>
						<TooltipTrigger asChild>
							<Button type="button" size="sm"
								aria-label={t("shell.board")}
								data-priority="secondary"
								disabled={startingOrchestrator}
								onClick={openBoard}
								style={noDragStyle}
								variant="quiet"
							>
								<LayoutDashboard className="size-icon-md" aria-hidden="true" />
								<span data-compact-label>{t("shell.board")}</span>
							</Button>
						</TooltipTrigger>
						<TooltipContent side="bottom">{t("shell.board")}</TooltipContent>
					</Tooltip>
					<Tooltip>
						<TooltipTrigger asChild>
							<span className="inline-flex" style={noDragStyle}>
								<Button type="button" size="sm"
									aria-label={t("shell.newTask")}
									data-priority="primary"
									disabled={startingOrchestrator || isProjectRestarting || isProvisioning}
									onClick={() => {
										captureTopbarAction("new_task", view);
										openNewTask();
									}}
									variant="primary"
								>
									<Plus className="size-icon-md" aria-hidden="true" />
									<span data-compact-label>{t("newTask.task")}</span>
								</Button>
							</span>
						</TooltipTrigger>
						<TooltipContent shortcut="new-session" side="bottom">{t("shell.newTask")}</TooltipContent>
					</Tooltip>
				</>
			) : null}
			{session || startingOrchestrator ? <span aria-hidden="true" className="topbar-divider" data-testid="session-actions-divider" /> : null}
		</PageTopbarActions>
	);
}

export function SessionArchiveShortcut({
	session,
	orchestratorId,
	onKilled,
}: {
	session: WorkspaceSession;
	orchestratorId?: string;
	onKilled: (workspaceId: string, orchestratorId?: string) => void;
}) {
	const [confirmOpen, setConfirmOpen] = useState(false);
	const queryClient = useQueryClient();
	const kill = useTerminateSession();
	const { error, isPending } = useTerminateSessionState(session.id, session.hostId);

	const confirmKill = ({ deleteArtifacts }: SessionArchiveChoice) => {
		setConfirmOpen(false);
		kill.mutate({ ...session, deleteArtifacts });
		onKilled(session.workspaceId, orchestratorId);
	};
	useSessionShortcut("archive-session", () => {
		if (isPending) return;
		clearTerminateSessionState(queryClient, session.id, session.hostId);
		setConfirmOpen(true);
	});
	useSessionMenuAvailable("archive-session", !isPending);

	return (
		<>
			<SessionArchiveDialog onConfirm={confirmKill} onOpenChange={setConfirmOpen} open={confirmOpen} session={session} />
			{error ? <TopbarActionError style={noDragStyle}>{error}</TopbarActionError> : null}
		</>
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
