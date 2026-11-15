import { apiErrorException } from "../lib/api-error-exception";
import { BrowserIcon } from "./icons";
import { GitHubIcon } from "./GitHubIcon";
import { artifactFileIcon } from "./artifact-file-icon";
import { ArtifactShareMark } from "./ArtifactShareMark";
import { useReconciledShareLinks, type ShareAccessByUrl } from "./ShareArtifactPopover";
import { Chat, CheckWaves, Folder, List, Package } from "@/components/app-icons";
import { AppLink } from "./AppLink";
import { ArtifactThumbnail } from "./ArtifactThumbnail";
import type { ArtifactRow } from "../lib/artifacts";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNavigate } from "@tanstack/react-router";
import { memo, useCallback, useEffect, useLayoutEffect, useRef, useState, type ComponentProps, type ReactNode } from "react";
import type { TFunction } from "i18next";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
	InspectorActivityTimelineView,
	InspectorPullRequestCardView,
	InspectorReviewsView,
	InspectorSection as Section,
	SessionInspectorShellView,
	SessionInspectorSummaryView,
	inspectorEmptyClass,
	type InspectorPullRequest,
	type InspectorInlineComment,
	type InspectorGithubReview,
	type InspectorReviewGroup,
	type InspectorReviewLabels,
	type InspectorReviewSummaryAction,
	type InspectorTimelineEvent,
	type InspectorView,
} from "@aoagents/product-ui";
import {
	Archive,
	ArrowUpRight,
	PanelLeft,
	GitPullRequest,
	GitMerge,
	Info,
	Play,
	Loader2,
	X,
} from "@/components/app-icons";
import { InfoRow, InfoRowButton, InfoRowLink } from "./InspectorInfoRow";
import type { components } from "../../api/schema";
import { usePRDetailsLoad } from "../hooks/usePRDetailsLoad";
import { usePRNoChecksClock } from "../hooks/usePRNoChecksClock";
import { interfaceTransitionIsActive, useSessionInterfaceTransitionStatus } from "../hooks/useSessionInterfaceTransition";
import { reviewerConversationQueryKey } from "../hooks/useReviewerConversation";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { sessionUiKey } from "../lib/hosts";
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
import { openPRTab, usePRTabs, type PRTabSection } from "../lib/pr-tabs";
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
import { PRDescriptionPreview } from "./PRDescriptionPreview";
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
import { ReviewerSelect } from "./ReviewerSelect";
import { agentLabel } from "../lib/agent-options";
import { useAgentReadinessQuery, useEnsureAgentReadiness } from "../hooks/useAgentReadinessQuery";
import { Switch } from "./ui/switch";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { appI18n } from "../i18n";
import type { MessageKey } from "../i18n";
import { usesPreviewWorkspaceData as usePreviewData } from "../lib/preview-mode";
import {
	openReviewStatesFor,
	reviewHasLiveActivity,
	reviewIsRunning,
	reviewRunDisabled,
	reviewSessionRunAction,
	sessionReviewsQueryOptions,
	sessionReviewsQueryKey,
	type PRReviewState,
	type ReviewRunFacts,
} from "../lib/session-reviews";
import type { CloudCpAOReviewRun, CloudCpProjectRepo, CloudCpSessionReviewState, CloudCpPullRequestSummary } from "../lib/cloud-cp";

type ProjectConfig = components["schemas"]["ProjectConfig"];
type OpenReviewerTerminal = (target: { handleId: string; harness: string }) => void;
type ReviewerSurface = components["schemas"]["DomainReviewerSurface"];

export type { InspectorView } from "@aoagents/product-ui";

const VIEW_DEFS: {
	id: InspectorView;
	labelKey: "inspector.summary" | "inspector.reviewTab" | "inspector.browser" | "inspector.sideChat" | "inspector.files" | "artifacts.title";
	icon: ReactNode;
}[] = [
	{ id: "summary", labelKey: "inspector.summary", icon: <List /> },
	{ id: "files", labelKey: "inspector.files", icon: <Folder data-testid="files-viewer-icon" /> },
	{ id: "reviews", labelKey: "inspector.reviewTab", icon: <CheckWaves /> },
	{ id: "browser", labelKey: "inspector.browser", icon: <BrowserIcon aria-hidden="true" /> },
	{ id: "sideChat", labelKey: "inspector.sideChat", icon: <Chat /> },
	{ id: "artifacts", labelKey: "artifacts.title", icon: <Package /> },
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
	// Badge the Artifacts tab when an artifact was registered while Browser or Files was in use.
	const artifactsUnseen = useUiStore((state) =>
		session ? Boolean(state.inspectorSessions[sessionUiKey(session.id, hostId)]?.artifactsUnseen) : false,
	);
	const inspectorQueryClient = useQueryClient();
	// An imported chat has no AO workspace until it is opened and resumes.
	const localFilesChangedCount = useSessionWorkspaceFilesChangedCount(
		orchestrator || session?.cloud || session?.needsResume || (session && !sessionWorkspaceReady(session)) ? undefined : session?.id,
		hostId,
	);
	const localWorkspaceData = session ? inspectorQueryClient.getQueryData<{ files?: unknown[] }>(sessionWorkspaceFilesQueryKey(session.id, hostId)) : undefined;
	const { client: cloudCpClient, ready: cloudReady, baseUrl: cloudBaseUrl } = useCloudCp();
	const cloudOrgId = session?.cloud?.orgId;
	const cloudReview = useQuery({
		queryKey: ["cloud-workspace-review", cloudBaseUrl, cloudOrgId ?? "", session?.id ?? "", "summary"],
		// Only a connected worker answers for its files, as in the Files panel: a
		// waking session's count is not polled until its worker is back, and the
		// count read before stays on the tab meanwhile.
		enabled: cloudReady && session?.cloud !== undefined && cloudOrgId !== undefined && session.runtimeConnected === true,
		refetchInterval: 5_000,
		queryFn: () => cloudCpClient.getWorkspaceReview(cloudOrgId!, session!.id),
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
	const availableViewDefs = orchestrator ? VIEW_DEFS.filter((entry) =>
		entry.id === "summary" || entry.id === "browser" || (entry.id === "artifacts" && ((session?.artifactFiles?.length ?? 0) > 0 || artifactRequestPending)))
		: VIEW_DEFS.filter((entry) => (!hostId || entry.id !== "browser" || browserView) && (reviewsAvailable || entry.id !== "reviews")
			// The host passes a side chat view only where side chats can run.
			&& (entry.id !== "sideChat" || sideChatView !== undefined)
			// Artifacts shows whenever the session lists files, or while an artifact
			// link waits for the list to refresh.
			&& (entry.id !== "artifacts" || (session?.artifactFiles?.length ?? 0) > 0 || artifactRequestPending));
	const view: InspectorView = availableViewDefs.some((entry) => entry.id === requestedView) ? requestedView : "summary";
	const sessionLoaded = session !== undefined;
	useEffect(() => {
		// Until the session loads, its tabs are unknown: keep the requested view.
		if (view === requestedView || (!sessionLoaded && !orchestrator)) return;
		setInternalView(view);
		onViewChange?.(view);
	}, [onViewChange, orchestrator, requestedView, sessionLoaded, view]);
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
				activeView={view}
				ariaLabel={t("inspector.aria")}
				artifactsView={
					session ? (
						<SessionArtifactsView hostId={hostId} key={sessionUiKey(session.id, hostId)} onRequestConsumed={onArtifactRequestConsumed} request={artifactRequest} session={session} />
					) : undefined
				}
				browserPoppedOut={browserPoppedOut}
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
			/>
		</div>
	);
});

function reviewsTabVisible(session: WorkspaceSession | undefined): boolean {
	if (!session) return true;
	// Cloud PR facts are queried directly by CloudReviewsSection instead of
	// piggybacking on the local daemon workspace projection.
	if (session.cloud) return true;
	return sortedPRs(session).some((pr) => pr.state === "open" || pr.state === "draft");
}

function externalReviewActorMatchesPRAuthor(actor: string | undefined, author: string | undefined): boolean {
	const normalizedActor = normalizeReviewerId(actor);
	const normalizedAuthor = normalizeReviewerId(author);
	return normalizedActor !== "" && normalizedActor === normalizedAuthor;
}

function normalizeReviewerId(value: string | undefined): string {
	return value?.trim().replace(/^@+/, "").toLowerCase() ?? "";
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
	const showSessionControls = hasPRs || scratchpadSession;
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
			activity={
				<>
					<ActivityTimeline hostId={hostId} prs={prSummaries} session={session} />
					<SessionAccountSummary session={session} hostId={hostId} />
					<ResumeAgentControl
						className="w-full"
						containerClassName="mt-3 border-t border-(--color-border-settings-input) pt-3"
						hostId={hostId}
						session={session}
					/>
				</>
			}
			activityTitle={t("inspector.activity")}
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
			completion={showSessionControls && !guest ? <SessionControls hostId={hostId} scratchpad={scratchpadSession} session={session} /> : undefined}
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

function SessionControls({ session, hostId, scratchpad }: { session: WorkspaceSession; hostId?: string; scratchpad: boolean }) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const workspaceKey = workspaceQueryKeyForHost(hostId);
	const [confirmOpen, setConfirmOpen] = useState(false);
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

	if (isStandaloneSession) {
		return (
			<Section title={t("inspector.sessionControls")}>
				{terminateAction}
			</Section>
		);
	}

	return (
		<Section title={t("inspector.sessionControls")}>
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
	const detailsFailed = usePRDetailsLoad(pr, sessionId, hostId, !cloudOrgId);
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
	const activeTabUrl = usePRTabs(sessionUiKey(sessionId, hostId)).active;
	const cardView = (
		<InspectorPullRequestCardView
			compact
			countNounLabel={(count, noun) => `${count} ${t(prNounKeys[noun], { count })}`}
			headerless={layout === "focus"}
			description={<PRDescriptionPreview cloud={Boolean(cloudOrgId)} detailsFailed={detailsFailed} pr={pr} sessionKey={sessionUiKey(sessionId, hostId)} />}
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
	// A local PR opens in its own tab, where its description, conversation and changes live.
	const opensTab = !cloudOrgId && !guest && Boolean(pr.url);
	const tabShowing = activeTabUrl === pr.url;
	const openTab = (section?: PRTabSection) => openPRTab(sessionUiKey(sessionId, hostId), pr.url, section);
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
	return (
		<section className="flex min-w-0 flex-col py-1" data-testid="pr-focus">
			{opensTab ? (
				<InfoRowButton
					action={providerLink}
					icon={state.icon}
					iconClassName={state.className}
					label={titleLabel}
					onClick={() => openTab()}
					title={pr.title}
					trailing={tabShowing ? undefined : (
						<span className="inline-flex h-6 shrink-0 items-center gap-1 rounded-full bg-muted px-2 text-micro font-medium text-muted-foreground transition-colors group-hover:text-foreground">
							<PanelLeft aria-hidden="true" className="size-icon-2xs shrink-0" />
							{t("pr.openTab")}
						</span>
					)}
				/>
			) : (
				<InfoRow
					action={providerLink}
					icon={state.icon}
					iconClassName={state.className}
					label={titleLabel}
					title={pr.title}
				/>
			)}
			{cardView}

			{work?.hasRows ? (
				<div className="flex min-w-0 flex-col border-t border-(--color-border-settings-input) pt-2 pb-1">{work.content}</div>
			) : elsewhere}
			{/* One button for everything on this PR, at the foot of its card: the branch's next git step, then the PR's own. */}
			{!cloudOrgId && !guest && pr.state !== "merged" ? (
				<div className={cn("pt-2 pb-2", !work?.hasRows && !elsewhere && "border-t border-(--color-border-settings-input)")}>
					<PRActionButton canMerge={canMerge} git={work?.git} hostId={hostId} pr={pr} sessionId={sessionId} />
				</div>
			) : cloudOrgId && !guest && pr.state !== "merged" && work?.git ? (
				<div className={cn("pt-2 pb-2", !work.hasRows && !elsewhere && "border-t border-(--color-border-settings-input)")}>
					<CloudPRGitStepButton git={work.git} />
				</div>
			) : null}
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
	const row: ArtifactRow = { ...artifact, sessionId: session.id, sessionTitle: session.title ?? "", cloud: session.cloud, shareAccess: artifact.shareUrl ? shareAccess.get(artifact.shareUrl) ?? artifact.shareAccess : undefined };
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

function ActivityTimeline({ hostId, prs, session }: { hostId?: string; prs: SessionPRSummary[]; session: WorkspaceSession }) {
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
	const cloudReview = useQuery({
		queryKey: ["cloud-workspace-review", cloudBaseUrl, cloudOrgId ?? "", session.id, "summary"],
		enabled: cloudReady && cloudOrgId !== undefined && session.runtimeConnected === true && session.kind !== "orchestrator",
		placeholderData: (previous) => previous,
		queryFn: () => cloudCpClient.getWorkspaceReview(cloudOrgId!, session.id),
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
	const reviewerOwnerKey = sessionUiKey(session.id, hostId);
	const openReviewStates = openReviewStatesFor(session, reviewsQuery.data?.reviews ?? []);
	const reviewHeads = openReviewStates.map((review) => `${review.prUrl}@${review.targetSha}`).sort().join("\n");
	const currentHeadReviewed = openReviewStates.some((review) => review.latestRun?.targetSha === review.targetSha && (review.latestRun.status === "complete" || review.latestRun.status === "delivered"));
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
	}, [hostId, session.id, session.reviewerConfig?.mode, session.reviewerConfig?.model, session.reviewerHarness]);
	const saveReviewer = useMutation({
		mutationKey: [...reviewsKey, "switch-reviewer"],
		mutationFn: async ({ harness, model, mode }: { harness: ReviewerHarness | ""; model: string; mode: string }) => {
			const clearingToProjectDefault = harness === "" && model === "" && mode === "";
			const currentEffectiveReviewerHarness = (session.reviewerHarness ?? "") || currentDefaultReviewerHarness;
			const nextEffectiveReviewerHarness = harness || currentDefaultReviewerHarness;
			const existingReviewerConfig =
				!clearingToProjectDefault && currentEffectiveReviewerHarness === nextEffectiveReviewerHarness
					? session.reviewerConfig
					: undefined;
			const nextReviewerConfig = buildReviewerAgentConfig(existingReviewerConfig, model, mode);
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
			// Keep agent/model overrides scoped to this pass; the daemon selects the surface.
			const reviewerConfig = reviewerModel || reviewerMode
				? { ...(reviewerModel ? { model: reviewerModel } : {}), ...(reviewerMode ? { mode: reviewerMode } : {}) }
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
	const githubReviews = prSummaries.filter(
		(pr) =>
			(pr.state === "open" || pr.state === "draft") &&
			((pr.review?.reviews?.length ?? 0) > 0 ||
				(pr.review?.unresolvedBy ?? []).some((reviewer) => reviewer.count > 0) ||
				(pr.review?.resolvedBy ?? []).some((reviewer) => reviewer.count > 0)),
	);
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
				onTrigger={() => {
					if (currentHeadReviewed) setRerunConfirmation({ ownerKey: reviewerOwnerKey, heads: reviewHeads });
					else triggerReview.mutate({ ownerKey: reviewerOwnerKey });
				}}
				reviewerHandleId={reviewsQuery.data?.reviewerHandleId ?? ""}
				reviewerSurface={reviewsQuery.data?.reviewerSurface}
				reviewerActivityState={reviewsQuery.data?.reviewerActivityState}
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
				onReviewerOverrideChange={(next, config) => {
					setReviewerOverride(next);
					setReviewerModel(config.model ?? "");
					setReviewerMode(config.mode ?? "");
					saveReviewer.mutate({ harness: next, model: config.model ?? "", mode: config.mode ?? "" });
				}}
				onReviewerHarnessPreviewChange={(next) => {
					setReviewerOverride(next);
					setReviewerModel("");
					setReviewerMode("");
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
				githubPRs={githubReviews}
				isLoading={scmSummary.isLoading}
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
				isSwitchingReviewer={false}
				activeReviewers={[]}
				onOpenReviewer={() => undefined}
				isTriggering={triggerReview.isPending}
				notice={reviewNotice}
				onAutoReviewChange={(enabled) => updateAutoReview.mutate(enabled)}
				onCancel={() => cancelReview.mutate()}
				onKill={() => undefined}
				onReviewerHarnessPreviewChange={() => undefined}
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
		status: run.status,
		targetSha: run.targetSha,
		triggerSource: run.triggerSource,
		verdict: run.verdict,
	};
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
 * AO's own reviewer passes and the reviews humans and bots left on GitHub, in
 * one list keyed by PR. They were two sections, which made the same PR appear
 * twice and left the reader joining them up by number; a review is a review,
 * and what matters is who wrote it. Each group inside a PR names its source —
 * "AO codex" against the agent that ran, "On GitHub" for everyone else.
 */
function MergedReviewsSection({
	githubPRs,
	hostId,
	readOnly = false,
	isLoading,
	onOpenReviewFile,
	onWorkerMessageSent,
	reviewStates,
	runs,
	session,
}: {
	githubPRs: SessionPRSummary[];
	hostId?: string;
	/** Reached through a share: the reviews show, but only the owner acts on them. */
	readOnly?: boolean;
	isLoading: boolean;
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	onWorkerMessageSent?: () => void;
	reviewStates: PRReviewState[];
	runs: ReviewRunFacts[];
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const { client: cloudClient } = useCloudCp();
	const openInAOBrowser = useSessionBrowserLink(session);
	const workspaceKey = workspaceQueryKeyForHost(hostId);
	const openReviewStates = openReviewStatesFor(session, reviewStates);
	const runsByPR = runsByPRFrom(openReviewStates, runs);
	const aoStates = triggeredReviewStatesFrom(openReviewStates, runs);

	// Union by PR, newest PR first. A PR can appear on either side alone. A
	// multi-repository session can have the same number in two repositories, so
	// there a PR is its repository and number; a single-repository session
	// matches by number alone, as before.
	const prRepositories = new Set([
		...aoStates.map((state) => prRepositoryFromUrl(state.prUrl)),
		...githubPRs.map((pr) => pr.repo || prRepositoryFromUrl(pr.htmlUrl || pr.url)),
	].filter((repository): repository is string => Boolean(repository)).map((repository) => repository.toLowerCase()));
	const keyByRepository = prRepositories.size > 1;
	const prKey = (url: string | undefined, number: number, repo?: string) =>
		keyByRepository ? `${(repo || prRepositoryFromUrl(url) || "").toLowerCase()}#${number}` : `#${number}`;
	const byPR = new Map<string, { number: number; repository?: string; ao?: PRReviewState; github?: SessionPRSummary }>();
	for (const state of aoStates) {
		const key = prKey(state.prUrl, state.prNumber);
		byPR.set(key, { number: state.prNumber, repository: prRepositoryFromUrl(state.prUrl), ...byPR.get(key), ao: state });
	}
	for (const pr of githubPRs) {
		const key = prKey(pr.htmlUrl || pr.url, pr.number, pr.repo);
		byPR.set(key, { number: pr.number, repository: pr.repo || prRepositoryFromUrl(pr.htmlUrl || pr.url), ...byPR.get(key), github: pr });
	}
	const severalRepositories = keyByRepository;
	const rows = [...byPR.entries()].sort(([, a], [, b]) => b.number - a.number);
	const labels = reviewLabels(t);
	const requestRereview = async (review: InspectorGithubReview) => {
		const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/rerequest", {
			params: { path: { sessionId: session.id } },
			body: { pullRequestUrl: review.pullRequestUrl, reviewerId: review.reviewerId },
		});
		if (error) throw apiErrorException(error, "Unable to request re-review");
	};
	const resolveInlineComment = async (comment: InspectorInlineComment) => {
		const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/comments/resolve", {
			params: { path: { sessionId: session.id } },
			body: { pullRequestUrl: comment.pullRequestUrl, commentUrl: comment.url ?? "" },
		});
		if (error) throw apiErrorException(error, "Unable to resolve review comment");
		void queryClient.invalidateQueries({ queryKey: sessionScmSummaryQueryKey(session.id, hostId) });
		void queryClient.invalidateQueries({ queryKey: workspaceKey });
	};
	const sendMessageToWorker = useSendToSessionWorker(session, hostId);
	const sendInlineCommentToWorker = async (comment: InspectorInlineComment & { reviewerId?: string }) => {
		await sendMessageToWorker(formatInlineReviewCommentMessage(comment), "Unable to send review comment to worker agent");
		onWorkerMessageSent?.();
	};
	const sendReviewSummaryToWorker = async (summary: InspectorReviewSummaryAction) => {
		if (session.cloud) {
			// Cloud delivers the stored review through the durable control-plane queue.
			const matchingRun = [...runs]
				.sort((left, right) => right.createdAt.localeCompare(left.createdAt))
				.find(
					(run) =>
						(aoReviewCommentUrl(run) ?? run.prUrl) === (summary.url || summary.pullRequestUrl) &&
						run.body.trim() === summary.body.trim(),
				);
			if (!matchingRun) throw new Error("Unable to find the stored Cloud review to send to the worker agent");
			await cloudClient.sendSessionReviewToWorker(session.cloud.orgId, session.id, matchingRun.id);
			onWorkerMessageSent?.();
			return;
		}
		await sendMessageToWorker(formatReviewSummaryMessage(summary), "Unable to send review summary to worker agent");
		onWorkerMessageSent?.();
	};
	const groups: InspectorReviewGroup[] = rows.map(([key, { number, repository, ao, github }]) => {
		const prLabel = severalRepositories && repository ? `${repository} #${number}` : `#${number}`;
		const aoRuns = ao ? [...(runsByPR.get(ao.prUrl) ?? [])].sort((a, b) => b.createdAt.localeCompare(a.createdAt)) : [];
		const entries = (github?.review?.reviews ?? []).filter(
			(entry) => !externalReviewActorMatchesPRAuthor(entry.reviewerId, github?.author),
		);
		const allUnresolvedReviewers = github?.review?.unresolvedBy ?? [];
		const allResolvedReviewers = github?.review?.resolvedBy ?? [];
		const agentReviewIds = new Set(aoRuns.map((run) => run.githubReviewId).filter(Boolean));
		const agentComments = new Map<string, { inlineComments: InspectorInlineComment[]; resolvedComments: InspectorInlineComment[] }>();
		const partitionReviewerComments = (
			reviewers: typeof allUnresolvedReviewers,
			resolved: boolean,
		) => reviewers
			.map((reviewer) => {
				const externalLinks = [] as typeof reviewer.links;
				for (const link of reviewer.links) {
					const reviewId = link.reviewId?.trim();
					if (!reviewId || !agentReviewIds.has(reviewId)) {
						externalLinks.push(link);
						continue;
					}
					const comments = agentComments.get(reviewId) ?? { inlineComments: [], resolvedComments: [] };
					const comment = {
						autoInjectReview: link.autoInjectReview,
						body: link.body,
						file: link.file,
						line: link.line,
						pullRequestUrl: github?.url,
						resolved,
						reviewerId: reviewer.reviewerId,
						url: link.url || reviewer.reviewUrl,
					};
					if (resolved) comments.resolvedComments.push(comment);
					else comments.inlineComments.push(comment);
					agentComments.set(reviewId, comments);
				}
				return { ...reviewer, count: externalLinks.length, links: externalLinks };
			})
			.filter((reviewer) => reviewer.links.length > 0);
		const unresolvedReviewers = partitionReviewerComments(allUnresolvedReviewers, false).filter(
			(reviewer) => !externalReviewActorMatchesPRAuthor(reviewer.reviewerId, github?.author),
		);
		const resolvedReviewers = partitionReviewerComments(allResolvedReviewers, true).filter(
			(reviewer) => !externalReviewActorMatchesPRAuthor(reviewer.reviewerId, github?.author),
		);
		const unresolved = unresolvedReviewers.reduce((count, reviewer) => count + reviewer.count, 0);
		const reviewRuns = aoRuns.map((run) => {
			const reviewUrl = aoReviewCommentUrl(run);
			return {
				autoInjectReview: run.autoInjectReview,
				body: run.body,
				createdAtLabel:
					run.triggerSource === "agent"
						? `${formatTimeCompact(run.createdAt)} · ${t("inspector.review.requestedByAgent")}`
						: formatTimeCompact(run.createdAt),
				harness: run.harness || "reviewer",
				id: run.id,
				inlineComments: agentComments.get(run.githubReviewId)?.inlineComments ?? [],
				resolvedComments: agentComments.get(run.githubReviewId)?.resolvedComments ?? [],
				status: run.status,
				url: reviewUrl ?? (ao?.prUrl || null),
				verdict: githubVerdict(run.verdict, t),
			};
		});
		const unresolvedByReviewer = new Map(
			unresolvedReviewers.map((reviewer) => [reviewer.reviewerId, reviewer]),
		);
		const resolvedByReviewer = new Map(
			resolvedReviewers.map((reviewer) => [reviewer.reviewerId, reviewer]),
		);
		const externalEntries = entries.map((entry) => {
			const reviewer = unresolvedByReviewer.get(entry.reviewerId);
			const resolvedReviewer = resolvedByReviewer.get(entry.reviewerId);
			unresolvedByReviewer.delete(entry.reviewerId);
			resolvedByReviewer.delete(entry.reviewerId);
			return {
				body: entry.body,
				canRequestRereview: canRequestPRRereview(entry.verdict, github?.url),
				id: entry.reviewUrl || `${entry.reviewerId}:${entry.submittedAt}`,
				pullRequestUrl: github?.url,
				inlineComments: (reviewer?.links ?? []).map((link) => ({
					autoInjectReview: link.autoInjectReview,
					body: link.body,
					file: link.file,
					line: link.line,
					pullRequestUrl: github?.url,
					url: link.url || reviewer?.reviewUrl,
				})),
				resolvedComments: (resolvedReviewer?.links ?? []).map((link) => ({
					autoInjectReview: link.autoInjectReview,
					body: link.body,
					file: link.file,
					line: link.line,
					pullRequestUrl: github?.url,
					resolved: true,
					url: link.url || resolvedReviewer?.reviewUrl,
				})),
				isBot: entry.isBot,
				reviewerId: entry.reviewerId,
				reviewUrl: entry.reviewUrl,
				submittedAt: entry.submittedAt,
				submittedAtLabel: formatTimeCompact(entry.submittedAt),
				verdict: githubVerdict(entry.verdict, t),
			};
		});
		for (const reviewer of unresolvedByReviewer.values()) {
			const resolvedReviewer = resolvedByReviewer.get(reviewer.reviewerId);
			resolvedByReviewer.delete(reviewer.reviewerId);
			externalEntries.push({
				body: undefined,
				canRequestRereview: canRequestPRRereview("changes_requested", github?.url),
				id: `unresolved:${reviewer.reviewerId}:${key}`,
				pullRequestUrl: github?.url,
				inlineComments: reviewer.links.map((link) => ({
					autoInjectReview: link.autoInjectReview,
					body: link.body,
					file: link.file,
					line: link.line,
					pullRequestUrl: github?.url,
					url: link.url || reviewer.reviewUrl,
				})),
				resolvedComments: (resolvedReviewer?.links ?? []).map((link) => ({
					autoInjectReview: link.autoInjectReview,
					body: link.body,
					file: link.file,
					line: link.line,
					pullRequestUrl: github?.url,
					resolved: true,
					url: link.url || resolvedReviewer?.reviewUrl,
				})),
				isBot: reviewer.isBot,
				reviewerId: reviewer.reviewerId,
				reviewUrl: reviewer.reviewUrl,
				submittedAt: "",
				submittedAtLabel: "",
				verdict: githubVerdict("none", t),
			});
		}
		for (const reviewer of resolvedByReviewer.values()) {
			externalEntries.push({
				body: undefined,
				canRequestRereview: false,
				id: `resolved:${reviewer.reviewerId}:${key}`,
				pullRequestUrl: github?.url,
				inlineComments: [],
				resolvedComments: reviewer.links.map((link) => ({
					autoInjectReview: link.autoInjectReview,
					body: link.body,
					file: link.file,
					line: link.line,
					pullRequestUrl: github?.url,
					resolved: true,
					url: link.url || reviewer.reviewUrl,
				})),
				isBot: reviewer.isBot,
				reviewerId: reviewer.reviewerId,
				reviewUrl: reviewer.reviewUrl,
				submittedAt: "",
				submittedAtLabel: "",
				verdict: githubVerdict("none", t),
			});
		}
		return {
			ao: ao
				? {
						dimmed: ao.status === "ineligible",
						historical:
							ao.status === "needs_review" &&
							Boolean(ao.previousRun) &&
							(!ao.latestRun || ao.latestRun.status === "failed" || ao.latestRun.status === "cancelled"),
						notInjected: aoRuns.some((run) => run.autoInjectReview === false),
						runs: reviewRuns,
					}
				: undefined,
			github: github
				? {
						entries: externalEntries,
						notInjected:
							entries.some((review) => review.autoInjectReview === false) ||
							unresolvedReviewers.some((reviewer) =>
								reviewer.links.some((link) => link.autoInjectReview === false),
							),
						unresolved,
						unresolvedBy: unresolvedReviewers.map((reviewer) => ({
							count: reviewer.count,
							isBot: reviewer.isBot,
							links: reviewer.links.map((link) => ({
								autoInjectReview: link.autoInjectReview,
								body: link.body,
								file: link.file,
								line: link.line,
								pullRequestUrl: github?.url,
								url: link.url,
							})),
							reviewerId: reviewer.reviewerId,
							reviewUrl: reviewer.reviewUrl,
						})),
					}
				: undefined,
			meta: [
				ao ? aoReviewMeta(ao) : prLabel,
				unresolved > 0 ? t("inspector.unresolvedCount", { count: unresolved }) : null,
			]
				.filter(Boolean)
				.join(" · "),
			number,
			key,
			title: (ao?.title ?? github?.title)?.trim() || `PR ${prLabel}`,
			verdict: ao ? reviewVerdict(ao) : undefined,
		};
	}).filter((group) =>
		Boolean(group.ao || (group.github && (group.github.entries.length > 0 || group.github.unresolved > 0))),
	);
	return (
		<InspectorReviewsView
			externalLink={ProductExternalLink}
			groups={groups}
			isLoading={isLoading}
			labels={labels}
			onRequestRereview={readOnly ? undefined : requestRereview}
			onResolveInlineComment={readOnly ? undefined : resolveInlineComment}
			onOpenInAOBrowser={openInAOBrowser}
			onSendInlineComment={readOnly ? undefined : sendInlineCommentToWorker}
			onSendReviewSummary={readOnly ? undefined : sendReviewSummaryToWorker}
			onViewInlineCommentInFile={(comment) => {
				if (comment.file) onOpenReviewFile?.({ line: comment.line, path: comment.file });
			}}
			renderAvatar={(harness) => (
				<AgentAvatar className="size-5 shrink-0" decorative provider={harness} />
			)}
			renderMarkdown={renderReviewMarkdown}
		/>
	);
}

function canRequestPRRereview(verdict: string | undefined, pullRequestUrl: string | undefined): boolean {
	if (!pullRequestUrl) return false;
	return verdict !== "approved";
}

function reviewLabels(t: TFunction): InspectorReviewLabels {
	return {
		aoSource: t("inspector.reviewBySource.ao"),
		bot: t("inspector.bot"),
		earlierPass: t("inspector.earlierPass"),
		githubSource: t("inspector.reviewBySource.github"),
		loadingReviews: t("inspector.loadingReviews"),
		loadMoreReviews: (count) => t("inspector.loadMoreReviews", { count }),
		noPastReviewSummaries: t("inspector.noPastReviewSummaries"),
		notInjected: t("inspector.review.notInjected"),
		openComments: t("inspector.openComments"),
		openInAOBrowser: t("inspector.openInAOBrowser"),
		openInSystemBrowser: t("inspector.openInSystemBrowser"),
		openInlineComments: (count) => t("inspector.openInlineComments", { count }),
		requestRereviewPR: t("inspector.requestRereviewPR"),
		reviewActions: t("inspector.reviewActions"),
		reviews: t("inspector.reviews"),
		reviewedAt: (time) => t("inspector.reviewedAt", { time }),
		resolvedComments: (count) => t("inspector.resolvedComments", { count }),
		rereviewRequested: t("inspector.rereviewRequested"),
		rereviewRequestFailed: t("inspector.rereviewRequestFailed"),
		resolveComment: t("inspector.resolveComment"),
		resolvedReview: t("inspector.resolvedReview"),
		resolveReviewFailed: t("inspector.resolveReviewFailed"),
		sendToWorkerAgent: t("inspector.sendToWorkerAgent"),
		sentToWorkerAgent: t("inspector.sentToWorkerAgent"),
		sendToWorkerAgentError: t("inspector.sendToWorkerAgentError"),
		workerAgentWorkingOnFeedback: t("inspector.workerAgentWorkingOnFeedback"),
		showLatestReviewOnly: t("inspector.showLatestReviewOnly"),
		showLess: t("inspector.showLess"),
		showMore: t("inspector.showMore"),
		commentNumber: (number) => t("inspector.commentNumber", { number }),
		unresolvedCount: (count) => t("inspector.unresolvedCount", { count }),
		viewInFile: t("inspector.viewInFile"),
		viewInFileWorkInProgress: t("inspector.viewInFileWorkInProgress"),
		viewOnPR: t("inspector.viewOnPR"),
	};
}

function renderReviewMarkdown(body: string) {
	return (
		<ReactMarkdown
			components={{
				a: ({ href, children }) => (
					<AppLink href={href} target="_blank" rel="noopener noreferrer">
						{children}
					</AppLink>
				),
			}}
			remarkPlugins={[remarkGfm]}
		>
			{body}
		</ReactMarkdown>
	);
}

function buildReviewerAgentConfig(
	existing: WorkspaceSession["reviewerConfig"] | undefined,
	model: string,
	mode: string,
): { model?: string; mode?: string; permissions?: string } | undefined {
	const next = { ...existing };
	if (model) next.model = model;
	else delete next.model;
	if (mode) next.mode = mode;
	else delete next.mode;
	return Object.keys(next).length > 0 ? next : undefined;
}

function formatInlineReviewCommentMessage(comment: InspectorInlineComment & { reviewerId?: string }): string {
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

function formatReviewSummaryMessage(summary: InspectorReviewSummaryAction): string {
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
	onReviewerOverrideChange,
	onReviewerHarnessPreviewChange,
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
	onReviewerOverrideChange: (next: ReviewerHarness | "", config: { model?: string; mode?: string }) => void;
	onReviewerHarnessPreviewChange: (next: ReviewerHarness | "") => void;
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
	// A failed run stores its reason in body. Show it for manual triggers too:
	// the trigger request itself only returns a generic "Review operation failed".
	const latestFailure = reviewStates
		.map((review) => review.latestRun)
		.filter(
			(run): run is ReviewRunFacts =>
				Boolean(
					run?.status === "failed" &&
						run.body?.trim() &&
						(run.triggerSource !== "auto" || autoReviewEnabled),
				),
		)
		.sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
	const [dismissedFailureId, setDismissedFailureId] = useState<string | null>(null);
	useEffect(() => {
		if (!latestFailure || latestFailure.id === dismissedFailureId) return;
		const timer = window.setTimeout(() => setDismissedFailureId(latestFailure.id), 10_000);
		return () => window.clearTimeout(timer);
	}, [dismissedFailureId, latestFailure]);
	if (sortedPRs(session).length === 0) {
		return <Section surface={false} title={t("inspector.review.controls")}><p className={inspectorEmptyClass}>{t("inspector.noPROpened")}</p></Section>;
	}
	if (isLoading) {
		return <Section surface={false} title={t("inspector.review.controls")}><p className={inspectorEmptyClass}>{t("inspector.loadingReviews")}</p></Section>;
	}

	const openReviewStates = openReviewStatesFor(session, reviewStates);
	// Whichever PR happens to come first is not the reviewer to name. With one PR
	// reviewed earlier by claude-code and another running under codex, taking the
	// first run reported the wrong agent as the one working. Prefer the run
	// actually in flight, then the newest recorded one.
	const runningRun = openReviewStates.find((review) => review.status === "running")?.latestRun;
	const newestRun = openReviewStates
		.map((review) => review.latestRun)
		.filter((run): run is NonNullable<typeof run> => Boolean(run))
		.sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
	const latest = runningRun ?? newestRun;
	const resolvedDefaultHarness = resolveDefaultReviewerHarness(config, session.provider);
	const effectiveReviewerHarness = reviewerOverride || resolvedDefaultHarness;
	const activeReviewerHarness = latest?.harness || effectiveReviewerHarness;
	const reviewFailure =
		latestFailure && latestFailure.id !== dismissedFailureId ? latestFailure : null;
	const hasReviewerSession = reviewerHandleId.trim() !== "" ||
		Boolean(reviewerSurface?.mode === "chat" && reviewerSurface.reviewId);
	const reviewRunning = reviewIsRunning(openReviewStates);
	const reviewLive = reviewHasLiveActivity(openReviewStates, reviewerActivityState, hasReviewerSession);
	const reviewHasRun = reviewRunning || Boolean(latest);
	const runAction = reviewSessionRunAction(openReviewStates, isTriggering);
	const runDisabled = isKilling || isSwitchingReviewer || (cloud && !cloudReviewerReady) || reviewRunDisabled(openReviewStates, isTriggering);
	const primaryReviewActionLabel = reviewRunning
		? isCancelling
			? t("inspector.review.cancelling")
			: t("inspector.review.cancel")
		: runAction;
	const archiveActionLabel = isKilling ? t("inspector.review.killingSession") : t("inspector.review.killSession");
	const killDisabled = isKilling || isCancelling || isTriggering || isSwitchingReviewer || !hasReviewerSession;

	return (
		<Section surface={false} title={t("inspector.review.controls")}>
				<div className="flex min-w-0 flex-col gap-3">
					{error ? (
						<p className="m-0 rounded-md border border-error/28 bg-error/8 px-2.5 py-2 text-xs leading-normal text-error">
							{apiErrorMessage(error, t("inspector.reviewRequestFailed"))}
					</p>
				) : null}
				{reviewFailure ? (
					<p className="m-0 rounded-md border border-error/28 bg-error/8 px-2.5 py-2 text-xs leading-normal text-error" role="status">
						<span className="font-semibold">
							{reviewFailure.triggerSource === "auto" ? t("inspector.autoReview") : t("pr.section.review")}{" "}
							{t("inspector.review.failed")}:
						</span>{" "}
						{reviewFailure.body.trim()}
					</p>
				) : null}
				{notice ? (
					<Tooltip>
						<TooltipTrigger asChild>
							<Pressable
								aria-label={notice}
								className="flex max-w-full shrink-0 items-start gap-1 self-start rounded-sm text-2xs font-medium leading-normal text-muted-foreground transition-colors hover:text-foreground"
							>
								<Info aria-hidden="true" className="mt-px size-icon-2xs shrink-0" />
								<span className="min-w-0 text-xs">{t("inspector.reviewAlreadyRanShort")}</span>
							</Pressable>
						</TooltipTrigger>
						<TooltipContent className="max-w-56 leading-normal">{notice}</TooltipContent>
					</Tooltip>
				) : null}
				{readOnly ? null : (
				<div className="review-run-controls-container flex min-w-0 flex-col text-xs">
					{cloud ? (
						<div className="flex min-h-8 min-w-0 items-center justify-between gap-3 py-1">
							<span className="min-w-0 text-sm text-settings-label">{t("inspector.selectReviewerAgent")}</span>
							<div className="flex min-w-0 items-center justify-end gap-1.5">
								<Select
									disabled={reviewRunning || isTriggering || isCancelling || isInstallingCloudHarness}
									onValueChange={(harness) => onCloudReviewerHarnessChange?.(harness)}
									value={cloudReviewerHarness}
								>
									<SelectTrigger
										aria-label={t("inspector.selectReviewerAgent")}
										className="h-control-md max-w-[11rem] text-xs"
										size="sm"
									>
										<SelectValue placeholder={t("inspector.selectReviewerAgent")} />
									</SelectTrigger>
									<SelectContent align="end">
										{["claude-code", "codex", "cursor"].map((harness) => (
											<SelectItem key={harness} value={harness}>
												{agentLabel(harness)}
												{cloudReviewerHarnesses.includes(harness) ? "" : " · Connect credential"}
											</SelectItem>
										))}
									</SelectContent>
								</Select>
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
										{isInstallingCloudHarness ? "Installing…" : "Install"}
									</Button>
								) : null}
							</div>
						</div>
					) : (
					<div className="flex min-h-8 min-w-0 items-center justify-between gap-3 py-1">
						<span className="min-w-0 text-sm text-settings-label">
							{t("inspector.selectReviewerAgent")}
						</span>
						<ReviewerSelect
							ariaLabel={t("inspector.selectReviewerAgent")}
							agents={agentCatalog?.agents}
							contentAlign="end"
							defaultHarness={resolvedDefaultHarness}
							disabled={reviewRunning || autoReviewEnabled || isKilling || isSwitchingReviewer || isTriggering || isCancelling}
							onChange={(next) => onReviewerHarnessPreviewChange(next as ReviewerHarness | "")}
							onConfigChange={(harness, config) => onReviewerOverrideChange(harness as ReviewerHarness | "", config)}
							model={reviewerModel}
							mode={reviewerMode}
							hostId={hostId}
							projectId={session.workspaceId}
							triggerClassName="review-run-agent-select ml-auto h-control-md w-auto min-w-0 shrink-0 justify-end px-2 text-right text-xs"
							value={reviewerOverride}
						/>
					</div>
					)}
					{/* Several reviewers can work on one worker at once (an agent may
					    ask a reviewer other than the selected one). The reviewer tab
					    shows one at a time, so each live reviewer gets a row to open it
					    whenever any of them is not the selected reviewer. */}
					{activeReviewers.some((surface) => surface.reviewId !== reviewerSurface?.reviewId)
						? activeReviewers.map((surface) => (
								<div className="flex min-h-8 min-w-0 items-center justify-between gap-3 py-1" key={surface.reviewId}>
									<span className="flex min-w-0 items-center gap-1.5 text-sm text-foreground">
										<AgentAvatar className="size-4" decorative provider={surface.harness} />
										<span className="truncate">{agentLabel(surface.harness)}</span>
									</span>
									<Button
										className="shrink-0 px-1.5"
										disabled={surface.mode !== "chat" && !surface.handleId}
										onClick={() => onOpenReviewer(surface)}
										size="sm"
										type="button"
										variant="ghost"
									>
										{t("inspector.review.openReviewer")}
									</Button>
								</div>
							))
						: null}
					<InspectorPolicyRow
						checked={autoReviewEnabled}
						description={t("inspector.autoReviewDescription")}
						disabled={isAutoReviewSaving}
						id={`auto-review-${sessionUiKey(session.id, hostId)}`}
						label={t("inspector.autoReview")}
						onCheckedChange={onAutoReviewChange}
						tooltipClassName="max-w-64"
					/>
					<div className="flex min-h-8 min-w-0 items-center justify-between gap-3 py-1">
						<span className="text-sm text-settings-label">{t("inspector.review.session")}</span>
						<div className="flex min-w-0 items-center justify-end gap-1.5">
							<Button
								aria-label={primaryReviewActionLabel}
								className="shrink-0 gap-1 px-1.5 [&_svg]:size-icon-sm"
								disabled={reviewRunning ? isCancelling || isKilling || isSwitchingReviewer : runDisabled || autoReviewEnabled}
								onClick={reviewRunning ? onCancel : onTrigger}
								size="sm"
								type="button"
								variant={reviewRunning ? "ghost" : reviewHasRun ? "secondary" : "primary"}
							>
								{reviewRunning ? <X aria-hidden="true" /> : <Play aria-hidden="true" />}
								<span className="review-run-action-label">{primaryReviewActionLabel}</span>
							</Button>
							{!cloud ? (
							<Tooltip>
								<TooltipTrigger asChild>
									<span className="inline-flex">
										<Button
											aria-label={archiveActionLabel}
											className="shrink-0 [&_svg]:size-icon-sm"
											disabled={killDisabled}
											onClick={onKill}
											size="icon-sm"
											type="button"
											variant="ghost"
										>
											<Archive aria-hidden="true" />
										</Button>
									</span>
								</TooltipTrigger>
								<TooltipContent>{archiveActionLabel}</TooltipContent>
							</Tooltip>
							) : null}
						</div>
					</div>
				</div>
				)}
				{reviewLive ? (
					<div className="flex items-center gap-2 border-t border-border pt-3">
						<Loader2 aria-hidden="true" className="size-icon-sm shrink-0 animate-spin text-muted-foreground" />
						<span className="min-w-0 flex-1 truncate text-xs font-medium text-muted-foreground">
							{isCancelling
								? t("inspector.review.cancelling")
								: `Review in progress · ${agentLabel(activeReviewerHarness)}`}
						</span>
					</div>
				) : null}
			</div>
		</Section>
	);
}

function githubVerdict(verdict: string, t: TFunction): { label: string; tone: "neutral" | "running" | "success" | "danger" } {
	switch (verdict) {
		case "approved":
			return { label: t("inspector.review.approved"), tone: "success" };
		case "changes_requested":
			return { label: t("inspector.review.changesRequested"), tone: "danger" };
		case "review_required":
			return { label: t("inspector.review.notRun"), tone: "neutral" };
		default:
			return { label: t("inspector.review.commented"), tone: "neutral" };
	}
}

/* The reviews view is assembled from two queries that live in different
   components, so the derivations both need are module-level rather than
   recomputed (and allowed to drift) in each. */

/** Every recorded reviewer pass per PR, so each reviewer keeps its own tab.
 *  Falls back to the state's own runs against a daemon predating the runs field. */
function runsByPRFrom(openReviewStates: PRReviewState[], runs: ReviewRunFacts[]): Map<string, ReviewRunFacts[]> {
	const byPR = new Map<string, ReviewRunFacts[]>();
	for (const run of runs.filter(
		(run) => (run.status === "complete" || run.status === "delivered") && Boolean(run.body?.trim()),
	)) {
		byPR.set(run.prUrl, [...(byPR.get(run.prUrl) ?? []), run]);
	}
	if (runs.length === 0) {
		for (const state of openReviewStates) {
			const fallback = [state.latestRun, state.previousRun].filter(
				(run): run is ReviewRunFacts =>
					Boolean(run) &&
					(run!.status === "complete" || run!.status === "delivered") &&
					Boolean(run!.body?.trim()),
			);
			if (fallback.length > 0) byPR.set(state.prUrl, fallback);
		}
	}
	return byPR;
}

function reviewRunHasOutcome(run: ReviewRunFacts | undefined): boolean {
	return Boolean(run?.verdict?.trim());
}

/** The PRs AO has an agent review outcome for. */
function triggeredReviewStatesFrom(openReviewStates: PRReviewState[], runs: ReviewRunFacts[]): PRReviewState[] {
	return openReviewStates.filter(
		(reviewState) =>
			reviewRunHasOutcome(reviewState.latestRun) ||
			reviewRunHasOutcome(reviewState.previousRun) ||
			runs.some((run) => run.prUrl === reviewState.prUrl && reviewRunHasOutcome(run)) ||
			reviewState.status === "up_to_date" ||
			reviewState.status === "changes_requested",
	);
}

function aoReviewMeta(reviewState: PRReviewState): string {
	if (
		reviewState.status === "needs_review" &&
		(!reviewState.latestRun ||
			reviewState.latestRun.status === "failed" ||
			reviewState.latestRun.status === "cancelled")
	) {
		return appI18n.t("inspector.latestCommitNotReviewedMeta", { number: reviewState.prNumber });
	}
	const displayRun = reviewState.latestRun ?? reviewState.previousRun;
	if (displayRun?.createdAt) {
		return `#${reviewState.prNumber} · ${formatTimeCompact(displayRun.createdAt)}`;
	}
	if (!displayRun && (reviewState.status === "needs_review" || reviewState.status === "ineligible")) {
		return appI18n.t("inspector.notRunMeta", { number: reviewState.prNumber });
	}
	return `#${reviewState.prNumber}`;
}

// GitHub anchors a posted review at #pullrequestreview-<id> on the PR page; we
// only have that link once the run has been delivered to GitHub.
function aoReviewCommentUrl(run: PRReviewState["latestRun"]): string | null {
	if (!run?.prUrl || !run.githubReviewId) return null;
	return `${run.prUrl}#pullrequestreview-${run.githubReviewId}`;
}

function reviewVerdict(reviewState: PRReviewState): {
	label: string;
	tone: "neutral" | "running" | "success" | "danger";
} {
	if (reviewState.status === "needs_review") {
		return { label: appI18n.t("inspector.review.needed"), tone: "neutral" };
	}
	if (reviewState.latestRun?.status === "failed") {
		return { label: appI18n.t("inspector.review.failed"), tone: "danger" };
	}
	if (reviewState.latestRun?.status === "cancelled") {
		return { label: appI18n.t("inspector.review.cancelled"), tone: "neutral" };
	}
	switch (reviewState.status) {
		case "running":
			return { label: appI18n.t("inspector.review.reviewing"), tone: "running" };
		case "up_to_date":
			return { label: appI18n.t("inspector.review.approved"), tone: "success" };
		case "changes_requested":
			return { label: appI18n.t("inspector.review.changesRequested"), tone: "danger" };
		case "ineligible":
			return { label: appI18n.t("inspector.review.notRun"), tone: "neutral" };
	}
	return { label: appI18n.t("inspector.review.notRun"), tone: "neutral" };
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
