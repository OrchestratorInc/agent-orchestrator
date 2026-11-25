import { useTelemetrySurface } from "../lib/ui-usage-telemetry";
import type { UiSurface } from "../lib/ui-surfaces";
import { apiErrorException } from "../lib/api-error-exception";
import { BrowserIcon, SubagentIcon } from "./icons";
import { GitHubIcon } from "./GitHubIcon";
import { artifactFileIcon } from "./artifact-file-icon";
import { ArtifactShareMark } from "./ArtifactShareMark";
import { useReconciledShareLinks, type ShareAccessByUrl } from "./ShareArtifactPopover";
import { Chat, CheckWaves, Folder, List, Package, Smartphone, SquareTerminal } from "@/components/app-icons";
import { DevicePanel } from "./DevicePanel";
import { useDevicesEnabled } from "../lib/devices-enabled";
import { AppLink } from "./AppLink";
import { buildReviewBlocks, type ReviewComment, type ReviewPRBlock } from "../lib/review-comments";
import { ReviewCommentsSection, type ReviewCommentActions } from "./review/ReviewComments";
import { ArtifactThumbnail } from "./ArtifactThumbnail";
import type { ArtifactRow } from "../lib/artifacts";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNavigate } from "@tanstack/react-router";
import { memo, useCallback, useEffect, useLayoutEffect, useRef, useState, type ComponentProps, type ReactNode } from "react";
import {
	InspectorActivityTimelineView,
	InspectorPullRequestCardView,
	InspectorSection as Section,
	SessionInspectorShellView,
	SessionInspectorSummaryView,
	inspectorEmptyClass,
	type InspectorPullRequest,
	type InspectorTimelineEvent,
	type InspectorView,
} from "@aoagents/product-ui";
import {
	Archive,
	ArrowUpRight,
	CircleStop,
	GitPullRequest,
	GitMerge,
	Info,
	Loader2,
	MoreVertical,
} from "@/components/app-icons";
import { InfoRow, InfoRowLabelButton, InfoRowLink } from "./InspectorInfoRow";
import type { components } from "../../api/schema";
import { usePRDetailsLoad } from "../hooks/usePRDetailsLoad";
import { usePRNoChecksClock } from "../hooks/usePRNoChecksClock";
import { interfaceTransitionIsActive, useSessionInterfaceTransitionStatus } from "../hooks/useSessionInterfaceTransition";
import { reviewerConversationQueryKey } from "../hooks/useReviewerConversation";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { sessionUiKey } from "../lib/hosts";
import { useBackgroundTasks, useReleaseTaskOutputs } from "../hooks/useBackgroundTasks";
import { BackgroundTasksPanel } from "./BackgroundTaskPanel";
import { supportsLocalBackgroundTasks } from "./BackgroundTasks";
import { projectNavigateTarget, sessionNavigateTarget } from "../lib/navigate-to-session";
import { defaultReviewerHarness, WORKER_DEFAULT_REVIEWERS } from "../lib/reviewer-harnesses";
import { cloudSessionsQueryKey, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { captureRendererEvent } from "../lib/telemetry";
import { formatTimeCompact } from "../lib/format-time";
import { AgentAvatar } from "./AgentAvatar";
import { SessionArtifactsView, type ArtifactOpenRequest } from "./SessionArtifactsView";
import { OrchestratorChildrenSection } from "./OrchestratorChildrenSection";
import { SessionForksSection } from "./SessionForksSection";
import { ProductExternalLink } from "./ProductExternalLink";
import { openPRTab } from "../lib/pr-tabs";
import { usePRViewEnabled } from "../lib/pr-view";
import { ClassicSessionBranchSummary } from "./ClassicSessionBranchSummary";
import { CopyButton } from "./chat/CopyButton";
import { ScriptRunPanel } from "./ScriptRunPanel";
import { useCloudScriptRuns } from "../hooks/useCloudScriptRuns";
import { latestScriptRun } from "../lib/cloud-script-runs";
import { ResumeAgentControl } from "./ResumeAgentControl";
import { SessionBranchSummary, useOtherPRConflictStep, type BranchWork } from "./SessionBranchSummary";
import { SessionAccountSummary } from "./SessionAccountSummary";
import { CloudPRGitStepButton, PRActionButton } from "./PRActions";
import { PRPills, prFocusKey, prStateIcon, sortSessionPRs } from "./PRPills";
import { PRChecksGroups, PRLabelsAndReviewers } from "./PRCardDetails";
import {
	sessionScmSummaryQueryKey,
	useSessionScmSummary,
	type SessionPRReference,
	type SessionPRSummary,
} from "../hooks/useSessionScmSummary";
import { sessionWorkspaceFilesQueryKey, sessionWorkspaceHistoryQueryOptions, useSessionWorkspaceFilesChangedCount } from "../hooks/useSessionWorkspaceFiles";
import { useCloudCp } from "../hooks/useCloudCp";
import { cloudWorkspaceReviewSummaryQueryOptions } from "../hooks/useCloudWorkspaceReview";
import { useCloudWorkspaceReady } from "../hooks/useCloudWorkspaceReady";
import { useSendToSessionWorker } from "../hooks/useSendToSessionWorker";
import { sanitizeWorkerMessagePart } from "../lib/worker-message";
import { pendingMergeRequestFor, useMergeRequestActions, useProjectMergeRequests } from "../hooks/useProjectMergeRequests";
import { useSessionBrowserLink } from "../hooks/useSessionBrowserLink";
import { clearTerminateSessionState, useTerminateSession } from "../hooks/useTerminateSession";
import { prBrowserUrl, prCanMerge, prCardPresentation, prHasNoChecks, prNounKeys, prRepositoryFromUrl, prsSpanRepositories, sessionBranchPR, sessionPRDisplaySummaries, prRepositoryShortName } from "../lib/pr-display";
import type { SessionArtifact, WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import {
	openPRs,
	resolveNextNavigationAfterSessionKill,
	sessionDeliverables,
	sessionWorkspaceReady,
	sortedPRs,
	STANDALONE_WORKSPACE_ID,
} from "../types/workspace";
import { getAgentActivityView, getSessionTimelinePillView } from "../lib/session-presentation";
import { BrowserPanelView, type BrowserAnnotationQueueModel } from "./BrowserPanel";
import type { BrowserViewModel } from "../hooks/useBrowserView";
import { FilesActionsHostContext, FilesTopbarHostContext } from "./files-topbar-host";
import { useUiStore } from "../stores/ui-store";
import { Button } from "./ui/button";
import { Pressable } from "./ui/pressable";
import { cn } from "../lib/utils";
import { ConfirmDialog } from "./ConfirmDialog";
import { SessionArchiveDialog, type SessionArchiveChoice } from "./SessionArchiveDialog";
import { CloudReviewerPicker, ReviewerPicker, type ReviewerModelConfig } from "./ReviewerPicker";
import { agentLabel } from "../lib/agent-options";
import { useAgentReadinessQuery, useEnsureAgentReadiness } from "../hooks/useAgentReadinessQuery";
import { Switch } from "./ui/switch";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { appI18n } from "../i18n";
import type { MessageKey } from "../i18n";
import { usesPreviewWorkspaceData as usePreviewData } from "../lib/preview-mode";
import {
	openReviewStatesFor,
	reviewIsRunning,
	reviewPhase,
	reviewPhaseRun,
	reviewRunDisabled,
	sessionReviewsQueryOptions,
	sessionReviewsQueryKey,
	type PRReviewState,
	type ReviewRunFacts,
} from "../lib/session-reviews";
import type { CloudCpAOReviewRun, CloudCpProjectRepo, CloudCpSessionReviewState, CloudCpPullRequestSummary } from "../lib/cloud-cp";

const INSPECTOR_SURFACES: Record<InspectorView, UiSurface> = {
	summary: "inspector_summary",
	reviews: "inspector_reviews",
	browser: "inspector_browser",
	device: "inspector_device",
	sideChat: "inspector_side_chat",
	subagents: "inspector_subagents",
	files: "inspector_files",
	artifacts: "inspector_artifacts",
	task: "inspector_background_tasks",
};

type ProjectConfig = components["schemas"]["ProjectConfig"];
type OpenReviewerTerminal = (target: { handleId: string; harness: string }) => void;
type ReviewerSurface = components["schemas"]["DomainReviewerSurface"];

export type { InspectorView } from "@aoagents/product-ui";

const VIEW_DEFS: {
	id: InspectorView;
	labelKey: "inspector.summary" | "inspector.reviewTab" | "inspector.browser" | "inspector.device" | "inspector.sideChat" | "inspector.subagents" | "inspector.files" | "artifacts.title" | "backgroundTasks.title";
	icon: ReactNode;
}[] = [
	{ id: "summary", labelKey: "inspector.summary", icon: <List /> },
	{ id: "files", labelKey: "inspector.files", icon: <Folder data-testid="files-viewer-icon" /> },
	{ id: "reviews", labelKey: "inspector.reviewTab", icon: <CheckWaves /> },
	{ id: "browser", labelKey: "inspector.browser", icon: <BrowserIcon aria-hidden="true" /> },
	{ id: "device", labelKey: "inspector.device", icon: <Smartphone aria-hidden="true" /> },
	{ id: "sideChat", labelKey: "inspector.sideChat", icon: <Chat /> },
	{ id: "subagents", labelKey: "inspector.subagents", icon: <SubagentIcon aria-hidden="true" /> },
	{ id: "artifacts", labelKey: "artifacts.title", icon: <Package /> },
	{ id: "task", labelKey: "backgroundTasks.title", icon: <SquareTerminal /> },
];

const prStateLabelKeys: Record<SessionPRSummary["state"], MessageKey> = {
	open: "pr.state.open",
	draft: "pr.state.draft",
	merged: "pr.state.merged",
	closed: "pr.state.closed",
};

/**
 * Tabbed inspector rail beside the terminal (Summary · Files · Reviews · Browser · Side chat · Artifacts).
 */
export const SessionInspector = memo(function SessionInspector({
	orchestrator = false,
	session,
	hostId: hostIdProp,
	onOpenReviewerTerminal,
	browserPoppedOut = false,
	browserAnnotationQueue,
	isInspectorVisible = true,
	onToggleBrowserPopOut,
	artifactRequest,
	onArtifactRequestConsumed,
	onOpenArtifact,
	onOpenFiles,
	onOpenReviewFile,
	onOpenReviewerChat,
	onWorkerMessageSent,
	filesView,
	browserView,
	sideChatView,
	subagentsView,
	view: viewProp,
	onViewChange,
	standaloneAgent = false,
}: {
	/** An orchestrator's rail has Summary, Browser, and contextual Artifacts. */
	orchestrator?: boolean;
	session?: WorkspaceSession;
	/** The session is a Cloud standalone agent's, alone in its own project. */
	standaloneAgent?: boolean;
	hostId?: string;
	onOpenReviewerTerminal?: OpenReviewerTerminal;
	browserPoppedOut?: boolean;
	browserAnnotationQueue?: BrowserAnnotationQueueModel;
	isInspectorVisible?: boolean;
	onToggleBrowserPopOut?: (next: boolean) => void;
	/** The artifact the Artifacts view should show, e.g. one clicked in the Summary. */
	artifactRequest?: ArtifactOpenRequest | null;
	onArtifactRequestConsumed?: (key: number) => void;
	onOpenArtifact?: (target: { feedback?: boolean; path: string }) => void;
	onOpenFiles?: () => void;
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	onOpenReviewerChat?: (reviewId: string) => void;
	onWorkerMessageSent?: () => void;
	filesView?: ReactNode;
	browserView?: BrowserViewModel;
	sideChatView?: ReactNode;
	/** The chat's Subagents panel; the tab shows only while the chat has subagents to list. */
	subagentsView?: ReactNode;
	/** Controlled active tab. Omit to let the inspector own its own selection. */
	view?: InspectorView;
	onViewChange?: (view: InspectorView) => void;
}) {
	const { t } = useTranslation();
	const hostId = hostIdProp ?? session?.hostId;
	const [internalView, setInternalView] = useState<InspectorView>("summary");
	const [browserTopbarHost, setBrowserTopbarHost] = useState<HTMLDivElement | null>(null);
	const [filesTopbarHost, setFilesTopbarHost] = useState<HTMLDivElement | null>(null);
	const [filesActionsHost, setFilesActionsHost] = useState<HTMLDivElement | null>(null);
	const requestedView = viewProp ?? internalView;
	// Badge the Browser tab when a preview target arrived without us opening it.
	const browserUnseen = useUiStore((state) =>
		session ? Boolean(state.inspectorSessions[sessionUiKey(session.id, hostId)]?.browserUnseen) : false,
	);
	const devicesEnabled = useDevicesEnabled();
	// Badge the Artifacts tab when an artifact was registered while Browser or Files was in use.
	const artifactsUnseen = useUiStore((state) =>
		session ? Boolean(state.inspectorSessions[sessionUiKey(session.id, hostId)]?.artifactsUnseen) : false,
	);
	// The Background tasks tab: every task this chat ran, and one at a time when opened.
	const uiKey = session ? sessionUiKey(session.id, hostId) : "";
	const taskTabs = useUiStore((state) => (uiKey ? state.inspectorSessions[uiKey]?.taskTabs : undefined));
	useReleaseTaskOutputs(session?.id ?? "", taskTabs);
	const activeTaskTabId = useUiStore((state) => (uiKey ? state.inspectorSessions[uiKey]?.activeTaskTab : undefined));
	const activeTaskTab = taskTabs?.find((tab) => tab.task.id === activeTaskTabId);
	// Only a local chat reports tasks: a session on another host (`hostId` prop or field) never polls this daemon.
	const tasksWatchable = Boolean(session && !hostId && supportsLocalBackgroundTasks(session));
	// Polls every 2 s only while the tab shows; each reading asks the provider.
	const tasksQuery = useBackgroundTasks(session?.id ?? "", tasksWatchable, requestedView === "task" && isInspectorVisible);
	const tasksReading = tasksQuery.data;
	// A failed refresh keeps the last reading: still shown, but marked stale.
	const tasksStale = tasksQuery.isError && tasksReading !== undefined;
	useEffect(() => {
		if (!uiKey) return;
		// An ended session sends no more readings, and an unavailable one means its
		// agent isn't reporting: a task still shown as live would offer a Stop that
		// can't land. Mark it ended; a reading that lists it again restores it.
		if (!tasksWatchable || tasksReading?.availability === "unavailable") useUiStore.getState().syncTaskTabs(uiKey, []);
		else if (tasksReading) useUiStore.getState().syncTaskTabs(uiKey, tasksReading.tasks);
	}, [tasksReading, tasksWatchable, uiKey]);
	const inspectorQueryClient = useQueryClient();
	// An imported chat has no AO workspace until it is opened and resumes.
	const localFilesChangedCount = useSessionWorkspaceFilesChangedCount(
		orchestrator || session?.cloud || session?.needsResume || (session && !sessionWorkspaceReady(session)) ? undefined : session?.id,
		hostId,
	);
	const localWorkspaceData = session ? inspectorQueryClient.getQueryData<{ files?: unknown[] }>(sessionWorkspaceFilesQueryKey(session.id, hostId)) : undefined;
	const { client: cloudCpClient, ready: cloudReady, baseUrl: cloudBaseUrl } = useCloudCp();
	const cloudOrgId = session?.cloud?.orgId;
	const cloudWorkspaceReady = useCloudWorkspaceReady(session);
	const cloudReview = useQuery({
		...cloudWorkspaceReviewSummaryQueryOptions({
			client: cloudCpClient, baseUrl: cloudBaseUrl, orgId: cloudOrgId ?? "", sessionId: session?.id ?? "",
			// Only a worker with its workspace ready answers for its files, as in
			// the Files panel: a waking or starting session's count is not polled
			// until it is, and the count read before stays on the tab meanwhile.
			enabled: cloudReady && session?.cloud !== undefined && cloudOrgId !== undefined && cloudWorkspaceReady,
		}),
	});
	const cloudFilesChangedCount = cloudReview.data?.summary.files;
	const filesChangedCount = session?.cloud ? (cloudFilesChangedCount ? cloudFilesChangedCount : undefined) : localFilesChangedCount;
	const hasWorkspaceInventory = session?.cloud ? Boolean(cloudReview.data?.files.length) : Boolean(localWorkspaceData?.files?.length);
	// An artifact link can open the Artifacts tab before a session refresh lists
	// the file. The request keeps the tab available until the file is listed or
	// another tab is picked.
	const [settledArtifactRequestKey, setSettledArtifactRequestKey] = useState<number>();
	const artifactRequestPending = artifactRequest != null && artifactRequest.key !== settledArtifactRequestKey;
	useEffect(() => {
		if (artifactRequest && session?.artifactFiles?.some((file) => file.path === artifactRequest.path)) {
			setSettledArtifactRequestKey(artifactRequest.key);
		}
	}, [artifactRequest, session?.artifactFiles]);
	const setView = useCallback((next: InspectorView) => {
		setInternalView(next);
		onViewChange?.(next);
		if (next === "files") onOpenFiles?.();
		if (next !== "artifacts" && artifactRequest) setSettledArtifactRequestKey(artifactRequest.key);
	}, [artifactRequest, onOpenFiles, onViewChange]);
	const openReviews = useCallback(() => setView("reviews"), [setView]);
	const openFilesView = useCallback(() => setView("files"), [setView]);
	// A persisted/controlled Reviews selection can outlive the last reviewable PR.
	// Keep the shell on a real, visible tab instead of rendering an empty, unlabelled body.
	const reviewsAvailable = reviewsTabVisible(session);
	// Simulators and emulators run on this machine: the Device tab serves
	// local worker sessions only until remote and cloud providers exist, and
	// only with Developer mode on.
	const deviceAvailable = devicesEnabled && session !== undefined && !session.cloud && !hostId && !orchestrator;
	// The orchestrator's rail also offers its chat's Side chat and Subagents panels
	// while the host passes them, and Background tasks once its composer strip has
	// opened one. Its chat accepts /btw, so Side chat must be able to open here.
	const availableViewDefs = orchestrator ? VIEW_DEFS.filter((entry) =>
		entry.id === "summary" || entry.id === "browser"
		|| (entry.id === "sideChat" && sideChatView !== undefined)
		|| (entry.id === "subagents" && subagentsView !== undefined)
		|| (entry.id === "task" && Boolean(taskTabs?.length))
		|| (entry.id === "artifacts" && ((session?.artifactFiles?.length ?? 0) > 0 || artifactRequestPending)))
		: VIEW_DEFS.filter((entry) => (!hostId || entry.id !== "browser" || browserView) && (reviewsAvailable || entry.id !== "reviews")
			// The host passes a side chat view only where side chats can run.
			&& (entry.id !== "sideChat" || sideChatView !== undefined)
			&& (entry.id !== "subagents" || subagentsView !== undefined)
			&& (entry.id !== "device" || deviceAvailable)
			// Artifacts shows whenever the session lists files, or while an artifact
			// link waits for the list to refresh.
			&& (entry.id !== "artifacts" || (session?.artifactFiles?.length ?? 0) > 0 || artifactRequestPending)
			// Background tasks shows once the chat has run one.
			&& (entry.id !== "task" || Boolean(taskTabs?.length)));
	const view: InspectorView = availableViewDefs.some((entry) => entry.id === requestedView) ? requestedView : "summary";
	useTelemetrySurface(INSPECTOR_SURFACES[view], Boolean(isInspectorVisible));
	// A failure shown in its tab has been seen: the composer strip stops raising it.
	const shownFailure = view === "task" && isInspectorVisible && activeTaskTab && !activeTaskTab.ended && activeTaskTab.task.state === "failed"
		? activeTaskTab.task
		: undefined;
	useEffect(() => {
		if (uiKey && shownFailure) useUiStore.getState().acknowledgeFailedTask(uiKey, shownFailure);
	}, [shownFailure, uiKey]);
	const sessionLoaded = session !== undefined;
	// The chat reports its subagents just after it mounts, so on the way back to a
	// session the Subagents tab is briefly missing. Show Summary through that gap but
	// keep the reader's choice, so the tab comes back selected.
	const awaitingSubagents = requestedView === "subagents" && subagentsView === undefined;
	useEffect(() => {
		// Until the session loads, its tabs are unknown: keep the requested view.
		if (view === requestedView || (!sessionLoaded && !orchestrator) || awaitingSubagents) return;
		setInternalView(view);
		onViewChange?.(view);
	}, [awaitingSubagents, onViewChange, orchestrator, requestedView, sessionLoaded, view]);
	const tabs = availableViewDefs.map((entry) => {
		const label = t(entry.labelKey);
		return {
			...entry,
			badge: (entry.id === "browser" && browserUnseen) || (entry.id === "artifacts" && artifactsUnseen),
			count: entry.id === "files" ? filesChangedCount : undefined,
			displayLabel:
				entry.id === "files" && filesChangedCount !== undefined && (filesChangedCount > 0 || hasWorkspaceInventory)
					? t("files.tabCount", { count: filesChangedCount })
					: label,
			label,
		};
	});
	return (
		// SessionInspectorShellView (packages/product-ui) doesn't accept a
		// className, but styles.css's native-composition transparency cascade
		// targets a `.session-inspector` ancestor around it (to punch a
		// see-through hole for the live browser page when the compositor's
		// shell is raised for an overlay). `contents` keeps this wrapper out of
		// layout/flex entirely — it exists purely as a CSS selector anchor.
		<div className="session-inspector contents">
			<SessionInspectorShellView
				sideChatView={sideChatView ?? <p className="p-4 text-xs text-muted-foreground">{t("sideChat.unavailable")}</p>}
				subagentsView={subagentsView}
				activeView={view}
				ariaLabel={t("inspector.aria")}
				artifactsView={
					session ? (
						<SessionArtifactsView active={isInspectorVisible && view === "artifacts"} hostId={hostId} key={sessionUiKey(session.id, hostId)} onRequestConsumed={onArtifactRequestConsumed} request={artifactRequest} session={session} />
					) : undefined
				}
				browserPoppedOut={browserPoppedOut}
				deviceView={session && deviceAvailable ? <DevicePanel isActive={isInspectorVisible} key={session.id} sessionId={session.id} /> : undefined}
				browserView={
					session && (!hostId || browserView) ? (
						<BrowserView
							browserPoppedOut={browserPoppedOut}
							browserAnnotationQueue={browserAnnotationQueue}
							browserView={browserView}
							isActive={isInspectorVisible && !browserPoppedOut}
							onTogglePopOut={onToggleBrowserPopOut}
							session={session}
							topbarHost={browserTopbarHost}
						/>
					) : undefined
				}
				filesView={
					session ? (
						<FilesTopbarHostContext.Provider value={filesTopbarHost}>
							<FilesActionsHostContext.Provider value={filesActionsHost}>
								<FilesView filesView={filesView} onOpenFiles={onOpenFiles} />
							</FilesActionsHostContext.Provider>
						</FilesTopbarHostContext.Provider>
					) : undefined
				}
				headerActions={
					<>
						{view === "browser" && !browserPoppedOut ? (
							<div className="browser-panel__topbar-host min-w-0 flex-1" ref={setBrowserTopbarHost} />
						) : view === "files" && filesView ? (
							<div className="files-panel__topbar-host min-w-0 flex-1" ref={setFilesTopbarHost} />
						) : null}
						<div className="session-inspector__trailing flex shrink-0 items-center">
							{view === "files" && filesView ? <div className="session-inspector__files-actions flex items-center gap-1" ref={setFilesActionsHost} /> : null}
						</div>
					</>
				}
				isVisible={isInspectorVisible}
				loadingText={session ? undefined : t("inspector.loadingSession")}
				onViewChange={setView}
				reviewsView={
					session ? <ReviewsView hostId={hostId} key={sessionUiKey(session.id, hostId)} onOpenReviewFile={onOpenReviewFile} onOpenReviewerTerminal={onOpenReviewerTerminal} onOpenReviewerChat={onOpenReviewerChat} onWorkerMessageSent={onWorkerMessageSent} session={session} /> : undefined
				}
				summaryView={
					session ? (
						<SummaryView
							canOpenReviews={reviewsAvailable}
							hostId={hostId}
							inspectorVisible={isInspectorVisible}
							key={sessionUiKey(session.id, hostId)}
							onOpenArtifact={onOpenArtifact}
							onOpenFiles={openFilesView}
							onOpenReviews={openReviews}
							onOpenReviewFile={onOpenReviewFile}
							orchestrator={orchestrator}
							session={session}
							standaloneAgent={standaloneAgent}
						/>
					) : undefined
				}
				tabs={tabs}
				taskView={session && taskTabs?.length ? (
					<BackgroundTasksPanel isVisible={isInspectorVisible} openTask={activeTaskTab} sessionId={session.id} stale={tasksStale} tasks={taskTabs} uiKey={uiKey} />
				) : undefined}
			/>
		</div>
	);
});

function reviewsTabVisible(session: WorkspaceSession | undefined): boolean {
	if (!session) return true;
	// Cloud PR facts are queried directly by CloudReviewsSection instead of
	// piggybacking on the local daemon workspace projection.
	if (session.cloud) return true;
	const prs = sortedPRs(session);
	// A worker that has not opened a PR yet shows where reviews will start; a
	// session whose PRs all merged or closed has nothing left to review.
	if (prs.length === 0) return session.workspaceId !== STANDALONE_WORKSPACE_ID;
	return prs.some((pr) => pr.state === "open" || pr.state === "draft");
}

const SummaryView = memo(function SummaryView({
	canOpenReviews,
	onOpenArtifact,
	hostId,
	inspectorVisible,
	onOpenFiles,
	onOpenReviewFile,
	onOpenReviews,
	orchestrator,
	session,
	standaloneAgent = false,
}: {
	canOpenReviews: boolean;
	onOpenArtifact?: (target: { feedback?: boolean; path: string }) => void;
	hostId?: string;
	/** The Summary stays mounted while the inspector is closed. */
	inspectorVisible: boolean;
	onOpenFiles: () => void;
	/** Opens a file's diff at a line, for a review thread's "View in diff". */
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	onOpenReviews: () => void;
	orchestrator: boolean;
	session: WorkspaceSession;
	standaloneAgent?: boolean;
}) {
	const { t } = useTranslation();
	const query = useSessionScmSummary(session.id, true, session.cloud?.orgId, session.cloud ? session.autoInjectCI === true : false, hostId);
	const linkedPRs = query.data?.linkedPrs ?? [];
	const projectQuery = useQuery({
		queryKey: hostId ? ["project", hostId, session.workspaceId] : ["project", session.workspaceId],
		enabled: linkedPRs.length > 0 && !session.cloud,
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/projects/{id}", {
				params: { path: { id: session.workspaceId } },
			});
			if (error) throw error;
			return data?.status === "ok" && "repo" in data.project ? data.project : undefined;
		},
	});
	// A Cloud project's repositories come from the control plane: its own and
	// the additional ones its sessions check out.
	const { client: cloudClient, ready: cloudReady, baseUrl: cloudBaseUrl } = useCloudCp(Boolean(session.cloud));
	const cloudProjectQuery = useQuery({
		queryKey: ["cloud-project-repositories", cloudBaseUrl, session.cloud?.orgId ?? "", session.workspaceId],
		enabled: linkedPRs.length > 0 && Boolean(session.cloud) && cloudReady,
		staleTime: 60_000,
		queryFn: async () => {
			const { project } = await cloudClient.getProject(session.cloud!.orgId, session.workspaceId);
			const repos = (project.config as { repos?: CloudCpProjectRepo[] } | undefined)?.repos ?? [];
			return [project.repositoryUrl, ...repos.map((repo) => repo.url)];
		},
	});
	const projectRepositories = session.cloud ? cloudProjectQuery.data : projectRepositoryUrls(projectQuery.data);
	const prSummaries = sessionPRDisplaySummaries(session, query.data?.prs);
	const prCount = prSummaries.length + linkedPRs.length;
	const hasPRs = prCount > 0;
	const artifacts = sessionDeliverables(session);
	const hasArtifacts = artifacts.length > 0;
	const opensPR = hasPRs || session.outputType === "pr" || session.outputType === "pr_artifact";
	const prSectionTitle = prCount > 1 ? t("inspector.pullRequests", { count: prCount }) : opensPR ? t("inspector.pullRequest") : t("inspector.branch");
	const artifactSectionTitle = artifacts.length > 1
		? t("inspector.artifacts", { count: artifacts.length })
		: t("inspector.artifact");
	const branchPR = sessionBranchPR(session, prSummaries);
	const openPRNumber = branchPR?.number;
	// One PR on screen: the one picked, else the PR the branch updates, else the
	// newest open one; with several, pills over the card pick another. Until a
	// pick, it follows the branch, which settles once the PR details load.
	const focusStore = `${hostId ?? ""}:${session.id}`;
	const [focusChoice, setFocusChoice] = useState<string | undefined>(() => prFocus.get(focusStore));
	const focusedPR = prSummaries.find((pr) => prFocusKey(pr) === focusChoice)
		?? branchPR
		?? sortSessionPRs(prSummaries)[0];
	const focusPR = (pr: SessionPRSummary) => {
		prFocus.set(focusStore, prFocusKey(pr));
		setFocusChoice(prFocusKey(pr));
	};
	const focusedIsBranchPR = Boolean(branchPR && focusedPR && prFocusKey(focusedPR) === prFocusKey(branchPR));
	// Another PR of the session that conflicts: its card hands that to the agent.
	// A Cloud session has no local branch rows to plan from, so its focused PR,
	// the branch's own included, takes the same step; a guest leaves it to the owner.
	const conflictStepPR = session.cloud ? (session.cloud.shared ? undefined : focusedPR) : focusedIsBranchPR ? undefined : focusedPR;
	const otherPRConflict = useOtherPRConflictStep(session, hostId, conflictStepPR);
	// CI, review, and merge policies act on a PR. Standalone sessions, local or
	// a Cloud standalone agent, keep the row for Archive.
	const scratchpadSession = session.workspaceId === STANDALONE_WORKSPACE_ID || standaloneAgent;
	const stopEligible = canStopTurn(session);
	const agentsQuery = useAgentReadinessQuery(stopEligible, hostId);
	const stopTurn = stopEligible && agentsQuery.data?.agents.some((agent) => agent.id === session.provider && agent.canStopTurn) === true;
	// Stop turn needs no PR, so a busy worker without one still gets the section.
	const showSessionControls = hasPRs || scratchpadSession || stopTurn;
	// Cloud orchestrators list the workers they spawned; local orchestrators
	// have no parent/child model and every other session has no children.
	const showWorkers =
		session.kind === "orchestrator" && (session.cloud !== undefined || usePreviewData);
	// Reached through a share: this person sees the session's pull requests but
	// leaves managing them, and the session's policies, to its owner.
	const guest = session.cloud?.shared === true;
	const prView = usePRViewEnabled();
	return (
		<SessionInspectorSummaryView
			// Activity is hidden for now. To restore it, uncomment these two props:
			// activity={<ActivityTimeline hostId={hostId} prs={prSummaries} session={session} />}
			// activityTitle={t("inspector.activity")}
			footer={
				<>
					<SessionAccountSummary session={session} hostId={hostId} />
					<ResumeAgentControl
						className="w-full"
						containerClassName="mt-3 border-t border-(--color-border-settings-input) pt-3"
						hostId={hostId}
						session={session}
					/>
				</>
			}
			artifactCards={
				hasArtifacts ? <ArtifactSummaryList artifacts={artifacts} onOpenArtifact={onOpenArtifact} previewLive={inspectorVisible} session={session} /> : undefined
			}
			artifactTitle={hasArtifacts ? artifactSectionTitle : undefined}
			branch={orchestrator ? undefined : prView ? (
				<SessionBranchSummary
					conflictFiles={branchPR?.mergeability.conflictFiles?.map((file) => file.path)}
					openPRConflicting={branchPR?.mergeability.state === "conflicting"}
					hostId={hostId}
					onOpenFile={onOpenReviewFile}
					merged={prSummaries.some((pr) => pr.state === "merged" && pr.headSha === session.branchState?.remoteHead)}
					onOpenFiles={onOpenFiles}
					openPRNumber={openPRNumber}
					shippedHeadShas={prSummaries.filter((pr) => pr.state === "merged" || pr.state === "closed").map((pr) => pr.headSha)}
					pullRequests={hasPRs ? (work: BranchWork | undefined) => (
						<>
							{focusedPR ? (
								// One card, one PR; with several, pills over it pick which.
								<div className="flex min-w-0 flex-col rounded-lg border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-3 py-1" data-testid="pr-card">
									{prSummaries.length > 1 ? (
										<div className="pt-2 pb-1">
											<PRPills
												branchPR={branchPR}
												focused={focusedPR}
												onFocus={focusPR}
												prs={prSummaries}
												showRepository={prsSpanRepositories(prSummaries)}
											/>
										</div>
									) : null}
									<PRSummaryCard
										canOpenReviews={canOpenReviews}
										cloudOrgId={session.cloud?.orgId}
										elsewhere={branchPR && prFocusKey(focusedPR) !== prFocusKey(branchPR) ? (
											<BranchWorkElsewhere branchPR={branchPR} />
										) : undefined}
										guest={guest}
										hostId={hostId}
										key={prFocusKey(focusedPR)}
										layout="focus"
										onOpenReviews={onOpenReviews}
										pr={focusedPR}
										projectId={session.workspaceId}
										sessionId={session.id}
										showRepository={prsSpanRepositories(prSummaries)}
										work={focusedIsBranchPR && !session.cloud ? work : otherPRConflict ? { content: null, hasRows: false, git: otherPRConflict } : undefined}
									/>
								</div>
							) : null}
							{linkedPRs.map((pr) => <LinkedPRCard external={isExternalRepository(pr, projectRepositories)} key={pr.url} pr={pr} />)}
						</>
					) : null}
					session={session}
					title={prSectionTitle}
				/>
			) : (
				<ClassicSessionBranchSummary
					hostId={hostId}
					merged={prSummaries.some((pr) => pr.state === "merged" && pr.headSha === session.branchState?.remoteHead)}
					onOpenFiles={onOpenFiles}
					openPRNumber={prSummaries.find((pr) => pr.state === "open" || pr.state === "draft")?.number}
					pullRequests={hasPRs ? (
						<>
							{prSummaries.map((pr) => (
								<PRSummaryCard
									canOpenReviews={canOpenReviews}
									key={pr.url || pr.htmlUrl || pr.number}
									showRepository={prsSpanRepositories(prSummaries)}
									onOpenReviews={onOpenReviews}
									pr={pr}
									hostId={hostId}
									sessionId={session.id}
									cloudOrgId={session.cloud?.orgId}
									guest={guest}
									projectId={session.workspaceId}
								/>
							))}
							{linkedPRs.map((pr) => <LinkedPRCard external={isExternalRepository(pr, projectRepositories)} key={pr.url} pr={pr} />)}
						</>
					) : null}
					session={session}
					title={prSectionTitle}
				/>
			)}
			completion={showSessionControls && !guest ? <SessionControls hostId={hostId} policies={hasPRs} scratchpad={scratchpadSession} session={session} stopTurn={stopTurn} /> : undefined}
			scripts={session.cloud ? <CloudScriptRunSections cloud={session.cloud} sessionId={session.id} /> : undefined}
			workers={showWorkers ? <OrchestratorChildrenSection session={session} /> : <SessionForksSection session={session} />}
		/>
	);
});

/**
 * The latest Setup and Cleanup runs, collapsed: a terminal session's agent
 * covers the startup loader once it starts, and Cleanup only runs after the
 * session is deleted, so this is where both stay reachable.
 */
function CloudScriptRunSections({ cloud, sessionId }: { cloud: NonNullable<WorkspaceSession["cloud"]>; sessionId: string }) {
	const { t } = useTranslation();
	const runs = useCloudScriptRuns(cloud.orgId, sessionId, { lifecycle: cloud });
	const setup = latestScriptRun(runs, "setup");
	const cleanup = latestScriptRun(runs, "cleanup");
	if (!setup && !cleanup) return null;
	return (
		<>
			{setup ? (
				<Section defaultOpen={false} surface={false} title={t("scriptRun.setup")}>
					<ScriptRunPanel run={setup} showTitle={false} />
				</Section>
			) : null}
			{cleanup ? (
				<Section defaultOpen={false} surface={false} title={t("scriptRun.cleanup")}>
					<ScriptRunPanel run={cleanup} showTitle={false} />
				</Section>
			) : null}
		</>
	);
}

const ReviewsView = memo(function ReviewsView({
	session,
	hostId,
	onOpenReviewFile,
	onOpenReviewerTerminal,
	onOpenReviewerChat,
	onWorkerMessageSent,
}: {
	session: WorkspaceSession;
	hostId?: string;
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	onOpenReviewerTerminal?: OpenReviewerTerminal;
	onOpenReviewerChat?: (reviewId: string) => void;
	onWorkerMessageSent?: () => void;
}) {
	return (
		<div role="tabpanel">
			<ReviewsSection hostId={hostId} onOpenReviewFile={onOpenReviewFile} onOpenReviewerTerminal={onOpenReviewerTerminal} onOpenReviewerChat={onOpenReviewerChat} onWorkerMessageSent={onWorkerMessageSent} session={session} />
		</div>
	);
});

function InspectorPolicyRow({
	id,
	label,
	ariaLabel = label,
	description,
	tooltipClassName,
	checked,
	disabled,
	onCheckedChange,
}: {
	id: string;
	label: string;
	ariaLabel?: string;
	description?: string;
	tooltipClassName?: string;
	checked: boolean;
	disabled: boolean;
	onCheckedChange: (checked: boolean) => void;
}) {
	return (
		<div className="flex min-h-8 items-center justify-between gap-3 py-1" data-slot="inspector-policy-row">
			<div className="flex min-w-0 items-center gap-1.5">
				<label className="min-w-0 text-sm text-settings-label" htmlFor={id}>
					{label}
				</label>
				{description ? (
					<Tooltip>
						<TooltipTrigger asChild>
							<Pressable
								aria-label={description}
								className="inline-flex size-5 shrink-0 items-center justify-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground"
							>
								<Info aria-hidden="true" className="size-icon-2xs" />
							</Pressable>
						</TooltipTrigger>
						<TooltipContent className={cn("leading-normal", tooltipClassName)}>{description}</TooltipContent>
					</Tooltip>
				) : null}
			</div>
			<Switch
				aria-label={ariaLabel}
				checked={checked}
				disabled={disabled}
				id={id}
				onCheckedChange={onCheckedChange}
			/>
		</div>
	);
}

function projectRepositoryUrls(project: components["schemas"]["Project"] | undefined): Array<string | undefined> | undefined {
	if (!project) return undefined;
	return [project.repo, project.config?.canonicalRepoURL, ...(project.workspaceRepos?.map((repo) => repo.repo) ?? [])];
}

function isExternalRepository(pr: SessionPRReference, repositories: Array<string | null | undefined> | undefined): boolean {
	if (!repositories) return false;
	const identities = repositories.flatMap((repository) => {
		if (!repository) return [];
		try {
			const url = new URL(repository.replace(/^git@([^:]+):/, "ssh://git@$1/"));
			return [`${url.hostname.toLowerCase()}/${url.pathname.replace(/^\/|\/$|\.git$/g, "").toLowerCase()}`];
		} catch {
			return [];
		}
	});
	return identities.length > 0 && !identities.includes(`${pr.host.toLowerCase()}/${pr.repo.toLowerCase()}`);
}

function LinkedPRCard({ external, pr }: { external: boolean; pr: SessionPRReference }) {
	const { t } = useTranslation();
	return (
		<article className="min-w-0 w-full rounded-lg border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-3 py-2.5">
			<div className="flex min-w-0 items-center justify-between gap-2">
				<ProductExternalLink className="inline-flex min-w-0 items-center gap-1 font-mono text-xs font-medium text-settings-label underline-offset-2 hover:underline focus-visible:rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" href={pr.url}>
					<GitPullRequest className="size-icon-sm shrink-0" aria-hidden="true" />
					<span className="truncate">{pr.host}/{pr.repo} {pr.provider === "gitlab" ? "MR" : "PR"} #{pr.number}</span>
					<ArrowUpRight aria-hidden="true" className="size-icon-2xs shrink-0" />
				</ProductExternalLink>
				<CopyButton compact label={t("link.copy")} text={pr.url} />
			</div>
			<div className="mt-1.5 flex items-center justify-between gap-2">
				<div className="flex min-w-0 items-center gap-1 text-xs text-settings-muted">
					<span>{t("inspector.reportedByWorker")}</span>
					<Tooltip>
						<TooltipTrigger asChild>
							<Pressable aria-label={t("inspector.linkedPRInfo")} className="inline-flex size-5 shrink-0 items-center justify-center rounded-full hover:bg-muted hover:text-foreground">
								<Info aria-hidden="true" className="size-icon-2xs" />
							</Pressable>
						</TooltipTrigger>
						<TooltipContent className="max-w-72 leading-normal text-pretty">{t("inspector.linkedPRInfo")}</TooltipContent>
					</Tooltip>
				</div>
				{external ? <span className="shrink-0 rounded-full border border-(--color-border-settings-input) px-1.5 py-0.5 text-[9px] leading-none text-settings-muted">{t("inspector.externalRepository")}</span> : null}
			</div>
		</article>
	);
}

function AutoInjectCIPolicyControl({ session, hostId }: { session: WorkspaceSession; hostId?: string }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const workspaceKey = workspaceQueryKeyForHost(hostId);
	const { client: cloudClient } = useCloudCp();
	const [enabled, setEnabled] = useState(session.autoInjectCI ?? true);
	useEffect(() => {
		setEnabled(session.autoInjectCI ?? true);
	}, [hostId, session.id, session.autoInjectCI]);
	const save = useMutation({
		mutationFn: async (autoInjectCI: boolean) => {
			if (usePreviewData) return;
			if (session.cloud) {
				await cloudClient.setSessionAutoInjectCI(session.cloud.orgId, session.id, autoInjectCI);
				return;
			}
			const { error, response } = await clientForSessionHost(hostId).PATCH("/api/v1/sessions/{sessionId}/auto-inject-ci", {
				params: { path: { sessionId: session.id } },
				body: { autoInjectCI },
			});
			if (error) throw apiErrorException(error, t("inspector.ci.autoInjectError", { status: response.status }));
		},
		onMutate: async (autoInjectCI) => {
			await queryClient.cancelQueries({ queryKey: workspaceKey });
			const previous = queryClient.getQueryData<WorkspaceSummary[]>(workspaceKey);
			queryClient.setQueryData<WorkspaceSummary[]>(workspaceKey, (current) =>
				updateSessionAutoInjectCI(current, session.id, autoInjectCI),
			);
			return { previous };
		},
		onError: (_error, _next, context) => {
			setEnabled(session.autoInjectCI ?? true);
			if (context?.previous) queryClient.setQueryData(workspaceKey, context.previous);
		},
		onSettled: () => {
			void queryClient.invalidateQueries({ queryKey: workspaceKey });
		},
	});
	const error = save.error instanceof Error ? save.error.message : null;

	return (
		<>
			<InspectorPolicyRow
				checked={enabled}
				description={t("inspector.ci.autoInjectDescription")}
				disabled={save.isPending}
				id={`auto-inject-ci-${sessionUiKey(session.id, hostId)}`}
				label={t("inspector.ci.autoInject")}
				onCheckedChange={(next) => {
					setEnabled(next);
					save.mutate(next);
				}}
				tooltipClassName="max-w-64"
			/>
			{error ? (
				<p className="mt-1 text-xs leading-normal text-error" role="status">
					{error}
				</p>
			) : null}
		</>
	);
}

function AutoInjectReviewPolicyControl({ session, hostId }: { session: WorkspaceSession; hostId?: string }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const workspaceKey = workspaceQueryKeyForHost(hostId);
	const { client: cloudClient } = useCloudCp();
	const [enabled, setEnabled] = useState(session.autoInjectReview ?? true);
	useEffect(() => {
		setEnabled(session.autoInjectReview ?? true);
	}, [hostId, session.id, session.autoInjectReview]);
	const save = useMutation({
		mutationFn: async (autoInjectReview: boolean) => {
			if (usePreviewData) return;
			if (session.cloud) {
				await cloudClient.setSessionAutoInjectReview(session.cloud.orgId, session.id, autoInjectReview);
				return;
			}
			const { error } = await clientForSessionHost(hostId).PATCH("/api/v1/sessions/{sessionId}/auto-inject-review", {
				params: { path: { sessionId: session.id } },
				body: { autoInjectReview },
			});
			if (error) throw apiErrorException(error, t("inspector.review.autoInjectError"));
		},
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: workspaceKey });
		},
		onError: () => {
			setEnabled(session.autoInjectReview ?? true);
		},
	});
	const error = save.error instanceof Error ? save.error.message : null;

	return (
		<>
			<InspectorPolicyRow
				checked={enabled}
				description={t("inspector.review.autoInjectDescription")}
				disabled={save.isPending}
				id={`auto-inject-review-${sessionUiKey(session.id, hostId)}`}
				label={t("inspector.review.autoInject")}
				onCheckedChange={(next) => {
					setEnabled(next);
					save.mutate(next);
				}}
				tooltipClassName="max-w-60"
			/>
			{error ? (
				<p className="mt-1 text-xs leading-normal text-error" role="status">
					{error}
				</p>
			) : null}
		</>
	);
}

function updateSessionAutoInjectCI(
	workspaces: WorkspaceSummary[] | undefined,
	sessionId: string,
	autoInjectCI: boolean,
): WorkspaceSummary[] | undefined {
	return workspaces?.map((workspace) => ({
		...workspace,
		sessions: workspace.sessions.map((candidate) =>
			candidate.id === sessionId ? { ...candidate, autoInjectCI } : candidate,
		),
	}));
}

function canStopTurn(session: WorkspaceSession): boolean {
	return !session.isTerminated && !session.cloud && session.mode !== "chat" && session.activity?.state === "active";
}

function SessionControls({ session, hostId, policies, scratchpad, stopTurn }: { session: WorkspaceSession; hostId?: string; policies: boolean; scratchpad: boolean; stopTurn: boolean }) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const workspaceKey = workspaceQueryKeyForHost(hostId);
	const [confirmOpen, setConfirmOpen] = useState(false);
	const [stoppedTurn, setStoppedTurn] = useState<string>();
	const turnKey = JSON.stringify([hostId, session.id, session.activity?.state, session.activity?.lastActivityAt, session.lastUserMessageAt, session.lastInteractionAt]);
	const terminate = useTerminateSession();
	const { client: cloudClient } = useCloudCp();
	const policy = useMutation({
		mutationFn: async (terminateOnPrMerge: boolean) => {
			if (usePreviewData) return;
			if (session.cloud) {
				await cloudClient.setSessionMergePolicy(session.cloud.orgId, session.id, terminateOnPrMerge);
				return;
			}
			const { error, response } = await clientForSessionHost(hostId).PATCH("/api/v1/sessions/{sessionId}/merge-policy", {
				params: { path: { sessionId: session.id } },
				body: { terminateOnPrMerge },
			});
			if (error) throw apiErrorException(error, `Failed to update merge policy (${response.status})`);
		},
		onMutate: async (terminateOnPrMerge) => {
			await queryClient.cancelQueries({ queryKey: workspaceKey });
			const previous = queryClient.getQueryData<WorkspaceSummary[]>(workspaceKey);
			queryClient.setQueryData<WorkspaceSummary[]>(workspaceKey, (current) =>
				updateSessionMergePolicy(current, session.id, terminateOnPrMerge),
			);
			return { previous };
		},
		onError: (_error, _next, context) => {
			if (context?.previous) queryClient.setQueryData(workspaceKey, context.previous);
		},
		onSettled: () => {
			void queryClient.invalidateQueries({ queryKey: workspaceKey });
		},
	});
	const interrupt = useMutation({
		mutationFn: async (_turn: string) => {
			if (usePreviewData) return;
			const { error, response } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/stop-turn",
				{ params: { path: { sessionId: session.id } } },
			);
			if (error?.code === "SESSION_TURN_STOP_PENDING") return;
			if (error) throw apiErrorException(error, `Unable to stop turn (${response.status})`);
		},
		onSuccess: (_data, turn) => setStoppedTurn(turn),
		onSettled: () => {
			void queryClient.invalidateQueries({ queryKey: workspaceKey });
		},
	});
	const policyError = policy.error instanceof Error ? policy.error.message : null;
	const canTerminateNow = session.status === "merged";
	// Its project has no board to return to, and no policies apart from Archive.
	const isStandaloneSession = scratchpad;

	const confirmTermination = ({ deleteArtifacts }: SessionArchiveChoice) => {
		const workspaces = queryClient.getQueryData<WorkspaceSummary[]>(workspaceKey) ?? [];
		const workspace = workspaces.find((w) => w.id === session.workspaceId);
		const nextNav = resolveNextNavigationAfterSessionKill(workspace, session.id);
		
		setConfirmOpen(false);
		terminate.mutate(hostId ? { ...session, hostId, deleteArtifacts } : { ...session, deleteArtifacts });
		
		if (nextNav.target === "session") {
			void navigate(sessionNavigateTarget(session.workspaceId, nextNav.sessionId, hostId));
		} else {
			if (isStandaloneSession) {
				void navigate({ to: "/" });
				return;
			}
			void navigate(projectNavigateTarget(session.workspaceId, hostId));
		}
	};

	if (session.isTerminated === true) return null;

	const terminateAction = (
		<div className="flex items-center justify-between gap-3 py-1">
			<span className="min-w-0 text-sm text-settings-label">{t("inspector.archiveShort")}</span>
			<span className="inline-flex">
				<SessionArchiveDialog
					onConfirm={confirmTermination}
					onOpenChange={setConfirmOpen}
					open={confirmOpen}
					session={session}
					trigger={
						<Button
							aria-label={t("inspector.archive")}
							size="icon-sm"
							variant="quiet"
							onClick={() => {
								clearTerminateSessionState(queryClient, session.id, hostId);
								// Always open the confirm; the modal owns its own dismissal.
								setConfirmOpen(true);
							}}
							type="button"
						>
							<Archive className="size-icon-sm" aria-hidden="true" />
						</Button>
					}
				/>
			</span>
		</div>
	);
	const interruptAction = stopTurn ? (
		<div className="py-1">
			<div className="flex items-center justify-between gap-3">
				<span className="text-xs font-medium text-settings-label">{t("inspector.currentTurn")}</span>
				<Button disabled={interrupt.isPending || stoppedTurn === turnKey} onClick={() => interrupt.mutate(turnKey)} size="sm" type="button" variant="outline">
					{stoppedTurn === turnKey ? t("inspector.stopRequested") : interrupt.isPending ? t("inspector.stoppingTurn") : t("inspector.stopTurn")}
				</Button>
			</div>
			{interrupt.error ? <p className="mt-1 text-2xs text-error" role="alert">{interrupt.error.message}</p> : null}
		</div>
	) : null;

	if (isStandaloneSession) {
		return (
			<Section title={t("inspector.sessionControls")}>
				{interruptAction}
				{terminateAction}
			</Section>
		);
	}

	if (!policies) {
		return <Section title={t("inspector.sessionControls")}>{interruptAction}</Section>;
	}

	return (
		<Section title={t("inspector.sessionControls")}>
			{interruptAction}
			<AutoInjectCIPolicyControl hostId={hostId} session={session} />
			<AutoInjectReviewPolicyControl hostId={hostId} session={session} />
			{session.kind === "orchestrator" ? null : canTerminateNow ? (
				terminateAction
			) : (
				<>
					<InspectorPolicyRow
						ariaLabel={t("inspector.terminateOnMerge")}
						checked={Boolean(session.terminateOnPrMerge)}
						description={t("inspector.terminateOnMergeDescription")}
						disabled={policy.isPending}
						id={`merge-policy-${sessionUiKey(session.id, hostId)}`}
						label={t("inspector.terminateOnMergeShort")}
						onCheckedChange={(checked) => policy.mutate(checked)}
						tooltipClassName="max-w-60"
					/>
					{policyError ? (
						<p className="mt-1 text-xs leading-normal text-error" role="status">
							{policyError}
						</p>
					) : null}
				</>
			)}
		</Section>
	);
}

function updateSessionMergePolicy(
	workspaces: WorkspaceSummary[] | undefined,
	sessionId: string,
	terminateOnPrMerge: boolean,
): WorkspaceSummary[] | undefined {
	return workspaces?.map((workspace) => ({
		...workspace,
		sessions: workspace.sessions.map((candidate) =>
			candidate.id === sessionId ? { ...candidate, terminateOnPrMerge } : candidate,
		),
	}));
}

/** The PR each session's card shows, once the person picked one; outlives the card's unmount. */
const prFocus = new Map<string, string>();

/** Forgets every remembered PR choice; for tests. */
export function resetPRFocus() {
	prFocus.clear();
}

/** Under a PR that is not the branch's: the git rows and actions belong to the PR the branch updates. */
function BranchWorkElsewhere({ branchPR }: { branchPR: SessionPRSummary }) {
	const { t } = useTranslation();
	return (
		<p className="m-0 border-t border-(--color-border-settings-input) pt-2 pb-2 text-xs text-passive" data-testid="branch-work-elsewhere">
			{t("pr.others.branchOn", { number: branchPR.number })}
		</p>
	);
}


/**
 * A PR's card: the PR view's (one PR in focus, its own tab, one action button)
 * while that Developer mode preview is on, else the card as it was before it.
 */
export function PRSummaryCard(props: ComponentProps<typeof PRViewSummaryCard>) {
	return usePRViewEnabled() ? <PRViewSummaryCard {...props} /> : <ClassicPRSummaryCard {...props} />;
}

function PRViewSummaryCard({
	canOpenReviews,
	elsewhere,
	hostId,
	cloudOrgId,
	guest = false,
	onOpenReviews,
	pr,
	projectId,
	liveSessionId,
	sessionId,
	layout = "card",
	showRepository = false,
	work,
}: {
	canOpenReviews: boolean;
	/** In focus without the branch's work: where that work is instead. */
	elsewhere?: ReactNode;
	hostId?: string;
	cloudOrgId?: string;
	/** Reached through a share: this person asks the project's owner to merge. */
	guest?: boolean;
	onOpenReviews: () => void;
	pr: SessionPRSummary;
	/** The cloud project, for a guest's merge requests. */
	projectId?: string;
	/** The session on screen, whose live stream says when merge requests change; the card's own by default. */
	liveSessionId?: string;
	sessionId: string;
	/** "focus" is the inspector's one PR at a time, under a header row; "card" stands alone. */
	layout?: "card" | "focus";
	/** Label the card with its repository: the session has PRs on several. */
	showRepository?: boolean;
	/** The branch rows and git action, when new commits land on this PR. */
	work?: BranchWork;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const workspaceKey = workspaceQueryKeyForHost(hostId);
	const { client: cloudClient, baseUrl: cloudBaseUrl } = useCloudCp();
	usePRNoChecksClock([pr]);
	const noChecks = prHasNoChecks(pr);
	// Labels, reviewers and checks on the card come from the PR's details.
	usePRDetailsLoad(pr, sessionId, hostId, !cloudOrgId);
	const presentation = prCardPresentation(pr, { noChecks });
	const canMerge = prCanMerge(pr, { noChecks }) && Boolean(pr.url && pr.headSha);
	const mergePr = useMutation({
		// Cloud sessions merge from here; a local session merges from its action button.
		mutationFn: async () => {
			if (usePreviewData || !cloudOrgId) return;
			await cloudClient.mergePullRequest(cloudOrgId, sessionId, pr.number, pr.url, pr.headSha);
		},
		onSuccess: () => Promise.all([
			queryClient.invalidateQueries({ queryKey: ["cloud-session-scm-summary", cloudBaseUrl, cloudOrgId, sessionId] }),
			queryClient.invalidateQueries({ queryKey: workspaceKey }),
		]),
	});
	const asksToMerge = guest && Boolean(cloudOrgId);
	const mergeRequests = useProjectMergeRequests(cloudOrgId, projectId, asksToMerge, liveSessionId ?? sessionId);
	const mergeRequestActions = useMergeRequestActions(cloudOrgId, projectId);
	const pendingRequest = pendingMergeRequestFor(mergeRequests.data, pr.url);
	const requestMerge = mergeRequestActions.request;
	const mergeFailure = asksToMerge ? requestMerge.error : mergePr.error;
	const mergeError = mergeFailure instanceof Error ? mergeFailure.message : null;
	const viewModel: InspectorPullRequest = {
		...pr,
		card: presentation,
		href: prBrowserUrl(pr),
		stateLabel: t(prStateLabelKeys[pr.state]),
		repositoryLabel: showRepository ? pr.repo || prRepositoryFromUrl(pr.htmlUrl || pr.url) : undefined,
		reviewDetailsAction: canOpenReviews && pr.review.decision !== "none" ? (
			<Button className="whitespace-nowrap text-settings-muted" onClick={onOpenReviews} type="button" variant="link">
				<span className="text-xs">{t("pr.review.viewDetails")} ↗</span>
			</Button>
		) : undefined,
	};
	// The title row's action button writes its status lines here, under the card.
	const [actionMessages, setActionMessages] = useState<HTMLDivElement | null>(null);
	const cardView = (
		<InspectorPullRequestCardView
			compact
			countNounLabel={(count, noun) => `${count} ${t(prNounKeys[noun], { count })}`}
			headerless={layout === "focus"}
			// The commits row under this card states the same diff: say it once, there.
			hideDiffCounts={layout === "focus" && Boolean(work?.diff && work.diff.additions === pr.additions && work.diff.deletions === pr.deletions)}
			externalIcon={<ArrowUpRight aria-hidden="true" className="size-icon-2xs shrink-0" strokeWidth={2} />}
			externalLink={ProductExternalLink}
			mergeAction={
				canMerge && asksToMerge ? (
					<Button
						aria-label={t("pr.merge.requestFor", { number: pr.number })}
						className="gap-1 px-2"
						disabled={requestMerge.isPending || Boolean(pendingRequest)}
						onClick={() => requestMerge.mutate({ sessionId, number: pr.number, prUrl: pr.url })}
						size="sm"
						type="button"
						variant="secondary"
						data-request-merge
					>
						{requestMerge.isPending ? (
							<Loader2 className="size-icon-sm animate-spin" aria-hidden="true" />
						) : (
							<GitMerge className="size-icon-sm" aria-hidden="true" />
						)}
						{pendingRequest ? t("pr.merge.requested") : t("pr.merge.request")}
					</Button>
				) : !cloudOrgId && !guest ? (
					// A local session acts from the one action button at the card's foot.
					undefined
				) : canMerge ? (
					<Button
						aria-label={t("pr.merge.actionFor", { number: pr.number })}
						className="gap-1 px-2 text-on-foreground [--button-color:var(--color-success)]"
						disabled={mergePr.isPending}
						onClick={() => mergePr.mutate()}
						size="sm"
						type="button"
					>
						{mergePr.isPending ? (
							<Loader2 className="size-icon-sm animate-spin" aria-hidden="true" />
						) : (
							<GitMerge className="size-icon-sm" aria-hidden="true" />
						)}
						{mergePr.isPending ? t("pr.merge.merging") : t("pr.merge.action")}
					</Button>
				) : undefined
			}
			mergeError={mergeError}
			openLabel={t("inspector.openPR", { number: pr.number })}
			openedViaLabel={t("pr.openedVia")}
			pr={viewModel}
			pullRequestIcon={<GitPullRequest className="size-icon-sm shrink-0" aria-hidden="true" />}
			details={<PRLabelsAndReviewers pr={pr} />}
			readinessDetails={(pr.ci.checks ?? []).length > 0 ? { ci: <PRChecksGroups pr={pr} /> } : undefined}
		/>
	);
	if (layout === "card") return cardView;
	const state = prStateIcon[pr.state];
	// A local PR opens in its own tab, in the centre: description, conversation and changes.
	const opensTab = !cloudOrgId && !guest && Boolean(pr.url);
	const titleLabel = (
		<>
			{/* The chips above name the repository in full on hover; here its short name leaves room for the title. */}
			<span className="font-medium">{viewModel.repositoryLabel ? `${prRepositoryShortName(viewModel.repositoryLabel)} ` : ""}PR #{pr.number}</span>
			{pr.title ? <span> {pr.title}</span> : null}
			{/* The icon carries the state; keep it for screen readers. */}
			<span className="sr-only"> {viewModel.stateLabel}</span>
		</>
	);
	// The logo says this one leaves for GitHub; the row itself stays in the app.
	const providerLink = (
		<InfoRowLink
			ariaLabel={t("inspector.openPR", { number: pr.number })}
			href={viewModel.href}
			icon={pr.provider === "github" ? <GitHubIcon className="size-icon-sm shrink-0 text-passive" /> : undefined}
		/>
	);
	// One button for everything on this PR, beside its title: the branch's next git step, then the PR's own.
	const actionButton = !cloudOrgId && !guest && pr.state !== "merged" ? (
		<PRActionButton canMerge={canMerge} git={work?.git} hostId={hostId} messages={actionMessages} pr={pr} sessionId={sessionId} />
	) : cloudOrgId && !guest && pr.state !== "merged" && work?.git ? (
		<CloudPRGitStepButton git={work.git} messages={actionMessages} />
	) : null;
	// A local PR's tab has Open on GitHub, so the row keeps its width for the button;
	// where the title can't open a tab (AO Cloud, a guest), the logo still links out.
	const action = <>{actionButton}{opensTab ? null : providerLink}</>;
	return (
		<section className="flex min-w-0 flex-col py-1" data-testid="pr-focus">
			{/* The title opens the PR's tab, where its description already is. */}
			{opensTab ? (
				<InfoRowLabelButton
					action={action}
					icon={state.icon}
					iconClassName={state.className}
					label={titleLabel}
					onClick={() => openPRTab(sessionUiKey(sessionId, hostId), pr.url)}
					title={pr.title}
				/>
			) : (
				<InfoRow action={action} icon={state.icon} iconClassName={state.className} label={titleLabel} title={pr.title} />
			)}
			{cardView}
			{work?.hasRows ? (
				<div className="flex min-w-0 flex-col border-t border-(--color-border-settings-input) pt-2 pb-1">{work.content}</div>
			) : elsewhere}
			<div className="empty:hidden pb-2" ref={setActionMessages} />
		</section>
	);
}

/** The card as it was before the PR view; shown while that preview is off. Delete with the prView flag. */
function ClassicPRSummaryCard({
	canOpenReviews,
	hostId,
	cloudOrgId,
	guest = false,
	onOpenReviews,
	pr,
	projectId,
	liveSessionId,
	sessionId,
	showRepository = false,
}: {
	canOpenReviews: boolean;
	hostId?: string;
	cloudOrgId?: string;
	/** Reached through a share: this person asks the project's owner to merge. */
	guest?: boolean;
	onOpenReviews: () => void;
	pr: SessionPRSummary;
	/** The cloud project, for a guest's merge requests. */
	projectId?: string;
	/** The session on screen, whose live stream says when merge requests change; the card's own by default. */
	liveSessionId?: string;
	sessionId: string;
	/** Label the card with its repository: the session has PRs on several. */
	showRepository?: boolean;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const workspaceKey = workspaceQueryKeyForHost(hostId);
	const { client: cloudClient, baseUrl: cloudBaseUrl } = useCloudCp();
	const presentation = prCardPresentation(pr);
	const canMerge = prCanMerge(pr) && Boolean(pr.url && pr.headSha);
	const mergePr = useMutation({
		mutationFn: async () => {
			if (usePreviewData) return;
			if (cloudOrgId) {
				await cloudClient.mergePullRequest(cloudOrgId, sessionId, pr.number, pr.url, pr.headSha);
				return;
			}
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/merge", {
				params: { path: { id: String(pr.number) } },
				body: { prUrl: pr.url, expectedHeadSha: pr.headSha },
			});
			if (error) throw apiErrorException(error, t("pr.merge.failed", { number: pr.number }));
			return data;
		},
		onSuccess: async (result) => {
			if (!cloudOrgId && result?.state === "merged") {
				queryClient.setQueryData<{ prs: SessionPRSummary[]; linkedPrs: SessionPRReference[] }>(
					sessionScmSummaryQueryKey(sessionId, hostId),
					(current) => current ? {
						...current,
						prs: current.prs.map((cached) => cached.url === pr.url ? {
							...cached,
							state: result.state,
							stateChangedAt: result.stateChangedAt,
							updatedAt: result.stateChangedAt,
						} : cached),
					} : current,
				);
				await queryClient.invalidateQueries({ queryKey: workspaceKey });
				return;
			}
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: cloudOrgId ? ["cloud-session-scm-summary", cloudBaseUrl, cloudOrgId, sessionId] : sessionScmSummaryQueryKey(sessionId, hostId) }),
				queryClient.invalidateQueries({ queryKey: workspaceKey }),
			]);
		},
	});
	const asksToMerge = guest && Boolean(cloudOrgId);
	const mergeRequests = useProjectMergeRequests(cloudOrgId, projectId, asksToMerge, liveSessionId ?? sessionId);
	const mergeRequestActions = useMergeRequestActions(cloudOrgId, projectId);
	const pendingRequest = pendingMergeRequestFor(mergeRequests.data, pr.url);
	const requestMerge = mergeRequestActions.request;
	const mergeFailure = asksToMerge ? requestMerge.error : mergePr.error;
	const mergeError = mergeFailure instanceof Error ? mergeFailure.message : null;
	const viewModel: InspectorPullRequest = {
		...pr,
		card: presentation,
		href: prBrowserUrl(pr),
		stateLabel: t(prStateLabelKeys[pr.state]),
		repositoryLabel: showRepository ? pr.repo || prRepositoryFromUrl(pr.htmlUrl || pr.url) : undefined,
		reviewDetailsAction: canOpenReviews && pr.review.decision !== "none" ? (
			<Button className="whitespace-nowrap text-settings-muted" onClick={onOpenReviews} type="button" variant="link">
				<span className="text-xs">{t("pr.review.viewDetails")} ↗</span>
			</Button>
		) : undefined,
	};
	return (
		<InspectorPullRequestCardView
			countNounLabel={(count, noun) => `${count} ${t(prNounKeys[noun], { count })}`}
			externalIcon={<ArrowUpRight aria-hidden="true" className="size-icon-2xs shrink-0" strokeWidth={2} />}
			externalLink={ProductExternalLink}
			mergeAction={
				canMerge && asksToMerge ? (
					<Button
						aria-label={t("pr.merge.requestFor", { number: pr.number })}
						className="gap-1 px-2"
						disabled={requestMerge.isPending || Boolean(pendingRequest)}
						onClick={() => requestMerge.mutate({ sessionId, number: pr.number, prUrl: pr.url })}
						size="sm"
						type="button"
						variant="secondary"
						data-request-merge
					>
						{requestMerge.isPending ? (
							<Loader2 className="size-icon-sm animate-spin" aria-hidden="true" />
						) : (
							<GitMerge className="size-icon-sm" aria-hidden="true" />
						)}
						{pendingRequest ? t("pr.merge.requested") : t("pr.merge.request")}
					</Button>
				) : canMerge ? (
					<Button
						aria-label={t("pr.merge.actionFor", { number: pr.number })}
						className="gap-1 bg-success px-2 text-on-foreground hover:bg-success/80 active:bg-success/70"
						disabled={mergePr.isPending}
						onClick={() => mergePr.mutate()}
						size="sm"
						type="button"
					>
						{mergePr.isPending ? (
							<Loader2 className="size-icon-sm animate-spin" aria-hidden="true" />
						) : (
							<GitMerge className="size-icon-sm" aria-hidden="true" />
						)}
						{mergePr.isPending ? t("pr.merge.merging") : t("pr.merge.action")}
					</Button>
				) : undefined
			}
			mergeError={mergeError}
			openLabel={t("inspector.openPR", { number: pr.number })}
			openedViaLabel={t("pr.openedVia")}
			pr={viewModel}
			pullRequestIcon={<GitPullRequest className="size-icon-sm shrink-0" aria-hidden="true" />}
		/>
	);
}

/** The row holds as many previews as fit at this width or wider, up to the maximum. */
const ARTIFACT_PREVIEW_MIN_WIDTH = 88;
const ARTIFACT_PREVIEW_GAP = 8;
const ARTIFACT_PREVIEW_MAX = 4;
/** Fills a 5:4 preview tile with a page laid out 1000 wide. */
const ARTIFACT_PREVIEW_PAGE_HEIGHT = 800;
const ARTIFACT_ROWS_COLLAPSED = 3;

/**
 * The Summary's artifacts: previews of the most recently updated ones, as many
 * as fit the width, then the rest by name. Each opens its file in the
 * inspector's Artifacts tab.
 */
function ArtifactSummaryList({
	artifacts,
	onOpenArtifact,
	previewLive,
	session,
}: {
	artifacts: SessionArtifact[];
	onOpenArtifact?: (target: { feedback?: boolean; path: string }) => void;
	/** Off while the inspector is closed, so hidden pages and PDFs unload. */
	previewLive: boolean;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	// Who each published file's link opens for, marked as on the Artifacts page.
	const shareAccess = useReconciledShareLinks();
	const container = useRef<HTMLDivElement>(null);
	const [columns, setColumns] = useState(3);
	const [expanded, setExpanded] = useState(false);
	useLayoutEffect(() => {
		const element = container.current;
		if (!element) return;
		const measure = (width: number) => {
			if (width <= 0) return;
			const fit = Math.floor((width + ARTIFACT_PREVIEW_GAP) / (ARTIFACT_PREVIEW_MIN_WIDTH + ARTIFACT_PREVIEW_GAP));
			setColumns(Math.min(ARTIFACT_PREVIEW_MAX, Math.max(1, fit)));
		};
		measure(element.getBoundingClientRect().width);
		const observer = new ResizeObserver(([entry]) => {
			if (entry) measure(entry.contentRect.width);
		});
		observer.observe(element);
		return () => observer.disconnect();
	}, []);
	const previews = artifacts.slice(0, columns);
	const rest = artifacts.slice(columns);
	const rows = expanded ? rest : rest.slice(0, ARTIFACT_ROWS_COLLAPSED);
	return (
		<div className="flex min-w-0 flex-col" ref={container}>
			<div className="grid" style={{ gap: ARTIFACT_PREVIEW_GAP, gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}>
				{previews.map((artifact) => (
					<ArtifactPreviewTile artifact={artifact} key={artifact.path} onOpenArtifact={onOpenArtifact} previewLive={previewLive} session={session} shareAccess={shareAccess} />
				))}
			</div>
			{rest.length > 0 ? (
				<ul className="mt-2 flex min-w-0 flex-col border-t border-border pt-1">
					{rows.map((artifact) => (
						<li key={artifact.path}>
							<ArtifactSummaryRow artifact={artifact} onOpenArtifact={onOpenArtifact} shareAccess={shareAccess} />
						</li>
					))}
					{rest.length > ARTIFACT_ROWS_COLLAPSED ? (
						<li>
							<Pressable
								aria-expanded={expanded}
								className="flex min-h-8 w-full min-w-0 items-center rounded-md px-1 text-sm text-muted-foreground transition-colors hover:bg-interactive-hover focus-visible:bg-interactive-hover focus-visible:ring-1 focus-visible:ring-ring"
								onClick={() => setExpanded((current) => !current)}
							>
								{t(expanded ? "inspector.showLess" : "inspector.showMore")}
							</Pressable>
						</li>
					) : null}
				</ul>
			) : null}
		</div>
	);
}

/** One Summary preview: a small thumbnail with the file's name, how it is shared and last update. */
function ArtifactPreviewTile({
	artifact,
	onOpenArtifact,
	previewLive,
	session,
	shareAccess,
}: {
	artifact: SessionArtifact;
	onOpenArtifact?: (target: { feedback?: boolean; path: string }) => void;
	previewLive: boolean;
	session: WorkspaceSession;
	shareAccess: ShareAccessByUrl;
}) {
	const row: ArtifactRow = { ...artifact, sessionId: session.id, sessionTitle: session.title ?? "", cloud: session.cloud, hostId: session.hostId, shareAccess: artifact.shareUrl ? shareAccess.get(artifact.shareUrl) ?? artifact.shareAccess : undefined };
	return (
		<Pressable
			className="group flex min-w-0 flex-col gap-1.5 rounded-md focus-visible:ring-1 focus-visible:ring-ring"
			onClick={() => onOpenArtifact?.({ path: artifact.path })}
			title={artifact.path}
		>
			<span className="relative block aspect-[5/4] overflow-hidden rounded-md bg-[var(--solid-muted,var(--muted))] ring-1 ring-border transition-shadow group-hover:ring-foreground/20 group-focus-visible:ring-foreground/20">
				<ArtifactThumbnail key={`${row.rawUrl}:${row.inlineUrl}:${row.updatedAt}`} pageHeight={ARTIFACT_PREVIEW_PAGE_HEIGHT} previewLive={previewLive} row={row} />
			</span>
			<span className="flex min-w-0 flex-col">
				<span className="truncate text-caption font-medium leading-4 text-foreground">{artifact.name}</span>
				{artifact.updatedAt || artifact.shareUrl ? (
					<span className="flex min-w-0 items-center gap-1">
						<ArtifactShareMark row={row} iconClassName="size-3" />
						{artifact.updatedAt ? (
							<time
								className="truncate text-caption leading-4 tabular-nums text-passive"
								dateTime={artifact.updatedAt}
								title={new Date(artifact.updatedAt).toLocaleString()}
							>
								{formatTimeCompact(artifact.updatedAt)}
							</time>
						) : null}
					</span>
				) : null}
			</span>
		</Pressable>
	);
}

/** One Summary row: icon, name, how it is shared and last update. */
function ArtifactSummaryRow({
	artifact,
	onOpenArtifact,
	shareAccess,
}: {
	artifact: SessionArtifact;
	onOpenArtifact?: (target: { feedback?: boolean; path: string }) => void;
	shareAccess: ShareAccessByUrl;
}) {
	const KindIcon = artifactFileIcon(artifact);
	return (
		<Pressable
			className="flex min-h-8 w-full min-w-0 items-center gap-2 rounded-md px-1 text-sm transition-colors hover:bg-interactive-hover focus-visible:bg-interactive-hover focus-visible:ring-1 focus-visible:ring-ring"
			onClick={() => onOpenArtifact?.({ path: artifact.path })}
			title={artifact.path}
		>
			<KindIcon aria-hidden="true" className="size-icon-sm shrink-0 text-settings-muted" />
			<span className="min-w-0 flex-1 truncate">{artifact.name}</span>
			<ArtifactShareMark row={{ shareUrl: artifact.shareUrl, shareAccess: artifact.shareUrl ? shareAccess.get(artifact.shareUrl) ?? artifact.shareAccess : undefined }} iconClassName="size-icon-sm" />
			{artifact.updatedAt ? (
				<time
					className="shrink-0 text-caption tabular-nums text-settings-muted"
					dateTime={artifact.updatedAt}
					title={new Date(artifact.updatedAt).toLocaleString()}
				>
					{formatTimeCompact(artifact.updatedAt)}
				</time>
			) : null}
		</Pressable>
	);
}

type SortableTimelineEvent = InspectorTimelineEvent & { sortTime: number };

// ponytail: newest 5 commits only; a "show all" control when long sessions need it.
const TIMELINE_COMMIT_LIMIT = 5;

export function ActivityTimeline({ hostId, prs, session }: { hostId?: string; prs: SessionPRSummary[]; session: WorkspaceSession }) {
	// Keyed on the daemon's commit count, not the shared history query that every
	// file edit invalidates, so only a new commit refetches.
	const commitCount = session.branchState?.commits ?? 0;
	const history = useQuery({
		queryKey: ["session-activity-commits", hostId ?? "", session.id, commitCount],
		queryFn: sessionWorkspaceHistoryQueryOptions(session.id, undefined, hostId).queryFn,
		staleTime: Infinity,
		placeholderData: (previous) => previous,
		enabled: !session.cloud && session.kind !== "orchestrator" && !session.needsResume && sessionWorkspaceReady(session),
	});
	// A Cloud session's commits come from its worker's workspace review, the
	// same read (and cache entry) the inspector polls for its Files count.
	const { client: cloudCpClient, ready: cloudReady, baseUrl: cloudBaseUrl } = useCloudCp();
	const cloudOrgId = session.cloud?.orgId;
	const cloudWorkspaceReady = useCloudWorkspaceReady(session);
	const cloudReview = useQuery({
		...cloudWorkspaceReviewSummaryQueryOptions({
			client: cloudCpClient, baseUrl: cloudBaseUrl, orgId: cloudOrgId ?? "", sessionId: session.id, visible: false,
			enabled: cloudReady && cloudOrgId !== undefined && cloudWorkspaceReady && session.kind !== "orchestrator",
		}),
		placeholderData: (previous) => previous,
	});
	const commits = session.cloud ? cloudReview.data?.commits : history.data?.commits;
	const events: SortableTimelineEvent[] = [];
	const pushEvent = (event: InspectorTimelineEvent, timestamp?: string | null) => {
		events.push({ ...event, sortTime: timelineSortTime(timestamp) });
	};
	const createdAt = session.createdAt ?? session.updatedAt;
	const isStandalone = session.workspaceId === STANDALONE_WORKSPACE_ID;
	const creationLabel = appI18n.t(isStandalone ? "inspector.timeline.createdSession" : "inspector.timeline.createdWorkspace");

	pushEvent(
		{
			tone: "neutral",
			content: <>{creationLabel}</>,
			timestamp: formatTimeCompact(createdAt),
		},
		createdAt,
	);

	for (const commit of commits?.slice(0, TIMELINE_COMMIT_LIMIT) ?? []) {
		pushEvent(
			{
				tone: "neutral",
				content: (
					<>
						{appI18n.t("inspector.timeline.committed")} <span className="text-passive">{commit.subject}</span>
					</>
				),
				timestamp: formatTimeCompact(commit.timestamp),
			},
			commit.timestamp,
		);
	}

	for (const pr of prs.filter((pr) => pr.state === "draft")) {
		pushEvent(
			{
				tone: "neutral",
				content: <PRTimelineLink pr={pr} verb={appI18n.t("inspector.timeline.draft")} />,
				timestamp: prStateTime(pr),
			},
			pr.stateChangedAt,
		);
	}

	for (const pr of prs.filter((pr) => pr.state !== "draft")) {
		pushEvent(
			{
				tone: "neutral",
				content: <PRTimelineLink pr={pr} verb={appI18n.t("inspector.timeline.opened")} />,
				timestamp: prCreatedTime(pr),
			},
			pr.createdAt,
		);
	}

	for (const pr of prs.filter((pr) => pr.state === "merged")) {
		pushEvent(
			{
				tone: "good",
				content: <PRTimelineLink pr={pr} verb={appI18n.t("inspector.timeline.merged")} />,
				timestamp: prStateTime(pr),
			},
			pr.stateChangedAt,
		);
	}

	if (session.status === "merged") {
		const mergedAt = latestMergedTimestamp(prs);
		pushEvent(
			{
				tone: "good",
				content: <>{appI18n.t("inspector.timeline.done")}</>,
				timestamp: mergedAt ? formatTimeCompact(mergedAt) : null,
			},
			mergedAt,
		);
	}

	// An account move stops the old agent before the new one starts. That stop
	// is not the session ending, so the move stands in for "Exited" meanwhile.
	// The session's own host: a remote session's id means nothing to the local daemon.
	const { transition } = useSessionInterfaceTransitionStatus(session.cloud ? undefined : session.id, hostId);
	const accountMoving = Boolean(transition?.targetAccountId) && interfaceTransitionIsActive(transition);
	const activityView = accountMoving
		? { ...getAgentActivityView({ state: "active", lastActivityAt: transition?.updatedAt ?? "" }), label: appI18n.t("settings.agentAccounts.switchingAccount") }
		: getAgentActivityView(session.activity);
	const activityAt = (accountMoving ? transition?.createdAt : undefined) ?? session.activity?.lastActivityAt ?? session.updatedAt ?? session.createdAt;
	pushEvent(
		{
			tone: "now",
			content: (
				<span className="inline-flex flex-wrap items-center gap-1.5">
					<span className="inline-flex align-middle">
						{accountMoving ? <TimelinePill {...activityView} /> : <InspectorActivityPill activity={session.activity} />}
					</span>
					{session.status === "no_signal" ? (
						<span className="inline-flex align-middle">
							<TimelinePill {...getSessionTimelinePillView("no_signal")} />
						</span>
					) : null}
					{scmTimelineStates(session).map((state) => (
						<span key={state} className="inline-flex align-middle">
							<InspectorScmPill state={state} />
						</span>
					))}
				</span>
			),
			timestamp: activityAt ? formatTimeCompact(activityAt) : null,
			markerTone: activityView.tone,
			markerBreathe: activityView.breathe,
		} satisfies InspectorTimelineEvent,
		activityAt,
	);

	return <InspectorActivityTimelineView events={[...events].sort((a, b) => b.sortTime - a.sortTime)} />;
}

function PRTimelineLink({ pr, verb }: { pr: SessionPRSummary; verb: string }) {
	return (
		<AppLink
			aria-label={`${verb} PR #${pr.number}`}
			className="inline-flex min-w-0 items-center gap-1 rounded-xs text-foreground underline-offset-2 transition-colors hover:text-accent hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent/50"
			href={prBrowserUrl(pr)}
			rel="noopener noreferrer"
			target="_blank"
		>
			<span>{verb} </span>
			<b>PR #{pr.number}</b>
			<ArrowUpRight aria-hidden="true" className="size-icon-2xs shrink-0" strokeWidth={2} />
		</AppLink>
	);
}

function prStateTime(pr: SessionPRSummary): string | null {
	return pr.stateChangedAt ? formatTimeCompact(pr.stateChangedAt) : null;
}

function prCreatedTime(pr: SessionPRSummary): string | null {
	return pr.createdAt ? formatTimeCompact(pr.createdAt) : null;
}

function latestMergedTimestamp(prs: SessionPRSummary[]): string | null {
	let latest: { timestamp: string; milliseconds: number } | undefined;
	for (const pr of prs) {
		if (pr.state !== "merged" || !pr.stateChangedAt) continue;
		const milliseconds = Date.parse(pr.stateChangedAt);
		if (!Number.isFinite(milliseconds)) continue;
		if (!latest || milliseconds > latest.milliseconds) {
			latest = { timestamp: pr.stateChangedAt, milliseconds };
		}
	}
	return latest?.timestamp ?? null;
}

function timelineSortTime(timestamp: string | null | undefined): number {
	if (!timestamp) return Number.NEGATIVE_INFINITY;
	const milliseconds = Date.parse(timestamp);
	return Number.isFinite(milliseconds) ? milliseconds : Number.NEGATIVE_INFINITY;
}

type ScmTimelineState = "ci_failed" | "changes_requested" | "commented" | "conflict";

function conflictPill() {
	return { label: appI18n.t("inspector.conflict"), tone: "var(--color-danger)", breathe: false };
}

function InspectorActivityPill({ activity }: { activity?: WorkspaceSession["activity"] }) {
	return <TimelinePill {...getAgentActivityView(activity)} />;
}

function InspectorScmPill({ state }: { state: ScmTimelineState }) {
	if (state === "conflict") return <TimelinePill {...conflictPill()} />;
	return <TimelinePill {...getSessionTimelinePillView(state)} />;
}

function TimelinePill({ label, tone }: { label: string; tone: string; breathe: boolean }) {
	return (
		<span className="inline-flex shrink-0 whitespace-nowrap text-xs font-semibold" style={{ color: tone }}>
			{label}
		</span>
	);
}

function scmTimelineStates(session: WorkspaceSession): ScmTimelineState[] {
	const states: ScmTimelineState[] = [];
	const seen = new Set<ScmTimelineState>();
	const add = (state: ScmTimelineState) => {
		if (seen.has(state)) return;
		seen.add(state);
		states.push(state);
	};

	if (session.status === "ci_failed") add("ci_failed");
	if (session.status === "changes_requested") add("changes_requested");
	if (session.status === "commented") add("commented");
	// Only open and draft PRs contribute live PR signals.
	for (const pr of openPRs(session)) {
		if (pr.ci === "failing") add("ci_failed");
		if (pr.review === "changes_requested") add("changes_requested");
		// Read the raw fact directly rather than session.status: status collapses
		// to "working" while the agent is active, but unresolved comments from a
		// non-blocking review must stay visible regardless of agent activity.
		if (pr.reviewComments) add("commented");
		if (pr.mergeability === "conflicting") add("conflict");
	}

	return states;
}

/** Reviewer harness the daemon accepts, typed from the generated schema. */
type ReviewerHarness = NonNullable<components["schemas"]["TriggerReviewRequest"]["harness"]>;
type AgentCatalog = components["schemas"]["AgentReadinessResponse"];

/**
 * The project-level reviewer model/mode/effort the daemon puts under a session's
 * own reviewer settings when the session reviews with the same agent (see
 * projectReviewerSelection and DefaultWorkerReviewer in the daemon). An empty
 * session field means "use this", not the model's own default.
 */
function inheritedReviewerConfig(config: ProjectConfig | undefined, harness: string): ReviewerModelConfig {
	const reviewer = config?.reviewers?.[0];
	if (reviewer) {
		if (reviewer.harness !== harness) return {};
		const { model, mode, effort } = reviewer.agentConfig ?? {};
		return { model: model || undefined, mode: mode || undefined, effort: effort || undefined };
	}
	const worker = config?.worker?.agent ?? "";
	// The project's default worker reviews with its own model and effort, but only
	// when that agent reviews as itself.
	if (!worker || WORKER_DEFAULT_REVIEWERS[worker] !== worker || worker !== harness) return {};
	return {
		model: config?.worker?.agentConfig?.model || config?.agentConfig?.model || undefined,
		effort: config?.worker?.agentConfig?.effort || config?.agentConfig?.effort || undefined,
	};
}

function resolveDefaultReviewerHarness(config: ProjectConfig | undefined, workerHarness: WorkspaceSession["provider"]): ReviewerHarness {
	const configuredHarness = config?.reviewers?.[0]?.harness;
	if (configuredHarness) return configuredHarness as ReviewerHarness;
	return WORKER_DEFAULT_REVIEWERS[workerHarness] ?? "claude-code";
}

function ReviewsSection({
	session,
	hostId,
	onOpenReviewFile,
	onOpenReviewerTerminal,
	onOpenReviewerChat,
	onWorkerMessageSent,
}: {
	session: WorkspaceSession;
	hostId?: string;
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	onOpenReviewerTerminal?: OpenReviewerTerminal;
	onOpenReviewerChat?: (reviewId: string) => void;
	onWorkerMessageSent?: () => void;
}) {
	if (session.cloud) {
		return (
			<CloudReviewsSection
				onOpenReviewFile={onOpenReviewFile}
				onWorkerMessageSent={onWorkerMessageSent}
				session={session}
			/>
		);
	}
	return (
		<LocalReviewsSection
			hostId={hostId}
			onOpenReviewFile={onOpenReviewFile}
			onOpenReviewerTerminal={onOpenReviewerTerminal}
			onOpenReviewerChat={onOpenReviewerChat}
			onWorkerMessageSent={onWorkerMessageSent}
			session={session}
		/>
	);
}

function LocalReviewsSection({
	session,
	hostId,
	onOpenReviewFile,
	onOpenReviewerTerminal,
	onOpenReviewerChat,
	onWorkerMessageSent,
}: {
	session: WorkspaceSession;
	hostId?: string;
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	onOpenReviewerTerminal?: OpenReviewerTerminal;
	onOpenReviewerChat?: (reviewId: string) => void;
	onWorkerMessageSent?: () => void;
}) {
	const { t } = useTranslation();
	const hasPr = sortedPRs(session).length > 0;
	const queryClient = useQueryClient();
	const workspaceKey = workspaceQueryKeyForHost(hostId);
	const reviewsKey = sessionReviewsQueryKey(session.id, hostId);
	const [rerunConfirmation, setRerunConfirmation] = useState<{ ownerKey: string; heads: string } | null>(null);
	const reviewsQuery = useQuery({
		...sessionReviewsQueryOptions(session, hasPr, undefined, hostId),
		refetchInterval: (query) => {
			const reviews = query.state.data?.reviews ?? [];
			if (reviews.some((review) => review.status === "running")) return 2500;
			return session.autoReviewEnabled === true ? 10_000 : false;
		},
	});
	const agentsQuery = useAgentReadinessQuery(true, hostId);
	useEnsureAgentReadiness({ hostId });
	const projectConfigQuery = useQuery({
		queryKey: hostId ? ["project-config", hostId, session.workspaceId] : ["project-config", session.workspaceId],
		enabled: hasPr,
		queryFn: async () => {
			if (usePreviewData) return mockProjectConfig();
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/projects/{id}", {
				params: { path: { id: session.workspaceId } },
			});
			if (error) return undefined;
			return projectConfig(data?.project);
		},
	});
	// The reviewer preference belongs to the worker session, not this component
	// or the whole project. Keep local state responsive while the daemon persists
	// it, and resync when the inspector moves to another session.
	const currentDefaultReviewerHarness = resolveDefaultReviewerHarness(projectConfigQuery.data, session.provider);
	const [reviewerOverride, setReviewerOverride] = useState<ReviewerHarness | "">(
		session.reviewerHarness ?? "",
	);
	const [reviewerModel, setReviewerModel] = useState(session.reviewerConfig?.model ?? "");
	const [reviewerMode, setReviewerMode] = useState(session.reviewerConfig?.mode ?? "");
	const [reviewerEffort, setReviewerEffort] = useState(session.reviewerConfig?.effort ?? "");
	const reviewerOwnerKey = sessionUiKey(session.id, hostId);
	const openReviewStates = openReviewStatesFor(session, reviewsQuery.data?.reviews ?? []);
	const reviewHeads = openReviewStates.map((review) => `${review.prUrl}@${review.targetSha}`).sort().join("\n");
	const currentHeadReviewed = openReviewStates.some((review) => review.latestRun?.targetSha === review.targetSha && review.latestRun.status === "completed");
	useEffect(() => {
		setRerunConfirmation((current) => current && (current.ownerKey !== reviewerOwnerKey || current.heads !== reviewHeads) ? null : current);
	}, [reviewerOwnerKey, reviewHeads]);
	useEnsureAgentReadiness({
		agentIds: reviewerOverride ? [reviewerOverride] : [],
		enabled: reviewerOverride !== "",
		hostId,
	});
	useEffect(() => {
		setReviewerOverride(session.reviewerHarness ?? "");
		setReviewerModel(session.reviewerConfig?.model ?? "");
		setReviewerMode(session.reviewerConfig?.mode ?? "");
		setReviewerEffort(session.reviewerConfig?.effort ?? "");
	}, [hostId, session.id, session.reviewerConfig?.effort, session.reviewerConfig?.mode, session.reviewerConfig?.model, session.reviewerHarness]);
	const saveReviewer = useMutation({
		mutationKey: [...reviewsKey, "switch-reviewer"],
		mutationFn: async ({ harness, model, mode, effort }: { harness: ReviewerHarness | ""; model: string; mode: string; effort: string }) => {
			const clearingToProjectDefault = harness === "" && model === "" && mode === "" && effort === "";
			const currentEffectiveReviewerHarness = (session.reviewerHarness ?? "") || currentDefaultReviewerHarness;
			const nextEffectiveReviewerHarness = harness || currentDefaultReviewerHarness;
			const existingReviewerConfig =
				!clearingToProjectDefault && currentEffectiveReviewerHarness === nextEffectiveReviewerHarness
					? session.reviewerConfig
					: undefined;
			const nextReviewerConfig = buildReviewerAgentConfig(existingReviewerConfig, model, mode, effort);
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/switch", {
				params: { path: { sessionId: session.id } },
				body: {
					harness: harness || undefined,
					agentConfig: nextReviewerConfig,
				},
			});
			if (error) throw apiErrorException(error, "Unable to save reviewer");
			if (data) queryClient.setQueryData(reviewsKey, data);
		},
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: reviewsKey });
			void queryClient.invalidateQueries({ queryKey: workspaceKey });
		},
	});
	const saveAutoReview = useMutation({
		mutationFn: async (enabled: boolean) => {
			// Intent, not effect: emitted before the PUT, so a failed save still
			// counts as the user reaching for the switch.
			void captureRendererEvent("ao.renderer.review_auto_review_toggled", { enabled });
			const { error } = await clientForSessionHost(hostId).PUT("/api/v1/sessions/{sessionId}/auto-review", {
				params: { path: { sessionId: session.id } },
				body: { enabled },
			});
			if (error) throw apiErrorException(error, t("inspector.reviewRequestFailed"));
		},
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: workspaceKey });
		},
	});
	const triggerReview = useMutation({
		mutationFn: async ({ ownerKey, rerun = false }: { ownerKey: string; rerun?: boolean }) => {
			// Keep agent/model/effort overrides scoped to this pass; the daemon selects the surface.
			const reviewerConfig = reviewerModel || reviewerMode || reviewerEffort
				? { ...(reviewerModel ? { model: reviewerModel } : {}), ...(reviewerMode ? { mode: reviewerMode } : {}), ...(reviewerEffort ? { effort: reviewerEffort } : {}) }
				: undefined;
			const selectedHarness = reviewerOverride || undefined;
			const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/trigger", {
				params: { path: { sessionId: session.id } },
				body: { ...(selectedHarness ? { harness: selectedHarness } : {}), ...(reviewerConfig ? { agentConfig: reviewerConfig } : {}), ...(rerun ? { rerun: true } : {}) },
			});
			if (error) throw apiErrorException(error, t("inspector.unableStartReview"));
			return { data, reused: response?.status === 200, rerun, ownerKey, reviewsKey, workspaceKey };
		},
		onMutate: async () => {
			await queryClient.cancelQueries({ queryKey: reviewsKey });
			return { reviewsKey };
		},
		onSuccess: ({ data, reused, rerun, ownerKey, reviewsKey: requestReviewsKey, workspaceKey: requestWorkspaceKey }) => {
			if (data) queryClient.setQueryData(requestReviewsKey, data);
			void queryClient.invalidateQueries({ queryKey: requestReviewsKey });
			void queryClient.invalidateQueries({ queryKey: requestWorkspaceKey });
			if (ownerKey !== reviewerOwnerKey) return;
			const started = data?.reviews?.some((review) => review.status === "running" && review.latestRun);
			if (!started && reused && !rerun) setRerunConfirmation({ ownerKey, heads: reviewHeads });
		},
		onError: (_error, _variables, context) => {
			void queryClient.invalidateQueries({ queryKey: context?.reviewsKey ?? reviewsKey });
		},
	});
	const cancelReview = useMutation({
		mutationFn: async () => {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/cancel", {
				params: { path: { sessionId: session.id } },
			});
			if (error) throw apiErrorException(error, t("inspector.unableCancelReview"));
		},
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: reviewsKey });
			void queryClient.invalidateQueries({ queryKey: workspaceKey });
		},
	});
	const killReview = useMutation({
		mutationFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/kill", {
				params: { path: { sessionId: session.id } },
			});
			if (error) throw apiErrorException(error, t("inspector.unableKillReviewSession"));
			return {
				data,
				requestReviewsKey: reviewsKey,
				requestWorkspaceKey: workspaceKey,
				reviewId: reviewsQuery.data?.reviewerSurface?.reviewId,
				requestHostId: hostId,
			};
		},
		onSuccess: ({ data, requestReviewsKey, requestWorkspaceKey, reviewId, requestHostId }) => {
			if (data) queryClient.setQueryData(requestReviewsKey, data);
			void queryClient.invalidateQueries({ queryKey: requestReviewsKey });
			void queryClient.invalidateQueries({ queryKey: requestWorkspaceKey });
			if (reviewId) {
				void queryClient.invalidateQueries({ queryKey: reviewerConversationQueryKey(reviewId, requestHostId) });
			}
		},
	});
	const reviewStates = reviewsQuery.data?.reviews ?? [];
	const autoReviewEnabled = session.autoReviewEnabled === true;
	const scmSummary = useSessionScmSummary(session.id, true, session.cloud?.orgId, session.cloud ? session.autoInjectCI === true : false, hostId);
	const prSummaries = sessionPRDisplaySummaries(session, scmSummary.data?.prs);
	const openProviderPRs = prSummaries.filter((pr) => pr.state === "open" || pr.state === "draft");
	// Review now, from the controls or a PR's reviewer list: a commit already
	// reviewed asks before it is reviewed again.
	const reviewNow = () => {
		if (currentHeadReviewed) setRerunConfirmation({ ownerKey: reviewerOwnerKey, heads: reviewHeads });
		else triggerReview.mutate({ ownerKey: reviewerOwnerKey });
	};
	return (
		<>
			{/* Running a review is an action; reading them is a list. The action stays
			    on top, then one list carrying both sources keyed by PR. */}
			<ReviewPanel
				autoReviewEnabled={autoReviewEnabled}
				config={projectConfigQuery.data}
				error={
					reviewsQuery.error ??
					triggerReview.error ??
					cancelReview.error ??
					killReview.error ??
					saveReviewer.error ??
					saveAutoReview.error
				}
				isLoading={reviewsQuery.isLoading}
				isCancelling={cancelReview.isPending}
				isAutoReviewSaving={saveAutoReview.isPending}
				isKilling={killReview.isPending}
				isSwitchingReviewer={saveReviewer.isPending}
				isTriggering={triggerReview.isPending}
				hostId={hostId}
				onCancel={() => cancelReview.mutate()}
				onAutoReviewChange={(enabled) => saveAutoReview.mutate(enabled)}
				onKill={() => killReview.mutate()}
				onTrigger={reviewNow}
				reviewerHandleId={reviewsQuery.data?.reviewerHandleId ?? ""}
				reviewerSurface={reviewsQuery.data?.reviewerSurface}
				reviewerActivityState={reviewsQuery.data?.reviewerActivityState}
				reviewerActivityHarness={reviewsQuery.data?.reviewerHarness}
				activeReviewers={reviewsQuery.data?.activeReviewers ?? []}
				onOpenReviewer={(surface) => {
					if (surface.mode === "chat") onOpenReviewerChat?.(surface.reviewId);
					else if (surface.handleId) onOpenReviewerTerminal?.({ handleId: surface.handleId, harness: surface.harness });
				}}
				reviewStates={reviewStates}
				agentCatalog={agentsQuery.data}
				reviewerOverride={reviewerOverride}
				reviewerModel={reviewerModel}
				reviewerMode={reviewerMode}
				reviewerEffort={reviewerEffort}
				onReviewerOverrideChange={(next, config) => {
					setReviewerOverride(next);
					setReviewerModel(config.model ?? "");
					setReviewerMode(config.mode ?? "");
					setReviewerEffort(config.effort ?? "");
					saveReviewer.mutate({ harness: next, model: config.model ?? "", mode: config.mode ?? "", effort: config.effort ?? "" });
				}}
				session={session}
			/>
			<ConfirmDialog
				open={rerunConfirmation?.ownerKey === reviewerOwnerKey && rerunConfirmation.heads === reviewHeads}
				title={t("inspector.rerunReviewTitle")}
				description={t("inspector.rerunReviewWarning")}
				confirmLabel={t("inspector.rerunReviewConfirm")}
				onOpenChange={(open) => { if (!open) setRerunConfirmation(null); }}
				onConfirm={() => {
					if (rerunConfirmation?.ownerKey !== reviewerOwnerKey || rerunConfirmation.heads !== reviewHeads) return;
					setRerunConfirmation(null);
					triggerReview.mutate({ ownerKey: reviewerOwnerKey, rerun: true });
				}}
			/>

			<MergedReviewsSection
				hostId={hostId}
				githubPRs={openProviderPRs}
				isLoading={scmSummary.isLoading || reviewsQuery.isLoading}
				onOpenReviewFile={onOpenReviewFile}
				onWorkerMessageSent={onWorkerMessageSent}
				reviewStates={reviewStates}
				runs={reviewsQuery.data?.runs ?? []}
				session={session}
			/>
		</>
	);
}

function CloudReviewsSection({
	session,
	onOpenReviewFile,
	onWorkerMessageSent,
}: {
	session: WorkspaceSession;
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	onWorkerMessageSent?: () => void;
}) {
	const { t } = useTranslation();
	const { client, ready, baseUrl } = useCloudCp();
	const queryClient = useQueryClient();
	const orgId = session.cloud!.orgId;
	const [reviewNotice, setReviewNotice] = useState<string | null>(null);
	const pullRequestsQuery = useQuery({
		queryKey: ["cloud-session-pull-requests", baseUrl, orgId, session.id],
		enabled: ready,
		queryFn: () => client.listSessionPullRequests(orgId, session.id),
		retry: 1,
	});
	const reviewsQuery = useQuery({
		queryKey: ["cloud-session-reviews", baseUrl, orgId, session.id],
		enabled: ready,
		queryFn: () => client.getSessionReviewState(orgId, session.id),
		retry: 1,
		refetchInterval: (query) =>
			query.state.data?.reviews.some((review) => review.status === "running") ? 2500 : false,
	});
	const harnessesQuery = useQuery({
		queryKey: ["cloud-session-reviewer-harnesses", baseUrl, orgId, session.id],
		enabled: ready && session.runtimeConnected !== false,
		queryFn: () => client.inspectSessionReviewerHarnesses(orgId, session.id),
		retry: 1,
	});
	// With no reviewer chosen, the worker's agent maps to its default reviewer
	// the way a local project's does (WORKER_DEFAULT_REVIEWERS).
	const defaultReviewer = defaultReviewerHarness(session.provider, true);
	const effectiveReviewerHarness = reviewsQuery.data?.reviewerHarness || session.reviewerHarness || defaultReviewer;
	const [selectedReviewerHarness, setSelectedReviewerHarness] = useState(effectiveReviewerHarness);
	useEffect(() => setSelectedReviewerHarness(effectiveReviewerHarness), [effectiveReviewerHarness, session.id]);
	const [autoReviewEnabled, setAutoReviewEnabled] = useState(session.autoReviewEnabled === true);
	useEffect(
		() => setAutoReviewEnabled(session.autoReviewEnabled === true),
		[session.autoReviewEnabled, session.id],
	);
	const triggerReview = useMutation({
		mutationFn: () => client.triggerSessionReviews(orgId, session.id),
		onMutate: () => setReviewNotice(null),
		onSuccess: (data) => {
			queryClient.setQueryData(["cloud-session-reviews", baseUrl, orgId, session.id], data);
			if (!data.reviews.some((review) => review.status === "running")) {
				setReviewNotice(t("inspector.reviewAlreadyRanForCommit"));
			}
		},
	});
	const cancelReview = useMutation({
		mutationFn: () => client.cancelSessionReviews(orgId, session.id),
		onSuccess: (data) => {
			setReviewNotice(null);
			queryClient.setQueryData(["cloud-session-reviews", baseUrl, orgId, session.id], data);
		},
	});
	const updateReviewerHarness = useMutation({
		mutationFn: (reviewerHarness: string) => client.updateSessionPreferences(orgId, session.id, { reviewerHarness }),
		onSuccess: (data) => {
			queryClient.setQueryData(
				["cloud-session-reviews", baseUrl, orgId, session.id],
				(current: CloudCpSessionReviewState | undefined) =>
					current
						? {
								...current,
								reviewerHarness: data.session.reviewerHarness || data.session.harness,
							}
						: current,
			);
			void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
		},
	});
	const installReviewerHarness = useMutation({
		mutationFn: (harness: string) => client.installSessionReviewerHarness(orgId, session.id, harness),
		onSuccess: async (_status, harness) => {
			await queryClient.invalidateQueries({
				queryKey: ["cloud-session-reviewer-harnesses", baseUrl, orgId, session.id],
			});
			updateReviewerHarness.mutate(harness);
		},
	});
	const updateAutoReview = useMutation({
		mutationFn: (autoReviewEnabled: boolean) =>
			client.updateSessionPreferences(orgId, session.id, { autoReviewEnabled }),
		onMutate: (enabled) => {
			const previous = autoReviewEnabled;
			setAutoReviewEnabled(enabled);
			return { previous };
		},
		onError: (_error, _enabled, context) => {
			setAutoReviewEnabled(context?.previous ?? session.autoReviewEnabled === true);
		},
		onSuccess: (data) => {
			setAutoReviewEnabled(data.session.autoReviewEnabled === true);
			void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
		},
	});
	const selectedHarnessStatus = harnessesQuery.data?.harnesses.find(
		(entry) => entry.harness === selectedReviewerHarness,
	);
	// The currently running worker proves its own harness binary exists. Keep
	// review available while the optional inspection request is still loading
	// (and for older control planes that do not expose it yet).
	const selectedHarnessReady =
		selectedHarnessStatus?.status === "ready" ||
		(selectedHarnessStatus === undefined && selectedReviewerHarness === session.provider);
	const credentialHarnesses = reviewsQuery.data?.availableReviewerHarnesses ?? [];
	const selectedCredentialReady =
		credentialHarnesses.includes(selectedReviewerHarness) ||
		(credentialHarnesses.length === 0 && selectedReviewerHarness === session.provider);
	const cloudSession = {
		...session,
		prs: (pullRequestsQuery.data?.pullRequests ?? []).map(cloudPullRequestFacts),
	};
	const reviewState = cloudReviewsToLocal(reviewsQuery.data, effectiveReviewerHarness);
	const guest = session.cloud?.shared === true;
	return (
		<>
			<ReviewPanel
				cloud
				readOnly={guest}
				autoReviewEnabled={autoReviewEnabled}
				error={
					pullRequestsQuery.error ??
					reviewsQuery.error ??
					harnessesQuery.error ??
					triggerReview.error ??
					cancelReview.error ??
					updateReviewerHarness.error ??
					installReviewerHarness.error ??
					updateAutoReview.error
				}
				isAutoReviewSaving={updateAutoReview.isPending}
				isCancelling={cancelReview.isPending}
				isKilling={false}
				isLoading={pullRequestsQuery.isLoading || reviewsQuery.isLoading}
				isSwitchingReviewer={updateReviewerHarness.isPending}
				activeReviewers={[]}
				onOpenReviewer={() => undefined}
				isTriggering={triggerReview.isPending}
				notice={reviewNotice}
				onAutoReviewChange={(enabled) => updateAutoReview.mutate(enabled)}
				onCancel={() => cancelReview.mutate()}
				onKill={() => undefined}
				onReviewerOverrideChange={() => undefined}
				cloudReviewerHarnesses={reviewsQuery.data?.availableReviewerHarnesses ?? []}
				cloudReviewerHarness={selectedReviewerHarness}
				cloudHarnessStatuses={harnessesQuery.data?.harnesses ?? []}
				cloudReviewerReady={selectedHarnessReady && selectedCredentialReady}
				isInstallingCloudHarness={installReviewerHarness.isPending}
				onCloudInstallHarness={() => installReviewerHarness.mutate(selectedReviewerHarness)}
				onCloudReviewerHarnessChange={(harness) => {
					setSelectedReviewerHarness(harness);
					const status = harnessesQuery.data?.harnesses.find((entry) => entry.harness === harness);
					if (status?.status === "ready") updateReviewerHarness.mutate(harness);
				}}
				onTrigger={() => triggerReview.mutate()}
				reviewerHandleId={reviewsQuery.data?.reviewerHandleId ?? ""}
				reviewerEffort=""
				reviewerMode=""
				reviewerModel=""
				reviewerOverride=""
				reviewStates={reviewState.reviews}
				session={cloudSession}
			/>
			<MergedReviewsSection
				githubPRs={[]}
				readOnly={guest}
				isLoading={pullRequestsQuery.isLoading || reviewsQuery.isLoading}
				prsLoaded={pullRequestsQuery.isSuccess}
				onOpenReviewFile={onOpenReviewFile}
				onWorkerMessageSent={onWorkerMessageSent}
				reviewStates={reviewState.reviews}
				runs={reviewState.runs}
				session={cloudSession}
			/>
		</>
	);
}

function cloudPullRequestFacts(pr: CloudCpPullRequestSummary): WorkspaceSession["prs"][number] {
	return {
		url: pr.url,
		number: pr.number,
		state: pr.state,
		ci: "unknown",
		review: "none",
		mergeability: "unknown",
		reviewComments: false,
		updatedAt: pr.updatedAt,
	};
}

function cloudReviewRun(run: CloudCpAOReviewRun, harness: string): ReviewRunFacts {
	return {
		autoInjectReview: false,
		batchId: run.batchId || `cloud:${run.id}`,
		body: run.body,
		createdAt: run.createdAt,
		githubReviewId: run.providerReviewId,
		harness: run.harness || harness,
		id: run.id,
		prUrl: run.pullRequestUrl,
		reviewId: run.reviewId,
		sessionId: run.sessionId,
		status: cloudReviewRunStatus(run.status),
		targetSha: run.targetSha,
		triggerSource: run.triggerSource,
		verdict: run.verdict,
	};
}

/** AO Cloud keeps its original run vocabulary; read it as the daemon's states. */
function cloudReviewRunStatus(status: CloudCpAOReviewRun["status"]): ReviewRunFacts["status"] {
	switch (status) {
		case "complete":
		case "delivered":
			return "completed";
		default:
			return status;
	}
}

function cloudReviewsToLocal(
	data: CloudCpSessionReviewState | undefined,
	harness: string,
): { reviews: PRReviewState[]; runs: ReviewRunFacts[] } {
	if (!data) return { reviews: [], runs: [] };
	const convert = (run: CloudCpAOReviewRun | undefined) => (run ? cloudReviewRun(run, harness) : undefined);
	return {
		reviews: data.reviews.map((review) => ({
			prNumber: review.pullRequestNumber,
			prUrl: review.pullRequestUrl,
			targetSha: review.targetSha,
			title: review.title,
			status: review.status,
			latestRun: convert(review.latestRun),
			previousRun: convert(review.previousRun),
		})),
		runs: data.runs.map((run) => cloudReviewRun(run, harness)),
	};
}

/**
 * The Reviews tab's Comments: the Orchestrator reviewer's passes and inline
 * comments beside the provider's reviews, threads and top-level comments, in
 * one list per open PR (see buildReviewBlocks). Each row's actions call the
 * same endpoints as the PR tab's comments: send to the worker, resolve, reply,
 * ask a reviewer again.
 */
function MergedReviewsSection({
	githubPRs,
	hostId,
	readOnly = false,
	isLoading,
	onOpenReviewFile,
	onWorkerMessageSent,
	prsLoaded = true,
	reviewStates,
	runs,
	session,
}: {
	/**
	 * The provider's PR summaries. Cloud passes none: its provider threads are not
	 * read here, since Reply, Resolve and the details load go to the local daemon.
	 */
	githubPRs: SessionPRSummary[];
	hostId?: string;
	/** Reached through a share: the reviews show, but only the owner acts on them. */
	readOnly?: boolean;
	isLoading: boolean;
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	onWorkerMessageSent?: () => void;
	/** Whether the session's PR list is known; Cloud fetches it, so "no PR" waits for it. */
	prsLoaded?: boolean;
	reviewStates: PRReviewState[];
	runs: ReviewRunFacts[];
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const { client: cloudClient } = useCloudCp();
	const openInAOBrowser = useSessionBrowserLink(session);
	const workspaceKey = workspaceQueryKeyForHost(hostId);
	const sendMessageToWorker = useSendToSessionWorker(session, hostId);
	const openPRs = sortedPRs(session).filter((pr) => pr.state === "open" || pr.state === "draft");
	const blocks = buildReviewBlocks({
		openPRs: openPRs.map((pr) => ({ url: pr.url, number: pr.number })),
		providerPRs: githubPRs,
		// Unfiltered: a PR the SCM summary adds before the session lists it still finds its state.
		reviewStates,
		runs,
	});
	const providerUrl = (block: ReviewPRBlock) => block.provider?.url ?? block.prUrl;
	const refreshProvider = () => Promise.all([
		queryClient.invalidateQueries({ queryKey: sessionScmSummaryQueryKey(session.id, hostId) }),
		queryClient.invalidateQueries({ queryKey: workspaceKey }),
	]);
	const sendOne = async (comment: ReviewComment, block: ReviewPRBlock) => {
		if (session.cloud && comment.runId && comment.source === "orchestrator" && comment.kind === "summary") {
			// Cloud delivers the stored review through the durable control-plane queue.
			await cloudClient.sendSessionReviewToWorker(session.cloud.orgId, session.id, comment.runId);
			return;
		}
		await sendMessageToWorker(reviewCommentWorkerMessage(comment, block), t("inspector.sendToWorkerAgentError"));
	};
	const actions: ReviewCommentActions = {
		onSend: async (comment, block) => {
			await sendOne(comment, block);
			onWorkerMessageSent?.();
		},
		onSendAll: async (block, comments, onSent) => {
			if (session.cloud) {
				// One delivery per comment: each is marked as it lands, so a failure partway
				// leaves only the rest to send again.
				for (const comment of comments) {
					await sendOne(comment, block);
					onSent(comment);
				}
			} else {
				await sendMessageToWorker(reviewCommentsWorkerMessage(block, comments), t("inspector.sendToWorkerAgentError"));
				for (const comment of comments) onSent(comment);
			}
			onWorkerMessageSent?.();
		},
		onResolve: session.cloud ? undefined : async (comment, block) => {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/comments/resolve", {
				params: { path: { sessionId: session.id } },
				body: { pullRequestUrl: providerUrl(block), commentUrl: comment.url ?? "" },
			});
			if (error) throw apiErrorException(error, t("pr.comments.resolveFailed"));
			void refreshProvider();
		},
		// Replies post as the signed-in GitHub account, as on the PR tab.
		onReply: session.cloud || usePreviewData ? undefined : async (comment, block, body) => {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/comments", {
				params: { path: { id: String(block.number) } },
				body: { prUrl: providerUrl(block), body, ...(comment.threadId ? { threadId: comment.threadId } : {}) },
			});
			if (error) throw apiErrorException(error, t("pr.comments.postFailed"));
			await refreshProvider();
		},
		onRerequest: session.cloud ? undefined : async (reviewerId, block) => {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/rerequest", {
				params: { path: { sessionId: session.id } },
				body: { pullRequestUrl: providerUrl(block), reviewerId },
			});
			if (error) throw apiErrorException(error, t("inspector.rereviewRequestFailed"));
			void refreshProvider();
		},
		onViewInFile: onOpenReviewFile ? (comment) => {
			if (comment.file) onOpenReviewFile({ line: comment.line, path: comment.file });
		} : undefined,
		// A Cloud session's link has no Browser to open in here; the forge link stays.
		onOpenInBrowser: session.cloud ? undefined : openInAOBrowser,
	};
	return (
		<ReviewCommentsSection
			actions={actions}
			blocks={blocks}
			hostId={hostId}
			loading={isLoading}
			noPR={prsLoaded && blocks.length === 0}
			readOnly={readOnly}
			sessionId={session.id}
		/>
	);
}

/** One comment as the worker reads it: who, where, what, and a link back. */
function reviewCommentWorkerMessage(comment: ReviewComment, block: ReviewPRBlock): string {
	const reviewerId = comment.source === "orchestrator" ? comment.harness || "reviewer" : comment.author;
	if (comment.kind === "inline") {
		return formatInlineReviewCommentMessage({ body: comment.body, file: comment.file, line: comment.line, reviewerId, url: comment.url });
	}
	return formatReviewSummaryMessage({
		body: comment.body,
		pullRequestUrl: block.webUrl,
		reviewerId,
		source: comment.source === "orchestrator" ? "agent" : "external",
		url: comment.url,
	});
}

/** A PR's open comments in one message, so the worker takes them as one round. */
function reviewCommentsWorkerMessage(block: ReviewPRBlock, comments: ReviewComment[]): string {
	const lines = [
		`Pull request #${block.number} has ${comments.length} open review ${comments.length === 1 ? "comment" : "comments"}. Address the actionable ones, run the relevant tests, commit the fixes, and push the branch.`,
	];
	comments.forEach((comment, index) => {
		const reviewer = sanitizeWorkerMessagePart(comment.source === "orchestrator" ? `Orchestrator review (${comment.harness || "reviewer"})` : `@${comment.author}`);
		const location = comment.file ? sanitizeWorkerMessagePart(`${comment.file}${comment.line ? `:${comment.line}` : ""}`) : comment.kind === "summary" ? "review summary" : "general PR comment";
		lines.push("", `${index + 1}. ${reviewer} · ${location}`, sanitizeWorkerMessagePart(comment.body.trim() || "No comment body provided."));
		if (comment.url) lines.push(`URL: ${sanitizeWorkerMessagePart(comment.url)}`);
	});
	return lines.join("\n");
}

function buildReviewerAgentConfig(
	existing: WorkspaceSession["reviewerConfig"] | undefined,
	model: string,
	mode: string,
	effort: string,
): { model?: string; mode?: string; effort?: string; permissions?: string } | undefined {
	const next = { ...existing };
	if (model) next.model = model;
	else delete next.model;
	if (mode) next.mode = mode;
	else delete next.mode;
	if (effort) next.effort = effort;
	else delete next.effort;
	return Object.keys(next).length > 0 ? next : undefined;
}

function formatInlineReviewCommentMessage(comment: { body?: string; file?: string; line?: number; reviewerId?: string; url?: string }): string {
	const reviewer = sanitizeWorkerMessagePart(comment.reviewerId?.trim() || "unknown reviewer");
	const file = sanitizeWorkerMessagePart(comment.file?.trim() || "");
	const location = file ? `${file}${comment.line ? `:${comment.line}` : ""}` : "general PR comment";
	const body = sanitizeWorkerMessagePart(comment.body?.trim() || "No comment body provided.");
	const url = sanitizeWorkerMessagePart(comment.url?.trim() || "");
	const lines = [
		`A reviewer left an unresolved inline comment on your PR. Address it, commit the fix, and push the branch to GitHub.`,
		"",
		`Reviewer: @${reviewer}`,
		`Location: ${location}`,
		"",
		"Comment:",
		body,
	];
	if (url) {
		lines.push("", `Comment URL: ${url}`);
	}
	lines.push("", "You should not need to re-fetch review data unless you need additional context beyond what Orchestrator has provided here.");
	return lines.join("\n");
}

function formatReviewSummaryMessage(summary: { body: string; pullRequestUrl?: string; reviewerId: string; source: "agent" | "external"; url?: string | null }): string {
	const reviewer = sanitizeWorkerMessagePart(summary.reviewerId.trim() || "reviewer");
	const body = sanitizeWorkerMessagePart(summary.body.trim());
	const url = sanitizeWorkerMessagePart((summary.url || summary.pullRequestUrl || "").trim());
	const source = summary.source === "agent" ? "Orchestrator review" : "external PR review";
	const lines = [
		`An ${source} from ${reviewer} has feedback for your pull request. Address the actionable items, run relevant tests, commit the fixes, and push the branch.`,
		"",
		"Review summary:",
		body,
	];
	if (url) lines.push("", `Review URL: ${url}`);
	return lines.join("\n");
}

function projectConfig(project: components["schemas"]["ProjectOrDegraded"] | undefined): ProjectConfig | undefined {
	if (!project || !("config" in project)) return undefined;
	return project.config;
}

function mockProjectConfig(): ProjectConfig {
	return {
		worker: { agent: "codex" },
		orchestrator: { agent: "codex" },
		reviewers: [{ harness: "codex" }],
	};
}

function ReviewPanel({
	cloud = false,
	readOnly = false,
	notice,
	cloudReviewerHarnesses = [],
	cloudReviewerHarness = "",
	cloudHarnessStatuses = [],
	cloudReviewerReady = true,
	isInstallingCloudHarness = false,
	onCloudReviewerHarnessChange,
	onCloudInstallHarness,
	autoReviewEnabled,
	session,
	hostId,
	config,
	reviewStates,
	reviewerHandleId,
	reviewerSurface,
	reviewerActivityState,
	reviewerActivityHarness,
	activeReviewers,
	onOpenReviewer,
	isLoading,
	isTriggering,
	isCancelling,
	isAutoReviewSaving,
	isKilling,
	isSwitchingReviewer,
	error,
	agentCatalog,
	reviewerOverride,
	reviewerModel,
	reviewerMode,
	reviewerEffort,
	onReviewerOverrideChange,
	onTrigger,
	onCancel,
	onAutoReviewChange,
	onKill,
}: {
	autoReviewEnabled: boolean;
	session: WorkspaceSession;
	hostId?: string;
	config?: ProjectConfig;
	reviewStates: PRReviewState[];
	reviewerHandleId: string;
	reviewerSurface?: components["schemas"]["ListReviewsResponse"]["reviewerSurface"];
	reviewerActivityState?: components["schemas"]["ListReviewsResponse"]["reviewerActivityState"];
	/** The reviewer reviewerActivityState describes; the selected one, not necessarily the one running. */
	reviewerActivityHarness?: string;
	activeReviewers: ReviewerSurface[];
	onOpenReviewer: (surface: ReviewerSurface) => void;
	isLoading: boolean;
	isTriggering: boolean;
	isCancelling: boolean;
	isKilling: boolean;
	isSwitchingReviewer: boolean;
	error: unknown;
	agentCatalog?: AgentCatalog;
	reviewerOverride: ReviewerHarness | "";
	reviewerModel: string;
	reviewerMode: string;
	reviewerEffort: string;
	onReviewerOverrideChange: (next: ReviewerHarness | "", config: { model?: string; mode?: string; effort?: string }) => void;
	onTrigger: () => void;
	onCancel: () => void;
	onAutoReviewChange: (enabled: boolean) => void;
	isAutoReviewSaving: boolean;
	onKill: () => void;
	/** Cloud sessions choose from only credentials connected for the session owner. */
	cloud?: boolean;
	/** Reached through a share: the reviews show, but only the owner runs them. */
	readOnly?: boolean;
	/** Cloud-only: the trigger declined to run and says why. */
	notice?: string | null;
	cloudReviewerHarnesses?: string[];
	cloudReviewerHarness?: string;
	cloudHarnessStatuses?: Array<{ harness: string; status: string; error?: string }>;
	cloudReviewerReady?: boolean;
	isInstallingCloudHarness?: boolean;
	onCloudReviewerHarnessChange?: (harness: string) => void;
	onCloudInstallHarness?: () => void;
}) {
	const { t } = useTranslation();
	// Cloud learns of a session's PRs from the control plane, so it counts as having one
	// while that list loads; once loaded, the list says.
	const hasOpenPR = sortedPRs(session).some((pr) => pr.state === "open" || pr.state === "draft") || (Boolean(cloud) && isLoading);
	if (hasOpenPR && isLoading) {
		return <Section surface={false} title={t("settings.models.reviewerRole")}><p className={inspectorEmptyClass}>{t("inspector.loadingReviews")}</p></Section>;
	}

	const openReviewStates = openReviewStatesFor(session, reviewStates);
	// Whichever PR happens to come first is not the reviewer to name. With one PR
	// reviewed earlier by claude-code and another running under codex, taking the
	// first run reported the wrong agent as the one working. Prefer the run
	// actually in flight, then the newest recorded one.
	const runningStates = openReviewStates.filter((review) => review.status === "running");
	const phase = reviewPhase(openReviewStates, reviewerActivityState, reviewerActivityHarness);
	// The phase and the run it describes come together: the run waiting for input, not whichever PR is first.
	// With no run for the phase, the label shows alone rather than another run's commit and timer.
	const runningRun = reviewPhaseRun(openReviewStates, phase, reviewerActivityHarness);
	// A push since the last pass leaves that pass as previousRun, and it still says when the PR was last reviewed.
	const newestRun = openReviewStates
		.flatMap((review) => [review.latestRun, review.previousRun])
		.filter((run): run is NonNullable<typeof run> => Boolean(run))
		.sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
	const latest = runningRun ?? newestRun;
	// A failed pass after a completed one still leaves the PR reviewed at that earlier pass.
	const lastCompletedRun = openReviewStates
		.flatMap((review) => [review.latestRun, review.previousRun])
		.filter((run): run is NonNullable<typeof run> => run?.status === "completed")
		.sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
	const resolvedDefaultHarness = resolveDefaultReviewerHarness(config, session.provider);
	const selectedHarness = cloud ? cloudReviewerHarness : reviewerOverride || resolvedDefaultHarness;
	const activeReviewerHarness = latest?.harness || selectedHarness;
	// The daemon records why a run failed as a code for diagnostics; people see
	// one generic line with Retry review, for manual triggers too. An automatic
	// pass's failure only shows while auto-review is on.
	const reviewFailure = newestRun?.status === "failed" && (newestRun.triggerSource !== "auto" || autoReviewEnabled) ? newestRun : null;
	const reviewRunning = reviewIsRunning(openReviewStates);
	// Auto-review does not lock the reviewer or Review now: a manual pass reuses one
	// already running, and a reviewed head asks before reviewing it again.
	const reviewerControlsLocked = reviewRunning || isKilling || isSwitchingReviewer || isTriggering || isCancelling;
	const runDisabled = !hasOpenPR || isKilling || isSwitchingReviewer || isCancelling || (cloud && !cloudReviewerReady) || reviewRunDisabled(openReviewStates, isTriggering);
	const hasReviewerSession = reviewerHandleId.trim() !== "" ||
		Boolean(reviewerSurface?.mode === "chat" && reviewerSurface.reviewId);
	// Each live reviewer can be opened; an agent may have asked one other than the selected reviewer.
	// A Cloud reviewer runs on the control plane and has no local surface to open.
	const openableReviewers = cloud ? [] : [
		...activeReviewers,
		...(reviewerSurface && !activeReviewers.some((surface) => surface.reviewId === reviewerSurface.reviewId) ? [reviewerSurface] : []),
	];
	const canEndSession = !cloud && hasReviewerSession;
	const showMore = !readOnly && (openableReviewers.length > 0 || canEndSession);
	const killDisabled = isKilling || isCancelling || isTriggering || isSwitchingReviewer;

	return (
		<Section surface={false} title={t("settings.models.reviewerRole")}>
			<div className="flex min-w-0 select-none flex-col">
				{error ? (
					<p className="m-0 pb-2 text-xs leading-normal text-error" role="alert">
						{apiErrorMessage(error, t("inspector.reviewRequestFailed"))}
					</p>
				) : null}
				{readOnly ? null : (
					<>
						{cloud ? (
							// The same pill as a local session's; Cloud picks only the agent.
							<div className="flex min-h-8 min-w-0 items-center justify-between gap-3 py-1">
								<span className="shrink-0 text-sm text-settings-label">{t("inspector.agent")}</span>
								<div className="flex min-w-0 items-center justify-end gap-1.5">
									<CloudReviewerPicker
										ariaLabel={t("inspector.selectReviewerAgent")}
										credentialHarnesses={cloudReviewerHarnesses}
										disabled={reviewerControlsLocked || isInstallingCloudHarness}
										harnessStatuses={cloudHarnessStatuses}
										onAgentChange={(harness) => onCloudReviewerHarnessChange?.(harness)}
										value={cloudReviewerHarness}
									/>
									{cloudHarnessStatuses.some(
										(entry) => entry.harness === cloudReviewerHarness && entry.status !== "ready",
									) ? (
										<Button
											disabled={isInstallingCloudHarness || reviewRunning}
											onClick={onCloudInstallHarness}
											size="sm"
											type="button"
											variant="secondary"
										>
											{isInstallingCloudHarness ? t("settings.harness.installing") : t("settings.harness.install")}
										</Button>
									) : null}
								</div>
							</div>
						) : (
							// Agent, model, and effort in one menu, picked the way the chat box picks them.
							<div className="flex min-h-8 min-w-0 items-center justify-between gap-3 py-1">
								<span className="shrink-0 text-sm text-settings-label">{t("inspector.agent")}</span>
								<ReviewerPicker
									ariaLabel={t("inspector.selectReviewerAgent")}
									agents={agentCatalog?.agents}
									defaultHarness={resolvedDefaultHarness}
									value={reviewerOverride}
									model={reviewerModel}
									mode={reviewerMode}
									effort={reviewerEffort}
									inherited={inheritedReviewerConfig(config, reviewerOverride || resolvedDefaultHarness)}
									projectId={session.workspaceId}
									hostId={hostId}
									disabled={reviewerControlsLocked}
									onAgentChange={(next) => onReviewerOverrideChange(next as ReviewerHarness | "", {})}
									onConfigChange={(next) => onReviewerOverrideChange(reviewerOverride, next)}
								/>
							</div>
						)}
						<InspectorPolicyRow
							checked={autoReviewEnabled}
							description={t("inspector.review.autoReviewHint")}
							disabled={isAutoReviewSaving}
							id={`auto-review-${sessionUiKey(session.id, hostId)}`}
							label={t("inspector.review.autoReviewLabel")}
							onCheckedChange={onAutoReviewChange}
							tooltipClassName="max-w-64"
						/>
						<div className="mt-2 flex min-w-0 items-center gap-1.5">
							{reviewRunning ? (
								<Button
									className="min-w-0 flex-1 [&_svg]:size-icon-sm"
									disabled={isCancelling || isKilling || isSwitchingReviewer}
									onClick={onCancel}
									size="sm"
									type="button"
									variant="secondary"
								>
									<CircleStop aria-hidden="true" />
									{isCancelling ? t("inspector.review.cancelling") : t("inspector.review.cancel")}
								</Button>
							) : (
								<Button
									className="min-w-0 flex-1"
									disabled={runDisabled}
									onClick={onTrigger}
									size="sm"
									type="button"
									variant="primary"
								>
									{isTriggering
										? t("inspector.review.starting")
										: reviewFailure
											? t("inspector.review.retryReview")
											: t("inspector.review.reviewNow")}
								</Button>
							)}
							{showMore ? (
								<DropdownMenu>
									<DropdownMenuTrigger asChild>
										<Button aria-label={t("inspector.review.moreActions")} size="icon-sm" title={t("inspector.review.moreActions")} type="button" variant="quiet">
											<MoreVertical aria-hidden="true" className="size-icon-sm" />
										</Button>
									</DropdownMenuTrigger>
									<DropdownMenuContent align="end" className="min-w-52">
										{openableReviewers.map((surface) => (
											<DropdownMenuItem
												disabled={surface.mode !== "chat" && !surface.handleId}
												key={surface.reviewId}
												onSelect={() => onOpenReviewer(surface)}
											>
												<AgentAvatar className="size-4" decorative provider={surface.harness} />
												{t("inspector.review.openReviewerNamed", { agent: agentLabel(surface.harness) })}
											</DropdownMenuItem>
										))}
										{canEndSession ? (
											<>
												{openableReviewers.length > 0 ? <DropdownMenuSeparator /> : null}
												<DropdownMenuItem disabled={killDisabled} onSelect={onKill} variant="destructive">
													<CircleStop aria-hidden="true" />
													{isKilling ? t("inspector.review.endingSession") : t("inspector.review.endSession")}
												</DropdownMenuItem>
											</>
										) : null}
									</DropdownMenuContent>
								</DropdownMenu>
							) : null}
						</div>
					</>
				)}
				<div className="mt-2">
					<ReviewStatusLine
						activeHarness={activeReviewerHarness}
						failed={Boolean(reviewFailure)}
						hasOpenPR={hasOpenPR}
						isCancelling={isCancelling}
						lastCompleted={lastCompletedRun}
						notice={notice ?? null}
						phase={phase}
						phaseRun={runningRun}
						running={runningStates}
						selectedHarness={selectedHarness}
					/>
				</div>
			</div>
		</Section>
	);
}

/** "0:42" while a review runs; minutes past an hour keep counting ("74:05"). */
function formatElapsed(ms: number): string {
	const seconds = Math.max(0, Math.floor(ms / 1000));
	return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

function useNowWhile(active: boolean): number {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!active) return;
		setNow(Date.now());
		const timer = window.setInterval(() => setNow(Date.now()), 1000);
		return () => window.clearInterval(timer);
	}, [active]);
	return now;
}

/**
 * One line under the Reviewer controls: what the reviewer is doing now (with
 * the commit and the time it has run), why the last pass failed, or when it
 * last reviewed. The verdict itself lives on the review in Comments.
 */
function ReviewStatusLine({
	activeHarness,
	failed,
	hasOpenPR,
	isCancelling,
	lastCompleted,
	notice,
	phase,
	phaseRun: runningRun,
	running,
	selectedHarness,
}: {
	activeHarness: string;
	failed: boolean;
	hasOpenPR: boolean;
	isCancelling: boolean;
	/** The newest completed pass, which says when the PR was last reviewed. */
	lastCompleted?: ReviewRunFacts;
	notice: string | null;
	phase: ReturnType<typeof reviewPhase>;
	/** The run behind the phase, whose commit and start time show. */
	phaseRun?: PRReviewState["latestRun"];
	running: PRReviewState[];
	selectedHarness: string;
}) {
	const { t } = useTranslation();
	const startedAt = runningRun ? runningRun.startedAt || runningRun.launchingAt || runningRun.createdAt : undefined;
	const now = useNowWhile(Boolean(phase && startedAt));
	const lineClass = "m-0 flex min-h-5 min-w-0 items-center gap-1.5 text-xs leading-normal";
	if (!hasOpenPR) return <p className={cn(lineClass, "text-muted-foreground")}>{t("inspector.review.noPRStatus")}</p>;
	if (phase) {
		const label = isCancelling || phase === "stopping"
			? t("inspector.review.cancelling")
			: phase === "starting"
				? t("inspector.review.starting")
				: phase === "waiting_input"
					? t("inspector.review.waitingForInput")
					: t("inspector.review.reviewingPR", { prs: running.map((review) => `#${review.prNumber}`).join(", ") });
		const facts = [
			// Name the agent when it is not the one picked above (an agent asked for another).
			phase === "waiting_input" || activeHarness !== selectedHarness ? agentLabel(activeHarness) : "",
			runningRun?.targetSha ? runningRun.targetSha.slice(0, 7) : "",
			startedAt ? formatElapsed(now - Date.parse(startedAt)) : "",
		].filter(Boolean);
		return (
			<p className={lineClass} role="status">
				<Loader2 aria-hidden="true" className="size-icon-xs shrink-0 animate-spin text-working" />
				<span className="min-w-0 truncate text-foreground">{label}</span>
				{facts.length > 0 ? <span className="shrink-0 tabular-nums text-passive">{facts.join(" · ")}</span> : null}
			</p>
		);
	}
	if (notice) return <p className={cn(lineClass, "text-muted-foreground")} role="status">{notice}</p>;
	if (failed) return <p className={cn(lineClass, "text-error")} role="status">{t("inspector.review.lastFailed")}</p>;
	const lastReviewed = lastCompleted;
	if (!lastReviewed) return <p className={cn(lineClass, "text-muted-foreground")}>{t("inspector.review.notReviewedYet")}</p>;
	return (
		<p className={cn(lineClass, "text-muted-foreground")}>
			<span className="min-w-0 truncate">{t("inspector.review.lastReviewed", { time: formatTimeCompact(lastReviewed.createdAt) })}</span>
			{lastReviewed.targetSha ? <span className="shrink-0 tabular-nums text-passive">· {lastReviewed.targetSha.slice(0, 7)}</span> : null}
		</p>
	);
}

function BrowserView({
	session,
	isActive,
	browserPoppedOut,
	browserAnnotationQueue,
	onTogglePopOut,
	browserView,
	topbarHost,
}: {
	session: WorkspaceSession;
	isActive: boolean;
	browserPoppedOut: boolean;
	browserAnnotationQueue?: BrowserAnnotationQueueModel;
	onTogglePopOut?: (next: boolean) => void;
	browserView?: BrowserViewModel;
	topbarHost?: HTMLElement | null;
}) {
	// While maximized, the browser is a full-window overlay that covers the rail,
	// so the inspector's Browser tab has nothing to show (and must not mount a
	// second BrowserPanelView — it would fight the overlay over the shared native
	// view slot). Exit is via the overlay's own minimize button.
	const { t } = useTranslation();
	// Stable, so the memoized panel skips this view's unrelated renders.
	const togglePopOut = useCallback((next: boolean) => onTogglePopOut?.(next), [onTogglePopOut]);
	if (browserPoppedOut) {
		return (
			<div className="h-full min-h-0" data-browser-dock-target="" role="tabpanel">
				<div className={cn(inspectorEmptyClass, "flex flex-col items-center gap-2 py-10 px-5 text-center")}>
					<p className="text-md-sm text-muted-foreground">{t("inspector.browserInCenter")}</p>
					<Button onClick={() => onTogglePopOut?.(false)} size="sm" type="button" variant="secondary">
						{t("inspector.returnToPanel")}
					</Button>
				</div>
			</div>
		);
	}

	if (!browserView || !browserAnnotationQueue) {
		return null;
	}

	return (
		<BrowserPanelView
			active={isActive}
			annotationQueue={browserAnnotationQueue}
			browserView={browserView}
			onTogglePopOut={togglePopOut}
			poppedOut={false}
			session={session}
			topbarHost={topbarHost}
		/>
	);
}

function FilesView({ filesView, onOpenFiles }: { filesView?: ReactNode; onOpenFiles?: () => void }) {
	const { t } = useTranslation();
	if (filesView) {
		return (
			<div className="h-full min-h-0" role="tabpanel">
				{filesView}
			</div>
		);
	}
	return (
		<div role="tabpanel">
			<div className={cn(inspectorEmptyClass, "flex flex-col items-center gap-2 px-5 py-10 text-center")}>
				<p className="text-md-sm text-muted-foreground">{t("inspector.filesUnavailable")}</p>
				<Button disabled={!onOpenFiles} onClick={() => onOpenFiles?.()} size="sm" type="button" variant="secondary">
					{t("inspector.openFiles")}
				</Button>
			</div>
		</div>
	);
}
