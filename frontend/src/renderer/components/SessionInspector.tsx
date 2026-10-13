import { MessageSquare } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { AppLink } from "./AppLink";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNavigate } from "@tanstack/react-router";
import { memo, useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import type { TFunction } from "i18next";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
	InspectorActivityTimelineView,
	InspectorReviewComments,
	InspectorReviewMarkdown,
	InspectorSection as Section,
	SessionInspectorShellView,
	SessionInspectorSummaryView,
	inspectorEmptyClass,
	prBranchRange,
	type InspectorActionMenuItem,
	type InspectorPullRequest,
	type PRDisplayTone,
	type InspectorInlineComment,
	type InspectorGithubReview,
	type InspectorReviewLabels,
	type InspectorReviewSummaryAction,
	type InspectorTimelineEvent,
	type InspectorView,
} from "@aoagents/product-ui";
import {
	Archive,
	ArrowUpRight,
	Bell,
	Check,
	ChevronRight,
	CircleAlert,
	CircleCheck,
	CircleDashed,
	CircleX,
	GitBranch,
	GitPullRequest,
	GitPullRequestClosed,
	GitPullRequestDraft,
	GitMerge,
	Globe,
	History,
	Info,
	Play,
	Loader2,
	MoreHorizontal,
	MoreVertical,
	PanelRight,
	SearchCheck,
	Shapes,
	CircleStop,
	SquareTerminal,
	type LucideIcon,
} from "lucide-react";
import type { components } from "../../api/schema";
import { reviewerConversationQueryKey } from "../hooks/useReviewerConversation";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { sessionUiKey } from "../lib/hosts";
import { projectNavigateTarget, sessionNavigateTarget } from "../lib/navigate-to-session";
import { WORKER_DEFAULT_REVIEWERS } from "../lib/reviewer-harnesses";
import { cloudSessionsQueryKey, workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { useOpenArtifactPreview } from "../hooks/useOpenArtifactPreview";
import { captureRendererEvent } from "../lib/telemetry";
import { formatTimeCompact } from "../lib/format-time";
import { SessionArtifactsView, type ArtifactOpenRequest } from "./SessionArtifactsView";
import { DropdownMenu as DropdownMenuPrimitive } from "radix-ui";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { actionMenuItemClass } from "./ui/menu-styles";
import { openLinkInSystemBrowser } from "../lib/external-link-policy";
import { OrchestratorChildrenSection } from "./OrchestratorChildrenSection";
import { TopbarButton } from "./TopbarButton";
import { DiffCounts, InfoRow, InfoRowLink } from "./InspectorInfoRow";
import { ProductExternalLink } from "./ProductExternalLink";
import { ResumeAgentControl } from "./ResumeAgentControl";
import { SessionBranchSummary } from "./SessionBranchSummary";
import {
	sessionScmSummaryQueryKey,
	useSessionScmSummary,
	type SessionPRReference,
	type SessionPRSummary,
} from "../hooks/useSessionScmSummary";
import { sessionWorkspaceFilesQueryKey, sessionWorkspaceHistoryQueryOptions, useSessionWorkspaceFilesChangedCount } from "../hooks/useSessionWorkspaceFiles";
import { useCloudCp } from "../hooks/useCloudCp";
import { useSessionBrowserLink } from "../hooks/useSessionBrowserLink";
import { clearTerminateSessionState, useTerminateSession } from "../hooks/useTerminateSession";
import { prBrowserUrl, prCanMerge, prCardPresentation, sessionPRDisplaySummaries } from "../lib/pr-display";
import type { SessionArtifact, WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import {
	openPRs,
	resolveNextNavigationAfterSessionKill,
	sessionArtifacts,
	sortedPRs,
	STANDALONE_WORKSPACE_ID,
} from "../types/workspace";
import { getAgentActivityView, getSessionTimelinePillView } from "../lib/session-presentation";
import { BrowserPanelView, type BrowserAnnotationQueueModel } from "./BrowserPanel";
import type { BrowserViewModel } from "../hooks/useBrowserView";
import { FilesTopbarHostContext } from "./files-topbar-host";
import { useUiStore } from "../stores/ui-store";
import { Button } from "./ui/button";
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
	reviewRunDisabled,
	reviewSessionRunAction,
	sessionReviewsQueryOptions,
	sessionReviewsQueryKey,
	type PRReviewState,
	type ReviewRunFacts,
} from "../lib/session-reviews";
import type { CloudCpAOReviewRun, CloudCpSessionReviewState, CloudCpPullRequestSummary } from "../lib/cloud-cp";

type ProjectConfig = components["schemas"]["ProjectConfig"];
type OpenReviewerTerminal = (target: { handleId: string; harness: string }) => void;
type ReviewerSurface = components["schemas"]["DomainReviewerSurface"];

export type { InspectorView } from "@aoagents/product-ui";

const VIEW_DEFS: {
	id: InspectorView;
	labelKey: "inspector.summary" | "inspector.reviewTab" | "inspector.browser" | "inspector.sideChat" | "inspector.files";
	icon: ReactNode;
}[] = [
	{
		id: "summary",
		labelKey: "inspector.summary",
		icon: (
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
				<line x1="8" y1="7" x2="20" y2="7" />
				<line x1="8" y1="12" x2="20" y2="12" />
				<line x1="8" y1="17" x2="16" y2="17" />
				<circle cx="4" cy="7" r="1" />
				<circle cx="4" cy="12" r="1" />
				<circle cx="4" cy="17" r="1" />
			</svg>
		),
	},
	{
		id: "reviews",
		labelKey: "inspector.reviewTab",
		icon: <SearchCheck aria-hidden="true" />,
	},
	{
		id: "browser",
		labelKey: "inspector.browser",
		icon: (
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
				<circle cx="12" cy="12" r="9" />
				<line x1="3" y1="12" x2="21" y2="12" />
				<path d="M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18" />
			</svg>
		),
	},
	{ id: "sideChat", labelKey: "inspector.sideChat", icon: <MessageSquare aria-hidden="true" /> },
	{
		id: "files",
		labelKey: "inspector.files",
		icon: (
			<svg
				viewBox="0 0 24 24"
				fill="none"
				stroke="currentColor"
				strokeWidth="1.7"
				strokeLinecap="round"
				strokeLinejoin="round"
				aria-hidden="true"
				data-testid="files-viewer-icon"
			>
				<path d="M3.5 7.5V5.75A1.75 1.75 0 0 1 5.25 4h4l2 2h7.5a1.75 1.75 0 0 1 1.75 1.75V18a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2V7.5Z" />
				<path d="M7 10h10M7 13.5h8M7 17h6" />
			</svg>
		),
	},
];

const prStateLabelKeys: Record<SessionPRSummary["state"], MessageKey> = {
	open: "pr.state.open",
	draft: "pr.state.draft",
	merged: "pr.state.merged",
	closed: "pr.state.closed",
};

/**
 * Tabbed inspector rail beside the terminal (Summary · Reviews · Browser · Files).
 */
/**
 * Whether the topbar's trailing column is too narrow for the ⋮ plus the
 * reserved space under the window-pinned controls, so they would slide over
 * the address or filter field. The field's grid track does not depend on the
 * trailing content, so folding never changes the measurement it was based on.
 */
function usePinnedActionsFold(
	trailing: HTMLElement | null,
	field: HTMLElement | null,
	onChange: ((folded: boolean) => void) | undefined,
): boolean {
	const [folded, setFolded] = useState(false);
	const foldedRef = useRef(false);
	const unfoldedWidth = useRef(0);
	useLayoutEffect(() => {
		const topbar = trailing?.parentElement;
		if (!trailing || !topbar || !field) {
			foldedRef.current = false;
			setFolded(false);
			return;
		}
		const measure = () => {
			const trailingRect = trailing.getBoundingClientRect();
			if (!foldedRef.current) unfoldedWidth.current = trailingRect.width;
			const fieldRect = field.getBoundingClientRect();
			// Narrow layouts move the field to its own row, where nothing overlaps.
			const sameRow = fieldRect.top < trailingRect.bottom && fieldRect.bottom > trailingRect.top;
			const next = sameRow && topbar.getBoundingClientRect().right - fieldRect.right < unfoldedWidth.current;
			foldedRef.current = next;
			setFolded(next);
		};
		measure();
		const observer = new ResizeObserver(measure);
		observer.observe(topbar);
		observer.observe(field);
		return () => observer.disconnect();
	}, [field, trailing]);
	useEffect(() => {
		onChange?.(folded);
	}, [folded, onChange]);
	useEffect(() => () => onChange?.(false), [onChange]);
	return folded;
}

export const SessionInspector = memo(function SessionInspector({
	browserOnly = false,
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
	notificationsUnread = 0,
	onOpenNotifications,
	onPinnedActionsFoldChange,
	onToggleInspector,
}: {
	browserOnly?: boolean;
	session?: WorkspaceSession;
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
	/**
	 * The window-pinned inspector toggle and bell sit over the topbar's right
	 * edge. When the address or filter field leaves them too little room, the
	 * inspector asks the owner to hide them and offers both from its ⋮ menu.
	 */
	onPinnedActionsFoldChange?: (folded: boolean) => void;
	onToggleInspector?: () => void;
	onOpenNotifications?: () => void;
	notificationsUnread?: number;
}) {
	const { t } = useTranslation();
	const hostId = hostIdProp ?? session?.hostId;
	const onlyBrowser = browserOnly;
	const [internalView, setInternalView] = useState<InspectorView>("summary");
	const [browserTopbarHost, setBrowserTopbarHost] = useState<HTMLDivElement | null>(null);
	const [filesTopbarHost, setFilesTopbarHost] = useState<HTMLDivElement | null>(null);
	const [trailingActions, setTrailingActions] = useState<HTMLDivElement | null>(null);
	const openingNotifications = useRef(false);
	const pinnedActionsFolded = usePinnedActionsFold(
		onPinnedActionsFoldChange && isInspectorVisible ? trailingActions : null,
		browserTopbarHost ?? filesTopbarHost,
		onPinnedActionsFoldChange,
	);
	const requestedView = viewProp ?? internalView;
	// Badge the Browser tab when a preview target arrived without us opening it.
	const browserUnseen = useUiStore((state) =>
		session ? Boolean(state.inspectorSessions[sessionUiKey(session.id, hostId)]?.browserUnseen) : false,
	);
	const inspectorQueryClient = useQueryClient();
	const localFilesChangedCount = useSessionWorkspaceFilesChangedCount(
		onlyBrowser || session?.cloud ? undefined : session?.id,
		hostId,
	);
	const localWorkspaceData = session ? inspectorQueryClient.getQueryData<{ files?: unknown[] }>(sessionWorkspaceFilesQueryKey(session.id, hostId)) : undefined;
	const { client: cloudCpClient, ready: cloudReady, baseUrl: cloudBaseUrl } = useCloudCp();
	const cloudOrgId = session?.cloud?.orgId;
	const cloudReview = useQuery({
		queryKey: ["cloud-workspace-review", cloudBaseUrl, cloudOrgId ?? "", session?.id ?? "", "summary"],
		enabled: cloudReady && session?.cloud !== undefined && cloudOrgId !== undefined,
		refetchInterval: 5_000,
		queryFn: () => cloudCpClient.getWorkspaceReview(cloudOrgId!, session!.id),
	});
	const cloudFilesChangedCount = cloudReview.data?.summary.files;
	const filesChangedCount = session?.cloud ? (cloudFilesChangedCount ? cloudFilesChangedCount : undefined) : localFilesChangedCount;
	const hasWorkspaceInventory = session?.cloud ? Boolean(cloudReview.data?.files.length) : Boolean(localWorkspaceData?.files?.length);
	const setView = useCallback((next: InspectorView) => {
		setInternalView(next);
		onViewChange?.(next);
		if (next === "files") onOpenFiles?.();
	}, [onOpenFiles, onViewChange]);
	const openReviews = useCallback(() => setView("reviews"), [setView]);
	const openFilesView = useCallback(() => setView("files"), [setView]);
	// A persisted/controlled Reviews selection can outlive the last reviewable PR.
	// Keep the shell on a real, visible tab instead of rendering an empty, unlabelled body.
	const reviewsAvailable = reviewsTabVisible(session);
	const availableViewDefs = onlyBrowser ? VIEW_DEFS.filter((entry) => entry.id === "browser")
		: VIEW_DEFS.filter((entry) => (!hostId || entry.id !== "browser" || browserView) && (reviewsAvailable || entry.id !== "reviews")
			// The host passes a side chat view only where side chats can run.
			&& (entry.id !== "sideChat" || sideChatView !== undefined));
	// Artifacts has no tab; the overflow menu opens it.
	const view: InspectorView = onlyBrowser ? "browser" : requestedView === "artifacts" && session ? "artifacts" : availableViewDefs.some((entry) => entry.id === requestedView) ? requestedView : "summary";
	useEffect(() => {
		if (view === requestedView) return;
		setInternalView(view);
		onViewChange?.(view);
	}, [onViewChange, requestedView, view]);
	const tabs = availableViewDefs.map((entry) => {
		const label = t(entry.labelKey);
		return {
			...entry,
			badge: entry.id === "browser" && browserUnseen,
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
							<FilesView filesView={filesView} onOpenFiles={onOpenFiles} />
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
								{/* The ⋮ sits just left of the pinned inspector toggle in every view. */}
								<div className="session-inspector__trailing flex shrink-0 items-center" ref={setTrailingActions}>
									{(session && !onlyBrowser) || pinnedActionsFolded ? (
										<DropdownMenu>
											<DropdownMenuTrigger asChild>
												<TopbarButton
													aria-label={t("inspector.more")}
													// -mr-2 pulls it into the spacer's lead so it sits the same 4px from the toggle as the toggle from the bell.
													className={cn(
														"relative data-[active]:bg-interactive-active data-[active]:text-foreground data-[state=open]:bg-interactive-active data-[state=open]:text-foreground",
														!pinnedActionsFolded && "-mr-2",
													)}
													data-active={view === "artifacts" ? "" : undefined}
													title={t("inspector.more")}
													variant="icon"
												>
													<MoreVertical aria-hidden="true" className="size-icon-md" />
													{pinnedActionsFolded && notificationsUnread > 0 ? (
														<span aria-hidden="true" className="pointer-events-none absolute right-1 top-1 size-1.5 rounded-full bg-accent-strong ring-2 ring-background" />
													) : null}
												</TopbarButton>
											</DropdownMenuTrigger>
											<DropdownMenuContent
												align="end"
												className="min-w-44"
												data-browser-native-overlay="true"
												// Opening notifications from here hands focus to their popover.
												onCloseAutoFocus={(event) => {
													if (openingNotifications.current) event.preventDefault();
												}}
											>
												{session && !onlyBrowser ? (
													<DropdownMenuItem onSelect={() => setView("artifacts")}>
														<Shapes aria-hidden="true" />
														{t("artifacts.title")}
													</DropdownMenuItem>
												) : null}
												{pinnedActionsFolded && onToggleInspector ? (
													<DropdownMenuItem onSelect={onToggleInspector}>
														<PanelRight aria-hidden="true" />
														{t("shell.closeInspector")}
													</DropdownMenuItem>
												) : null}
												{pinnedActionsFolded && onOpenNotifications ? (
													<DropdownMenuItem
														onSelect={() => {
															openingNotifications.current = true;
															requestAnimationFrame(() => {
																openingNotifications.current = false;
																onOpenNotifications();
															});
														}}
													>
														<Bell aria-hidden="true" />
														{notificationsUnread > 0 ? t("notify.unreadCount", { count: notificationsUnread }) : t("notify.bell")}
													</DropdownMenuItem>
												) : null}
											</DropdownMenuContent>
										</DropdownMenu>
									) : null}
									{pinnedActionsFolded ? null : <span aria-hidden="true" className="session-inspector-actions-spacer" />}
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
							key={sessionUiKey(session.id, hostId)}
							onOpenArtifact={onOpenArtifact}
							onOpenFiles={openFilesView}
							onOpenReviews={openReviews}
							session={session}
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
	onOpenFiles,
	onOpenReviews,
	session,
}: {
	canOpenReviews: boolean;
	onOpenArtifact?: (target: { feedback?: boolean; path: string }) => void;
	hostId?: string;
	onOpenFiles: () => void;
	onOpenReviews: () => void;
	session: WorkspaceSession;
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
	const prSummaries = sessionPRDisplaySummaries(session, query.data?.prs);
	const prCount = prSummaries.length + linkedPRs.length;
	const hasPRs = prCount > 0;
	const artifacts = sessionArtifacts(session);
	const hasArtifacts = artifacts.length > 0;
	const opensPR = hasPRs || session.outputType === "pr" || session.outputType === "pr_artifact";
	const prSectionTitle = prCount > 1 ? t("inspector.pullRequests", { count: prCount }) : opensPR ? t("inspector.pullRequest") : t("inspector.branch");
	const artifactSectionTitle = artifacts.length > 1
		? t("inspector.artifacts", { count: artifacts.length })
		: t("inspector.artifact");
	const openPRNumber = prSummaries.find((pr) => pr.state === "open" || pr.state === "draft")?.number;
	// CI, review, and merge policies act on a PR. Standalone sessions keep the row for Archive.
	const showSessionControls = hasPRs || session.workspaceId === STANDALONE_WORKSPACE_ID;
	// Cloud orchestrators list the workers they spawned; local orchestrators
	// have no parent/child model and every other session has no children.
	const showWorkers =
		session.kind === "orchestrator" && (session.cloud !== undefined || usePreviewData);
	return (
		<SessionInspectorSummaryView
			activity={
				<>
					<ActivityTimeline hostId={hostId} prs={prSummaries} session={session} />
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
				hasArtifacts ? <ArtifactSummaryList artifacts={artifacts} onOpenArtifact={onOpenArtifact} session={session} /> : undefined
			}
			artifactTitle={hasArtifacts ? artifactSectionTitle : undefined}
			branch={
				<SessionBranchSummary
					hostId={hostId}
					onOpenFiles={onOpenFiles}
					openPRNumber={openPRNumber}
					pullRequests={hasPRs ? (branchWork) => (
						<>
							{prSummaries.map((pr) => (
								<PRSummaryCard
									branchWork={pr.number === openPRNumber ? branchWork : undefined}
									canOpenReviews={canOpenReviews}
									current={pr.number === openPRNumber}
									key={pr.url || pr.htmlUrl || pr.number}
									onOpenReviews={onOpenReviews}
									pr={pr}
									hostId={hostId}
									sessionId={session.id}
									cloudOrgId={session.cloud?.orgId}
								/>
							))}
							{linkedPRs.map((pr) => <LinkedPRCard external={isExternalRepository(pr, projectQuery.data)} key={pr.url} pr={pr} />)}
						</>
					) : undefined}
					session={session}
					title={prSectionTitle}
				/>
			}
			completion={showSessionControls ? <SessionControls hostId={hostId} session={session} /> : undefined}
			workers={showWorkers ? <OrchestratorChildrenSection session={session} /> : undefined}
		/>
	);
});

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
							<button
								aria-label={description}
								className="inline-flex size-5 shrink-0 items-center justify-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
								type="button"
							>
								<Info aria-hidden="true" className="size-icon-2xs" />
							</button>
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

function isExternalRepository(pr: SessionPRReference, project: components["schemas"]["Project"] | undefined): boolean {
	if (!project) return false;
	const repositories = [project.repo, project.config?.canonicalRepoURL, ...(project.workspaceRepos?.map((repo) => repo.repo) ?? [])];
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
	const kind = pr.provider === "gitlab" ? "MR" : "PR";
	// Same row as a tracked PR, but AO has only the reported URL: no title,
	// state or checks. The muted icon and "Not tracked" note say so, and the
	// info button explains why.
	return (
		<article className="flex min-w-0 flex-col py-1.5">
			<InfoRow
				action={<InfoRowLink ariaLabel={t("inspector.openPR", { number: pr.number })} href={pr.url} />}
				icon={GitPullRequest}
				iconClassName="text-passive"
				label={
					<>
						<span className="font-medium">{kind} #{pr.number}</span>
						{external ? <span className="text-muted-foreground"> {pr.repo}</span> : null}
					</>
				}
				note={
					<>
						{t("inspector.notTracked")}
						{external ? ` · ${t("inspector.externalRepository")}` : null}
					</>
				}
				noteClassName="text-muted-foreground"
				title={pr.url}
				trailing={
					<Tooltip>
						<TooltipTrigger asChild>
							<button aria-label={t("inspector.linkedPRInfo")} className="inline-flex size-6 shrink-0 items-center justify-center rounded-md text-passive transition-colors duration-fast hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" type="button">
								<Info aria-hidden="true" className="size-icon-2xs" />
							</button>
						</TooltipTrigger>
						<TooltipContent className="max-w-72 leading-normal text-pretty">{t("inspector.linkedPRInfo")}</TooltipContent>
					</Tooltip>
				}
			/>
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
			if (error) throw new Error(apiErrorMessage(error, t("inspector.ci.autoInjectError", { status: response.status })));
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
			if (error) throw new Error(apiErrorMessage(error, t("inspector.review.autoInjectError")));
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

function SessionControls({ session, hostId }: { session: WorkspaceSession; hostId?: string }) {
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
			if (error) throw new Error(apiErrorMessage(error, `Failed to update merge policy (${response.status})`));
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
	const isStandaloneSession = session.workspaceId === STANDALONE_WORKSPACE_ID;

	const confirmTermination = ({ deleteArtifacts }: SessionArchiveChoice) => {
		const workspaces = queryClient.getQueryData<WorkspaceSummary[]>(workspaceKey) ?? [];
		const workspace = workspaces.find((w) => w.id === session.workspaceId);
		const nextNav = resolveNextNavigationAfterSessionKill(workspace, session.id);
		
		setConfirmOpen(false);
		terminate.mutate(hostId ? { ...session, hostId, deleteArtifacts } : { ...session, deleteArtifacts });
		
		if (nextNav.target === "session") {
			void navigate(sessionNavigateTarget(session.workspaceId, nextNav.sessionId, hostId));
		} else {
			if (session.workspaceId === STANDALONE_WORKSPACE_ID) {
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
			<Tooltip>
				<TooltipTrigger asChild>
					<span className="inline-flex">
						<SessionArchiveDialog
							onConfirm={confirmTermination}
							onOpenChange={setConfirmOpen}
							open={confirmOpen}
							session={session}
							trigger={
								<button
									aria-label={t("inspector.archive")}
									className="inline-flex size-control-md items-center justify-center rounded-sm text-passive transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
									onClick={() => {
										clearTerminateSessionState(queryClient, session.id, hostId);
										// Always open the confirm; the modal owns its own dismissal.
										setConfirmOpen(true);
									}}
									type="button"
								>
									<Archive className="size-icon-sm" aria-hidden="true" />
								</button>
							}
						/>
					</span>
				</TooltipTrigger>
				<TooltipContent side="bottom">{t("inspector.archive")}</TooltipContent>
			</Tooltip>
		</div>
	);

	if (isStandaloneSession) {
		return <Section title={t("inspector.sessionControls")}>{terminateAction}</Section>;
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

function PRSummaryCard({
	branchWork,
	canOpenReviews,
	current,
	hostId,
	cloudOrgId,
	onOpenReviews,
	pr,
	sessionId,
}: {
	/** The branch's commit rows, shown inside the open PR they land on. */
	branchWork?: ReactNode;
	canOpenReviews: boolean;
	current: boolean;
	hostId?: string;
	cloudOrgId?: string;
	onOpenReviews: () => void;
	pr: SessionPRSummary;
	sessionId: string;
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
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/prs/{id}/merge", {
				params: { path: { id: String(pr.number) } },
				body: { prUrl: pr.url, expectedHeadSha: pr.headSha },
			});
			if (error) throw new Error(apiErrorMessage(error, t("pr.merge.failed", { number: pr.number })));
		},
		onSuccess: async () => {
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: cloudOrgId ? ["cloud-session-scm-summary", cloudBaseUrl, cloudOrgId, sessionId] : sessionScmSummaryQueryKey(sessionId, hostId) }),
				queryClient.invalidateQueries({ queryKey: workspaceKey }),
			]);
		},
	});
	const mergeError = mergePr.error instanceof Error ? mergePr.error.message : null;
	const viewModel: InspectorPullRequest = {
		...pr,
		card: presentation,
		href: prBrowserUrl(pr),
		stateLabel: t(prStateLabelKeys[pr.state]),
		reviewDetailsAction: canOpenReviews && pr.review.decision !== "none" ? (
			<button className="whitespace-nowrap text-settings-muted underline-offset-2 hover:underline" onClick={onOpenReviews} type="button">
				<span className="text-xs">{t("pr.review.viewDetails")} ↗</span>
			</button>
		) : undefined,
	};
	return (
		<PullRequestRows
			branchWork={branchWork}
			current={current}
			stateKey={`${hostId ?? ""}:${sessionId}:${pr.url || pr.number}`}
			mergeAction={
				canMerge ? (
					<Button
						aria-label={t("pr.merge.actionFor", { number: pr.number })}
						className="h-control-sm shrink-0 gap-1 bg-success px-2 text-xs text-background hover:bg-[color-mix(in_oklch,var(--success),var(--foreground)_10%)]"
						disabled={mergePr.isPending}
						onClick={() => mergePr.mutate()}
						size="none"
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
			pr={viewModel}
		/>
	);
}

const prToneText: Record<PRDisplayTone, string> = {
	neutral: "text-muted-foreground",
	passive: "text-passive",
	success: "text-success",
	review: "text-status-in-review",
	warning: "text-warning",
	error: "text-error",
};

const prToneIcon: Record<PRDisplayTone, LucideIcon> = {
	neutral: CircleDashed,
	passive: CircleDashed,
	success: CircleCheck,
	review: CircleAlert,
	warning: CircleAlert,
	error: CircleX,
};

/**
 * A pull request as inspector rows: the PR itself, its branches, each status,
 * and merge readiness. A status that only repeats the readiness label folds
 * into the readiness row instead of showing twice.
 */
/** Per-session PR expansion the person chose; outlives the section's unmount. */
const prExpansion = new Map<string, boolean>();

function PullRequestRows({
	branchWork,
	current,
	mergeAction,
	mergeError,
	openLabel,
	pr,
	stateKey,
}: {
	branchWork?: ReactNode;
	/** Identifies this PR in this session, for remembering its expansion. */
	stateKey: string;
	/** The open or draft PR that new commits update. */
	current: boolean;
	mergeAction?: ReactNode;
	mergeError?: string | null;
	openLabel: string;
	pr: InspectorPullRequest;
}) {
	const range = prBranchRange(pr);
	const readiness = pr.card.readiness;
	const statuses = pr.card.statusRows ?? (readiness ? [] : [pr.card.primary, ...pr.card.supporting]);
	const readinessTwin = readiness ? statuses.find((status) => status.label === readiness.label) : undefined;
	const showStatus = pr.state !== "merged";
	// The PR new commits update starts open; every other PR starts folded. A
	// toggle is remembered, so closing the section or switching tabs keeps it.
	const [expanded, setExpandedState] = useState(() => prExpansion.get(stateKey) ?? current);
	const setExpanded = (update: (open: boolean) => boolean) => {
		setExpandedState((open) => {
			const next = update(open);
			prExpansion.set(stateKey, next);
			return next;
		});
	};
	const reduceMotion = useReducedMotion();
	const state = prStateIcon[pr.state];
	return (
		<article className="flex min-w-0 flex-col py-1.5">
			<InfoRow
				action={
					<InfoRowLink ariaLabel={openLabel} href={pr.href}>
						{pr.additions + pr.deletions > 0 ? <DiffCounts additions={pr.additions} deletions={pr.deletions} /> : null}
					</InfoRowLink>
				}
				expanded={expanded}
				icon={state.icon}
				iconClassName={state.className}
				label={
					<>
						<span className="font-medium">PR #{pr.number}</span>
						{pr.title ? <span> {pr.title}</span> : null}
						{/* The icon carries the state; keep it for screen readers. */}
						<span className="sr-only"> {pr.stateLabel}</span>
					</>
				}
				onClick={() => setExpanded((open) => !open)}
				title={pr.title}
			/>
			<AnimatePresence initial={false}>
				{expanded ? (
					<motion.div
						animate={{ height: "auto", opacity: 1, transitionEnd: { overflow: "visible" } }}
						// The clip box reaches the rows' 6px hover bleed, so pills are never cut.
						className="-mx-1.5 flex min-w-0 flex-col px-1.5"
						exit={{ height: 0, opacity: 0, overflow: "hidden" }}
						initial={{ height: 0, opacity: 0, overflow: "hidden" }}
						transition={reduceMotion ? { duration: 0 } : { duration: 0.22, ease: [0.4, 0, 0.2, 1] }}
					>
						<PullRequestDetails mergeAction={mergeAction} mergeError={mergeError} pr={pr} range={range} readiness={readiness} readinessTwin={readinessTwin} showStatus={showStatus} statuses={statuses} />
						{branchWork}
					</motion.div>
				) : null}
			</AnimatePresence>
		</article>
	);
}

// GitHub's state icons and colours, so the header needs no state chip.
const prStateIcon: Record<InspectorPullRequest["state"], { icon: LucideIcon; className: string }> = {
	open: { icon: GitPullRequest, className: "text-success" },
	draft: { icon: GitPullRequestDraft, className: "text-passive" },
	merged: { icon: GitMerge, className: "text-status-merged" },
	closed: { icon: GitPullRequestClosed, className: "text-error" },
};

function PullRequestDetails({
	mergeAction,
	mergeError,
	pr,
	range,
	readiness,
	readinessTwin,
	showStatus,
	statuses,
}: {
	mergeAction?: ReactNode;
	mergeError?: string | null;
	pr: InspectorPullRequest;
	range: string | undefined;
	readiness: InspectorPullRequest["card"]["readiness"];
	readinessTwin: InspectorPullRequest["card"]["primary"] | undefined;
	showStatus: boolean;
	statuses: InspectorPullRequest["card"]["primary"][];
}) {
	return (
		<>
			{range ? <InfoRow icon={GitBranch} label={range} title={range} /> : null}
			{showStatus
				? statuses
						.filter((status) => status !== readinessTwin)
						.map((status) => (
							<InfoRow
								href={status.href}
								icon={prToneIcon[status.tone]}
								iconClassName={cn(prToneText[status.tone], status.breathe && "animate-status-pulse")}
								key={status.key}
								// One short phrase per row; the rest is a tooltip. "Review status" is
								// only a heading, so the review row shows its outcome instead.
								label={status.key === "review" && status.detail ? status.detail : status.label}
								title={status.detail}
								trailing={status.key === "review" ? pr.reviewDetailsAction : undefined}
							/>
						))
				: null}
			{showStatus && readiness ? (
				<InfoRow
					href={mergeAction ? undefined : readinessTwin?.href}
					icon={prToneIcon[readiness.tone]}
					iconClassName={prToneText[readiness.tone]}
					label={readiness.label}
					title={readiness.detail}
					trailing={mergeAction}
				/>
			) : null}
			{mergeError ? (
				<p className="mt-1 text-xs leading-normal text-error" role="status">
					{mergeError}
				</p>
			) : null}
		</>
	);
}

/**
 * One row in the Summary panel's Artifacts list. HTML artifacts open in the
 * Browser panel; markdown/file artifacts open in the inspector's Artifacts view,
 * since artifact files live outside the git workspace.
 */
const ARTIFACT_PREVIEW_COUNT = 4;

/** The newest artifacts first, capped until the person asks for the rest. */
function ArtifactSummaryList({
	artifacts,
	onOpenArtifact,
	session,
}: {
	artifacts: SessionArtifact[];
	onOpenArtifact?: (target: { feedback?: boolean; path: string }) => void;
	session: WorkspaceSession;
}) {
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState(false);
	const reduceMotion = useReducedMotion();
	const newestFirst = [...artifacts].sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt));
	const hidden = newestFirst.length - ARTIFACT_PREVIEW_COUNT;
	return (
		<>
			{newestFirst.slice(0, ARTIFACT_PREVIEW_COUNT).map((artifact) => (
				<ArtifactSummaryCard artifact={artifact} key={artifact.path} onOpenArtifact={onOpenArtifact} session={session} />
			))}
			<AnimatePresence initial={false}>
				{expanded && hidden > 0 ? (
					// -mt cancels the list's gap while collapsed; the inner pt restores it once open.
					<motion.div
						animate={{ height: "auto", opacity: 1, transitionEnd: { overflow: "visible" } }}
						className="-mt-1.5"
						exit={{ height: 0, opacity: 0, overflow: "hidden" }}
						initial={{ height: 0, opacity: 0, overflow: "hidden" }}
						transition={reduceMotion ? { duration: 0 } : { duration: 0.22, ease: [0.4, 0, 0.2, 1] }}
					>
						<div className="flex flex-col gap-1.5 pt-1.5">
							{newestFirst.slice(ARTIFACT_PREVIEW_COUNT).map((artifact) => (
								<ArtifactSummaryCard artifact={artifact} key={artifact.path} onOpenArtifact={onOpenArtifact} session={session} />
							))}
						</div>
					</motion.div>
				) : null}
			</AnimatePresence>
			{hidden > 0 ? (
				<Button
					aria-expanded={expanded}
					// A faint resting fill: present, but quieter than the rows above it.
					className="h-control-sm gap-1 self-start bg-interactive-hover px-2.5 text-xs text-muted-foreground hover:bg-interactive-active hover:text-foreground dark:hover:bg-interactive-active focus-visible:ring-2 focus-visible:ring-ring/60"
					onClick={() => setExpanded((open) => !open)}
					size="none"
					type="button"
					variant="ghost"
				>
					{expanded ? t("inspector.showLess") : t("inspector.showMore")}
					<ChevronRight aria-hidden="true" className={cn("size-icon-sm transition-transform motion-reduce:transition-none", expanded ? "-rotate-90" : "rotate-90")} />
				</Button>
			) : null}
		</>
	);
}

function ArtifactSummaryCard({
	artifact,
	onOpenArtifact,
	session,
}: {
	artifact: SessionArtifact;
	onOpenArtifact?: (target: { feedback?: boolean; path: string }) => void;
	session: WorkspaceSession;
}) {
	const openArtifactPreview = useOpenArtifactPreview(session.id, session.hostId);
	const handleOpen = () => {
		if (artifact.kind === "html" && artifact.previewUrl) {
			openArtifactPreview(artifact.previewUrl);
			return;
		}
		onOpenArtifact?.({ path: artifact.path });
	};
	return (
		<div className="flex w-full min-w-0 items-center rounded-md border border-(--color-border-settings-input) text-sm transition-colors hover:bg-interactive-hover focus-within:bg-interactive-hover focus-within:ring-1 focus-within:ring-ring">
			<button
				className="flex min-w-0 flex-1 items-center gap-2 px-2.5 py-1.5 text-left outline-none"
				onClick={handleOpen}
				type="button"
			>
				{artifact.kind === "html" ? (
					<Globe aria-hidden="true" className="size-icon-sm shrink-0 text-settings-muted" />
				) : (
					<Shapes aria-hidden="true" className="size-icon-sm shrink-0 text-settings-muted" />
				)}
				<span className="min-w-0 flex-1 truncate">{artifact.name}</span>
			</button>
		</div>
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
		enabled: !session.cloud && session.kind !== "orchestrator",
	});
	const events: SortableTimelineEvent[] = [];
	const pushEvent = (event: InspectorTimelineEvent, timestamp?: string | null) => {
		events.push({ ...event, sortTime: timelineSortTime(timestamp) });
	};
	const createdAt = session.createdAt ?? session.updatedAt;

	pushEvent(
		{
			tone: "neutral",
			content: <>{appI18n.t("inspector.timeline.createdWorkspace")}</>,
			timestamp: formatTimeCompact(createdAt),
		},
		createdAt,
	);

	for (const commit of history.data?.commits.slice(0, TIMELINE_COMMIT_LIMIT) ?? []) {
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

	const activityView = getAgentActivityView(session.activity);
	const activityAt = session.activity?.lastActivityAt ?? session.updatedAt ?? session.createdAt;
	pushEvent(
		{
			tone: "now",
			content: (
				<span className="inline-flex flex-wrap items-center gap-1.5">
					<span className="inline-flex align-middle">
						<InspectorActivityPill activity={session.activity} />
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
	const open = new Set(openPRs(session));
	const add = (state: ScmTimelineState) => {
		if (seen.has(state)) return;
		seen.add(state);
		states.push(state);
	};

	if (session.status === "ci_failed") add("ci_failed");
	if (session.status === "changes_requested") add("changes_requested");
	if (session.status === "commented") add("commented");
	for (const pr of session.prs) {
		if (open.has(pr) && pr.ci === "failing") add("ci_failed");
		if (pr.review === "changes_requested") add("changes_requested");
		// Read the raw fact directly rather than session.status: status collapses
		// to "working" while the agent is active, but unresolved comments from a
		// non-blocking review must stay visible regardless of agent activity.
		// Gated on the PR still being open so a stale comment on a merged/closed
		// PR cannot keep the pill around.
		if (open.has(pr) && pr.reviewComments) add("commented");
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
				onOpenReviewerTerminal={onOpenReviewerTerminal}
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
			if (error) throw new Error(apiErrorMessage(error, "Unable to save reviewer"));
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
			if (error) throw new Error(apiErrorMessage(error, t("inspector.reviewRequestFailed")));
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
			if (error) throw new Error(apiErrorMessage(error, t("inspector.unableStartReview")));
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
			const started = data?.reviews?.find((review) => review.status === "running" && review.latestRun);
			if (!started?.latestRun) {
				if (reused && !rerun) setRerunConfirmation({ ownerKey, heads: reviewHeads });
				return;
			}
			if (data?.reviewerSurface?.mode === "chat" && data.reviewerSurface.reviewId) {
				onOpenReviewerChat?.(data.reviewerSurface.reviewId);
			} else if (data?.reviewerHandleId) {
				const harness = started.latestRun.harness || "reviewer";
				onOpenReviewerTerminal?.({ handleId: data.reviewerHandleId, harness });
			}
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
			if (error) throw new Error(apiErrorMessage(error, t("inspector.unableCancelReview")));
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
			if (error) throw new Error(apiErrorMessage(error, t("inspector.unableKillReviewSession")));
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
	const startReview = () => {
		if (currentHeadReviewed) setRerunConfirmation({ ownerKey: reviewerOwnerKey, heads: reviewHeads });
		else triggerReview.mutate({ ownerKey: reviewerOwnerKey });
	};
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
				onTrigger={startReview}
				reviewerHandleId={reviewsQuery.data?.reviewerHandleId ?? ""}
				reviewerSurface={reviewsQuery.data?.reviewerSurface}
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
				onRerun={autoReviewEnabled ? undefined : startReview}
				onWorkerMessageSent={onWorkerMessageSent}
				rerunHarness={reviewerOverride || currentDefaultReviewerHarness}
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
	onOpenReviewerTerminal,
	onWorkerMessageSent,
}: {
	session: WorkspaceSession;
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	onOpenReviewerTerminal?: OpenReviewerTerminal;
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
	const effectiveReviewerHarness = reviewsQuery.data?.reviewerHarness || session.reviewerHarness || session.provider;
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
			const running = data.reviews.find((review) => review.status === "running");
			if (running && data.reviewerHandleId) {
				onOpenReviewerTerminal?.({
					handleId: data.reviewerHandleId,
					harness: data.reviewerHarness || session.provider,
				});
			} else if (!running) {
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
	const reviewState = cloudReviewsToLocal(reviewsQuery.data, session.provider);
	return (
		<>
			<ReviewPanel
				cloud
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
 * one list keyed by PR. What needs the person comes first ("Needs you"), then
 * every PR as one shared card: one row per reviewer showing its latest pass,
 * with older passes folded under "Earlier passes".
 */
function MergedReviewsSection({
	githubPRs,
	hostId,
	isLoading,
	onOpenReviewFile,
	onRerun,
	onWorkerMessageSent,
	rerunHarness,
	reviewStates,
	runs,
	session,
}: {
	githubPRs: SessionPRSummary[];
	hostId?: string;
	isLoading: boolean;
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	/** Re-runs the selected reviewer; offered on that reviewer's latest pass. */
	onRerun?: () => void;
	onWorkerMessageSent?: () => void;
	rerunHarness?: string;
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
	const runsByPR = reviewRunsByPR(openReviewStates, runs);

	// Union by PR number. A PR can appear on either side alone.
	const byNumber = new Map<number, { ao?: PRReviewState; github?: SessionPRSummary }>();
	for (const state of openReviewStates) {
		if ((runsByPR.get(state.prUrl) ?? []).length === 0 && state.status !== "up_to_date" && state.status !== "changes_requested") continue;
		byNumber.set(state.prNumber, { ...byNumber.get(state.prNumber), ao: state });
	}
	for (const pr of githubPRs) {
		byNumber.set(pr.number, { ...byNumber.get(pr.number), github: pr });
	}
	const labels = reviewLabels(t);
	const requestRereview = async (review: InspectorGithubReview) => {
		const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/rerequest", {
			params: { path: { sessionId: session.id } },
			body: { pullRequestUrl: review.pullRequestUrl, reviewerId: review.reviewerId },
		});
		if (error) throw new Error(apiErrorMessage(error, "Unable to request re-review"));
	};
	const resolveInlineComment = async (comment: InspectorInlineComment) => {
		const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/reviews/comments/resolve", {
			params: { path: { sessionId: session.id } },
			body: { pullRequestUrl: comment.pullRequestUrl, commentUrl: comment.url ?? "" },
		});
		if (error) throw new Error(apiErrorMessage(error, "Unable to resolve review comment"));
		void queryClient.invalidateQueries({ queryKey: sessionScmSummaryQueryKey(session.id, hostId) });
		void queryClient.invalidateQueries({ queryKey: workspaceKey });
	};
	const sendMessageToWorker = async (message: string, fallbackError: string) => {
		if (session.cloud) {
			await cloudClient.sendSessionMessage(session.cloud.orgId, session.id, { text: message });
			return;
		}
		if (session.mode === "chat") {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/conversation/messages", {
				params: { path: { sessionId: session.id } },
				body: { text: message, clientMessageId: crypto.randomUUID() },
			});
			if (error) throw new Error(apiErrorMessage(error, fallbackError));
			return;
		}
		const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/send", {
			params: { path: { sessionId: session.id } },
			body: { message, userAuthored: true },
		});
		if (error) throw new Error(apiErrorMessage(error, fallbackError));
	};
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
	const prs: ReviewPRView[] = [...byNumber.entries()].map(([number, { ao, github }]) => {
		const aoRuns = ao ? runsByPR.get(ao.prUrl) ?? [] : [];
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
		const headSha = ao?.targetSha || github?.headSha || "";
		const aoPasses: ReviewPassView[] = aoRuns.map((run) => {
			const reviewUrl = aoReviewCommentUrl(run);
			const outcome = aoRunOutcome(run, t);
			return {
				body: run.body?.trim() || undefined,
				createdAt: run.createdAt,
				inlineComments: agentComments.get(run.githubReviewId)?.inlineComments ?? [],
				key: `ao:${run.id}`,
				pullRequestUrl: run.prUrl,
				requestedByAgent: run.triggerSource === "agent",
				resolvedComments: agentComments.get(run.githubReviewId)?.resolvedComments ?? [],
				reviewerKey: `ao:${run.harness || "reviewer"}`,
				reviewerName: run.harness || "reviewer",
				source: "agent",
				stale: Boolean(run.targetSha && headSha && run.targetSha !== headSha),
				status: run.status,
				timeLabel: formatTimeCompact(run.createdAt),
				tone: outcome.tone,
				url: reviewUrl ?? (ao?.prUrl || null),
				verdict: run.status === "complete" || run.status === "delivered" ? run.verdict : run.status,
				verdictLabel: outcome.label,
			};
		});
		if (ao && aoPasses.length === 0 && (ao.status === "up_to_date" || ao.status === "changes_requested")) {
			// A verdict with no recorded pass (older daemons): one row carries it.
			const verdict = ao.status === "up_to_date" ? "approved" : "changes_requested";
			const outcome = githubOutcome(verdict, t);
			aoPasses.push({
				createdAt: "",
				inlineComments: [],
				key: "ao-state:" + ao.prUrl,
				pullRequestUrl: ao.prUrl,
				resolvedComments: [],
				reviewerKey: "ao:state",
				reviewerName: "",
				source: "agent",
				stale: false,
				status: "complete",
				timeLabel: "",
				tone: outcome.tone,
				url: ao.prUrl,
				verdict,
				verdictLabel: outcome.label,
			});
		}
		const unresolvedByReviewer = new Map(unresolvedReviewers.map((reviewer) => [reviewer.reviewerId, reviewer]));
		const resolvedByReviewer = new Map(resolvedReviewers.map((reviewer) => [reviewer.reviewerId, reviewer]));
		// Only a reviewer's newest GitHub review carries its open threads.
		const newestEntryByReviewer = new Map<string, (typeof entries)[number]>();
		for (const entry of entries) {
			const current = newestEntryByReviewer.get(entry.reviewerId);
			if (!current || entry.submittedAt.localeCompare(current.submittedAt) > 0) newestEntryByReviewer.set(entry.reviewerId, entry);
		}
		const commentsFor = (links: SessionPRSummary["review"]["unresolvedBy"][number]["links"], reviewUrl: string | undefined, resolved: boolean) =>
			links.map((link) => ({
				autoInjectReview: link.autoInjectReview,
				body: link.body,
				file: link.file,
				line: link.line,
				pullRequestUrl: github?.url,
				...(resolved ? { resolved: true } : {}),
				url: link.url || reviewUrl,
			}));
		const githubPass = (review: InspectorGithubReview, rawVerdict: string): ReviewPassView => {
			const outcome = githubOutcome(rawVerdict, t);
			return {
				body: review.body?.trim() || undefined,
				createdAt: review.submittedAt,
				github: review,
				inlineComments: review.inlineComments ?? [],
				isBot: review.isBot,
				key: `gh:${review.id}`,
				pullRequestUrl: review.pullRequestUrl,
				resolvedComments: review.resolvedComments ?? [],
				reviewerKey: `gh:${review.reviewerId}`,
				reviewerName: review.reviewerId,
				source: "external",
				stale: false,
				status: "submitted",
				timeLabel: review.submittedAtLabel,
				tone: outcome.tone,
				url: review.reviewUrl || review.pullRequestUrl,
				verdict: rawVerdict,
				verdictLabel: outcome.label,
			};
		};
		const githubPasses: ReviewPassView[] = entries.map((entry) => {
			const newest = newestEntryByReviewer.get(entry.reviewerId) === entry;
			const reviewer = newest ? unresolvedByReviewer.get(entry.reviewerId) : undefined;
			const resolvedReviewer = newest ? resolvedByReviewer.get(entry.reviewerId) : undefined;
			if (newest) {
				unresolvedByReviewer.delete(entry.reviewerId);
				resolvedByReviewer.delete(entry.reviewerId);
			}
			return githubPass({
				body: entry.body,
				canRequestRereview: canRequestPRRereview(entry.verdict, github?.url),
				id: entry.reviewUrl || `${entry.reviewerId}:${entry.submittedAt}`,
				inlineComments: commentsFor(reviewer?.links ?? [], reviewer?.reviewUrl, false),
				isBot: entry.isBot,
				pullRequestUrl: github?.url,
				resolvedComments: commentsFor(resolvedReviewer?.links ?? [], resolvedReviewer?.reviewUrl, true),
				reviewerId: entry.reviewerId,
				reviewUrl: entry.reviewUrl,
				submittedAt: entry.submittedAt,
				submittedAtLabel: formatTimeCompact(entry.submittedAt),
				verdict: githubVerdict(entry.verdict, t),
			}, entry.verdict);
		});
		// Reviewers with threads but no review of record still get a row.
		for (const reviewer of unresolvedByReviewer.values()) {
			const resolvedReviewer = resolvedByReviewer.get(reviewer.reviewerId);
			resolvedByReviewer.delete(reviewer.reviewerId);
			githubPasses.push(githubPass({
				canRequestRereview: canRequestPRRereview("changes_requested", github?.url),
				id: `unresolved:${reviewer.reviewerId}:${number}`,
				inlineComments: commentsFor(reviewer.links, reviewer.reviewUrl, false),
				isBot: reviewer.isBot,
				pullRequestUrl: github?.url,
				resolvedComments: commentsFor(resolvedReviewer?.links ?? [], resolvedReviewer?.reviewUrl, true),
				reviewerId: reviewer.reviewerId,
				reviewUrl: reviewer.reviewUrl,
				submittedAt: "",
				submittedAtLabel: "",
				verdict: githubVerdict("none", t),
			}, "none"));
		}
		for (const reviewer of resolvedByReviewer.values()) {
			githubPasses.push(githubPass({
				canRequestRereview: false,
				id: `resolved:${reviewer.reviewerId}:${number}`,
				inlineComments: [],
				isBot: reviewer.isBot,
				pullRequestUrl: github?.url,
				resolvedComments: commentsFor(reviewer.links, reviewer.reviewUrl, true),
				reviewerId: reviewer.reviewerId,
				reviewUrl: reviewer.reviewUrl,
				submittedAt: "",
				submittedAtLabel: "",
				verdict: githubVerdict("none", t),
			}, "none"));
		}
		const { latest, earlier } = splitLatestPasses([...aoPasses, ...githubPasses]);
		const prFacts = session.prs.find((pr) => pr.url === (ao?.prUrl ?? github?.url));
		const state = (github?.state ?? prFacts?.state ?? "open") as InspectorPullRequest["state"];
		return {
			earlier,
			key: ao?.prUrl || github?.url || String(number),
			latest,
			number,
			state,
			title: (ao?.title ?? github?.title)?.trim() || undefined,
			unresolved,
			url: github ? prBrowserUrl(github) : ao?.prUrl,
		};
	}).filter((pr) => pr.latest.length > 0 || pr.unresolved > 0);
	const needsYou = prs.flatMap((pr) => [
		...pr.latest
			.filter(passNeedsYou)
			.map((pass) => ({ kind: "changes" as const, pass, pr })),
		...(pr.unresolved > 0 ? [{ kind: "threads" as const, pr }] : []),
	]);
	const attention = (pr: ReviewPRView) =>
		pr.latest.some((pass) => pass.status === "running") ? 2 : pr.unresolved > 0 || pr.latest.some(passNeedsYou) ? 1 : 0;
	const ordered = [...prs].sort((a, b) => attention(b) - attention(a) || b.number - a.number);
	const context: ReviewPassContext = {
		labels,
		onOpenInAOBrowser: openInAOBrowser,
		onOpenReviewFile,
		onRequestRereview: requestRereview,
		onRerun,
		onResolveInlineComment: resolveInlineComment,
		onSendInlineComment: sendInlineCommentToWorker,
		onSendReviewSummary: sendReviewSummaryToWorker,
		rerunHarness,
	};
	if (prs.length === 0) {
		if (!isLoading) return null;
		return (
			<Section surface={false} title={t("inspector.pullRequest")}>
				<p className={inspectorEmptyClass}>{t("inspector.loadingReviews")}</p>
			</Section>
		);
	}
	return (
		<>
			<AnimatePresence initial={false}>
				{needsYou.length > 0 ? (
					<ReviewReveal key="needs-you">
						<Section surface={false} title={t("inspector.review.needsYou")}>
							<div className="flex min-w-0 flex-col" data-testid="review-needs-you">
								{needsYou.map((item) =>
									item.kind === "changes" ? (
										<NeedsYouChangesRow context={context} key={item.pass.key} pass={item.pass} prNumber={item.pr.number} />
									) : (
										<InfoRow
											ariaLabel={t("inspector.openPR", { number: item.pr.number })}
											href={item.pr.url}
											icon={MessageSquare}
											iconClassName="text-warning"
											key={`threads:${item.pr.key}`}
											label={t("inspector.review.unresolvedThreads", { count: item.pr.unresolved, number: item.pr.number })}
										/>
									),
								)}
							</div>
						</Section>
					</ReviewReveal>
				) : null}
			</AnimatePresence>
			<Section surface={false} title={prs.length > 1 ? t("inspector.pullRequests", { count: prs.length }) : t("inspector.pullRequest")}>
				<div className="flex min-w-0 flex-col divide-y divide-border rounded-(--radius-panel) border border-border/50 bg-card px-3">
					{ordered.map((pr) => (
						<ReviewPRGroup
							context={context}
							defaultExpanded={attention(pr) > 0}
							key={pr.key}
							pr={pr}
							stateKey={`${hostId ?? ""}:${session.id}:${pr.key}`}
						/>
					))}
				</div>
			</Section>
		</>
	);
}

type ReviewTone = "success" | "warning" | "error" | "working" | "neutral";

const reviewToneText: Record<ReviewTone, string> = {
	success: "text-success",
	warning: "text-warning",
	error: "text-error",
	working: "text-status-working",
	neutral: "text-muted-foreground",
};

const reviewToneIcon: Record<ReviewTone, LucideIcon> = {
	success: CircleCheck,
	warning: CircleAlert,
	error: CircleX,
	working: Loader2,
	neutral: CircleDashed,
};

/** One reviewer pass, AO or GitHub, as the Reviews tab lists it. */
type ReviewPassView = {
	body?: string;
	createdAt: string;
	github?: InspectorGithubReview;
	inlineComments: InspectorInlineComment[];
	isBot?: boolean;
	key: string;
	pullRequestUrl?: string;
	requestedByAgent?: boolean;
	resolvedComments: InspectorInlineComment[];
	/** Groups passes by who wrote them: `ao:<harness>` or `gh:<login>`. */
	reviewerKey: string;
	reviewerName: string;
	source: "agent" | "external";
	/** Reviewed a commit other than the PR's current head. */
	stale: boolean;
	status: string;
	timeLabel: string;
	tone: ReviewTone;
	url?: string | null;
	verdict: string;
	verdictLabel: string;
};

type ReviewPRView = {
	earlier: ReviewPassView[];
	key: string;
	latest: ReviewPassView[];
	number: number;
	state: InspectorPullRequest["state"];
	title?: string;
	unresolved: number;
	url?: string;
};

type ReviewPassContext = {
	labels: InspectorReviewLabels;
	onOpenInAOBrowser?: (url: string) => void;
	onOpenReviewFile?: (target: { line?: number; path: string }) => void;
	onRequestRereview: (review: InspectorGithubReview) => Promise<void>;
	onRerun?: () => void;
	onResolveInlineComment: (comment: InspectorInlineComment) => Promise<void>;
	onSendInlineComment: (comment: InspectorInlineComment & { reviewerId?: string }) => Promise<void>;
	onSendReviewSummary: (summary: InspectorReviewSummaryAction) => Promise<void>;
	rerunHarness?: string;
};

/** A current-head change request that is the reviewer's latest word. */
function passNeedsYou(pass: ReviewPassView): boolean {
	return pass.verdict === "changes_requested" && !pass.stale && pass.status !== "running";
}

/** Each reviewer's newest pass, and everything older (newest first). */
function splitLatestPasses(passes: ReviewPassView[]): { earlier: ReviewPassView[]; latest: ReviewPassView[] } {
	const newestFirst = [...passes].sort((a, b) => b.createdAt.localeCompare(a.createdAt));
	const seen = new Set<string>();
	const latest: ReviewPassView[] = [];
	const earlier: ReviewPassView[] = [];
	for (const pass of newestFirst) {
		if (seen.has(pass.reviewerKey)) earlier.push(pass);
		else {
			seen.add(pass.reviewerKey);
			latest.push(pass);
		}
	}
	return { earlier, latest };
}

function aoRunOutcome(run: ReviewRunFacts, t: TFunction): { label: string; tone: ReviewTone } {
	if (run.status === "running") return { label: t("inspector.review.reviewing"), tone: "working" };
	if (run.status === "failed") return { label: t("inspector.review.failed"), tone: "error" };
	if (run.status === "cancelled") return { label: t("inspector.review.cancelled"), tone: "neutral" };
	return githubOutcome(run.verdict, t);
}

function githubOutcome(verdict: string, t: TFunction): { label: string; tone: ReviewTone } {
	switch (verdict) {
		case "approved":
			return { label: t("inspector.review.approved"), tone: "success" };
		case "changes_requested":
			return { label: t("inspector.review.changesRequested"), tone: "warning" };
		case "review_required":
			return { label: t("inspector.review.notRun"), tone: "neutral" };
		default:
			return { label: t("inspector.review.commented"), tone: "neutral" };
	}
}

/** Review disclosures the person chose per PR; outlives tab switches like prExpansion. */
const reviewPRExpansion = new Map<string, boolean>();

const reviewRevealTransition = { duration: 0.22, ease: [0.4, 0, 0.2, 1] } as const;

/** The Summary tab's disclosure motion: height and opacity, instant under reduced motion. */
function ReviewReveal({ children, className, id }: { children: ReactNode; className?: string; id?: string }) {
	const reduceMotion = useReducedMotion();
	return (
		<motion.div
			animate={{ height: "auto", opacity: 1, transitionEnd: { overflow: "visible" } }}
			className={className}
			exit={{ height: 0, opacity: 0, overflow: "hidden" }}
			id={id}
			initial={{ height: 0, opacity: 0, overflow: "hidden" }}
			transition={reduceMotion ? { duration: 0 } : reviewRevealTransition}
		>
			{children}
		</motion.div>
	);
}

const EARLIER_PASSES_PAGE = 5;

function ReviewPRGroup({
	context,
	defaultExpanded,
	pr,
	stateKey,
}: {
	context: ReviewPassContext;
	defaultExpanded: boolean;
	pr: ReviewPRView;
	stateKey: string;
}) {
	const { t } = useTranslation();
	const [choice, setChoice] = useState<boolean | undefined>(() => reviewPRExpansion.get(stateKey));
	const expanded = choice ?? defaultExpanded;
	const [earlierOpen, setEarlierOpen] = useState(false);
	const [earlierVisible, setEarlierVisible] = useState(EARLIER_PASSES_PAGE);
	const bodyId = useId();
	const earlierId = useId();
	const toggle = () => {
		reviewPRExpansion.set(stateKey, !expanded);
		setChoice(!expanded);
	};
	const state = prStateIcon[pr.state];
	return (
		<article className="flex min-w-0 flex-col py-1.5" data-testid="review-pr-row">
			<InfoRow
				action={pr.url ? <InfoRowLink ariaLabel={t("inspector.openPR", { number: pr.number })} href={pr.url} /> : undefined}
				ariaControls={bodyId}
				expanded={expanded}
				icon={state.icon}
				iconClassName={state.className}
				label={
					<>
						<span className="font-medium">PR #{pr.number}</span>
						{pr.title ? <span> {pr.title}</span> : null}
					</>
				}
				onClick={toggle}
				title={pr.title}
				trailing={<ReviewVerdictCounts passes={pr.latest} />}
			/>
			<AnimatePresence initial={false}>
				{expanded ? (
					<ReviewReveal className="-mx-1.5 flex min-w-0 flex-col px-1.5" id={bodyId}>
						<AnimatePresence initial={false}>
							{pr.latest.map((pass) => (
								<ReviewReveal key={pass.reviewerKey}>
									<ReviewPassRow context={context} latest pass={pass} prNumber={pr.number} />
								</ReviewReveal>
							))}
						</AnimatePresence>
						{pr.earlier.length > 0 ? (
							<>
								<InfoRow
									ariaControls={earlierId}
									expanded={earlierOpen}
									icon={History}
									label={t("inspector.review.earlierPasses", { count: pr.earlier.length })}
									onClick={() => setEarlierOpen((open) => !open)}
								/>
								<AnimatePresence initial={false}>
									{earlierOpen ? (
										<ReviewReveal className="-mx-1.5 flex min-w-0 flex-col px-1.5" id={earlierId}>
											<div className="flex min-w-0 flex-col" data-testid="review-earlier-passes">
												{pr.earlier.slice(0, earlierVisible).map((pass) => (
													<ReviewPassRow context={context} key={pass.key} pass={pass} prNumber={pr.number} />
												))}
											</div>
											{pr.earlier.length > earlierVisible ? (
												<Button
													className="my-1 h-control-sm gap-1 self-start bg-interactive-hover px-2.5 text-xs text-muted-foreground hover:bg-interactive-active hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/60 dark:hover:bg-interactive-active"
													onClick={() => setEarlierVisible((count) => count + EARLIER_PASSES_PAGE)}
													size="none"
													type="button"
													variant="ghost"
												>
													{t("inspector.showMore")}
													<ChevronRight aria-hidden="true" className="size-icon-sm rotate-90" />
												</Button>
											) : null}
										</ReviewReveal>
									) : null}
								</AnimatePresence>
							</>
						) : null}
					</ReviewReveal>
				) : null}
			</AnimatePresence>
		</article>
	);
}

/** Compact verdict tally for a PR header; the words stay available to screen readers. */
function ReviewVerdictCounts({ passes }: { passes: ReviewPassView[] }) {
	const { t } = useTranslation();
	const count = (tone: ReviewTone) => passes.filter((pass) => pass.tone === tone).length;
	const tallies: { count: number; label: string; tone: ReviewTone }[] = [
		{ count: count("working"), label: t("inspector.review.countReviewing", { count: count("working") }), tone: "working" },
		{ count: count("warning"), label: t("inspector.review.countChangesRequested", { count: count("warning") }), tone: "warning" },
		{ count: count("error"), label: t("inspector.review.countFailed", { count: count("error") }), tone: "error" },
		{ count: count("success"), label: t("inspector.review.countApproved", { count: count("success") }), tone: "success" },
	];
	const shown = tallies.filter((tally) => tally.count > 0);
	if (shown.length === 0) return null;
	return (
		<span className="inline-flex shrink-0 items-center gap-2 text-caption tabular-nums">
			{shown.map((tally) => {
				const Icon = reviewToneIcon[tally.tone];
				return (
					<span className={cn("inline-flex items-center gap-0.5", reviewToneText[tally.tone])} key={tally.tone}>
						<Icon aria-hidden="true" className={cn("size-icon-2xs", tally.tone === "working" && "animate-spin motion-reduce:animate-none")} />
						<span aria-hidden="true">{tally.count}</span>
						<span className="sr-only">{tally.label}</span>
					</span>
				);
			})}
		</span>
	);
}

type ReviewSendState = "idle" | "sending" | "sent" | "error";

/** Send-to-worker and re-review requests for one pass, shared by its buttons and menu. */
function useReviewPassActions(pass: ReviewPassView, context: ReviewPassContext) {
	const [sendState, setSendState] = useState<ReviewSendState>("idle");
	const [rereviewState, setRereviewState] = useState<ReviewSendState>("idle");
	const canSend = Boolean(pass.body) && pass.status !== "failed";
	const send = async () => {
		if (!pass.body || sendState === "sending") return;
		setSendState("sending");
		try {
			await context.onSendReviewSummary({
				body: pass.body,
				pullRequestUrl: pass.pullRequestUrl,
				reviewerId: pass.reviewerName,
				source: pass.source,
				url: pass.url,
			});
			setSendState("sent");
		} catch {
			setSendState("error");
		}
	};
	const canRequestRereview = Boolean(pass.github?.canRequestRereview);
	const requestRereview = async () => {
		if (!pass.github || rereviewState === "sending") return;
		setRereviewState("sending");
		try {
			await context.onRequestRereview(pass.github);
			setRereviewState("sent");
		} catch {
			setRereviewState("error");
		}
	};
	return { canRequestRereview, canSend, requestRereview, rereviewState, send, sendState };
}

/** The reviewer's name; a verdict with no recorded reviewer names its verdict instead. */
function reviewerDisplayName(pass: ReviewPassView): string {
	if (!pass.reviewerName) return pass.verdictLabel;
	return pass.source === "agent" ? agentLabel(pass.reviewerName) : pass.reviewerName;
}

function ReviewPassRow({
	context,
	latest = false,
	pass,
	prNumber,
}: {
	context: ReviewPassContext;
	latest?: boolean;
	pass: ReviewPassView;
	prNumber: number;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const bodyId = useId();
	const actions = useReviewPassActions(pass, context);
	const name = reviewerDisplayName(pass);
	const canRerun =
		latest && pass.source === "agent" && Boolean(context.onRerun) && pass.status !== "running" &&
		pass.reviewerName === context.rerunHarness;
	const hasDetails = Boolean(pass.body) || pass.inlineComments.length > 0 || pass.resolvedComments.length > 0;
	const Icon = reviewToneIcon[pass.tone];
	const noteParts = [
		pass.source === "external" ? (pass.isBot ? `${t("inspector.review.github")} · ${t("inspector.bot")}` : t("inspector.review.github")) : null,
		pass.requestedByAgent ? t("inspector.review.requestedByAgent") : null,
		pass.stale ? t("inspector.earlierPass") : null,
	].filter(Boolean);
	const menuItems: InspectorActionMenuItem[] = [
		...(actions.canSend ? [{
			disabled: actions.sendState === "sending" || actions.sendState === "sent",
			key: "send",
			label: actions.sendState === "sent" ? t("inspector.sentToWorkerAgent") : t("inspector.sendToWorkerAgent"),
			onSelect: () => void actions.send(),
		}] : []),
		...(canRerun ? [{ key: "rerun", label: t("inspector.review.rerun"), onSelect: () => context.onRerun?.() }] : []),
		...(actions.canRequestRereview ? [{
			disabled: actions.rereviewState === "sending" || actions.rereviewState === "sent",
			key: "rereview",
			label: actions.rereviewState === "sent" ? t("inspector.rereviewRequested") : t("inspector.requestRereviewPR"),
			onSelect: () => void actions.requestRereview(),
		}] : []),
		...(pass.url && context.onOpenInAOBrowser ? [{ key: "ao-browser", label: t("inspector.openInAOBrowser"), onSelect: () => context.onOpenInAOBrowser?.(pass.url!) }] : []),
		...(pass.url ? [{ href: pass.url, key: "system-browser", label: t("inspector.openInSystemBrowser") }] : []),
	];
	const menuLabel = t("inspector.review.actionsFor", { number: prNumber, reviewer: name });
	return (
		<div className="flex min-w-0 flex-col" data-testid={pass.source === "agent" ? "review-run-summary-row" : "github-review-card"}>
			<InfoRow
				action={hasDetails && menuItems.length > 0 ? <ReviewActionMenu items={menuItems} label={menuLabel} /> : undefined}
				ariaControls={hasDetails ? bodyId : undefined}
				expanded={hasDetails ? open : undefined}
				icon={Icon}
				iconClassName={cn(reviewToneText[pass.tone], pass.tone === "working" && "animate-spin motion-reduce:animate-none")}
				label={name}
				note={pass.reviewerName ? (
					<>
						<span className={reviewToneText[pass.tone]}>{pass.verdictLabel}</span>
						{noteParts.map((part) => <span key={part}> · {part}</span>)}
					</>
				) : undefined}
				noteClassName="text-muted-foreground"
				onClick={hasDetails ? () => setOpen((current) => !current) : undefined}
				trailing={pass.timeLabel ? <span className="shrink-0 text-caption tabular-nums text-muted-foreground">{pass.timeLabel}</span> : undefined}
			/>
			<AnimatePresence initial={false}>
				{hasDetails && open ? (
					<ReviewReveal className="min-w-0" id={bodyId}>
						<div className="flex min-w-0 flex-col gap-2 pt-1 pb-2 pl-5">
							{pass.body ? (
								pass.status === "failed" ? (
									<p className="m-0 whitespace-pre-wrap break-words text-xs text-error">{pass.body}</p>
								) : (
									<InspectorReviewMarkdown
										body={pass.body.replace(/\n{3,}/g, "\n\n")}
										renderMarkdown={renderReviewMarkdown}
										testId={pass.source === "agent" ? "review-run-summary" : "github-review-summary"}
									/>
								)
							) : null}
							<InspectorReviewComments
								comments={pass.inlineComments}
								externalLink={ProductExternalLink}
								labels={context.labels}
								onResolveInlineComment={context.onResolveInlineComment}
								onSendInlineComment={context.onSendInlineComment}
								onViewInlineCommentInFile={(comment) => {
									if (comment.file) context.onOpenReviewFile?.({ line: comment.line, path: comment.file });
								}}
								renderActionMenu={renderReviewActionMenu}
								resolvedComments={pass.resolvedComments}
								reviewerId={pass.reviewerName}
								reviewUrl={pass.url ?? undefined}
							/>
							<ReviewPassButtons actions={actions} />
						</div>
					</ReviewReveal>
				) : null}
			</AnimatePresence>
		</div>
	);
}

function ReviewPassButtons({ actions }: { actions: ReturnType<typeof useReviewPassActions> }) {
	const { t } = useTranslation();
	if (!actions.canSend && !actions.canRequestRereview) return null;
	return (
		<div className="flex min-w-0 flex-wrap items-center gap-1.5">
			{actions.canSend ? (
				<Button
					disabled={actions.sendState === "sending" || actions.sendState === "sent"}
					onClick={() => void actions.send()}
					size="sm"
					type="button"
					variant="secondary"
				>
					{actions.sendState === "sent" ? t("inspector.sentToWorkerAgent") : t("inspector.sendToWorkerAgent")}
				</Button>
			) : null}
			{actions.canRequestRereview ? (
				<Button
					disabled={actions.rereviewState === "sending" || actions.rereviewState === "sent"}
					onClick={() => void actions.requestRereview()}
					size="sm"
					type="button"
					variant="ghost"
				>
					{actions.rereviewState === "sent" ? t("inspector.rereviewRequested") : t("inspector.requestRereviewPR")}
				</Button>
			) : null}
			{actions.sendState === "error" ? <span className="text-caption text-error" role="status">{t("inspector.sendToWorkerAgentError")}</span> : null}
			{actions.rereviewState === "error" ? <span className="text-caption text-error" role="status">{t("inspector.rereviewRequestFailed")}</span> : null}
		</div>
	);
}

/** A current-head change request in "Needs you", with its send action beside it. */
function NeedsYouChangesRow({ context, pass, prNumber }: { context: ReviewPassContext; pass: ReviewPassView; prNumber: number }) {
	const { t } = useTranslation();
	const actions = useReviewPassActions(pass, context);
	const name = reviewerDisplayName(pass);
	return (
		<div className="flex min-w-0 items-center gap-1">
			<div className="min-w-0 flex-1">
				<InfoRow
					icon={CircleAlert}
					iconClassName="text-warning"
					label={pass.reviewerName ? t("inspector.review.requestedChanges", { number: prNumber, reviewer: name }) : name + " · #" + prNumber}
					title={name}
					trailing={pass.timeLabel ? <span className="shrink-0 text-caption tabular-nums text-muted-foreground">{pass.timeLabel}</span> : undefined}
				/>
			</div>
			{actions.canSend ? (
				<Button
					aria-label={t("inspector.review.sendFor", { number: prNumber, reviewer: name })}
					className="shrink-0 px-2 text-xs"
					disabled={actions.sendState === "sending" || actions.sendState === "sent"}
					onClick={() => void actions.send()}
					size="sm"
					type="button"
					variant="ghost"
				>
					{actions.sendState === "sent" ? t("inspector.sentToWorkerAgent") : actions.sendState === "error" ? t("inspector.sendToWorkerAgentError") : t("inspector.review.send")}
				</Button>
			) : null}
		</div>
	);
}

/** Review and comment menus on the shared DropdownMenu primitive. */
function ReviewActionMenu({ items, label }: { items: InspectorActionMenuItem[]; label: string }) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button
					aria-label={label}
					className="shrink-0 text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/60 [&_svg]:size-icon-sm"
					size="icon-sm"
					type="button"
					variant="ghost"
				>
					<MoreHorizontal aria-hidden="true" />
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end">
				{items.map((item) => (
					<DropdownMenuItem
						className={cn(item.tone === "success" && "text-success", item.tone === "error" && "text-error")}
						disabled={item.disabled}
						key={item.key}
						onSelect={() => {
							if (item.href) void openLinkInSystemBrowser(item.href);
							else item.onSelect?.();
						}}
					>
						{item.label}
					</DropdownMenuItem>
				))}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

function renderReviewActionMenu(menu: { items: InspectorActionMenuItem[]; label: string }) {
	return <ReviewActionMenu items={menu.items} label={menu.label} />;
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
		commentActions: (location) => t("inspector.review.commentActionsFor", { location }),
		copyCommentLink: t("inspector.review.copyCommentLink"),
		openOnGitHub: t("inspector.review.openOnGitHub"),
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
	lines.push("", "You should not need to re-fetch review data unless you need additional context beyond what AO has provided here.");
	return lines.join("\n");
}

function formatReviewSummaryMessage(summary: InspectorReviewSummaryAction): string {
	const reviewer = sanitizeWorkerMessagePart(summary.reviewerId.trim() || "reviewer");
	const body = sanitizeWorkerMessagePart(summary.body.trim());
	const url = sanitizeWorkerMessagePart((summary.url || summary.pullRequestUrl || "").trim());
	const source = summary.source === "agent" ? "AO agent review" : "external PR review";
	const lines = [
		`A ${source} from ${reviewer} has feedback for your pull request. Address the actionable items, run relevant tests, commit the fixes, and push the branch.`,
		"",
		"Review summary:",
		body,
	];
	if (url) lines.push("", `Review URL: ${url}`);
	return lines.join("\n");
}

function sanitizeWorkerMessagePart(value: string): string {
	return value.replace(/[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F]/g, "");
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
	const latestAutoFailure = reviewStates
		.map((review) => review.latestRun)
		.filter(
			(run): run is ReviewRunFacts =>
				Boolean(
					autoReviewEnabled &&
						run?.triggerSource === "auto" &&
						run.status === "failed" &&
						run.body?.trim(),
				),
		)
		.sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
	const [dismissedAutoFailureId, setDismissedAutoFailureId] = useState<string | null>(null);
	useEffect(() => {
		if (!latestAutoFailure || latestAutoFailure.id === dismissedAutoFailureId) return;
		const timer = window.setTimeout(() => setDismissedAutoFailureId(latestAutoFailure.id), 10_000);
		return () => window.clearTimeout(timer);
	}, [dismissedAutoFailureId, latestAutoFailure]);
	const announcement = useReviewAnnouncement(openReviewStatesFor(session, reviewStates), !isLoading);
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
	const autoReviewFailure =
		latestAutoFailure && latestAutoFailure.id !== dismissedAutoFailureId ? latestAutoFailure.body.trim() : null;
	const hasReviewerSession = reviewerHandleId.trim() !== "" ||
		Boolean(reviewerSurface?.mode === "chat" && reviewerSurface.reviewId);
	// Every pass in flight, whichever reviewer surface is selected.
	const inFlight = openReviewStates.filter((review) => review.status === "running" || review.latestRun?.status === "running");
	const reviewRunning = inFlight.length > 0;
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
	const selectDisabled = reviewRunning || autoReviewEnabled || isKilling || isSwitchingReviewer || isTriggering || isCancelling;
	const selectDisabledReason = reviewRunning
		? t("inspector.review.selectLockedRunning")
		: autoReviewEnabled && !cloud
			? t("inspector.review.selectLockedAuto")
			: undefined;
	const primaryDisabled = reviewRunning ? isCancelling || isKilling || isSwitchingReviewer : runDisabled || autoReviewEnabled;
	const primaryDisabledReason = reviewRunning
		? undefined
		: autoReviewEnabled
			? t("inspector.review.runLockedAuto")
			: reviewRunDisabled(openReviewStates, false)
				? t("inspector.review.runLockedNoPR")
				: cloud && !cloudReviewerReady
					? t("inspector.review.runLockedCloudReviewer")
					: undefined;
	// The selected reviewer plus any other live one (an agent may ask a reviewer
	// other than the selected one); each can be opened from the menu.
	const currentSurface: ReviewerSurface | undefined = reviewerSurface
		? { ...reviewerSurface, handleId: reviewerSurface.handleId || reviewerHandleId || undefined }
		: reviewerHandleId
			? { handleId: reviewerHandleId, harness: activeReviewerHarness, mode: "tui", reviewId: "" }
			: undefined;
	const sameSurface = (a: ReviewerSurface, b: ReviewerSurface) =>
		Boolean((a.reviewId && a.reviewId === b.reviewId) || (a.handleId && a.handleId === b.handleId));
	const openableReviewers = [
		...activeReviewers,
		...(currentSurface && hasReviewerSession && !activeReviewers.some((surface) => sameSurface(surface, currentSurface)) ? [currentSurface] : []),
	];

	return (
		<div
			aria-label={t("inspector.review.controls")}
			className="flex min-w-0 flex-col gap-1 px-3 pt-1.5 pb-3.5"
			data-testid="review-toolbar"
			role="group"
		>
			{error ? (
				<p className="m-0 flex min-w-0 items-start gap-1.5 text-xs text-error" role="alert">
					<CircleX aria-hidden="true" className="mt-0.5 size-icon-sm shrink-0" />
					<span className="min-w-0">{apiErrorMessage(error, t("inspector.reviewRequestFailed"))}</span>
				</p>
			) : null}
			{autoReviewFailure ? (
				<p className="m-0 flex min-w-0 items-start gap-1.5 text-xs text-error" role="status">
					<CircleX aria-hidden="true" className="mt-0.5 size-icon-sm shrink-0" />
					<span className="min-w-0">
						<span className="font-semibold">
							{t("inspector.autoReview")} {t("inspector.review.failed")}:
						</span>{" "}
						{autoReviewFailure}
					</span>
				</p>
			) : null}
			{notice ? (
				<Tooltip>
					<TooltipTrigger asChild>
						<button
							aria-label={notice}
							className="flex max-w-full shrink-0 items-start gap-1 self-start rounded-sm text-left text-xs font-medium text-muted-foreground transition-colors duration-fast hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
							type="button"
						>
							<Info aria-hidden="true" className="mt-0.5 size-icon-2xs shrink-0" />
							<span className="min-w-0">{t("inspector.reviewAlreadyRanShort")}</span>
						</button>
					</TooltipTrigger>
					<TooltipContent className="max-w-56">{notice}</TooltipContent>
				</Tooltip>
			) : null}
			<div className="review-run-controls-container flex min-h-8 min-w-0 items-center gap-1.5">
				<div className="flex min-w-0 flex-1 items-center gap-1.5">
					<DisabledReason reason={selectDisabled ? selectDisabledReason : undefined}>
						{cloud ? (
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
								<SelectContent align="start">
									{["claude-code", "codex", "cursor"].map((harness) => (
										<SelectItem key={harness} value={harness}>
											{agentLabel(harness)}
											{cloudReviewerHarnesses.includes(harness) ? "" : " · Connect credential"}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						) : (
							<ReviewerSelect
								ariaLabel={t("inspector.selectReviewerAgent")}
								agents={agentCatalog?.agents}
								contentAlign="start"
								defaultHarness={resolvedDefaultHarness}
								disabled={selectDisabled}
								onChange={(next) => onReviewerHarnessPreviewChange(next as ReviewerHarness | "")}
								onConfigChange={(harness, config) => onReviewerOverrideChange(harness as ReviewerHarness | "", config)}
								model={reviewerModel}
								mode={reviewerMode}
								hostId={hostId}
								projectId={session.workspaceId}
								triggerClassName="review-run-agent-select h-control-md w-auto min-w-0 max-w-full shrink justify-start px-2 text-left text-xs"
								value={reviewerOverride}
							/>
						)}
					</DisabledReason>
					{cloud && cloudHarnessStatuses.some(
						(entry) => entry.harness === cloudReviewerHarness && entry.status !== "ready",
					) ? (
						<Button
							disabled={isInstallingCloudHarness || reviewRunning}
							onClick={onCloudInstallHarness}
							size="sm"
							type="button"
							variant="outline"
						>
							{isInstallingCloudHarness ? "Installing…" : "Install"}
						</Button>
					) : null}
				</div>
				<DisabledReason reason={primaryDisabled ? primaryDisabledReason : undefined}>
					<Button
						aria-label={primaryReviewActionLabel}
						className="shrink-0 gap-1 px-2 text-xs [&_svg]:size-icon-sm"
						disabled={primaryDisabled}
						onClick={reviewRunning ? onCancel : onTrigger}
						size="sm"
						type="button"
						variant={reviewRunning || reviewHasRun ? "secondary" : "primary"}
					>
						{reviewRunning ? <CircleStop aria-hidden="true" /> : <Play aria-hidden="true" />}
						<span className="review-run-action-label">{primaryReviewActionLabel}</span>
					</Button>
				</DisabledReason>
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<Button
							aria-label={t("inspector.review.moreOptions")}
							className="shrink-0 text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/60 [&_svg]:size-icon-sm"
							size="icon-sm"
							type="button"
							variant="ghost"
						>
							<MoreVertical aria-hidden="true" />
						</Button>
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end" className="w-64">
						<DropdownMenuPrimitive.CheckboxItem
							checked={autoReviewEnabled}
							className={cn(actionMenuItemClass, "items-start")}
							disabled={isAutoReviewSaving}
							onCheckedChange={(checked) => onAutoReviewChange(checked === true)}
						>
							<span className="flex size-icon-lg shrink-0 items-center justify-center">
								<DropdownMenuPrimitive.ItemIndicator>
									<Check aria-hidden="true" />
								</DropdownMenuPrimitive.ItemIndicator>
							</span>
							<span className="flex min-w-0 flex-col gap-0.5">
								<span>{t("inspector.autoReview")}</span>
								<span className="text-caption text-muted-foreground">{t("inspector.review.autoReviewHint")}</span>
							</span>
						</DropdownMenuPrimitive.CheckboxItem>
						{!cloud ? (
							<>
								<DropdownMenuSeparator />
								{openableReviewers.length > 0 ? (
									openableReviewers.map((surface) => (
										<DropdownMenuItem
											className="items-start"
											disabled={surface.mode !== "chat" && !surface.handleId}
											key={surface.reviewId || surface.handleId || surface.harness}
											onSelect={() => onOpenReviewer(surface)}
										>
											<SquareTerminal aria-hidden="true" />
											<span className="flex min-w-0 flex-col gap-0.5">
												<span>{t("inspector.review.openReviewerFor", { agent: agentLabel(surface.harness) })}</span>
												{surface.mode !== "chat" && !surface.handleId ? <span className="text-caption">{t("inspector.review.noReviewerSession")}</span> : null}
											</span>
										</DropdownMenuItem>
									))
								) : (
									<DropdownMenuItem className="items-start" disabled>
										<SquareTerminal aria-hidden="true" />
										<span className="flex min-w-0 flex-col gap-0.5">
											<span>{t("inspector.review.openReviewer")}</span>
											<span className="text-caption">{t("inspector.review.noReviewerSession")}</span>
										</span>
									</DropdownMenuItem>
								)}
								<DropdownMenuItem className="items-start" disabled={killDisabled} onSelect={onKill}>
									<Archive aria-hidden="true" />
									<span className="flex min-w-0 flex-col gap-0.5">
										<span>{archiveActionLabel}</span>
										{!hasReviewerSession ? <span className="text-caption">{t("inspector.review.noReviewerSession")}</span> : null}
									</span>
								</DropdownMenuItem>
							</>
						) : null}
					</DropdownMenuContent>
				</DropdownMenu>
			</div>
			<AnimatePresence initial={false}>
				{inFlight.map((review) => (
					<ReviewReveal key={review.latestRun?.id ?? review.prUrl}>
						<ReviewNowRow
							disabled={isCancelling || isKilling || isSwitchingReviewer}
							harness={review.latestRun?.harness || activeReviewerHarness}
							isCancelling={isCancelling}
							number={review.prNumber}
							onCancel={onCancel}
							startedAt={review.latestRun?.createdAt}
						/>
					</ReviewReveal>
				))}
			</AnimatePresence>
			<p aria-atomic="true" aria-live="polite" className="sr-only" data-testid="review-live-region">{announcement}</p>
		</div>
	);
}

/** A disabled control's reason, on hover and to screen readers. */
function DisabledReason({ children, reason }: { children: ReactNode; reason?: string }) {
	if (!reason) return <>{children}</>;
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex min-w-0">
					{children}
					<span className="sr-only">{reason}</span>
				</span>
			</TooltipTrigger>
			<TooltipContent>{reason}</TooltipContent>
		</Tooltip>
	);
}

/** "Now": a pass in flight, with its reviewer, PR, age and a way to stop it. */
function ReviewNowRow({
	disabled,
	harness,
	isCancelling,
	number,
	onCancel,
	startedAt,
}: {
	disabled: boolean;
	harness: string;
	isCancelling: boolean;
	number: number;
	onCancel: () => void;
	startedAt?: string;
}) {
	const { t } = useTranslation();
	const agent = agentLabel(harness);
	return (
		<div className="flex min-h-8 min-w-0 items-center gap-2 text-sm text-foreground" data-testid="review-now-row">
			<Loader2 aria-hidden="true" className="size-icon-sm shrink-0 animate-spin text-status-working motion-reduce:animate-none" />
			<span className="min-w-0 flex-1 truncate">
				{t("inspector.review.nowReviewing", { agent, number })}
				{startedAt ? <span className="text-caption tabular-nums text-muted-foreground"> · {formatTimeCompact(startedAt)}</span> : null}
			</span>
			<Button
				aria-label={t("inspector.review.stopFor", { agent, number })}
				className="shrink-0 px-2 text-xs"
				disabled={disabled}
				onClick={onCancel}
				size="sm"
				type="button"
				variant="ghost"
			>
				{isCancelling ? t("inspector.review.cancelling") : t("inspector.review.stop")}
			</Button>
		</div>
	);
}

/** Text for the tab's one polite live region: a pass starting, then its verdict. */
function useReviewAnnouncement(reviewStates: PRReviewState[], ready: boolean): string {
	const { t } = useTranslation();
	const previous = useRef<Map<string, { id: string; status: string }> | null>(null);
	const [message, setMessage] = useState("");
	const signature = reviewStates.map((state) => `${state.prUrl}|${state.latestRun?.id ?? ""}|${state.latestRun?.status ?? ""}`).join("\n");
	useEffect(() => {
		if (!ready) return;
		const next = new Map<string, { id: string; status: string }>();
		for (const state of reviewStates) {
			if (state.latestRun) next.set(state.prUrl, { id: state.latestRun.id, status: state.latestRun.status });
		}
		const before = previous.current;
		previous.current = next;
		// The first load is the state the person arrived to, not news.
		if (!before) return;
		for (const state of reviewStates) {
			const run = state.latestRun;
			if (!run) continue;
			const prior = before.get(state.prUrl);
			const reviewer = agentLabel(run.harness || "reviewer");
			if (run.status === "running") {
				if (!prior || prior.id !== run.id || prior.status !== "running") {
					setMessage(t("inspector.review.announceStarted", { number: state.prNumber, reviewer }));
				}
			} else if (prior?.id === run.id && prior.status === "running") {
				setMessage(t("inspector.review.announceVerdict", { number: state.prNumber, reviewer, verdict: aoRunOutcome(run, t).label }));
			}
		}
		// The signature carries every fact the effect reads.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [ready, signature]);
	return message;
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

/** Every recorded reviewer pass per PR, newest first: running, failed and
 *  cancelled ones included, so the list shows what happened. Folds in each
 *  state's own runs, which also covers daemons predating the runs field. */
function reviewRunsByPR(openReviewStates: PRReviewState[], runs: ReviewRunFacts[]): Map<string, ReviewRunFacts[]> {
	// A state's own runs belong to that state's PR, whatever URL form they carry.
	const candidates = [
		...openReviewStates.flatMap((state) =>
			[state.latestRun, state.previousRun].map((run) => ({ prUrl: state.prUrl, run })),
		),
		...runs.map((run) => ({ prUrl: run.prUrl, run })),
	];
	const byId = new Map<string, { prUrl: string; run: ReviewRunFacts }>();
	for (const { prUrl, run } of candidates) {
		if (!run || byId.has(run.id)) continue;
		const settled = run.status === "complete" || run.status === "delivered";
		const shown = run.status === "running" || run.status === "failed" || run.status === "cancelled" ||
			(settled && (Boolean(run.body?.trim()) || Boolean(run.verdict?.trim())));
		if (shown) byId.set(run.id, { prUrl, run });
	}
	const byPR = new Map<string, ReviewRunFacts[]>();
	for (const { prUrl, run } of [...byId.values()].sort((a, b) => b.run.createdAt.localeCompare(a.run.createdAt))) {
		byPR.set(prUrl, [...(byPR.get(prUrl) ?? []), run]);
	}
	return byPR;
}

// GitHub anchors a posted review at #pullrequestreview-<id> on the PR page; we
// only have that link once the run has been delivered to GitHub.
function aoReviewCommentUrl(run: PRReviewState["latestRun"]): string | null {
	if (!run?.prUrl || !run.githubReviewId) return null;
	return `${run.prUrl}#pullrequestreview-${run.githubReviewId}`;
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
	if (browserPoppedOut) {
		return (
			<div className="h-full min-h-0" data-browser-dock-target="" role="tabpanel">
				<div className={cn(inspectorEmptyClass, "flex flex-col items-center gap-2 py-10 px-5 text-center")}>
					<p className="text-md-sm text-muted-foreground">{t("inspector.browserInCenter")}</p>
					<Button onClick={() => onTogglePopOut?.(false)} size="sm" type="button" variant="outline">
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
			onTogglePopOut={(next) => onTogglePopOut?.(next)}
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
				<Button disabled={!onOpenFiles} onClick={() => onOpenFiles?.()} size="sm" type="button" variant="outline">
					{t("inspector.openFiles")}
				</Button>
			</div>
		</div>
	);
}
