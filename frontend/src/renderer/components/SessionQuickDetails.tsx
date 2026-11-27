import { useQuery, useQueryClient, type InfiniteData } from "@tanstack/react-query";
import { branchTopic } from "@aoagents/product-ui";
import { useReducedMotion } from "motion/react";
import { memo, useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
	CheckWaves,
	FileDiff,
	GitBranch,
	GitMerge,
	GitPullRequest,
	GitPullRequestClosed,
	GitPullRequestDraft,
	List,
	MoreHorizontal,
	Package,
	Settings2,
} from "@/components/app-icons";
import { useCloudCp } from "../hooks/useCloudCp";
import { conversationModelsQueryKey, conversationQueryKey } from "../hooks/useConversation";
import {
	isChangedWorkspaceFile,
	sessionWorkspaceFilesQueryOptions,
	type WorkspaceFilesResponse,
} from "../hooks/useSessionWorkspaceFiles";
import { agentModelDisplayLabel } from "../lib/agent-model-choices";
import { agentLabel } from "../lib/agent-options";
import type { ArtifactRow } from "../lib/artifacts";
import { aoBridge } from "../lib/bridge";
import { claudeModelName } from "../lib/cloud-model-catalog";
import { sessionUiKey } from "../lib/hosts";
import { isMacPlatform } from "../lib/platform";
import { sessionBranchPresentation } from "../lib/session-branch";
import { cn } from "../lib/utils";
import { useUiStore, type InspectorView } from "../stores/ui-store";
import { answeringModel, type ChatModel, type ConversationSnapshot } from "../types/conversation";
import {
	sessionDeliverables,
	sessionWorkspaceReady,
	sortedPRs,
	STANDALONE_WORKSPACE_ID,
	type PRState,
	type PullRequestFacts,
	type SessionArtifact,
	type WorkspaceSession,
} from "../types/workspace";
import { AgentAvatar } from "./AgentAvatar";
import { ArtifactThumbnail } from "./ArtifactThumbnail";
import { SubagentIcon } from "./icons";
import { MenuKitContext, dropdownMenuKit } from "./menu-model";
import { SessionContextMenuItems, useCopyToClipboard } from "./SessionContextMenuItems";
import { INSPECTOR_SPRING_MS } from "./SessionInspectorRail";
import { formatEffortLabel } from "./settings/EffortPicker";
import { Button } from "./ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

/** Minimum session canvas width (px) before Quick Details reserves horizontal space instead of floating over the chat. */
export const QUICK_DETAILS_DOCK_MIN_WIDTH_PX = 940;
/** Width (px) of the floating Quick Details card. */
export const QUICK_DETAILS_WIDTH_PX = 296;
/** Internal padding (px) of the card; card border-radius = button radius (--radius-md) + this padding. */
export const QUICK_DETAILS_INNER_PADDING_PX = 6;
/** Equal inset (px) from the top and right edges of the session canvas. */
export const QUICK_DETAILS_INSET_PX = 10;
/** Total horizontal width reserved on wide windows (card + right inset + left breathing gap). */
export const QUICK_DETAILS_RESERVE_PX = QUICK_DETAILS_WIDTH_PX + QUICK_DETAILS_INSET_PX * 2;

const QUICK_DETAILS_MAX_ARTIFACT_PREVIEWS = 4;
const noDragStyle = isMacPlatform() ? ({ WebkitAppRegion: "no-drag" } as CSSProperties) : undefined;
const numberFormat = new Intl.NumberFormat();

const prIcons: Record<PRState, { icon: typeof GitPullRequest; className: string }> = {
	open: { icon: GitPullRequest, className: "text-success" },
	draft: { icon: GitPullRequestDraft, className: "text-passive" },
	merged: { icon: GitMerge, className: "text-status-merged" },
	closed: { icon: GitPullRequestClosed, className: "text-error" },
};

export type SessionQuickDetailsProps = {
	session: WorkspaceSession;
	hostId?: string;
	orchestrator: boolean;
	subagentsAvailable: boolean;
	previewLive?: boolean;
	onOpenArtifact?: (target: { feedback?: boolean; path: string }) => void;
	onOpenInspectorView: (view: InspectorView) => void;
	onClose?: () => void;
	onRequestArchive?: () => void;
};

function useSessionDiffStats(session: WorkspaceSession, orchestrator: boolean, hostId?: string) {
	const localEnabled = !orchestrator && !session.cloud && !session.needsResume && sessionWorkspaceReady(session);
	const localQuery = useQuery({
		...sessionWorkspaceFilesQueryOptions(session.id, undefined, hostId),
		enabled: localEnabled,
		refetchInterval: false,
	});
	const { client: cloudClient, ready: cloudReady, baseUrl: cloudBaseUrl } = useCloudCp(Boolean(session.cloud));
	const cloudOrgId = session.cloud?.orgId;
	const cloudQuery = useQuery({
		queryKey: ["cloud-workspace-review", cloudBaseUrl, cloudOrgId ?? "", session.id, "summary"],
		enabled: !orchestrator && cloudReady && Boolean(session.cloud) && Boolean(cloudOrgId) && session.runtimeConnected === true,
		refetchInterval: 5_000,
		queryFn: () => cloudClient.getWorkspaceReview(cloudOrgId!, session.id),
	});

	if (session.cloud) {
		const summary = cloudQuery.data?.summary;
		return summary
			? { files: summary.files, additions: summary.additions, deletions: summary.deletions, loaded: true }
			: { files: 0, additions: 0, deletions: 0, loaded: false };
	}

	const data: WorkspaceFilesResponse | undefined = localQuery.data;
	if (!data) {
		const fallback = session.changedFiles ?? [];
		if (fallback.length > 0) {
			return {
				files: fallback.length,
				additions: fallback.reduce((total, f) => total + f.additions, 0),
				deletions: fallback.reduce((total, f) => total + f.deletions, 0),
				loaded: true,
			};
		}
		return { files: 0, additions: 0, deletions: 0, loaded: false };
	}

	const changed = data.files.filter(isChangedWorkspaceFile);
	return {
		files: changed.length,
		additions: changed.reduce((total, f) => total + (f.additions ?? 0), 0),
		deletions: changed.reduce((total, f) => total + (f.deletions ?? 0), 0),
		loaded: true,
	};
}

/**
 * Compact floating quick-details card for a session: project & branch header +
 * `...` menu, `Changes +X -Y`, separate status and agent/model rows, PR row,
 * artifact thumbnail previews, and quick Inspector links.
 */
export const SessionQuickDetailsPanel = memo(function SessionQuickDetailsPanel({
	session,
	hostId: hostIdProp,
	orchestrator,
	subagentsAvailable,
	previewLive = true,
	onOpenArtifact,
	onOpenInspectorView,
	onClose,
	onRequestArchive,
}: SessionQuickDetailsProps) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const copyToClipboard = useCopyToClipboard();
	const hostId = hostIdProp ?? session.hostId;
	const uiSessionId = sessionUiKey(session.id, hostId);

	const artifactsUnseen = useUiStore((state) => Boolean(state.inspectorSessions[uiSessionId]?.artifactsUnseen));

	const diff = useSessionDiffStats(session, orchestrator, hostId);
	const snapshot = queryClient.getQueryData<InfiniteData<ConversationSnapshot>>(conversationQueryKey(session.id, hostId))?.pages[0];
	const cachedModels = queryClient.getQueryData<ChatModel[]>(conversationModelsQueryKey(session.id, hostId)) ?? [];
	const rawModel = snapshot?.settings ? answeringModel(snapshot) : snapshot?.modelReroute?.toModel;
	const strippedModel = rawModel ? rawModel.slice(rawModel.lastIndexOf("/") + 1).replace(/\[.*$/, "") : undefined;
	const matchedModel = strippedModel
		? cachedModels.find((entry) => entry.id === rawModel || entry.id === strippedModel)
		: undefined;
	const modelLabel = strippedModel
		? agentModelDisplayLabel(
			session.provider,
			matchedModel?.displayName ?? claudeModelName(strippedModel) ?? strippedModel,
		)
		: undefined;
	const rawEffort = snapshot?.settings?.reasoningEffort || matchedModel?.defaultEffort;
	const effortLabel = rawEffort ? formatEffortLabel(rawEffort, t) : undefined;

	const providerName = agentLabel(session.provider);

	const { branch } = sessionBranchPresentation(session);
	const showBranch = !orchestrator && Boolean(branch);
	const prs = sortedPRs(session);

	const prStatus = (pr: PullRequestFacts): { label: string; className: string } => {
		if (pr.state === "merged") return { label: t("pr.tab.state.merged"), className: "text-status-merged" };
		if (pr.state === "closed") return { label: t("pr.tab.state.closed"), className: "text-error" };
		if (pr.state === "draft") return { label: t("pr.tab.state.draft"), className: "text-passive" };
		if (pr.mergeability === "conflicting") return { label: t("pr.card.mergeConflict"), className: "text-error" };
		if (pr.ci === "failing") return { label: t("pr.card.checksFailing"), className: "text-error" };
		if (pr.review === "changes_requested") return { label: t("pr.review.changesRequested"), className: "text-warning" };
		if (pr.ci === "pending") return { label: t("pr.card.checksPending"), className: "text-passive" };
		if (pr.mergeability === "mergeable") return { label: t("pr.card.readyToMerge"), className: "text-success" };
		return { label: t("pr.tab.state.open"), className: "text-passive" };
	};

	const deliverables = sessionDeliverables(session);
	const artifacts = deliverables.length > 0 ? deliverables : (session.artifactFiles ?? []);
	const artifactCount = artifacts.length;
	const artifactPreviews = artifacts.slice(0, QUICK_DETAILS_MAX_ARTIFACT_PREVIEWS);
	const showReviews = !orchestrator && (Boolean(session.cloud) || prs.some((p) => p.state === "open" || p.state === "draft"));
	const showChanges = !orchestrator && (!session.needsResume || diff.files > 0);
	const title =
		session.workspaceId === STANDALONE_WORKSPACE_ID
			? session.title || session.workspaceName
			: session.workspaceName || session.title;

	const jumpTo = (view: InspectorView) => {
		onOpenInspectorView(view);
	};

	const openPR = (url: string) => {
		if (!url) return;
		void aoBridge.app.openExternal(url).catch(() => undefined);
	};

	return (
		<div
			className="flex min-w-0 flex-col select-none"
			data-testid="session-quick-details-content"
			style={{ padding: QUICK_DETAILS_INNER_PADDING_PX }}
		>
			{/* Header: project name + branch + "..." actions */}
			<div className="flex items-start justify-between gap-2 px-2 py-1.5">
				<div className="min-w-0 flex-1">
					<div className="truncate text-sm font-medium text-muted-foreground" title={title}>
						{title}
					</div>
					{showBranch && branch ? (
						<button
							aria-label={t("command.copyBranch")}
							className="mt-1 inline-flex max-w-full min-w-0 items-center gap-2 rounded-sm font-mono text-xs text-passive hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
							onClick={() => copyToClipboard(branch)}
							title={branch}
							type="button"
						>
							<span className="inline-flex size-icon-sm shrink-0 items-center justify-center">
								<GitBranch aria-hidden="true" className="size-3 shrink-0" />
							</span>
							<span className="min-w-0 truncate">{branchTopic(branch)}</span>
						</button>
					) : null}
				</div>
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<Button
							aria-label={t("quickDetails.actions")}
							className="shrink-0 text-muted-foreground transition-none before:inset-0 before:transition-none! hover:text-foreground"
							data-testid="quick-details-actions-trigger"
							size="icon-xs"
							type="button"
							variant="quiet"
						>
							<MoreHorizontal aria-hidden="true" className="size-icon-sm" />
						</Button>
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end" className="min-w-48">
						<MenuKitContext.Provider value={dropdownMenuKit}>
							<SessionContextMenuItems
								onArchive={
									onRequestArchive
										? () => {
											onClose?.();
											onRequestArchive();
										}
										: undefined
								}
								session={session}
							/>
						</MenuKitContext.Provider>
					</DropdownMenuContent>
				</DropdownMenu>
			</div>

			{/* Primary row: Changes +X -Y */}
			{showChanges ? (
				<button
					className="group flex w-full min-w-0 cursor-pointer items-center justify-between gap-2 rounded-md px-2 py-1.5 text-left text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					data-testid="quick-details-changes-row"
					onClick={() => jumpTo("files")}
					type="button"
				>
					<span className="flex min-w-0 items-center gap-2 text-foreground/85 group-hover:text-foreground">
						<FileDiff aria-hidden="true" className="size-icon-sm shrink-0 text-muted-foreground group-hover:text-foreground" />
						<span className="truncate font-medium">{t("quickDetails.changes")}</span>
						{diff.files > 0 && (diff.additions > 0 || diff.deletions > 0) ? (
							<span className="shrink-0 text-xs text-passive group-hover:text-muted-foreground">
								{t("quickDetails.filesChanged", { count: diff.files })}
							</span>
						) : null}
					</span>
					{diff.additions > 0 || diff.deletions > 0 ? (
						<span className="inline-flex shrink-0 items-center gap-1.5 font-mono text-xs tabular-nums" data-testid="quick-details-diff-stats">
							{diff.additions > 0 ? <span className="text-success">+{numberFormat.format(diff.additions)}</span> : null}
							{diff.deletions > 0 ? <span className="text-error">-{numberFormat.format(diff.deletions)}</span> : null}
						</span>
					) : (
						<span className="shrink-0 text-xs text-passive group-hover:text-muted-foreground">
							{diff.files > 0
								? t("quickDetails.filesChanged", { count: diff.files })
								: diff.loaded
									? t("quickDetails.noChanges")
									: "—"}
						</span>
					)}
				</button>
			) : null}

			{/* Divider */}
			<div className="mx-2 my-1.5 border-t border-border" />

			{/* Session details: agent/model row */}
			<div
				className="flex min-w-0 items-center justify-between gap-2 px-2 py-1.5 text-sm text-foreground"
				data-testid="quick-details-agent-row"
				title={[providerName, modelLabel ?? rawModel, effortLabel].filter(Boolean).join(" · ")}
			>
				<span className="inline-flex min-w-0 items-center gap-2">
					<span className="inline-flex size-icon-sm shrink-0 items-center justify-center">
						<AgentAvatar className="size-3.5 shrink-0" decorative provider={session.provider} />
					</span>
					<span className="truncate">{providerName}</span>
				</span>
				{modelLabel || effortLabel ? (
					<span className="inline-flex min-w-0 shrink items-baseline gap-1 text-xs" data-testid="quick-details-agent">
						{modelLabel ? <span className="truncate text-muted-foreground">{modelLabel}</span> : null}
						{effortLabel ? <span className="shrink-0 text-passive">{effortLabel}</span> : null}
					</span>
				) : null}
			</div>

			{/* Pull Requests */}
			{prs.length > 0 ? (
				<>
					<div className="mx-2 my-1.5 border-t border-border" />
					<div className="flex flex-col gap-0.5" data-testid="quick-details-prs-section">
						{prs.map((pr) => {
							const PrIcon = prIcons[pr.state].icon;
							const status = prStatus(pr);
							return (
								<QuickRow
									icon={<PrIcon aria-hidden="true" className={cn("size-icon-sm shrink-0", prIcons[pr.state].className)} />}
									key={pr.url || pr.number}
									label={t("pr.others.short", { number: pr.number })}
									meta={status.label}
									metaClassName={status.className}
									onClick={() => openPR(pr.url)}
									testId="quick-details-pr-row"
								/>
							);
						})}
					</div>
				</>
			) : null}

			{/* Artifacts with thumbnail previews */}
			{artifactCount > 0 ? (
				<>
					<div className="mx-2 my-1.5 border-t border-border" />
					<div className="flex flex-col gap-1.5" data-testid="quick-details-artifacts-section">
						<QuickRow
							badge={artifactsUnseen}
							count={artifactCount}
							icon={<Package aria-hidden="true" className="size-icon-sm shrink-0 text-muted-foreground group-hover:text-foreground" />}
							label={t("artifacts.title")}
							onClick={() => jumpTo("artifacts")}
							testId="quick-details-link-artifacts"
						/>
						<div className="grid grid-cols-2 gap-2 px-2 pb-1.5">
							{artifactPreviews.map((artifact) => (
								<QuickArtifactPreviewTile
									artifact={artifact}
									key={artifact.path}
									onOpen={() => {
										if (onOpenArtifact) onOpenArtifact({ path: artifact.path });
										else jumpTo("artifacts");
									}}
									previewLive={previewLive}
									session={session}
								/>
							))}
						</div>
					</div>
				</>
			) : null}

			{/* Quick Inspector links */}
			<div className="mx-2 my-1.5 border-t border-border" />
			<div className="flex flex-col gap-0.5">
				{showReviews ? (
					<QuickRow
						icon={<CheckWaves aria-hidden="true" className="size-icon-sm shrink-0 text-muted-foreground group-hover:text-foreground" />}
						label={t("inspector.reviewTab")}
						onClick={() => jumpTo("reviews")}
						testId="quick-details-link-reviews"
					/>
				) : null}
				{subagentsAvailable ? (
					<QuickRow
						icon={<SubagentIcon aria-hidden="true" className="size-icon-sm shrink-0 text-muted-foreground group-hover:text-foreground" />}
						label={t("inspector.subagents")}
						onClick={() => jumpTo("subagents")}
						testId="quick-details-link-subagents"
					/>
				) : null}
				<QuickRow
					icon={<List aria-hidden="true" className="size-icon-sm shrink-0 text-muted-foreground group-hover:text-foreground" />}
					label={t("quickDetails.viewAll")}
					muted
					onClick={() => jumpTo("summary")}
					testId="quick-details-link-summary"
				/>
			</div>
		</div>
	);
});

function QuickArtifactPreviewTile({
	artifact,
	session,
	previewLive,
	onOpen,
}: {
	artifact: SessionArtifact;
	session: WorkspaceSession;
	previewLive: boolean;
	onOpen: () => void;
}) {
	const row: ArtifactRow = {
		...artifact,
		sessionId: session.id,
		sessionTitle: session.title ?? "",
		cloud: session.cloud,
		hostId: session.hostId,
	};
	return (
		<button
			className="group flex min-w-0 flex-col gap-1.5 rounded-md text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
			data-testid="quick-details-artifact-preview"
			onClick={onOpen}
			title={artifact.path}
			type="button"
		>
			<span className="relative block aspect-[16/10] w-full overflow-hidden rounded-md bg-background ring-1 ring-border group-hover:ring-foreground/25">
				<ArtifactThumbnail
					key={`${row.rawUrl}:${row.inlineUrl}:${row.updatedAt}`}
					previewLive={previewLive}
					row={row}
				/>
			</span>
			<span className="truncate text-caption text-muted-foreground group-hover:text-foreground">
				{artifact.name}
			</span>
		</button>
	);
}

function QuickRow({
	icon,
	label,
	count,
	meta,
	metaClassName,
	badge,
	muted = false,
	onClick,
	testId,
}: {
	icon: ReactNode;
	label: string;
	count?: number;
	meta?: string;
	metaClassName?: string;
	badge?: boolean;
	muted?: boolean;
	onClick: () => void;
	testId: string;
}) {
	return (
		<button
			className={cn(
				"group flex w-full min-w-0 cursor-pointer items-center justify-between gap-2 rounded-md px-2 py-1.5 text-left text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
				muted ? "text-muted-foreground hover:text-foreground" : "text-foreground/85 hover:text-foreground",
			)}
			data-testid={testId}
			onClick={onClick}
			type="button"
		>
			<span className="flex min-w-0 items-center gap-2">
				{icon}
				<span className="truncate">{label}</span>
			</span>
			<span className="flex shrink-0 items-center gap-1.5">
				{badge ? <span aria-hidden="true" className="size-1.5 rounded-full bg-primary" /> : null}
				{meta ? <span className={cn("text-xs", metaClassName ?? "text-passive group-hover:text-muted-foreground")}>{meta}</span> : null}
				{count !== undefined && count > 0 ? (
					<span className="font-mono text-xs tabular-nums text-passive group-hover:text-muted-foreground">{count}</span>
				) : null}
			</span>
		</button>
	);
}

/**
 * Floating Quick Details card pinned to the top-right of the session canvas
 * with equal top and right spacing (`10px`).
 * - Card border radius follows `buttonRadius (--radius-md) + innerPadding (6px)`
 *   so inner buttons sit concentrically inside the panel.
 * - When `reserveSpace` is true (wide window), a sibling flex spacer reserves
 *   the card's horizontal width so the chat stays cleanly to its left.
 * - When `reserveSpace` is false (narrow window), the card floats in the exact
 *   same top-right spot over the chat and dismisses on outside click or Escape.
 */
export function SessionQuickDetailsFloatingPanel({
	sessionKey,
	isOpen,
	reserveSpace,
	onDismiss,
	children,
}: {
	sessionKey: string;
	isOpen: boolean;
	reserveSpace: boolean;
	onDismiss?: () => void;
	children: ReactNode;
}) {
	const { t } = useTranslation();
	const prefersReducedMotion = useReducedMotion();
	const cardRef = useRef<HTMLElement>(null);

	const [motionState, setMotionState] = useState({ sessionKey, isOpen, animate: false });
	if (motionState.sessionKey !== sessionKey || motionState.isOpen !== isOpen) {
		setMotionState({ sessionKey, isOpen, animate: motionState.sessionKey === sessionKey });
	}
	// Mount lazily on first open for a session, then keep mounted so artifact
	// previews never flash fallbacks while sliding in or out.
	const [mountedSession, setMountedSession] = useState<string | null>(isOpen ? sessionKey : null);
	if (isOpen && mountedSession !== sessionKey) setMountedSession(sessionKey);
	const [settledClosed, setSettledClosed] = useState(!isOpen);
	if (isOpen && settledClosed) setSettledClosed(false);

	useEffect(() => {
		if (!motionState.animate) return;
		const timer = window.setTimeout(
			() => setMotionState((state) => (state === motionState ? { ...state, animate: false } : state)),
			INSPECTOR_SPRING_MS + 100,
		);
		return () => window.clearTimeout(timer);
	}, [motionState]);

	// In narrow (floating overlay) mode, dismiss on Escape or clicking outside
	// the card (ignoring the topbar trigger and portaled Radix menus/dialogs).
	useEffect(() => {
		if (!isOpen || reserveSpace || !onDismiss) return;
		const handleKeyDown = (event: KeyboardEvent) => {
			if (event.key === "Escape") onDismiss();
		};
		const handlePointerDown = (event: PointerEvent) => {
			const target = event.target as HTMLElement | null;
			if (!target) return;
			if (cardRef.current?.contains(target)) return;
			if (target.closest('[data-testid="quick-details-trigger"]')) return;
			if (target.closest("[data-radix-popper-content-wrapper], [role='dialog'], [role='menu']")) return;
			onDismiss();
		};
		document.addEventListener("keydown", handleKeyDown);
		document.addEventListener("pointerdown", handlePointerDown);
		return () => {
			document.removeEventListener("keydown", handleKeyDown);
			document.removeEventListener("pointerdown", handlePointerDown);
		};
	}, [isOpen, onDismiss, reserveSpace]);

	const animate = !prefersReducedMotion && motionState.animate;
	const motionMode = animate ? "animated" : "instant";

	useLayoutEffect(() => {
		if (isOpen || settledClosed) return;
		if (!animate) {
			setSettledClosed(true);
			return;
		}
		const card = cardRef.current;
		const onTransitionEnd = (event: TransitionEvent) => {
			if (event.target === card && event.propertyName === "transform") setSettledClosed(true);
		};
		card?.addEventListener("transitionend", onTransitionEnd);
		const fallbackTimer = window.setTimeout(() => setSettledClosed(true), INSPECTOR_SPRING_MS + 100);
		return () => {
			card?.removeEventListener("transitionend", onTransitionEnd);
			window.clearTimeout(fallbackTimer);
		};
	}, [animate, isOpen, settledClosed]);

	const hidden = !isOpen && settledClosed;

	return (
		<>
			<div
				aria-hidden="true"
				className="relative shrink-0"
				data-motion={motionMode}
				data-slot="quick-details-gap"
				style={{ width: isOpen && reserveSpace ? QUICK_DETAILS_RESERVE_PX : 0 }}
			/>
			<aside
				aria-hidden={hidden}
				aria-label={t("quickDetails.aria")}
				className={cn(
					"board-scrollbar absolute z-overlay overflow-x-hidden overflow-y-auto border border-border bg-[color-mix(in_oklch,var(--background)_45%,var(--card))] text-card-foreground shadow-md",
					hidden && "invisible",
				)}
				data-mode={reserveSpace ? "docked" : "floating"}
				data-motion={motionMode}
				data-settled={settledClosed ? "true" : "false"}
				data-slot="quick-details-card"
				data-state={isOpen ? "expanded" : "collapsed"}
				data-testid="panel-quick-details"
				inert={hidden}
				ref={cardRef}
				style={{
					top: QUICK_DETAILS_INSET_PX,
					right: QUICK_DETAILS_INSET_PX,
					width: QUICK_DETAILS_WIDTH_PX,
					maxHeight: `calc(100% - ${QUICK_DETAILS_INSET_PX * 2}px)`,
					borderRadius: `calc(var(--radius-md) + ${QUICK_DETAILS_INNER_PADDING_PX}px)`,
				}}
			>
				{mountedSession === sessionKey ? children : null}
			</aside>
		</>
	);
}

/** Topbar trigger button for toggling the floating Quick Details panel. */
export function SessionQuickDetailsTrigger({
	isOpen,
	onToggle,
}: {
	isOpen: boolean;
	onToggle: () => void;
}) {
	const { t } = useTranslation();

	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button
					aria-expanded={isOpen}
					aria-label={isOpen ? t("quickDetails.close") : t("quickDetails.open")}
					aria-pressed={isOpen}
					className={cn(isOpen && "bg-secondary text-foreground")}
					data-testid="quick-details-trigger"
					onClick={onToggle}
					size="icon-sm"
					style={noDragStyle}
					type="button"
					variant="quiet"
				>
					<Settings2 aria-hidden="true" className="size-icon-md" />
				</Button>
			</TooltipTrigger>
			<TooltipContent side="bottom">
				{isOpen ? t("quickDetails.close") : t("quickDetails.open")}
			</TooltipContent>
		</Tooltip>
	);
}
