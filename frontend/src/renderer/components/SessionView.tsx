import { apiErrorException } from "../lib/api-error-exception";
import { getApiBaseUrl } from "../lib/api-client";
import { baseUrlForHost } from "../lib/host-clients";
import { artifactAtPath } from "../../../../packages/shared/artifacts/deliverables";
import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, PanelRight, Plus, SquareTerminal } from "@/components/app-icons";
import { useBlocker } from "@tanstack/react-router";
import { motion } from "motion/react";
import {
	useCallback,
	useEffect,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
	type CSSProperties,
} from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { isFileViewCapture } from "../../shared/chat-file-reference";
import { BrowserPanelView, useBrowserAnnotationQueue } from "./BrowserPanel";
import { CenterPane } from "./CenterPane";
import { AgentAvatar } from "./AgentAvatar";
import { SplitCloseFocusedContext, SplitHiddenTabKeysContext } from "./split-pane-context";
import { SessionTabAgentMenuContext, SessionTabMenuButton, TabSplitMenuContext, type SessionTabAgentMenu, type TabMenuSpec, type TabSplitActions } from "./TabContextMenu";
import { useSplitPanesEnabled } from "../lib/split-panes";
import { useSessionSplitGroups, useSplitPanesStore } from "../stores/split-panes-store";
import { PANE_HEADER_PX, SplitDivider, SplitGroupTab, SplitJunction, SplitPaneHeader, SplitPaneTerminal, findJunctions, type SplitGroupMember } from "./SplitView";
import { WorkspaceEntryIcon } from "./WorkspaceEntryIcon";
import { registerPaneDropTarget, type PaneDropResolution } from "../lib/pane-drag";
import {
	containsPoint,
	edgeAt,
	expandGroupTabOrder,
	GROUP_KEY_PREFIX,
	groupContaining,
	groupTabKey,
	halfOf,
	layoutBoxes,
	layoutCss,
	leafKeys,
	newGroupId,
	placeInGroup,
	reconcileGroups,
	SESSION_TAB_KEY,
	setRatioAt,
	splitBeside,
	toClientRect,
	ungroupById,
	withoutTab,
	type ClientRect,
	type SplitSide,
	type TabGroup,
} from "../lib/pane-split";
import {
	describePaneLayout,
	latestPaneLayout,
	PaneCommandError,
	registerPaneSession,
	reportPaneLayout,
	type PaneCommandHandler,
	type PaneLayout,
	type PaneTabDescription,
} from "../lib/pane-relay";
import { useShellAgentStore } from "../stores/shell-agent-store";
import { createTerminalMux, muxUrlFromApiBase } from "../lib/terminal-mux";
import type { FileOpenOptions, FileViewMode } from "./FileContentPane";
import { SessionChatSurface } from "./chat/SessionChatSurface";
import { CHAT_EVENTS_SAFETY_POLL_MS, CloudSessionChatSurface } from "./chat/CloudSessionChatSurface";
import { useCloudOpening } from "../lib/cloud-opening";
import { ReviewerChatSurface } from "./chat/ReviewerChatSurface";
import { CloudReviewerChatSurface } from "./chat/CloudReviewerChatSurface";
import { SessionInspectorRail, initialInspectorSize, inspectorSizing, sizingGeometryEqual, INSPECTOR_SPRING_MS, INSPECTOR_SPRING_EASING, type InspectorSizing } from "./SessionInspectorRail";
import { SessionFileExplorer } from "./SessionFileExplorer";
import { FilesTopbarHostContext } from "./files-topbar-host";
import { CloudFileContentPane, CloudWorkspaceDiff } from "./CloudWorkspaceDiff";
import { CloudSessionStartupError } from "./CloudSessionStartupError";
import { CloudSessionStartupLoader } from "./CloudSessionStartupLoader";
import { SessionFileTab } from "./SessionFileTabs";
import { SessionFileWorkspace } from "./SessionFileWorkspace";
import { SessionFilesPopOut } from "./SessionFilesPopOut";
import { sessionBrowserUnavailable } from "../lib/artifact-preview";
import { SessionBrowserPopOut } from "./SessionBrowserPopOut";
import { SessionInspector } from "./SessionInspector";
import { PRTabChip, PRTabPane } from "./PRTabPane";
import { activatePRTab, closePRTab, deactivatePRTab, prTabKey, prTabUrl, usePRTabs, type PRTabsState } from "../lib/pr-tabs";
import { usePRViewEnabled } from "../lib/pr-view";
import { sessionPRDisplaySummaries } from "../lib/pr-display";
import { useSessionScmSummary } from "../hooks/useSessionScmSummary";
import type { ArtifactOpenRequest } from "./SessionArtifactsView";
import { SessionTopbarActions } from "./SessionTopbarActions";
import { captureTopbarAction } from "../lib/topbar-telemetry";
import { PageTopbar, PageTopbarTrailingAction } from "./PageTopbar";
import { SessionTopbarHost } from "./SessionTopbarPortal";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { useAgentAccountLabel } from "../hooks/useAgentAccounts";
import { useBrowserView } from "../hooks/useBrowserView";
import { useFileAnnotation } from "../hooks/useFileAnnotation";
import {
	adoptedShellHandle,
	useCloseShellTerminal,
	useOpenShellTerminal,
	useRenameShellTerminal,
	useShellTerminals,
} from "../hooks/useShellTerminals";
import { useSessionInterfaceSwitch } from "../hooks/useSessionInterfaceSwitch";
import { SESSION_WORKSPACE_ID } from "../hooks/useHeldSession";
import { canResumeAgent, resumeAgentOnOpen, resumeOpenElsewhere } from "../hooks/useCanResumeAgent";
import { trackImportFirstOpen, useImportedAgentBlock } from "../lib/session-import";
import { useSessionInterfaceTransitionStatus } from "../hooks/useSessionInterfaceTransition";
import { conversationQueryKey, conversationSettled } from "../hooks/useConversation";
import { discardCapturedPendingFileAttachments } from "../hooks/useFileAttachments";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudOffering } from "../hooks/useCloudOffering";
import { CloudCpError, type CloudCpAOReviewRun, type CloudCpClientEvent, type CloudCpSessionReviewState } from "../lib/cloud-cp";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { sessionWasSharedWithYou } from "../lib/shared-session-memory";
import {
	refetchCloudProjectsForUnknownSessions,
	refreshCloudSessionLifecycle,
	toCloudWorkspaceSession,
	useCloudSessionQuery,
	useRemoteSessionProjectKind,
	useWorkspaceQuery,
	useWorkspaceSession,
	workspaceQueryKeyForHost,
} from "../hooks/useWorkspaceQuery";
import { subscribeChatReveal } from "../lib/chat-context-bus";
import { cloudChatStartingInPlace, cloudLifecycleStage, cloudStartupProblem, isCloudPausedStage, isWorkspaceStartupError, resumeCloudSession } from "../lib/cloud-lifecycle";
import { FREESTYLE_PROVIDER, SHARE_FEATURE } from "../lib/cloud-offering";
import { useCloudSessionEventStream } from "../hooks/useCloudSessionEvents";
import { latestScriptRun } from "../lib/cloud-script-runs";
import { cloudChatEventsQueryKey, recordCloudScriptStepEvents, useCachedQueryData, useCloudScriptRuns } from "../hooks/useCloudScriptRuns";
import { useTerminalResetStore } from "../stores/terminal-reset-store";
import { useSessionSwitchStore } from "../stores/session-switch-store";
import { apiErrorCode, apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { useHostConnection } from "../hooks/useHostConnection";
import { sessionReviewsQueryKey } from "../lib/session-reviews";
import { HOST_UNREACHABLE_CODES, sessionUiKey } from "../lib/hosts";
import { resolveSessionWorkspacePath, sessionWorkspaceFilesQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import { isAbsoluteFileReference, matchWorkspaceFilePath, normalizeWorkspaceFileReference } from "../lib/workspace-file-path";
import { markFileViewerPerformance } from "../lib/file-viewer-performance";
import { filesProjectKind } from "../lib/files-review-selection";
import { aoBridge } from "../lib/bridge";
import {
	chatDraftDialogCopy,
} from "../lib/chat-draft-boundary";
import {
	activateSessionFile,
	closeSessionFile,
	EMPTY_SESSION_FILE_TABS,
	openSessionFile,
 sessionFileTabKey,
	type SessionFileTabState,
} from "../lib/session-file-tabs";
import { isMacPlatform } from "../lib/platform";
import { useShell } from "../lib/shell-context";
import { cn } from "../lib/utils";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { isOrchestratorSession, sessionAgentExited, sessionIsActive, type SessionArtifact, type WorkspaceSummary } from "../types/workspace";
import { terminalTargetBelongsToSession, type TerminalTarget } from "../types/terminal";
import { useCloudSessionPresence } from "../hooks/useCloudSessionPresence";
import { SessionPeopleControl } from "./SessionPeople";
import { SessionAccessTag } from "./teams/SessionAccessTag";
import { SharedProjectPanel } from "./SharedProjectPanel";
import { useSessionMenuAvailable } from "../hooks/useAppMenuCommand";
import { focusAfterPaint, useSessionShortcut } from "../hooks/useAppShortcut";
import { openComposerModelPicker, toggleComposerDictation } from "../lib/composer-shortcuts";
import { inspectorIsOpen, useResolvedTheme, useUiStore, type InspectorView } from "../stores/ui-store";
import { useSideChatsAvailable } from "../lib/side-chats";
import {
	INSPECTOR_SEPARATOR_RESERVE_PX,
	inspectorMaxWidthCss,
} from "../lib/inspector-width";

type CenterFileOpenRequest = { path?: string; source?: FileOpenOptions["source"]; annotationRange?: FileOpenOptions["annotationRange"]; previousPath?: string; commitSha?: string; editing: boolean; key: number; line?: number; mode: FileViewMode; scope?: FileOpenOptions["scope"] };
const EMPTY_AUXILIARY_TAB_ORDER: string[] = [];
// Centre-file open requests take keys from this process-wide counter, not a
// per-mount one: the display mode remembered for a request (ui-store) outlives a
// SessionView remount, so a new request must never reuse a recorded key.
let lastCenterFileRequestKey = 0;
function nextCenterFileRequestKey(): number {
	lastCenterFileRequestKey += 1;
	return lastCenterFileRequestKey;
}
// The inspector tab labels respond to the tablist's remaining width. The
// 239px tablist breakpoint plus the 76px pinned-action reserve and 10px leading
// inset gives a 325px inspector breakpoint for the animation lock.
const INSPECTOR_COMPACT_MAX_PX = 325;
const TOPBAR_SECONDARY_COMPACT_MAX_PX = 759;
const isMac = isMacPlatform();
const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as CSSProperties) : undefined;

type ReviewsResponse = components["schemas"]["ListReviewsResponse"];
type ReviewerSurface = components["schemas"]["DomainReviewerSurface"];
type ReviewerTerminalTarget = { handleId: string; harness: string };
type ReviewerChatTarget = { reviewId: string; harness: string };

type BrowserPopOutPhase = "docked" | "mounting" | "open";
type BrowserPopOutState = {
	sessionId: string;
	phase: BrowserPopOutPhase;
};

function topbarSecondaryLabelMode(width: number): "compact" | "expanded" {
	return width <= TOPBAR_SECONDARY_COMPACT_MAX_PX ? "compact" : "expanded";
}

function previewRevealKey(previewUrl?: string, previewRevision?: number): string {
	const target = previewUrl?.trim();
	if (!target) return "";
	if (typeof previewRevision === "number") return `revision:${previewRevision}`;
	return `url:${target}`;
}

const BROWSER_REVEAL_ACTIONS = new Set(["open", "tab-new"]);

function browserIsVisible(sessionId: string, browserPoppedOut: boolean): boolean {
	if (browserPoppedOut) return true;
	const { inspectorSessions } = useUiStore.getState();
	return inspectorIsOpen(inspectorSessions, sessionId) && (inspectorSessions[sessionId]?.view ?? "summary") === "browser";
}

// workingReviewerSurface is the live reviewer to show when the selected one is
// idle but another is working (an agent asked a different reviewer). The
// selected reviewer fields keep meaning the session's selected reviewer.
function workingReviewerSurface(data?: ReviewsResponse): ReviewerSurface | undefined {
	const active = data?.activeReviewers ?? [];
	if (active.some((surface) => surface.reviewId === data?.reviewerSurface?.reviewId)) return undefined;
	return active[0];
}

function reviewerTerminalFromReviews(data?: ReviewsResponse, selected?: TerminalTarget): ReviewerTerminalTarget | undefined {
	// Several reviewers can run on one worker at once. The reviewer tab follows
	// whichever live reviewer the user opened (from the inspector), and
	// otherwise shows the selected reviewer, or the working one when it is idle.
	if (selected?.kind === "reviewer") {
		const opened = data?.activeReviewers?.find((surface) => surface.mode !== "chat" && surface.handleId === selected.handleId);
		if (opened?.handleId) return { handleId: opened.handleId, harness: opened.harness || selected.harness };
	}
	const working = workingReviewerSurface(data);
	if (working) return working.mode !== "chat" && working.handleId ? { handleId: working.handleId, harness: working.harness || "codex" } : undefined;
	if (data?.reviewerSurface?.mode === "chat") return undefined;
	const handleId = data?.reviewerHandleId?.trim();
	if (!handleId) return undefined;
	const latest = data?.reviews?.find((review) => review.latestRun)?.latestRun;
	return { handleId, harness: data?.reviewerHarness || latest?.harness || "codex" };
}

function cloudReviewRunForTerminal(
	data: CloudCpSessionReviewState | undefined,
	terminalID: string,
): CloudCpAOReviewRun | undefined {
	return data?.runs.find((run) => run.reviewerTerminalId === terminalID);
}

function cloudReviewerChatFromReviews(data: CloudCpSessionReviewState | undefined): ReviewerChatTarget | undefined {
	const surface = data?.reviewerSurface;
	return surface?.mode === "chat" && surface.reviewId ? { reviewId: surface.reviewId, harness: surface.harness || "claude-code" } : undefined;
}

function reviewerChatFromReviews(data?: ReviewsResponse): ReviewerChatTarget | undefined {
	const surface = workingReviewerSurface(data) ?? data?.reviewerSurface;
	if (surface?.mode !== "chat" || !surface.reviewId) return undefined;
	return { reviewId: surface.reviewId, harness: surface.harness || "codex" };
}

type SessionViewProps = {
	sessionId: string;
	cloudOrgId?: string;
	projectId?: string;
	hostId?: string;
};

// The session detail screen: terminal + git rail, with shared tab-strip chrome
// and session actions inside the center panel on every desktop platform.
// Rendered by both the project-scoped and cross-project session routes.
// The persistent shell cache owns terminal lifetime by logical session + handle:
// route switches retain the xterm instance and latest output, while a replacement
// handle gets a clean xterm/mux binding.
//
// The inspector uses the same Motion spring as the left sidebar (gap width +
// x-transform). Summary/Reviews/Files share a utility width, while Browser
// automatically grows into a co-work canvas. Chat readability clamps either
// profile before the conversation can become unusably narrow.
// Startup steps: 0 creating the workspace, 1 connecting to the worker,
// 2 preparing the repository and agent, 3 connecting the terminal. The
// workspace exists once the sandbox reaches "bootstrapping" (the provider
// reports it running and AO is starting its worker inside); "requested" and
// "provisioning" are still creating it.
function cloudStartupStage(observedState: string | undefined, workerConnected: boolean, terminalOnly = false): number {
	return terminalOnly ? 3
		: workerConnected && (observedState === "bootstrapping" || observedState === "running") ? 2
		: observedState === "bootstrapping" || observedState === "running" ? 1
		: 0;
}

/** How often the startup screen reads the session's events while their stream is down or still opening. */
const STARTUP_EVENTS_POLL_MS = 2_000;

const CHAT_VIEW_LEASE_MS = 30_000;

/**
 * The furthest startup step a session's events confirm, with when it was
 * reached. Events from before the cutoff are an earlier epoch's: one can hold
 * agent.ready long before this workspace restarted.
 */
function startupEventProgress(events: readonly CloudCpClientEvent[], cutoff: number): { index: number; since: string } | undefined {
	let latest: { index: number; since: string } | undefined;
	for (const event of events) {
		const occurredAt = Date.parse(event.createdAt);
		if (!Number.isFinite(occurredAt) || occurredAt < cutoff) continue;
		const index = event.type === "worker.connected" || event.type === "worker.ready" ? 2
			: event.type === "agent.ready" ? 3
			: undefined;
		if (index !== undefined) latest = { index, since: event.createdAt };
	}
	return latest;
}

function CloudSessionLifecycleLoader({ sessionId, orgId, createdAt, observedState, workerConnected, terminalOnly, completed = false, startupNote, coderWorkspaceUrl, eventsFromChat = false, queuedMessage }: {
	sessionId: string; orgId: string; createdAt?: string; observedState?: string; workerConnected: boolean; terminalOnly: boolean; completed?: boolean; startupNote?: string; coderWorkspaceUrl?: string; queuedMessage?: string;
	/**
	 * The session's events are the chat's to read: its Chat surface is mounted
	 * under this screen (it reads them as it opens, then polls only as a safety
	 * net while the stream is up), or the session has not resolved yet and may
	 * be one. The steps then follow the chat's list rather than a poll here.
	 */
	eventsFromChat?: boolean;
}) {
	const { baseUrl, client } = useCloudCp();
	const queryClient = useQueryClient();
	// The project's Setup log, fed by this loader's own stream and poll below,
	// or read from the chat's own list under a Chat session.
	const scriptRuns = useCloudScriptRuns(orgId || undefined, sessionId, { fetch: false });
	const latestSetup = terminalOnly ? undefined : latestScriptRun(scriptRuns, "setup");
	const setupRun = latestSetup?.status === "running" || latestSetup?.status === "failed" ? latestSetup : undefined;
	const factIndex = cloudStartupStage(observedState, workerConnected, terminalOnly);
	const [factProgress, setFactProgress] = useState({ index: factIndex, since: createdAt ?? new Date().toISOString() });
	const [remoteProgress, setRemoteProgress] = useState({ index: factIndex, since: createdAt ?? new Date().toISOString() });
	const [progress, setProgress] = useState({ index: terminalOnly ? 3 : 0, since: createdAt ?? new Date().toISOString() });
	const replayCutoff = useRef(Date.now() - 60 * 60 * 1_000);
	// The loader comes down once the sessions list says the worker connected.
	// Its own stream and poll learn that first, and refresh the list then
	// instead of leaving the loader up until the list's next poll.
	const listedConnected = useRef(workerConnected);
	listedConnected.current = workerConnected;
	const mountedAt = useRef(Date.now());
	// The loader's own poll learns the workspace link as soon as the workspace
	// exists, ahead of the session list's slower refresh.
	const [polledWorkspaceUrl, setPolledWorkspaceUrl] = useState<string>();
	// The session's shared event stream, from its start: the Setup log and the
	// stages need every event.
	const streamOpen = useCloudSessionEventStream({
		baseUrl,
		orgId,
		sessionId,
		enabled: !terminalOnly,
		resumeAfter: () => 0,
		onEvent: (event) => {
			if (event.sessionId === sessionId) recordCloudScriptStepEvents(queryClient, orgId, sessionId, [event]);
			const occurredAt = Date.parse(event.createdAt);
			if (event.sessionId !== sessionId || !Number.isFinite(occurredAt) || occurredAt < replayCutoff.current) return;
			// sandbox.provisioning only marks the start of workspace creation;
			// the workspace's completion comes from the observed state above.
			const index = event.type === "worker.connected" || event.type === "worker.ready" ? 2
				: event.type === "agent.ready" ? 3
				: undefined;
			if (index !== undefined) setProgress((current) => index > current.index ? { index, since: event.createdAt } : current);
			// A connect from before this loader (allowing for clock skew) is an
			// earlier worker's, not the one it waits for.
			if (index === 2 && !listedConnected.current && occurredAt >= mountedAt.current - 5_000) void refreshCloudSessionLifecycle(queryClient);
		},
	});
	useEffect(() => {
		setFactProgress((current) => current.index === factIndex ? current : { index: factIndex, since: new Date().toISOString() });
	}, [factIndex]);
	useEffect(() => {
		if (!orgId || terminalOnly) return;
		const controller = new AbortController();
		let timer: number | undefined;
		const poll = async () => {
			try {
				const { session } = await client.getSession(orgId, sessionId, { signal: controller.signal });
				if (controller.signal.aborted) return;
				refetchCloudProjectsForUnknownSessions(queryClient, baseUrl, orgId, [session]);
				if (session.runtimeConnected && !listedConnected.current) void refreshCloudSessionLifecycle(queryClient);
				const index = cloudStartupStage(session.observedState, session.runtimeConnected);
				setPolledWorkspaceUrl(session.coderWorkspaceUrl);
				setRemoteProgress((current) => current.index === index ? current : { index, since: new Date().toISOString() });
			} catch {
				// Keep the last confirmed stage and retry while the loader is visible.
			} finally {
				if (!controller.signal.aborted) timer = window.setTimeout(() => void poll(), 2_000);
			}
		};
		void poll();
		return () => {
			controller.abort();
			if (timer !== undefined) window.clearTimeout(timer);
		};
	}, [baseUrl, client, orgId, queryClient, sessionId, terminalOnly]);
	// Under a Chat session the steps follow the chat's own list of the events.
	const chatEvents = useCachedQueryData<CloudCpClientEvent[]>(cloudChatEventsQueryKey(orgId, sessionId), Boolean(orgId) && !terminalOnly && eventsFromChat);
	useEffect(() => {
		const latest = chatEvents && startupEventProgress(chatEvents, replayCutoff.current);
		if (latest) setProgress((current) => latest.index > current.index ? latest : current);
	}, [chatEvents]);
	// Otherwise this screen reads them: once as it opens, then, while the stream
	// carries them, only as the chat's safety net does, and every two seconds
	// only while the stream is down. A boot takes minutes.
	const streamOpenRef = useRef(streamOpen);
	streamOpenRef.current = streamOpen;
	const pollEvents = Boolean(orgId) && !terminalOnly && !eventsFromChat;
	useEffect(() => {
		if (!pollEvents) return;
		const controller = new AbortController();
		let after = 0;
		let readAt: number | undefined;
		let timer: number | undefined;
		const poll = async () => {
			try {
				const interval = streamOpenRef.current ? CHAT_EVENTS_SAFETY_POLL_MS : STARTUP_EVENTS_POLL_MS;
				if (readAt !== undefined && Date.now() - readAt < interval) return;
				readAt = Date.now();
				const events: CloudCpClientEvent[] = [];
				for (;;) {
					const page = await client.listChatEvents(orgId, sessionId, { after, limit: 500 }, { signal: controller.signal });
					if (controller.signal.aborted) return;
					recordCloudScriptStepEvents(queryClient, orgId, sessionId, page.events);
					events.push(...page.events);
					after = page.nextAfter;
					if (!page.hasMore) break;
				}
				// The whole replay is read before a stage is painted.
				const latest = startupEventProgress(events, replayCutoff.current);
				if (latest) setProgress((current) => latest.index > current.index ? latest : current);
			} catch {
				// Keep the current stage and retry while the session is loading.
			} finally {
				// Checked every two seconds, so a stream that drops is noticed soon.
				if (!controller.signal.aborted) timer = window.setTimeout(() => void poll(), STARTUP_EVENTS_POLL_MS);
			}
		};
		void poll();
		return () => {
			controller.abort();
			if (timer !== undefined) window.clearTimeout(timer);
		};
	}, [client, orgId, pollEvents, queryClient, sessionId]);
	const confirmedFacts = remoteProgress.index > factProgress.index ? remoteProgress : factProgress;
	const target = confirmedFacts.index > progress.index ? confirmedFacts : progress;
	return <CloudSessionStartupLoader activeIndex={target.index} completed={completed} startupNote={startupNote} coderWorkspaceUrl={polledWorkspaceUrl ?? coderWorkspaceUrl} setupRun={setupRun} queuedMessage={terminalOnly ? undefined : queuedMessage} />;
}

function CloudInterfaceSwitchLoader({ target }: { target: "chat" | "tui" }) {
	const label = `Switching to ${target === "chat" ? "Chat UI" : "Terminal UI"}`;
	return (
		<div className="absolute inset-0 z-chrome grid place-items-center bg-background" data-testid="cloud-interface-switch-loader-screen" data-startup-cover="">
			<div role="status" aria-live="polite" aria-label={label} className="flex flex-col items-center gap-3 text-muted-foreground">
				<Loader2 aria-hidden="true" className="size-6 animate-spin" />
				<span className="text-sm">{label}</span>
			</div>
		</div>
	);
}

/** Paused-sandbox chip. Names Coder only for a Coder session; any other provider reads "Paused". */
function CloudPausedStatus({ stage }: { stage: "paused" | "paused_by_coder" }) {
	const { t } = useTranslation();
	return (
		<motion.div
			animate={{ opacity: 1, y: 0 }}
			aria-live="polite"
			className={cn(
				"absolute right-3 top-3 z-20 flex h-7 items-center gap-2 rounded-sm border px-2.5",
				"bg-background/92 font-mono text-[11px] tracking-tight shadow-sm backdrop-blur-sm",
				"border-border/80 text-foreground",
			)}
			data-cloud-lifecycle-stage={stage}
			initial={{ opacity: 0, y: -4 }}
			role="status"
		>
			<span
				aria-hidden="true"
				className="size-1.5 rounded-full bg-warning"
			/>
			{stage === "paused_by_coder" ? t("cloud.lifecycle.pausedByCoder") : t("cloud.lifecycle.paused")}
		</motion.div>
	);
}

export function SessionView({ sessionId, cloudOrgId, projectId, hostId }: SessionViewProps) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const uiSessionId = sessionUiKey(sessionId, hostId);
	const sessionTopbarRoute = useMemo(() => ({ hostId, projectId, sessionId }), [hostId, projectId, sessionId]);
	const { baseUrl: remoteBase, label: hostLabel } = useHostConnection(hostId);
	const refreshWorkspaces = useCallback(
		() => queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) }),
		[hostId, queryClient],
	);
	const workspaceQuery = useWorkspaceQuery({ enabled: !hostId });
	const remoteSessionQuery = useWorkspaceSession(sessionId, hostId, false);
	const workspaces = hostId ? [] : workspaceQuery.data ?? [];
	const routedWorkspaces = projectId ? workspaces.filter((workspace) => workspace.id === projectId) : [];
	const listedMatches = (projectId ? routedWorkspaces : workspaces.filter((workspace) => workspace.kind !== "cloud"))
		.flatMap((workspace) => workspace.sessions.filter((candidate) => candidate.id === sessionId));
	const listedSession = listedMatches.length === 1 ? listedMatches[0] : undefined;
	const routedWorkspace = projectId
		? routedWorkspaces.find((workspace) => listedSession && workspace.sessions.includes(listedSession)) ??
			(routedWorkspaces.length === 1 ? routedWorkspaces[0] : undefined)
		: undefined;
	const ambiguousRoute = listedMatches.length > 1 || Boolean(projectId && routedWorkspaces.length > 1 && !listedSession);
	const isCloudRoute = routedWorkspace?.kind === "cloud";
	// The unscoped /sessions/:id route is local; Cloud sessions have project routes.
	// Newly-created Cloud sessions can be routed before the list cache refreshes.
	const cloudLookupEnabled = Boolean(cloudOrgId && projectId && !ambiguousRoute && (isCloudRoute || (!routedWorkspace && !listedSession)));
	// A project shared from another organization is looked up in that one.
	const routeCloudOrgId = routedWorkspace?.cloudOrgId ?? cloudOrgId;
	const cloudRouteSession = useCloudSessionQuery(
		routeCloudOrgId,
		sessionId,
		cloudLookupEnabled,
		!listedSession,
	);
	// The person's own workspace: a missing session in another one was shared
	// with them, so it is gone because it no longer is.
	const { org: ownCloudOrg } = useCloudOrg(Boolean(routeCloudOrgId) && !hostId);
	const localLookupEnabled = !ambiguousRoute && !isCloudRoute && (
		!cloudOrgId || !projectId || Boolean(listedSession && !listedSession.cloud) ||
		Boolean(routedWorkspace) || cloudRouteSession.isError ||
		Boolean(projectId && cloudRouteSession.data && cloudRouteSession.data.projectId !== projectId)
	);
	const workspaceSessionQuery = useWorkspaceSession(sessionId, undefined, !hostId && localLookupEnabled);
	const directCloudWorkspace = cloudRouteSession.data
		? workspaces.find((workspace) => workspace.kind === "cloud" && workspace.id === cloudRouteSession.data?.projectId)
		: undefined;
	const cloudSessionWorkspace = directCloudWorkspace ?? (isCloudRoute ? routedWorkspace : undefined);
	const directCloudSession = routeCloudOrgId && cloudRouteSession.data && (!routedWorkspace || isCloudRoute) &&
		projectId === cloudRouteSession.data.projectId
		? toCloudWorkspaceSession(cloudRouteSession.data, {
			id: cloudSessionWorkspace?.id ?? cloudRouteSession.data.projectId,
			displayName: cloudSessionWorkspace?.name ?? "Cloud project",
		}, routeCloudOrgId)
		: undefined;
	const fallbackSession = !projectId || workspaceSessionQuery.data?.workspaceId === projectId
		? workspaceSessionQuery.data : undefined;
	const scopedFallback = routedWorkspace && fallbackSession && Boolean(fallbackSession.cloud) !== isCloudRoute
		? undefined : fallbackSession;
	// What a host showed last stays until a read replaces it, also across a failed
	// read or a reconnect; only a host that refuses this device takes it away.
	const remoteCode = hostId && remoteSessionQuery.error ? apiErrorCode(remoteSessionQuery.error) : undefined;
	const hostUnreachable = Boolean(hostId) && (!remoteBase || HOST_UNREACHABLE_CODES.has(remoteCode));
	const hostRefused = remoteCode === "BAD_PASSWORD" || remoteCode === "HOST_API_INCOMPATIBLE";
	const session = hostId ? (!hostRefused && remoteSessionQuery.data?.workspaceId === (projectId ?? remoteSessionQuery.data?.workspaceId) ? remoteSessionQuery.data : undefined)
		: ambiguousRoute ? undefined : listedSession ?? directCloudSession ?? scopedFallback;
	// The Files review offers only what the session's kind of project has.
	const remoteWorkspaceKind = useRemoteSessionProjectKind(sessionId, hostId);
	const sessionWorkspace = hostId ? undefined : workspaces.find((workspace) => workspace.id === session?.workspaceId);
	const sessionWorkspaceKind = hostId ? remoteWorkspaceKind : sessionWorkspace?.kind;
	const filesKind = session ? filesProjectKind(session, sessionWorkspaceKind) : undefined;
	const interfaceContext = hostId ?? (session ? session.cloud ?? null : cloudOrgId && projectId ? { orgId: cloudOrgId } : undefined);
	const interfaceUi = useSessionInterfaceSwitch(sessionId, session, interfaceContext);
	const accountLabel = useAgentAccountLabel(Boolean(interfaceUi.accountMoveTarget), hostId);
	const accountSwitchingLabel = interfaceUi.accountMoveTarget
		? t("settings.agentAccounts.switching", { email: accountLabel(interfaceUi.accountMoveTarget) })
		: undefined;
	// Reading a cloud session counts as using it, so it is not idle-paused
	// under someone who is watching the agent work.
	useCloudSessionPresence(session?.cloud?.orgId, session?.cloud ? session.id : undefined, Boolean(session?.cloud));
	const { draftBoundaries: chatDraftBoundaries, confirmUnsafeDraftLeave } = interfaceUi;
	const remoteHostsEnabled = useUiStore((state) => state.remoteHosts);
	const setRemoteHosts = useUiStore((state) => state.setRemoteHosts);
	useBlocker({
		disabled: chatDraftBoundaries.length === 0,
		enableBeforeUnload: chatDraftBoundaries.length > 0,
		shouldBlockFn: async () => {
			const decision = await confirmUnsafeDraftLeave();
			if (decision.kind === "cancelled") {
				if (hostId && !remoteHostsEnabled) setRemoteHosts(true);
				return true;
			}
			if (decision.kind === "confirmed") {
				// Route navigation is the boundary itself, so confirmed in-flight file
				// work can be invalidated now. Interface switches defer this until the
				// exact durable transition reports completed.
				discardCapturedPendingFileAttachments(decision.pendingAttachments);
			}
			return false;
		},
	});
	useEffect(() => {
		aoBridge.app.setChatDraftRisk?.(chatDraftBoundaries, chatDraftDialogCopy(chatDraftBoundaries));
	}, [chatDraftBoundaries, t]);
	useEffect(
		() => () => aoBridge.app.setChatDraftRisk?.([]),
		[sessionId],
	);
	const { client: cloudCpClient, ready: cloudCpReady, baseUrl: cloudCpBaseUrl, userId: cloudCpUserId } = useCloudCp();
	// The session's own organization decides whether it can be shared (feature=share).
	const sessionOffering = useCloudOffering(Boolean(session?.cloud), session?.cloud?.orgId);
	const theme = useResolvedTheme();
	const browserOnly = Boolean(session && isOrchestratorSession(session));
	const isInspectorOpen = useUiStore((state) => inspectorIsOpen(state.inspectorSessions, uiSessionId));
	const [sideChatContainer, setSideChatContainer] = useState<HTMLDivElement | null>(null);
	const sideChatsAvailable = useSideChatsAvailable(session, hostId);
	const inspectorView = useUiStore((state) => state.inspectorSessions[uiSessionId]?.view ?? "summary");
	const browserUnseen = useUiStore((state) => Boolean(state.inspectorSessions[uiSessionId]?.browserUnseen));
	const setInspectorOpenForSession = useUiStore((state) => state.setInspectorOpen);
	const toggleInspector = useUiStore((state) => state.toggleInspector);
	const setInspectorViewForSession = useUiStore((state) => state.setInspectorView);
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const setFilesChangedOnly = useUiStore((state) => state.setFilesChangedOnly);
	const resetFilesSelection = useUiStore((state) => state.resetFilesSelection);
	const workspaceFileOpenRequest = useUiStore((state) => state.workspaceFileOpenRequest);
	const clearWorkspaceFileOpenRequest = useUiStore((state) => state.clearWorkspaceFileOpenRequest);
	const initializeInspectorSession = useUiStore((state) => state.initializeInspectorSession);
	const setBrowserContentRevealed = useUiStore((state) => state.setBrowserContentRevealed);
	const setBrowserUnseen = useUiStore((state) => state.setBrowserUnseen);
	const setArtifactsUnseen = useUiStore((state) => state.setArtifactsUnseen);
	const { daemonStatus } = useShell();
	const resumeStatus = useSessionInterfaceTransitionStatus(canResumeAgent(session) ? session?.id : undefined, hostId);
	const canResume = canResumeAgent(session, resumeStatus.transition) && !resumeStatus.isLoading && !resumeStatus.statusError;
	const importBlock = useImportedAgentBlock(session, hostId);
	const openedSession = useRef({ key: uiSessionId, checked: false });
	const autoResume = useMutation({
		mutationKey: ["resume-agent", "local", sessionId],
		mutationFn: resumeAgentOnOpen,
		onSettled: async (_data, _error, id) => {
			await Promise.all([
				refreshWorkspaces(),
				queryClient.invalidateQueries({ queryKey: conversationQueryKey(id) }),
			]);
		},
	});
	// True while a background resume owns the chat surface: hide the stopped
	// banner and disable sending, but keep history readable. Covers the initial
	// check (before the mutation fires) and the mutation in flight. Once checked
	// is set, only the in-flight mutation keeps it true — switching back to an
	// already-resumed session no longer flashes "Resuming agent…" while the
	// workspace query catches up with the new activity state.
	const quietResume = (autoResume.variables === sessionId && autoResume.isPending) ||
		(!usesPreviewWorkspaceData && !hostId && canResumeAgent(session, resumeStatus.transition) &&
		!resumeStatus.statusError && !importBlock.block && (openedSession.current.key !== uiSessionId || !openedSession.current.checked));
	const resumeOnOpen = autoResume.mutate;
	// This session's background resume failed; the chat shows why.
	const autoResumeFailed = !hostId && autoResume.variables === sessionId && autoResume.isError;
	useEffect(() => {
		if (openedSession.current.key !== uiSessionId) openedSession.current = { key: uiSessionId, checked: false };
		if (usesPreviewWorkspaceData || hostId || session?.cloud || !session || daemonStatus.state !== "ready" ||
			(session.statusReadiness && session.statusReadiness !== "ready") || openedSession.current.checked) return;
		if (!session.needsResume && !sessionAgentExited(session)) {
			openedSession.current.checked = true;
			return;
		}
		if (!canResume || importBlock.block || importBlock.checking) return;
		// One attempt per route opening; a failed resume or a later agent exit
		// stays stopped rather than entering an automatic restart loop.
		openedSession.current.checked = true;
		if (session.needsResume) trackImportFirstOpen(session.provider, resumeAgentOnOpen(session.id));
		resumeOnOpen(session.id);
	}, [canResume, daemonStatus.state, hostId, importBlock.block, importBlock.checking, resumeOnOpen, session, uiSessionId]);
	const previewBaselineRef = useRef<{ sessionId: string; key: string } | null>(null);
	const revealedBrowserCommandsRef = useRef<{ sessionId: string; commands: Set<string> }>({ sessionId: uiSessionId, commands: new Set() });
	const sessionSplitRef = useRef<HTMLDivElement | null>(null);
	const terminalLiveResizeTimerRef = useRef<number | null>(null);
	const workspaceResizeTimerRef = useRef<number | null>(null);
	const [inspectorSettledClosed, setInspectorSettledClosed] = useState(!isInspectorOpen);
	const inspectorPanelVisible = isInspectorOpen || !inspectorSettledClosed;
	const [terminalTarget, setTerminalTarget] = useState<TerminalTarget>({ kind: "worker" });
	const [reviewerChatId, setReviewerChatId] = useState<string | null>(null);
	const [browserPopOutState, setBrowserPopOutState] = useState<BrowserPopOutState>({
		sessionId,
		phase: "docked",
	});
	const [filesPoppedOut, setFilesPoppedOut] = useState(false);
	const [filesSplit, setFilesSplit] = useState(() => window.localStorage.getItem("ao.files.diffStyle") === "split");
	const [artifactRequestsBySession, setArtifactRequestsBySession] = useState<Record<string, ArtifactOpenRequest>>({});
	const [fileTabsBySession, setFileTabsBySession] = useState<Record<string, SessionFileTabState>>({});
	const fileTabs = fileTabsBySession[uiSessionId] ?? EMPTY_SESSION_FILE_TABS;
	// PRs opened from the inspector's card, as tabs beside the chat and file tabs.
	// They belong to the PR view, a Developer mode preview: off, none show.
	const prView = usePRViewEnabled();
	const prTabState = usePRTabs(uiSessionId);
	const prTabs = prView ? prTabState : NO_PR_TABS;
	const activePRUrl = prTabs.active ?? undefined;
	// A PR that left the session (moved to another session) takes its tab with it.
	// The PRs are the ones the inspector shows, its summary as well as the
	// session's facts, so a PR the summary found first keeps the tab it opened.
	// Nothing closes until the summary has loaded.
	const prSummaryQuery = useSessionScmSummary(session?.id, prView && Boolean(session) && !session?.cloud, undefined, false, hostId);
	const prSummaries = prSummaryQuery.data?.prs;
	const sessionPRUrls = session && prSummaries ? sessionPRDisplaySummaries(session, prSummaries).map((pr) => pr.url).join("\n") : undefined;
	useEffect(() => {
		if (sessionPRUrls === undefined) return;
		const urls = new Set(sessionPRUrls.split("\n"));
		for (const url of prTabs.open) if (!urls.has(url)) closePRTab(uiSessionId, url);
	}, [prTabs.open, sessionPRUrls, uiSessionId]);
	const [dirtyFilesBySession, setDirtyFilesBySession] = useState<Record<string, Record<string, true>>>({});
	const dirtyFiles = dirtyFilesBySession[uiSessionId] ?? {};
	const [centerFileRequestsBySession, setCenterFileRequestsBySession] = useState<
		Record<string, Record<string, CenterFileOpenRequest>>
	>({});
	const consumedCenterEditingRequestsRef = useRef(new Set<string>());
	const consumedCenterLineRequestsRef = useRef(new Set<string>());
	const activeCenterFileRequest = fileTabs.activePath
		? centerFileRequestsBySession[uiSessionId]?.[fileTabs.activePath]
		: undefined;
	const activeCenterFileRequestToken = fileTabs.activePath && activeCenterFileRequest
		? `${uiSessionId}:${fileTabs.activePath}:${activeCenterFileRequest.key}`
		: undefined;
	const activeCenterFileInitialEditing = Boolean(
		activeCenterFileRequest?.editing
		&& activeCenterFileRequestToken
		&& !consumedCenterEditingRequestsRef.current.has(activeCenterFileRequestToken),
	);
	const activeCenterFileInitialLine = activeCenterFileRequest?.line != null
		&& activeCenterFileRequestToken
		&& !consumedCenterLineRequestsRef.current.has(activeCenterFileRequestToken)
		? activeCenterFileRequest.line
		: undefined;
	const [auxiliaryTabOrderBySession, setAuxiliaryTabOrderBySession] = useState<Record<string, string[]>>({});
	const auxiliaryTabOrder = auxiliaryTabOrderBySession[uiSessionId] ?? EMPTY_AUXILIARY_TAB_ORDER;
	const setAuxiliaryTabOrder = useCallback(
		(nextOrder: string[]) => {
			setAuxiliaryTabOrderBySession((current) => {
				const currentOrder = current[uiSessionId] ?? [];
				const visibleKeys = new Set(nextOrder);
				let nextIndex = 0;
				const mergedOrder = currentOrder.map((key) =>
					visibleKeys.has(key) ? nextOrder[nextIndex++]! : key,
				);
				while (nextIndex < nextOrder.length) mergedOrder.push(nextOrder[nextIndex++]!);
				return { ...current, [uiSessionId]: mergedOrder };
			});
		},
		[uiSessionId],
	);
	const removeAuxiliaryTab = useCallback(
		(key: string) => {
			setAuxiliaryTabOrderBySession((current) => {
				const currentOrder = current[uiSessionId];
				if (!currentOrder?.includes(key)) return current;
				const nextOrder = currentOrder.filter((candidate) => candidate !== key);
				if (nextOrder.length === 0) {
					const { [uiSessionId]: _removed, ...rest } = current;
					return rest;
				}
				return { ...current, [uiSessionId]: nextOrder };
			});
		},
		[uiSessionId],
	);
	const browserPopOutPhase = browserPopOutState.sessionId === sessionId ? browserPopOutState.phase : "docked";
	const browserPopOutMounted = browserPopOutPhase !== "docked";
	const browserPoppedOut = browserPopOutPhase === "open";
	const [browserPopoutTopbarHost, setBrowserPopoutTopbarHost] = useState<HTMLDivElement | null>(null);
	useLayoutEffect(() => {
		if (browserPopOutPhase !== "mounting" || !browserPopoutTopbarHost) return;
		// Establish the portal destination before moving the browser. This layout
		// effect completes before paint, so the user sees one atomic geometry change
		// and the address/tabs never spend a frame underneath the native view.
		setBrowserPopOutState({ sessionId, phase: "open" });
	}, [browserPopOutPhase, browserPopoutTopbarHost, sessionId]);
	// One SessionView serves every local session, so session-scoped UI state must
	// reset on a switch. Reset during render, not in an effect: children must never
	// mount (or fetch) against the previous session's reviewer, popout, or dialog.
	const [stateSessionId, setStateSessionId] = useState(sessionId);
	if (stateSessionId !== sessionId) {
		setStateSessionId(sessionId);
		setTerminalTarget({ kind: "worker" });
		setReviewerChatId(null);
		setBrowserPopOutState({ sessionId, phase: "docked" });
		setFilesPoppedOut(false);
	}
	const stopTerminalLiveResize = useCallback(() => {
		if (terminalLiveResizeTimerRef.current !== null) {
			window.clearTimeout(terminalLiveResizeTimerRef.current);
			terminalLiveResizeTimerRef.current = null;
		}
		sessionSplitRef.current?.removeAttribute("data-terminal-live-resize");
		sessionSplitRef.current?.removeAttribute("data-inspector-label-mode");
		sessionSplitRef.current?.removeAttribute("data-topbar-secondary-label-mode");
	}, []);
	const startTerminalLiveResize = useCallback(
		(labelMode: "compact" | "expanded", topbarLabelMode: "compact" | "expanded") => {
			const split = sessionSplitRef.current;
			if (!split) return;
			if (terminalLiveResizeTimerRef.current !== null) {
				window.clearTimeout(terminalLiveResizeTimerRef.current);
			}
			split.setAttribute("data-terminal-live-resize", "true");
			split.setAttribute("data-inspector-label-mode", labelMode);
			split.setAttribute("data-topbar-secondary-label-mode", topbarLabelMode);
			terminalLiveResizeTimerRef.current = window.setTimeout(() => {
				split.removeAttribute("data-terminal-live-resize");
				split.removeAttribute("data-inspector-label-mode");
				split.removeAttribute("data-topbar-secondary-label-mode");
				terminalLiveResizeTimerRef.current = null;
			}, INSPECTOR_SPRING_MS);
		},
		[],
	);

	useEffect(() => stopTerminalLiveResize, [stopTerminalLiveResize]);

	const cloudStage = cloudLifecycleStage(session);
	// The first message this device sent to a new Chat session. The startup
	// screen shows it while it covers the chat the message went to; the session
	// may not have resolved yet, so the interface comes from the send.
	const cloudOpening = useCloudOpening(session?.cloud?.orgId ?? cloudOrgId, sessionId);
	const startupQueuedMessage = cloudOpening && (session ? session.mode === "chat" : cloudOpening.interfaceMode === "chat")
		? cloudOpening.text
		: undefined;
	// A startup failure AO gave up on (or a runtime that ended without one)
	// replaces the pane; while AO still retries, the loader carries the reason.
	const startupProblem = cloudStartupProblem(session);
	const startupFailure = startupProblem && startupProblem.phase !== "retrying" ? startupProblem : undefined;
	// Set by a restore and cleared only by the session's terminal pane once the
	// fresh worker's terminal exists, so it means anything only while that pane
	// can mount: a Chat-mode session never mounts one, and would otherwise keep
	// the "Connecting your terminal" loader up for good. Its own lifecycle stage
	// still raises the loader while the restored workspace comes up.
	const cloudReconnectRequested = useTerminalResetStore((state) => Boolean(state.reconnecting[sessionId]));
	const [terminalAttachment, setTerminalAttachment] = useState({ sessionId: "", attached: false });
	const terminalAttached = terminalAttachment.sessionId === sessionId && terminalAttachment.attached;
	const onSessionTerminalAttached = useCallback((attached: boolean) => {
		setTerminalAttachment((current) => current.sessionId === sessionId && current.attached === attached
			? current
			: { sessionId, attached });
	}, [sessionId]);
	// Latch the session that has reached "connected" at least once (keyed on
	// sessionId so it resets cleanly when the view switches sessions). After the
	// first successful connect, a transient runtime-connection drop while the
	// sandbox is still running (stage flips to "restoring_agent", e.g. the worker
	// relay row cycles mid-turn) or a terminal reconnect must NOT re-raise the
	// full-screen lifecycle loader over the terminal for the rest of the turn.
	// Only a genuine workspace (re)start — VM stopped/resuming/provisioning/
	// bootstrapping — should block after the session has connected once.
	// Checkout and agent startup continue after the worker connects. Wait for
	// the actual terminal attachment before dismissing the startup view.
	// browserOnly limits the orchestrator's inspector tabs, not its terminal.
	const expectsTerminal = session?.mode !== "chat" && (!browserOnly || Boolean(session?.cloud));
	const cloudReconnecting = cloudReconnectRequested && expectsTerminal;
	const sessionReady = expectsTerminal ? terminalAttached : cloudStage === "connected";
	useEffect(() => {
		if (session?.kind !== "orchestrator" || session.cloud || session.mode === "chat") return;
		if (terminalAttached || session.provisionState === "failed") {
			useUiStore.getState().setProjectProvisioning(session.workspaceId, false, hostId);
		}
	}, [hostId, session?.kind, session?.cloud, session?.mode, session?.provisionState, session?.workspaceId, terminalAttached]);
	const connectedSessionRef = useRef("");
	if (sessionReady) connectedSessionRef.current = sessionId;
	const hasConnectedOnce = connectedSessionRef.current === sessionId;
	const workspaceRestarting = cloudStage === "resuming_workspace"
		|| cloudStage === "waiting_for_coder_agent"
		|| cloudStage === "starting_ao_worker";
	// After the first connect, the ONLY case we stop blocking on is
	// "restoring_agent" while the sandbox is still running (a transient runtime
	// relay drop mid-turn). A terminal re-mint (cloudReconnecting, covers a blank
	// flash) and a genuine workspace restart still raise the loader.
	const cloudSessionResolving = cloudLookupEnabled && cloudRouteSession.isLoading;
	// A Freestyle Chat session starting or waking stays on its conversation, not
	// the startup screen: its composer shimmers and its messages queue until the
	// worker connects (cloudChatStartingInPlace). Latched until it connects, so
	// the moment its sandbox reports running before the worker does never
	// flashes the screen in. Other providers keep the startup screen: a Coder
	// start shows its steps and its workspace link there.
	const chatStartingInPlaceRef = useRef("");
	const freestyleChat = session?.mode === "chat" && session.cloud?.sandboxProvider === FREESTYLE_PROVIDER;
	if (cloudChatStartingInPlace(session)) chatStartingInPlaceRef.current = sessionId;
	else if (!freestyleChat || cloudStage === "connected" || isCloudPausedStage(cloudStage)) chatStartingInPlaceRef.current = "";
	const chatStartingInPlace = chatStartingInPlaceRef.current === sessionId;
	// Waking an idle-paused session keeps its paused badge until the worker is
	// back, rather than raising the startup screen over the conversation.
	const chatWakingRef = useRef("");
	if (freestyleChat && (isCloudPausedStage(cloudStage) || cloudStage === "resuming_workspace")) chatWakingRef.current = sessionId;
	else if (!chatStartingInPlace) chatWakingRef.current = "";
	const chatWaking = chatWakingRef.current === sessionId && chatStartingInPlace;
	const showLifecycleLoader = !startupFailure && !chatStartingInPlace && ((!session && cloudSessionResolving) || (hasConnectedOnce
		? (cloudReconnecting || workspaceRestarting)
		: (cloudReconnecting || (cloudStage != null && !isCloudPausedStage(cloudStage) && !sessionReady))));
	const loaderVisibleLongEnoughRef = useRef("");
	const [completionDismissed, setCompletionDismissed] = useState(false);
	useEffect(() => {
		if (!showLifecycleLoader) return;
		loaderVisibleLongEnoughRef.current = "";
		setCompletionDismissed(false);
		const timer = window.setTimeout(() => { loaderVisibleLongEnoughRef.current = sessionId; }, 200);
		return () => window.clearTimeout(timer);
	}, [sessionId, showLifecycleLoader]);
	const showCompletedLoader = !showLifecycleLoader && sessionReady && loaderVisibleLongEnoughRef.current === sessionId && !completionDismissed;
	useEffect(() => {
		if (!showCompletedLoader) return;
		const timer = window.setTimeout(() => setCompletionDismissed(true), 360);
		return () => window.clearTimeout(timer);
	}, [showCompletedLoader]);
	const cloudResumeRef = useRef("");
	const requestCloudResume = useCallback(async (prepareWorkspace: boolean) => {
		if (!session?.cloud) return;
		await resumeCloudSession(cloudCpClient, session.cloud.orgId, session.id);
		if (prepareWorkspace) await cloudCpClient.requestWorkspaceCheckout(session.cloud.orgId, session.id);
		await refreshWorkspaces();
	}, [cloudCpClient, refreshWorkspaces, session]);
	useEffect(() => {
		if (!session?.cloud || cloudResumeRef.current === session.id) return;
		cloudResumeRef.current = session.id;
		// Opening the session asks the worker to prepare its workspace, which
		// retries a failed checkout. A failed Setup is not re-run just because
		// the session is viewed: its scripts can have side effects, so only the
		// failure card's Retry runs it again.
		const problem = cloudStartupProblem(session);
		const setupFailed = problem?.phase === "failed" && problem.code === "project_setup_failed";
		// Only a connected worker can prepare its workspace again. A session
		// still waking has none (the request is refused), and the worker it
		// wakes prepares the workspace as it starts. A recorded checkout failure
		// still asks, in case the list has not yet seen its worker connect.
		const workerCanPrepare = session.runtimeConnected === true || (problem?.phase === "failed" && isWorkspaceStartupError(problem.code));
		void requestCloudResume(!setupFailed && workerCanPrepare).catch(() => {
			// Keep the paused lifecycle projection visible. A later message, shell
			// open, or route visit can issue a fresh explicit resume intent.
		});
	}, [requestCloudResume, session]);
	const reviewerQuery = useQuery({
		queryKey: sessionReviewsQueryKey(sessionId, hostId),
		enabled: Boolean(
			(hostId ? remoteBase : window.ao) && session && !session.cloud && sessionIsActive(session) && !isOrchestratorSession(session) && session.prs.length > 0,
		),
		refetchInterval: (query) => {
			const data = query.state.data as ReviewsResponse | undefined;
			return data?.reviews?.some((review) => review.status === "running") ? 2500 : false;
		},
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/reviews", {
				params: { path: { sessionId } },
			});
			if (error) throw apiErrorException(error, "Unable to load reviews");
			return data ?? ({ reviewerHandleId: "", reviews: [], runs: [], activeReviewers: [] } satisfies ReviewsResponse);
		},
	});
	const reviewerSwitchPending = useIsMutating({
		mutationKey: [...sessionReviewsQueryKey(sessionId, hostId), "switch-reviewer"],
	}) > 0;
	// Cloud reviews come from the control plane; local reviews stay on the daemon path above.
	const cloudReviewerQuery = useQuery({
		queryKey: ["cloud-session-reviews", cloudCpBaseUrl, session?.cloud?.orgId, sessionId],
		enabled: Boolean(session?.cloud && cloudCpReady && sessionIsActive(session)),
		queryFn: () => {
			if (!session?.cloud) throw new Error("Cloud session is unavailable");
			return cloudCpClient.getSessionReviewState(session.cloud.orgId, sessionId);
		},
		retry: 1,
		// The agent (`ao review trigger`) and automatic review start reviews too, so
		// keep watching while the session has a PR; a running review polls faster.
		refetchInterval: (query) =>
			query.state.data?.reviews.some((review) => review.status === "running")
				? 2500
				: session?.prs.length ? 5000 : false,
	});
	const availableReviewerTerminal = session?.cloud
		? cloudReviewerQuery.data?.reviewerHandleId?.trim()
			? {
					handleId: cloudReviewerQuery.data.reviewerHandleId,
					harness: cloudReviewerQuery.data.reviewerHarness || session.provider,
			  }
			: undefined
		: reviewerTerminalFromReviews(reviewerQuery.data, terminalTarget);
	const retainedCloudReviewerTerminal =
		session?.cloud && terminalTarget.kind === "reviewer"
			? cloudReviewRunForTerminal(cloudReviewerQuery.data, terminalTarget.handleId)
				? { handleId: terminalTarget.handleId, harness: terminalTarget.harness }
				: undefined
			: undefined;
	const reviewerTerminal = session && sessionIsActive(session)
		? availableReviewerTerminal ?? retainedCloudReviewerTerminal
		: undefined;
	// A Cloud review that runs in Chat shows as a reviewer chat tab, as a local one does.
	const availableReviewerChat = session?.cloud
		? cloudReviewerChatFromReviews(cloudReviewerQuery.data)
		: reviewerChatFromReviews(reviewerQuery.data);
	const reviewerChat = session && sessionIsActive(session) ? availableReviewerChat : undefined;
	// The selected reviewer chat: a Cloud review's conversation lives on the
	// control plane, a local one on this session's daemon.
	const renderReviewerChat = (reviewId: string) => session?.cloud ? (
		<CloudReviewerChatSurface hideHeader session={session} reviewId={reviewId} harness={reviewerChat?.harness ?? "claude-code"} />
	) : (
		<ReviewerChatSurface
			hideHeader
			hostId={hostId}
			hostUnreachable={hostUnreachable}
			workerSessionId={sessionId}
			reviewId={reviewId}
			// The reviewer runs in this session's worktree, so its stopped banner's
			// Open shell opens the same shell the worker's banner does.
			onOpenShell={addShellTerminal}
			openingShell={openShellTerminal.isPending}
			shellError={openShellTerminal.error ? apiErrorMessage(openShellTerminal.error) : undefined}
		/>
	);

	// Shell terminals opened inside a session live beside its pane as extra tabs,
	// scoped to the session on screen so each session has its own shell set.
	const shellTerminalsQuery = useShellTerminals(hostId);
	const allShellTerminals = shellTerminalsQuery.data ?? [];
	const shellTerminals = useMemo(
		() => allShellTerminals.filter((shell) => shell.sessionId === sessionId),
		[allShellTerminals, sessionId],
	);
	const resolvedAuxiliaryTabOrder = useMemo(() => {
		const openFileKeys = fileTabs.openPaths.map((path) => `file:${path}`);
		const openShellKeys = shellTerminals.map((shell) => shell.handleId);
		const available = [
			...(reviewerTerminal ? [`reviewer:${reviewerTerminal.handleId}`] : []),
			...(!reviewerTerminal && reviewerChat ? [`reviewer-chat:${reviewerChat.reviewId}`] : []),
			...prTabs.open.map(prTabKey),
			...openFileKeys,
			...openShellKeys,
		];
		const availableKeys = new Set(available);
		// A pending shell tab keeps its place once it becomes its shell.
		const resolved = [
			...new Set(auxiliaryTabOrder.map((key) => adoptedShellHandle(key) ?? key)),
		].filter((key) => availableKeys.has(key));
		for (const key of available) {
			if (!resolved.includes(key)) resolved.push(key);
		}
		return resolved;
	}, [auxiliaryTabOrder, fileTabs.openPaths, prTabs.open, reviewerChat, reviewerTerminal, shellTerminals]);
	useEffect(() => {
		setAuxiliaryTabOrderBySession((current) => {
			const storedOrder = current[uiSessionId] ?? [];
			const currentOrder = [...new Set(storedOrder.map((key) => adoptedShellHandle(key) ?? key))];
			const newKeys = resolvedAuxiliaryTabOrder.filter((key) => !currentOrder.includes(key));
			if (newKeys.length === 0 && currentOrder.length === storedOrder.length
				&& currentOrder.every((key, index) => key === storedOrder[index])) {
				return current;
			}
			return { ...current, [uiSessionId]: [...currentOrder, ...newKeys] };
		});
	}, [resolvedAuxiliaryTabOrder, uiSessionId]);

	const openShellTerminal = useOpenShellTerminal(hostId);
	// Split panes are a Developer mode preview; off, no split is shown and
	// turning it off mid-split puts every tab back in the strip.
	const splitPanes = useSplitPanesEnabled();
	// Tabs can be shown together: they merge into one group tab in the strip and
	// the screen splits into one pane per member, where any pane can split
	// again. Every view stays mounted where it is and is only placed into its
	// pane, so terminals, chats and editors survive each move.
	// Layouts live in the split panes store and are remembered across reloads;
	// members that did not come back (a file tab, a closed shell) drop out
	// when the tabs are reconciled.
	const storedGroups = useSessionSplitGroups(uiSessionId);
	// A Cloud session shows one file view at a time, so its files are never split.
	// A PR's tab covers the whole center area, so it is never split either.
	const splittableKeys = useMemo(
		() => new Set([SESSION_TAB_KEY, ...resolvedAuxiliaryTabOrder.filter((key) => !prTabUrl(key) && !(session?.cloud && key.startsWith("file:")))]),
		[resolvedAuxiliaryTabOrder, session?.cloud],
	);
	// Until the shell list loads, an absent shell is unknown rather than closed.
	const shellListKnown = shellTerminalsQuery.data !== undefined;
	// Placeholders of shells being opened that the shell list has shown.
	const seenPendingShellsRef = useRef(new Set<string>());
	for (const key of splittableKeys) if (key.startsWith(PENDING_SHELL_KEY_PREFIX)) seenPendingShellsRef.current.add(key);
	const groups = useMemo(() => {
		if (!splitPanes) return EMPTY_TAB_GROUPS;
		if (!shellListKnown) return storedGroups;
		// A shell opened into a split is a placeholder until the daemon answers:
		// keep its pane until the list shows it, then follow it to the shell it
		// becomes. One that left the list without becoming a shell failed to
		// open, and is dropped like a closed shell.
		let available: ReadonlySet<string> = splittableKeys;
		for (const group of storedGroups) {
			for (const key of leafKeys(group.root)) {
				if (!key.startsWith(PENDING_SHELL_KEY_PREFIX) || adoptedShellHandle(key) || available.has(key)) continue;
				if (seenPendingShellsRef.current.has(key)) continue;
				available = new Set([...available, key]);
			}
		}
		return reconcileGroups(storedGroups, available, adoptedShellHandle);
	}, [shellListKnown, splitPanes, splittableKeys, storedGroups]);
	const updateGroups = useCallback(
		(update: (current: TabGroup[]) => TabGroup[]) => useSplitPanesStore.getState().updateGroups(uiSessionId, update),
		[uiSessionId],
	);
	// A closed member leaves its group; a group of one becomes a plain tab.
	// While split panes are off the stored layouts are kept for when they return.
	useEffect(() => {
		if (splitPanes && groups !== storedGroups) updateGroups(() => groups);
	}, [groups, splitPanes, storedGroups, updateGroups]);
	const groupedKeysSignature = groups.flatMap((group) => leafKeys(group.root)).join("\n");
	const groupedKeySet = useMemo<ReadonlySet<string>>(
		() => new Set(groupedKeysSignature ? groupedKeysSignature.split("\n") : []),
		[groupedKeysSignature],
	);
	// The tab the selection points at. A grouped tab shows its whole group,
	// with its own pane focused.
	const selectedTabKey = activePRUrl
		? prTabKey(activePRUrl)
		: fileTabs.activePath
			? `file:${fileTabs.activePath}`
			: reviewerChatId
				? `reviewer-chat:${reviewerChatId}`
				: terminalTargetBelongsToSession(terminalTarget, sessionId)
					? terminalTabKey(terminalTarget)
					: SESSION_TAB_KEY;
	const shownGroup = groupContaining(groups, selectedTabKey);
	const singleAuxiliaryTabOrder = useMemo(
		() => resolvedAuxiliaryTabOrder.filter((key) => !groupedKeySet.has(key)),
		[groupedKeySet, resolvedAuxiliaryTabOrder],
	);
	// Strip order: a group stands where its first member stood, or first when
	// it holds the session's own tab.
	const mainAuxiliaryTabOrder = useMemo(() => {
		if (groups.length === 0) return resolvedAuxiliaryTabOrder;
		const placed = new Set<string>();
		const order: string[] = [];
		const place = (group: TabGroup) => {
			if (placed.has(group.id)) return;
			placed.add(group.id);
			order.push(groupTabKey(group));
		};
		for (const group of groups) if (leafKeys(group.root).includes(SESSION_TAB_KEY)) place(group);
		for (const key of resolvedAuxiliaryTabOrder) {
			const group = groupedKeySet.has(key) ? groupContaining(groups, key) : undefined;
			if (group) place(group);
			else order.push(key);
		}
		for (const group of groups) place(group);
		return order;
	}, [groupedKeySet, groups, resolvedAuxiliaryTabOrder]);
	const setMainAuxiliaryTabOrder = useCallback(
		(order: string[]) => setAuxiliaryTabOrder(expandGroupTabOrder(order, groups)),
		[groups, setAuxiliaryTabOrder],
	);
	// While a group is on screen, the session's own surface shows its own view
	// (chat or agent terminal); the other members have panes of their own.
	const mainActiveFilePath = shownGroup || !fileTabs.activePath ? null : fileTabs.activePath;
	const mainReviewerChatId = shownGroup ? null : reviewerChatId;
	const closeShellTerminal = useCloseShellTerminal(hostId);
	const renameShellTerminal = useRenameShellTerminal(hostId);
	const activeShellTerminalHandleId = useUiStore((state) => state.activeShellTerminalHandleId);
	const setActiveShellTerminal = useUiStore((state) => state.setActiveShellTerminal);
	const setVisibleTerminalKind = useUiStore((state) => state.setVisibleTerminalKind);
	const clearVisibleTerminalKind = useUiStore((state) => state.clearVisibleTerminalKind);
	const renameShellTerminalByHandle = useCallback(
		(handleId: string, title: string) => renameShellTerminal.mutate({ handleId, title }),
		[renameShellTerminal],
	);

	// Scoped to the session on screen so the daemon roots the shell in that
	// session's worktree (the project id is only the fallback when the session's
	// workspace can no longer be resolved).
	const addShellTerminal = useCallback(() => {
		const shell = openShellTerminal.open({ projectId: session?.workspaceId, sessionId, cloud: session?.cloud });
		if (!shell) return;
		setFileTabsBySession((current) => ({
			...current,
			[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
		}));
		setActiveShellTerminal(shell.handleId);
		setTerminalTarget({
			generation: shell.createdAt,
			kind: "shell",
			handleId: shell.handleId,
			sessionId,
			title: shell.title,
		});
	}, [
		openShellTerminal,
		sessionId,
		session?.cloud,
		session?.workspaceId,
		setActiveShellTerminal,
		uiSessionId,
	]);

	const activateAuxiliaryTab = useCallback(
		(key?: string) => {
			const prUrl = prTabUrl(key);
			if (prUrl) {
				setReviewerChatId(null);
				// As for a file tab: a shell left selected would take the view back when it changes.
				setActiveShellTerminal(null);
				setTerminalTarget({ kind: "worker" });
				setFileTabsBySession((current) => ({
					...current,
					[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
				}));
				activatePRTab(uiSessionId, prUrl);
				return;
			}
			deactivatePRTab(uiSessionId);
			if (key?.startsWith("file:")) {
				const path = key.slice("file:".length);
				setActiveShellTerminal(null);
				setTerminalTarget({ kind: "worker" });
				setFileTabsBySession((current) => ({
					...current,
					[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, path),
				}));
				return;
			}
			if (reviewerTerminal && key === `reviewer:${reviewerTerminal.handleId}`) {
				setActiveShellTerminal(null);
				setTerminalTarget({
					kind: "reviewer",
					handleId: reviewerTerminal.handleId,
					harness: reviewerTerminal.harness,
					sessionId,
				});
				setFileTabsBySession((current) => ({
					...current,
					[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
				}));
				return;
			}
			if (reviewerChat && key === `reviewer-chat:${reviewerChat.reviewId}`) {
				setActiveShellTerminal(null);
				setTerminalTarget({ kind: "worker" });
				setReviewerChatId(reviewerChat.reviewId);
				setFileTabsBySession((current) => ({
					...current,
					[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
				}));
				return;
			}
			const shell = shellTerminals.find((candidate) => candidate.handleId === key);
			if (shell) {
				setActiveShellTerminal(shell.handleId);
				setTerminalTarget({
					generation: shell.createdAt,
					kind: "shell",
					handleId: shell.handleId,
					sessionId,
					title: shell.title,
				});
				setFileTabsBySession((current) => ({
					...current,
					[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
				}));
				return;
			}
			setActiveShellTerminal(null);
			setTerminalTarget({ kind: "worker" });
			setFileTabsBySession((current) => ({
				...current,
				[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
			}));
		},
		[reviewerChat, reviewerTerminal, sessionId, shellTerminals, setActiveShellTerminal, uiSessionId],
	);
	const adjacentAuxiliaryTab = useCallback(
		(closingKey: string) => {
			const closingIndex = singleAuxiliaryTabOrder.indexOf(closingKey);
			if (closingIndex < 0) return undefined;
			return singleAuxiliaryTabOrder[closingIndex - 1] ?? singleAuxiliaryTabOrder[closingIndex + 1];
		},
		[singleAuxiliaryTabOrder],
	);

	const selectShellTerminal = useCallback(
		(handleId: string) => {
			const shell = shellTerminals.find((s) => s.handleId === handleId);
			if (!shell) return;
			deactivatePRTab(uiSessionId);
			setReviewerChatId(null);
			setActiveShellTerminal(shell.handleId);
			setFileTabsBySession((current) => ({
				...current,
				[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
			}));
			setTerminalTarget({
				generation: shell.createdAt,
				kind: "shell",
				handleId: shell.handleId,
				sessionId,
				title: shell.title,
			});
		},
		[sessionId, shellTerminals, setActiveShellTerminal, uiSessionId],
	);

	// Closes a terminal and settles when the daemon has, rejecting if it
	// refused. The tab is removed at once; the strip follows on success.
	const closeShellTerminalAsync = useCallback(
		async (handleId: string): Promise<void> => {
			if (terminalTarget.kind === "shell" && terminalTarget.handleId === handleId) {
				// Match the visible mixed strip, not the shell-only creation order.
				activateAuxiliaryTab(adjacentAuxiliaryTab(handleId));
			} else if (activeShellTerminalHandleId === handleId) {
				setActiveShellTerminal(null);
			}
			try {
				await closeShellTerminal.mutateAsync(handleId);
				removeAuxiliaryTab(handleId);
			} catch (error) {
				if (apiErrorCode(error) === "SHELL_TERMINAL_NOT_FOUND") removeAuxiliaryTab(handleId);
				else throw error;
			}
		},
		[
			activeShellTerminalHandleId,
			activateAuxiliaryTab,
			adjacentAuxiliaryTab,
			closeShellTerminal,
			removeAuxiliaryTab,
			setActiveShellTerminal,
			terminalTarget,
		],
	);
	const closeShellTerminalByHandle = useCallback(
		(handleId: string) => {
			// A refusal leaves the terminal listed, which is what the user sees.
			void closeShellTerminalAsync(handleId).catch(() => undefined);
		},
		[closeShellTerminalAsync],
	);

	// Selecting the session's own pane also drops the active shell, so the effect
	// above does not immediately pull the view back to that shell.
	const selectSessionTerminal = useCallback(() => {
		// Bringing any tab forward puts an open PR description away.
		deactivatePRTab(uiSessionId);
		setActiveShellTerminal(null);
		setTerminalTarget({ kind: "worker" });
		setReviewerChatId(null);
		setFileTabsBySession((current) => ({
			...current,
			[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
		}));
	}, [setActiveShellTerminal, uiSessionId]);
	// "Ask in chat" from a file tab brings the session's Chat surface forward.
	useEffect(() => subscribeChatReveal(uiSessionId, selectSessionTerminal), [selectSessionTerminal, uiSessionId]);
	const selectReviewerTerminal = useCallback((target: ReviewerTerminalTarget) => {
		deactivatePRTab(uiSessionId);
		setReviewerChatId(null);
		setActiveShellTerminal(null);
		setTerminalTarget({ kind: "reviewer", handleId: target.handleId, harness: target.harness, sessionId });
		setFileTabsBySession((current) => ({
			...current,
			[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
		}));
	}, [sessionId, setActiveShellTerminal, uiSessionId]);
	const selectReviewerChat = useCallback((reviewId: string) => {
		deactivatePRTab(uiSessionId);
		setActiveShellTerminal(null);
		setTerminalTarget({ kind: "worker" });
		setReviewerChatId(reviewId);
		setFileTabsBySession((current) => ({
			...current,
			[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
		}));
	}, [setActiveShellTerminal, uiSessionId]);
	// Reveal the requested transcript before it scrolls to the palette hit.
	const chatFocusRequest = useUiStore((state) => {
		const request = state.chatMessageFocusRequest;
		return request?.sessionId === uiSessionId ? request : undefined;
	});
	useEffect(() => {
		if (chatFocusRequest?.reviewId) selectReviewerChat(chatFocusRequest.reviewId);
		else if (chatFocusRequest && session?.mode === "chat") selectSessionTerminal();
	}, [chatFocusRequest, session?.mode, selectReviewerChat, selectSessionTerminal]);
	const openCenterFile = useCallback(
		(path: string, options?: FileOpenOptions) => {
			const filePath = path;
			// Live paths retain their existing identities; snapshots coexist with them.
			path = sessionFileTabKey(path, options?.source);
			deactivatePRTab(uiSessionId);
			setReviewerChatId(null);
			setActiveShellTerminal(null);
			setTerminalTarget({ kind: "worker" });
			const key = nextCenterFileRequestKey();
			setCenterFileRequestsBySession((current) => {
				const sessionRequests = current[uiSessionId] ?? {};
				if (dirtyFiles[path] && sessionRequests[path]) return current;
				return {
					...current,
					[uiSessionId]: {
						...sessionRequests,
						[path]: {
							path: filePath,
							source: options?.source,
 annotationRange: options?.annotationRange ? {...options.annotationRange,requestKey:key}:undefined,
							previousPath: options?.previousPath,
							commitSha: options?.commitSha,
							editing: options?.editing ?? false,
							key,
							line: options?.line,
							mode: options?.mode ?? "file",
							scope: options?.scope,
						},
					},
				};
			});
			setFileTabsBySession((current) => ({
				...current,
				[uiSessionId]: openSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, path),
			}));
		},
		[setActiveShellTerminal, uiSessionId, dirtyFiles],
	);

	const markCenterFileEditingConsumed = useCallback((path: string, requestKey: number) => {
		consumedCenterEditingRequestsRef.current.add(`${uiSessionId}:${path}:${requestKey}`);
	}, [uiSessionId]);
	const markCenterFileLineConsumed = useCallback((path: string, requestKey: number) => {
		consumedCenterLineRequestsRef.current.add(`${uiSessionId}:${path}:${requestKey}`);
	}, [uiSessionId]);
	const setCenterFileDirty = useCallback((path: string, dirty: boolean) => {
		setDirtyFilesBySession((current) => {
			const sessionFiles = current[uiSessionId] ?? {};
			if (dirty) {
				if (sessionFiles[path]) return current;
				return { ...current, [uiSessionId]: { ...sessionFiles, [path]: true } };
			}
			if (!sessionFiles[path]) return current;
			const nextSessionFiles = { ...sessionFiles };
			delete nextSessionFiles[path];
			return { ...current, [uiSessionId]: nextSessionFiles };
		});
	}, [uiSessionId]);
	const activateCenterFile = useCallback((path: string) => {
		deactivatePRTab(uiSessionId);
		setReviewerChatId(null);
		setActiveShellTerminal(null);
		setTerminalTarget({ kind: "worker" });
		setFileTabsBySession((current) => ({
			...current,
			[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, path),
		}));
	}, [setActiveShellTerminal, uiSessionId]);
	// Closed file tabs, newest last, so ⌘⇧T can bring them back.
	const closedFilePathsRef = useRef<Record<string, string[]>>({});
	const closeCenterFile = useCallback((path: string) => {
		const closed = (closedFilePathsRef.current[uiSessionId] ??= []);
		closed.push(path);
		if (closed.length > 20) closed.shift();
		setFileTabsBySession((current) => ({
			...current,
			[uiSessionId]: closeSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, path),
		}));
		if (fileTabs.activePath === path) {
			activateAuxiliaryTab(adjacentAuxiliaryTab(`file:${path}`));
		}
		removeAuxiliaryTab(`file:${path}`);
	}, [activateAuxiliaryTab, adjacentAuxiliaryTab, fileTabs.activePath, removeAuxiliaryTab, uiSessionId]);
	// The shell layout owns opening (it is mounted on every route, so the button
	// and ⌘T / Ctrl+T work everywhere); this view only follows the result. When a new
	// shell becomes active while a session is on screen, switch the pane to it —
	// that is what makes the shortcut feel like it opened a terminal *here*.
	useEffect(() => {
		if (!activeShellTerminalHandleId) return;
		const shell = shellTerminals.find((s) => s.handleId === activeShellTerminalHandleId);
		if (!shell) return;
		if (terminalTarget.kind === "shell" && terminalTarget.handleId === shell.handleId &&
			terminalTarget.generation === shell.createdAt && terminalTarget.title === shell.title &&
			!fileTabs.activePath && !reviewerChatId) return;
		selectShellTerminal(shell.handleId);
	}, [activeShellTerminalHandleId, fileTabs.activePath, reviewerChatId, selectShellTerminal, shellTerminals, terminalTarget]);
	// A tab selected while it was pending follows it to the shell it became.
	// Only what still points at the pending tab moves: the user may have
	// selected another tab in the meantime.
	useEffect(() => {
		const adoptedActive = activeShellTerminalHandleId ? adoptedShellHandle(activeShellTerminalHandleId) : undefined;
		if (adoptedActive) {
			setActiveShellTerminal(adoptedActive);
			return;
		}
		if (terminalTarget.kind !== "shell") return;
		const adoptedTarget = adoptedShellHandle(terminalTarget.handleId);
		if (adoptedTarget && shellTerminals.some((shell) => shell.handleId === adoptedTarget)) {
			selectShellTerminal(adoptedTarget);
		}
	}, [activeShellTerminalHandleId, selectShellTerminal, setActiveShellTerminal, shellTerminals, terminalTarget]);

	// If the pane is pointed at a shell that is not in THIS session's strip — e.g.
	// after navigating to a different session whose globally-active shell belongs
	// elsewhere — fall back to the session's own pane rather than render a tab
	// that isn't shown here.
	useEffect(() => {
		setTerminalTarget((current) =>
			current.kind === "shell" && !shellTerminals.some((s) => s.handleId === current.handleId)
				? { kind: "worker" }
				: current,
		);
	}, [shellTerminals]);
	// Reviewer selection belongs to the tab, not its replaceable controller.
	// Keep it through switch teardown, then follow the replacement surface.
	useEffect(() => {
		if (session?.cloud) return;
		if ((!reviewerChatId && terminalTarget.kind !== "reviewer") || !reviewerQuery.isFetched || reviewerSwitchPending) return;
		if (availableReviewerChat) {
			if (reviewerChatId !== availableReviewerChat.reviewId) selectReviewerChat(availableReviewerChat.reviewId);
		} else if (availableReviewerTerminal) {
			if (terminalTarget.kind !== "reviewer" || terminalTarget.handleId !== availableReviewerTerminal.handleId || terminalTarget.harness !== availableReviewerTerminal.harness) {
				selectReviewerTerminal(availableReviewerTerminal);
			}
		} else {
			selectSessionTerminal();
		}
	}, [availableReviewerChat, availableReviewerTerminal, reviewerChatId, reviewerQuery.isFetched, reviewerSwitchPending, selectReviewerChat, selectReviewerTerminal, selectSessionTerminal, session?.cloud, terminalTarget]);
	useEffect(() => {
		if (!session?.cloud) return;
		setTerminalTarget((current) =>
			current.kind === "reviewer" &&
			cloudReviewerQuery.isFetched &&
			(!availableReviewerTerminal || availableReviewerTerminal.handleId !== current.handleId) &&
			!cloudReviewRunForTerminal(cloudReviewerQuery.data, current.handleId)
				? { kind: "worker" }
				: current,
		);
	}, [availableReviewerTerminal, cloudReviewerQuery.data, cloudReviewerQuery.isFetched, session?.cloud]);
	useEffect(() => {
		if (!session?.cloud) return;
		setTerminalTarget((current) => {
			if (current.kind !== "reviewer") return current;
			const run = cloudReviewRunForTerminal(cloudReviewerQuery.data, current.handleId);
			if (!run || current.reviewStatus === run.status) return current;
			return { ...current, reviewStatus: run.status };
		});
	}, [cloudReviewerQuery.data, session?.cloud]);
	// A Cloud trigger can replace a previously ended reviewer with a new terminal
	// while the inspector remains mounted. Make that replacement visible even if
	// the trigger response raced the inspector callback: the shared Cloud review
	// query is the durable source of truth for the active reviewer handle.
	const cloudReviewIsRunning = Boolean(
		session?.cloud && cloudReviewerQuery.data?.reviews.some((review) => review.status === "running"),
	);
	// Open each newly started reviewer once, then leave the tab choice to the
	// user so Chat stays reachable while it runs. Automatic reviews only add
	// the Reviewer tab, as they do in local sessions.
	const openedCloudReviewersRef = useRef(new Set<string>());
	const cloudReviewerHandleId = reviewerTerminal?.handleId;
	const cloudReviewerHarness = reviewerTerminal?.harness;
	const cloudReviewerTrigger = cloudReviewerHandleId
		? cloudReviewRunForTerminal(cloudReviewerQuery.data, cloudReviewerHandleId)?.triggerSource
		: undefined;
	useEffect(() => {
		if (!session?.cloud || !cloudReviewerHandleId || !cloudReviewerHarness || !cloudReviewIsRunning) return;
		if (openedCloudReviewersRef.current.has(cloudReviewerHandleId)) return;
		openedCloudReviewersRef.current.add(cloudReviewerHandleId);
		if (cloudReviewerTrigger === "auto") return;
		setTerminalTarget((current) =>
			current.kind === "reviewer" && current.handleId === cloudReviewerHandleId
				? current
				: {
						kind: "reviewer",
						handleId: cloudReviewerHandleId,
						harness: cloudReviewerHarness,
						sessionId,
					},
		);
	}, [cloudReviewIsRunning, cloudReviewerHandleId, cloudReviewerHarness, cloudReviewerTrigger, session?.cloud, sessionId]);
	const isOrchestrator = session ? isOrchestratorSession(session) : false;
	const hasInspector = Boolean(session);
	const sizing = useMemo(() => inspectorSizing(inspectorView), [inspectorView]);
	const browserEntryWidthFloorRef = useRef<number | null>(null);

	// Arm the shared width transition before the selected inspector surface
	// changes its CSS variable. Browser becomes a co-work canvas; utility views
	// return to their stable rail width on the same spring as the shell sidebar.
	const armWorkspaceTransition = useCallback(() => {
		const split = sessionSplitRef.current;
		if (!split) return;
		if (workspaceResizeTimerRef.current !== null) window.clearTimeout(workspaceResizeTimerRef.current);
		split.setAttribute("data-workspace-resizing", "true");
		void split.offsetWidth;
		workspaceResizeTimerRef.current = window.setTimeout(() => {
			split.removeAttribute("data-workspace-resizing");
			workspaceResizeTimerRef.current = null;
		}, INSPECTOR_SPRING_MS);
	}, []);

	useEffect(
		() => () => {
			if (workspaceResizeTimerRef.current !== null) window.clearTimeout(workspaceResizeTimerRef.current);
			sessionSplitRef.current?.removeAttribute("data-workspace-resizing");
		},
		[],
	);

	const prepareWorkspaceProfile = useCallback(
		(nextSizing: InspectorSizing) => {
			armWorkspaceTransition();
			const groupWidth = sessionSplitRef.current?.clientWidth || window.innerWidth;
			const availableWidth = Math.max(0, groupWidth - INSPECTOR_SEPARATOR_RESERVE_PX);
			const targetInspectorWidth = Number.parseFloat(initialInspectorSize(nextSizing, availableWidth));
			startTerminalLiveResize(
				targetInspectorWidth <= INSPECTOR_COMPACT_MAX_PX ? "compact" : "expanded",
				topbarSecondaryLabelMode(Math.max(0, availableWidth - targetInspectorWidth)),
			);
		},
		[armWorkspaceTransition, startTerminalLiveResize],
	);

	const transitionInspectorView = useCallback(
		(next: InspectorView) => {
			if (next === inspectorView) return;
			if (next === "browser") {
				const currentWidth = Number(window.localStorage.getItem(sizing.storageKey));
				browserEntryWidthFloorRef.current = Number.isFinite(currentWidth) ? currentWidth : null;
			} else {
				browserEntryWidthFloorRef.current = null;
			}
			const nextSizing = inspectorSizing(next);
			if (!sizingGeometryEqual(sizing, nextSizing)) prepareWorkspaceProfile(nextSizing);
			setInspectorViewForSession(uiSessionId, next);
		},
		[
			inspectorView,
			prepareWorkspaceProfile,
			uiSessionId,
			setInspectorViewForSession,
			sizing,
		],
	);

	const newTerminalError = openShellTerminal.error ? apiErrorMessage(openShellTerminal.error) : undefined;
	const newShellTerminalAction = useMemo(() =>
		session && !isOrchestrator ? (
			<Tooltip>
				<TooltipTrigger asChild>
					<Button size="icon-sm"
						aria-label={t("shortcut.new-shell-terminal")}
						data-terminal-focus-handoff="true"
						onClick={addShellTerminal}
						type="button"
						variant="quiet"
					>
						<Plus aria-hidden="true" className="size-icon-md" />
					</Button>
				</TooltipTrigger>
				<TooltipContent shortcut={newTerminalError ? undefined : "new-shell-terminal"} side="bottom">
					{newTerminalError ?? t("terminal.new")}
				</TooltipContent>
			</Tooltip>
		) : null,
		[addShellTerminal, isOrchestrator, newTerminalError, session, t],
	);
	const sendCloudFileAnnotation = useCallback(async (message: string) => {
		const orgId = session?.cloud?.orgId;
		if (!orgId) throw new Error(t("files.feedbackError"));
		await cloudCpClient.sendSessionMessage(orgId, sessionId, { text: message });
	}, [cloudCpClient, session?.cloud?.orgId, sessionId, t]);
	const fileAnnotation = useFileAnnotation(sessionId, { hostId, sendMessage: session?.cloud ? sendCloudFileAnnotation : undefined });
	const centerFileTabs = useMemo(
		() =>
			fileTabs.openPaths.map((path) => ({
				key: `file:${path}`,
				content: (
					<SessionFileTab
						active={mainActiveFilePath === path}
						dirty={Boolean(dirtyFiles[path])}
						onActivate={() => activateCenterFile(path)}
						onClose={() => closeCenterFile(path)}
						path={centerFileRequestsBySession[uiSessionId]?.[path]?.path ?? path}
						sourceLabel={
							centerFileRequestsBySession[uiSessionId]?.[path]?.source?.kind === "snapshot"
								? (centerFileRequestsBySession[uiSessionId]?.[path]?.source as { label?: string })?.label
								: undefined
						}
					/>
				),
				onSelect: () => activateCenterFile(path),
				onClose: () => closeCenterFile(path),
				menu: {
					kind: "file",
					tabKey: `file:${path}`,
					onClose: () => closeCenterFile(path),
					file: {
						path: centerFileRequestsBySession[uiSessionId]?.[path]?.path ?? path,
						remote: Boolean(hostId || session?.cloud),
						sessionId,
						source: centerFileRequestsBySession[uiSessionId]?.[path]?.source,
					},
				} satisfies TabMenuSpec,
			})),
		[activateCenterFile, closeCenterFile, dirtyFiles, mainActiveFilePath, fileTabs.openPaths, centerFileRequestsBySession, uiSessionId, hostId, session?.cloud, sessionId],
	);

	const closeCenterPR = useCallback((url: string) => {
		if (activePRUrl === url) activateAuxiliaryTab(adjacentAuxiliaryTab(prTabKey(url)));
		closePRTab(uiSessionId, url);
		removeAuxiliaryTab(prTabKey(url));
	}, [activateAuxiliaryTab, activePRUrl, adjacentAuxiliaryTab, removeAuxiliaryTab, uiSessionId]);
	const workspaceTabs = useMemo(
		() => [
			...prTabs.open.map((url) => ({
				key: prTabKey(url),
				content: (
					<PRTabChip
						active={activePRUrl === url}
						hostId={hostId}
						onActivate={() => activateAuxiliaryTab(prTabKey(url))}
						onClose={() => closeCenterPR(url)}
						session={session}
						url={url}
					/>
				),
				onSelect: () => activateAuxiliaryTab(prTabKey(url)),
				onClose: () => closeCenterPR(url),
			})),
			...centerFileTabs,
		],
		[activateAuxiliaryTab, activePRUrl, centerFileTabs, closeCenterPR, hostId, prTabs.open, session],
	);
	// Opening a PR's tab from the card puts the file tab in front away, and
	// lets go of a selected shell, which would take the view back when it changes.
	useEffect(() => {
		if (!activePRUrl) return;
		setReviewerChatId(null);
		setActiveShellTerminal(null);
		setTerminalTarget((current) => (current.kind === "worker" ? current : { kind: "worker" }));
		setFileTabsBySession((current) => {
			const tabs = current[uiSessionId];
			return tabs?.activePath ? { ...current, [uiSessionId]: activateSessionFile(tabs, null) } : current;
		});
	}, [activePRUrl, setActiveShellTerminal, uiSessionId]);
	// A group without the session's tab is selected like a workspace tab. With
	// it, the session surface is on screen and stays the selected tab there. A
	// PR's tab is never in a group.
	const activeWorkspaceTabKey = activePRUrl
		? prTabKey(activePRUrl)
		: mainActiveFilePath
			? `file:${mainActiveFilePath}`
			: shownGroup && !leafKeys(shownGroup.root).includes(SESSION_TAB_KEY)
				? groupTabKey(shownGroup)
				: undefined;
	const previewUrl = session?.previewUrl?.trim() || undefined;
	const previewRevision = session?.previewRevision;
	const browserSlotVisible = Boolean(
		session &&
			hasInspector &&
			(browserPoppedOut || (inspectorPanelVisible && inspectorView === "browser")),
	);
	useEffect(() => {
		if (hostId && remoteBase && browserSlotVisible) void remoteSessionQuery.refetch();
	}, [hostId, remoteBase, browserSlotVisible, remoteSessionQuery.refetch]);
	const terminated = session ? !sessionIsActive(session) : false;
	// A completed session's HTML artifact is static output the daemon keeps
	// serving, so an artifact preview the user opened stays visible; every other
	// preview of a terminated session is a stale DB fact and is still torn down.
	const browserTerminated = sessionBrowserUnavailable(session, hostId);
	const browserView = useBrowserView({
		sessionId: uiSessionId,
		origin: hostId ? { hostId, sessionId, proxyBase: remoteBase ?? "" } : undefined,
		cloudAgentBrowser: session?.cloud && cloudCpReady ? { baseUrl: cloudCpBaseUrl, orgId: session.cloud.orgId, sessionId: session.id } : undefined,
		active: browserSlotVisible,
		poppedOut: browserPoppedOut,
		terminated: browserTerminated,
		sessionEnded: session?.isTerminated,
		previewUrl,
		previewRevision,
	});
	const browserAnnotationQueue = useBrowserAnnotationQueue({
		sessionId: session?.id,
		hostId,
		sourcePreviewUrl: session?.previewUrl,
		navUrl: browserView.navState.url,
	});
	const browserViewId = browserView.viewId;
	useEffect(
		() =>
			window.ao?.browser.onClosePanel((targetViewId) => {
				if (!browserViewId || targetViewId !== browserViewId) return;
				setBrowserPopOutState({ sessionId, phase: "docked" });
				setInspectorOpenForSession(uiSessionId, false);
			}),
		[browserViewId, sessionId, uiSessionId, setInspectorOpenForSession],
	);
	const browserUrl = browserView.navState.url.trim();
	// A terminated session's `previewUrl` is a stale DB fact; useBrowserView
	// suppresses and destroys the live preview for it, so it must not count as
	// content here either — otherwise a merged/terminated session with an old
	// preview auto-opens Browser onto a view the hook has already torn down.
	const hasBrowserContent = !browserTerminated && Boolean(previewUrl || browserUrl);

	// Entering a session for the first time ever always starts on Summary. This
	// must fire exactly once per session's *lifetime*, not once per "was this
	// the last session I looked at" or "is this view currently mounted" — so
	// the initialized flag lives in the ui-store (inspectorSessions[sessionId])
	// rather than a component-local ref, and survives both re-entering a
	// different previously-visited session and unmounting/remounting this view
	// entirely (e.g. across route transitions). Treat browser content that
	// already existed when the route resolved as the baseline for that visit;
	// only preview work arriving afterward may reveal Browser automatically.
	useLayoutEffect(() => {
		if (!session) return;
		initializeInspectorSession(uiSessionId, hasBrowserContent, hasInspector);
	}, [hasBrowserContent, hasInspector, session, uiSessionId, initializeInspectorSession]);

	// Keep rejecting another session's shell/reviewer handle here as well, so it
	// can never be cached under the destination session.
	const routedTerminalTarget = terminalTargetBelongsToSession(terminalTarget, sessionId) && !shownGroup
		? terminalTarget
		: ({ kind: "worker" } satisfies TerminalTarget);
	// Chat surface stays mounted in chat mode for worker, reviewer, and shell
	// targets. A terminal pane (reviewer or shell) renders as a tab inside the
	// chat surface, so opening one never costs the user the conversation.
	const chatTargetKind = routedTerminalTarget.kind;
	const renderedSessionMode = interfaceUi.renderedMode;
	const showChatSurface =
		session !== undefined &&
		(renderedSessionMode === "chat" || session.needsResume) &&
		(chatTargetKind === "worker" || chatTargetKind === "reviewer" || chatTargetKind === "shell");
	const chatViewActive =
		session?.mode === "chat" &&
		!session.cloud &&
		(hostId ? Boolean(remoteBase) : daemonStatus.state === "ready") &&
		routedTerminalTarget.kind === "worker" &&
		(!activeShellTerminalHandleId ||
			(shellTerminalsQuery.data !== undefined &&
				!shellTerminals.some((shell) => shell.handleId === activeShellTerminalHandleId))) &&
		!mainReviewerChatId &&
		!mainActiveFilePath &&
		!activePRUrl;

	// Routes the selection to a tab; a grouped tab brings its group with it.
	const selectTab = useCallback(
		(key: string) => (key === SESSION_TAB_KEY ? selectSessionTerminal() : activateAuxiliaryTab(key)),
		[activateAuxiliaryTab, selectSessionTerminal],
	);
	// ⌘D's action, set further down; tab menus reuse it.
	// An agent's `ao pane split terminal` uses it too, with its own anchor.
	const splitNewTerminalRef = useRef<(side: SplitSide, anchorKey?: string) => string | undefined>(() => undefined);
	// Tab menus split their tab beside the one on screen, as dropping it on
	// that pane's edge does; the tab on screen splits off a new terminal, as
	// ⌘D does. Only while split panes are on.
	const splitTabMenu = useMemo(() => {
		if (!splitPanes) return undefined;
		return (key: string): TabSplitActions => {
			const anchor = selectedTabKey;
			if (key === anchor) {
				if (!session || isOrchestrator) return {};
				return {
					right: () => splitNewTerminalRef.current("right"),
					down: () => splitNewTerminalRef.current("bottom"),
					newTerminal: true,
				};
			}
			if (!splittableKeys.has(anchor) || !splittableKeys.has(key)) return {};
			const split = (side: "right" | "bottom") => () => {
				updateGroups((current) => splitBeside(current, anchor, key, side));
				selectTab(key);
			};
			return { right: split("right"), down: split("bottom") };
		};
	}, [isOrchestrator, selectTab, selectedTabKey, session, splitPanes, splittableKeys, updateGroups]);
	const splitScreenRef = useRef<HTMLDivElement | null>(null);
	// Clicking into a pane focuses its member, as clicking its name does. This
	// listens natively: terminals are portaled from the shared cache, so React
	// would deliver their events elsewhere, while the topbar strip (portaled
	// the other way) is outside this element and never counts.
	const focusPaneRef = useRef<(key: string) => void>(() => undefined);
	focusPaneRef.current = (key) => {
		if (shownLayout?.leaves.has(key) && key !== selectedTabKey) selectTab(key);
	};
	useEffect(() => {
		const onPointerDown = (event: PointerEvent) => {
			const screen = splitScreenRef.current;
			const pane = event.target instanceof Element ? event.target.closest<HTMLElement>("[data-split-pane]") : null;
			if (screen && pane?.dataset.splitPane && screen.contains(pane)) focusPaneRef.current(pane.dataset.splitPane);
		};
		document.addEventListener("pointerdown", onPointerDown, true);
		return () => document.removeEventListener("pointerdown", onPointerDown, true);
	}, []);
	const centerColumnRef = useRef<HTMLDivElement | null>(null);
	const splitDropStateRef = useRef<SplitDropState | null>(null);
	splitDropStateRef.current = {
		groups,
		shownGroup,
		selectedTabKey,
		splittableKeys,
		selectTab,
		updateGroups,
		labels: { split: t("splitPane.dropSplit"), join: t("splitPane.dropJoin"), unsplit: t("splitPane.dropUnsplit") },
	};
	useEffect(
		() =>
			!splitPanes ? undefined : registerPaneDropTarget({
				// Other tabs come to the tab on screen; it never leaves itself. A
				// member of the group on screen can still move within or out of it.
				canDrag: (key) => {
					const state = splitDropStateRef.current;
					if (!splitScreenRef.current || !state?.splittableKeys.has(key)) return false;
					return key !== state.selectedTabKey || Boolean(state.shownGroup);
				},
				resolve: (key, x, y) => {
					const state = splitDropStateRef.current;
					return state ? resolveSplitDrop(state, splitScreenRef.current, centerColumnRef.current, key, x, y) : null;
				},
			}),
		[splitPanes],
	);
	const shownGroupId = shownGroup?.id;
	const resizeSplit = useCallback(
		(path: string, ratio: number) => {
			if (!shownGroupId) return;
			updateGroups((current) => {
				let changed = false;
				const next = current.map((group) => {
					if (group.id !== shownGroupId) return group;
					const root = setRatioAt(group.root, path, ratio);
					if (root === group.root) return group;
					changed = true;
					return { ...group, root };
				});
				return changed ? next : current;
			});
		},
		[shownGroupId, updateGroups],
	);
	const shownLayout = useMemo(() => (shownGroup ? layoutCss(shownGroup.root) : null), [shownGroup]);
	const paneStyle = (key: string): CSSProperties => {
		const box = shownLayout?.leaves.get(key);
		// Each pane keeps a title bar on top; the content takes what is below it.
		return box
			? { position: "absolute", left: box.left, width: box.width, top: `calc(${box.top} + ${PANE_HEADER_PX}px)`, height: `calc(${box.height} - ${PANE_HEADER_PX}px)` }
			: FULL_PANE_STYLE;
	};
	// The reviewer chat stays mounted while it is a member of a split, so it
	// survives moving between panes and leaving its group's tab. Otherwise it
	// is only mounted while selected, as before split panes.
	const groupedReviewerChatId = reviewerChat && groupedKeySet.has(`reviewer-chat:${reviewerChat.reviewId}`) ? reviewerChat.reviewId : null;
	const retainedReviewerChatId = reviewerChatId ?? groupedReviewerChatId;
	const reviewerPaneKey = retainedReviewerChatId ? `reviewer-chat:${retainedReviewerChatId}` : undefined;
	const reviewerInPane = Boolean(reviewerPaneKey && shownLayout?.leaves.has(reviewerPaneKey));
	const reviewerVisible = reviewerInPane || Boolean(retainedReviewerChatId && mainReviewerChatId === retainedReviewerChatId);
	const sessionPaneHidden = Boolean(shownLayout && !shownLayout.leaves.has(SESSION_TAB_KEY));
	const paneCreateMux = useMemo(
		() => (remoteBase ? () => createTerminalMux(muxUrlFromApiBase(remoteBase)) : undefined),
		[remoteBase],
	);
	const paneDaemonReady = hostId ? Boolean(remoteBase) : daemonStatus.state === "ready";
	const paneTerminalTarget = (key: string): TerminalTarget | undefined => {
		if (reviewerTerminal && key === `reviewer:${reviewerTerminal.handleId}`) {
			return { kind: "reviewer", handleId: reviewerTerminal.handleId, harness: reviewerTerminal.harness, sessionId };
		}
		const shell = shellTerminals.find((candidate) => candidate.handleId === key);
		return shell
			? { kind: "shell", handleId: shell.handleId, generation: shell.createdAt, sessionId, title: shell.title }
			: undefined;
	};
	// A group remembers the member last focused, so its tab names that member
	// and selecting it returns there.
	useEffect(() => {
		if (!shownGroup || shownGroup.focus === selectedTabKey) return;
		const id = shownGroup.id;
		updateGroups((current) => current.map((group) => (group.id === id ? { ...group, focus: selectedTabKey } : group)));
	}, [selectedTabKey, shownGroup, updateGroups]);
	// Closing a pane (⌘W, or the ✕ on its group tab) keeps the rest of the
	// split on screen. The session's own pane and reviewers are not closable,
	// so they return to the strip as tabs of their own.
	const closePaneRef = useRef<(key: string) => Promise<void>>(async () => undefined);
	closePaneRef.current = async (key) => {
		const group = groupContaining(groups, key);
		if (!group) return;
		const members = leafKeys(group.root);
		const index = members.indexOf(key);
		const sibling = selectedTabKey === key ? (members[index - 1] ?? members[index + 1]) : undefined;
		if (key.startsWith("file:")) {
			const path = key.slice("file:".length);
			const closed = (closedFilePathsRef.current[uiSessionId] ??= []);
			closed.push(path);
			if (closed.length > 20) closed.shift();
			setFileTabsBySession((current) => ({
				...current,
				[uiSessionId]: closeSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, path),
			}));
			removeAuxiliaryTab(key);
		} else if (shellTerminals.some((shell) => shell.handleId === key)) {
			if (activeShellTerminalHandleId === key) setActiveShellTerminal(null);
			// The rest of the split takes the selection at once; the caller
			// waits for the daemon to close the terminal.
			if (sibling) selectTab(sibling);
			try {
				await closeShellTerminal.mutateAsync(key);
				removeAuxiliaryTab(key);
			} catch (error) {
				if (apiErrorCode(error) === "SHELL_TERMINAL_NOT_FOUND") removeAuxiliaryTab(key);
				else throw error;
			}
			return;
		} else {
			updateGroups((current) => withoutTab(current, key));
		}
		if (sibling) selectTab(sibling);
	};
	const selectedTabKeyRef = useRef(selectedTabKey);
	selectedTabKeyRef.current = selectedTabKey;
	const closePane = useCallback((key: string) => void closePaneRef.current(key).catch(() => undefined), []);
	const closeFocusedPane = useCallback(() => void closePaneRef.current(selectedTabKeyRef.current).catch(() => undefined), []);

	// The group tabs only change with the groups, the selection and the
	// members' names, so the strips do not redo their work on every render.
	const sessionLabel = session ? (isOrchestrator ? t("shell.orchestrator") : session.title) : t("terminal.noSession");
	const sessionProvider = session?.provider;
	const reviewerHarness = reviewerTerminal?.harness ?? reviewerChat?.harness;
	const groupMember = useCallback((key: string): SplitGroupMember => {
			const onSelect = () => selectTab(key);
			const onClose = () => closePane(key);
			if (key === SESSION_TAB_KEY) {
				return {
					key,
					onSelect,
					onClose,
					label: sessionLabel,
					icon: sessionProvider ? <AgentAvatar className="size-icon-sm!" decorative provider={sessionProvider} /> : null,
				};
			}
			if (key.startsWith("file:")) {
				const path = key.slice("file:".length);
				const request = centerFileRequestsBySession[uiSessionId]?.[path];
				const sourceLabel = request?.source?.kind === "snapshot" ? (request.source as { label?: string }).label : undefined;
				const name = `${(request?.path ?? path).split("/").pop() || path}${sourceLabel ? ` · ${sourceLabel}` : ""}`;
				return { key, onSelect, onClose, label: name, icon: <WorkspaceEntryIcon className="size-icon-sm shrink-0" kind="file" name={name} /> };
			}
			if (key.startsWith("reviewer")) {
				return {
					key,
					onSelect,
					onClose,
					label: t("terminal.reviewer"),
					icon: reviewerHarness ? <AgentAvatar className="size-icon-sm!" decorative provider={reviewerHarness} /> : null,
				};
			}
			const shell = shellTerminals.find((candidate) => candidate.handleId === key);
			// Like a strip tab, a local shell shows the agent running in it; the
			// group tab looks the agent up.
			const tracksAgent = shell && !shell.optimistic && !shell.cloud && !shell.hostId;
			return {
				key,
				onSelect,
				onClose,
				label: shell?.title ?? key,
				icon: <SquareTerminal aria-hidden="true" className="size-icon-sm" />,
				agentHandleId: tracksAgent ? key : undefined,
			};
	}, [centerFileRequestsBySession, closePane, reviewerHarness, selectTab, sessionLabel, sessionProvider, shellTerminals, t, uiSessionId]);
	// Pressing a file pane's title bar moves keyboard focus into that file.
	const [paneFocusRequests, setPaneFocusRequests] = useState<Record<string, number>>({});
	const groupWorkspaceTabs = useMemo(() => {
		const groupFocus = (group: TabGroup) => {
			const members = leafKeys(group.root);
			return group.focus && members.includes(group.focus) ? group.focus : members[0]!;
		};
		// The focused member's own tab menu, below the group's Ungroup. A
		// member's title is not edited in the group tab, so it has no rename.
		const memberMenu = (key: string, shown: boolean): TabMenuSpec | undefined => {
			const filePath = key.startsWith("file:") ? key.slice("file:".length) : undefined;
			const request = filePath ? centerFileRequestsBySession[uiSessionId]?.[filePath] : undefined;
			const base = { tabKey: key, closeShortcut: shown, onClose: () => closePane(key) };
			if (key === SESSION_TAB_KEY) {
				if (!session) return { ...base, kind: "plain" };
				const canArchive = !isOrchestrator && !session.cloud && sessionIsActive(session);
				return { ...base, kind: "session", session, canArchive };
			}
			if (filePath) {
				return { ...base, kind: "file", file: { path: request?.path ?? filePath, remote: Boolean(hostId || session?.cloud), sessionId, source: request?.source } };
			}
			return { ...base, kind: "plain" };
		};
		return groups.map((group) => ({
			key: groupTabKey(group),
			content: (
				<SplitGroupTab
					active={group === shownGroup}
					focusedKey={group === shownGroup ? selectedTabKey : groupFocus(group)}
					members={leafKeys(group.root).map(groupMember)}
					menu={{
						kind: "group",
						onUngroup: () => updateGroups((current) => ungroupById(current, group.id)),
						member: memberMenu(group === shownGroup ? selectedTabKey : groupFocus(group), group === shownGroup),
					}}
				/>
			),
			onSelect: () => selectTab(groupFocus(group)),
		}));
	}, [
		centerFileRequestsBySession,
		closePane,
		groupMember,
		groups,
		hostId,
		isOrchestrator,
		reviewerHarness,
		selectTab,
		selectedTabKey,
		session,
		sessionId,
		sessionLabel,
		sessionProvider,
		shellTerminals,
		shownGroup,
		t,
		uiSessionId,
		updateGroups,
	]);


	// ⌘D opens a new terminal to the right of the focused pane and ⌘⇧D one
	// below it, splitting the tab on screen if it is not split yet.
	splitNewTerminalRef.current = (side, anchorKey) => {
		if (!session || isOrchestrator || !splitPanes) return undefined;
		const anchor = anchorKey ?? (splittableKeys.has(selectedTabKey) ? selectedTabKey : SESSION_TAB_KEY);
		const shell = openShellTerminal.open({ projectId: session.workspaceId, sessionId, cloud: session.cloud });
		if (!shell) return undefined;
		const key = shell.handleId;
		updateGroups((current) => splitBeside(current, anchor, key, side));
		deactivatePRTab(uiSessionId);
		setReviewerChatId(null);
		setFileTabsBySession((current) => ({
			...current,
			[uiSessionId]: activateSessionFile(current[uiSessionId] ?? EMPTY_SESSION_FILE_TABS, null),
		}));
		setActiveShellTerminal(key);
		setTerminalTarget({ generation: shell.createdAt, kind: "shell", handleId: key, sessionId, title: shell.title });
		return key;
	};
	useSessionShortcut("split-shell-terminal", () => splitNewTerminalRef.current("right"), splitPanes && Boolean(session) && !isOrchestrator);
	useSessionShortcut("split-shell-terminal-down", () => splitNewTerminalRef.current("bottom"), splitPanes && Boolean(session) && !isOrchestrator);

	// Agents split this session's panes with `ao pane` (where split panes are
	// on, local sessions only). While this view is on screen the relay hands it their
	// requests, and it reports what it shows whenever that changes.
	const paneRelayEnabled = splitPanes && Boolean(session) && !hostId && !session?.cloud;
	const describePaneTab = (key: string): PaneTabDescription => {
		if (key === SESSION_TAB_KEY) return { key, kind: "session", title: sessionLabel };
		if (key.startsWith("file:")) {
			const path = key.slice("file:".length);
			return { key, kind: "file", title: centerFileRequestsBySession[uiSessionId]?.[path]?.path ?? path };
		}
		if (key.startsWith("reviewer")) return { key, kind: "reviewer", title: t("terminal.reviewer") };
		const shell = shellTerminals.find((candidate) => candidate.handleId === key);
		const agent = useShellAgentStore.getState().agents[key]?.harness;
		return { key, kind: "terminal", title: shell?.title ?? key, ...(agent ? { agent } : {}) };
	};
	const describeSessionPanesRef = useRef<() => PaneLayout>(() => ({ enabled: false, selected: SESSION_TAB_KEY, tabs: [], groups: [] }));
	describeSessionPanesRef.current = () =>
		describePaneLayout({
			enabled: splitPanes,
			selected: selectedTabKey,
			shownGroupId: shownGroup?.id,
			tabs: [...splittableKeys].map(describePaneTab),
			groups,
		});
	const lastPaneReportRef = useRef("");
	const reportSessionPanesRef = useRef(() => undefined as void);
	reportSessionPanesRef.current = () => {
		if (!paneRelayEnabled) {
			lastPaneReportRef.current = "";
			return;
		}
		const layout = describeSessionPanesRef.current();
		const signature = `${sessionId}\n${JSON.stringify(layout)}`;
		if (signature === lastPaneReportRef.current) return;
		lastPaneReportRef.current = signature;
		reportPaneLayout(sessionId, layout);
	};
	// Changes whenever this view's relay registration starts or ends (the session
	// changed, or the relay was switched off), so a command that waited on a slow
	// lookup can tell the view it was sent to is gone.
	const paneRegistrationRef = useRef(0);
	// A command that waits across renders must select with the latest callback,
	// which knows the terminals listed by then.
	const selectTabRef = useRef(selectTab);
	selectTabRef.current = selectTab;
	const paneCommandRef = useRef<PaneCommandHandler>(async () => describeSessionPanesRef.current());
	paneCommandRef.current = async (request) => {
		if (!splitPanes) {
			throw new PaneCommandError("SPLIT_PANES_OFF", "Split panes is off in the AO window. Turn it on in Settings → General (Developer Mode).");
		}
		const tab = request.tab?.trim() ?? "";
		const requireTab = (key: string) => {
			if (!splittableKeys.has(key)) throw new PaneCommandError("TAB_NOT_FOUND", `This session has no tab "${key}". Run \`ao pane\` to list its tab keys.`);
		};
		if (request.action === "split") {
			const anchor = request.beside?.trim() || (splittableKeys.has(selectedTabKey) ? selectedTabKey : SESSION_TAB_KEY);
			requireTab(anchor);
			const side: SplitSide = request.side ?? "right";
			if (tab === "terminal") {
				if (isOrchestrator) throw new PaneCommandError("UNSUPPORTED", "The orchestrator's view does not open split terminals.");
				const opened = splitNewTerminalRef.current(side, anchor);
				if (!opened) throw new PaneCommandError("TERMINAL_NOT_OPENED", "The AO window could not open a terminal.");
				// A new shell is a placeholder until the daemon creates it. Wait for
				// the real terminal to be listed: its key is the one later commands
				// must use, and a failed creation must not read as success.
				let seen = false;
				let confirmed: string | undefined;
				for (let waited = 0; waited < 15_000 && !confirmed; waited += 100) {
					await new Promise((resolve) => setTimeout(resolve, 100));
					const keys = new Set(latestPaneLayout(sessionId)?.tabs.map((candidate) => candidate.key) ?? []);
					const real = adoptedShellHandle(opened);
					if (real && keys.has(real)) {
						confirmed = real;
					} else if (keys.has(opened)) {
						seen = true;
					} else if (!real && (seen || waited >= 1_000)) {
						throw new PaneCommandError("TERMINAL_NOT_OPENED", "The AO window could not open a terminal.");
					}
				}
				if (!confirmed) throw new PaneCommandError("TERMINAL_TIMEOUT", "The terminal did not open in time.");
				// Selection can still name the placeholder; the agent types into the
				// selected terminal, so point it at the real one.
				selectTabRef.current(confirmed);
			} else if (tab.startsWith("file:")) {
				const registration = paneRegistrationRef.current;
				const path = await resolvePaneFile(tab.slice("file:".length));
				if (anchor === `file:${path}`) throw new PaneCommandError("INVALID_PANE_COMMAND", "A tab cannot be split beside itself.");
				// Opening a file resets this view's selection, so it must still be
				// the view the command was sent to.
				if (paneRegistrationRef.current !== registration) {
					throw new PaneCommandError("VIEW_CHANGED", "The AO window moved to another session before the file could be split.");
				}
				openCenterFile(path);
				updateGroups((current) => splitBeside(current, anchor, `file:${path}`, side));
			} else {
				requireTab(tab);
				if (tab === anchor) throw new PaneCommandError("INVALID_PANE_COMMAND", "A tab cannot be split beside itself.");
				updateGroups((current) => splitBeside(current, anchor, tab, side));
				selectTab(tab);
			}
		} else if (request.action === "remove") {
			if (!groupContaining(groups, tab)) throw new PaneCommandError("NOT_IN_SPLIT", `"${tab}" is not in a split.`);
			updateGroups((current) => withoutTab(current, tab));
		} else if (request.action === "ungroup") {
			const groupId = request.group?.trim() || shownGroup?.id;
			if (!groupId || !groups.some((group) => group.id === groupId)) {
				throw new PaneCommandError("NO_SPLIT", groupId ? `This session has no split "${groupId}".` : "No split is on screen.");
			}
			updateGroups((current) => ungroupById(current, groupId));
		} else if (request.action === "close") {
			// Close a terminal as its tab's ✕ does, so the strip, the split and
			// the selection all follow. Other tabs are left to the user.
			if (!shellTerminals.some((shell) => shell.handleId === tab)) {
				throw new PaneCommandError("TAB_NOT_FOUND", `"${tab}" is not one of this session's terminal tabs.`);
			}
			// Wait for the daemon, so a refused close is the agent's error and the
			// layout it gets back no longer lists the terminal.
			if (groupContaining(groups, tab)) await closePaneRef.current(tab);
			else await closeShellTerminalAsync(tab);
		} else if (request.action === "focus") {
			requireTab(tab);
			selectTab(tab);
		} else {
			throw new PaneCommandError("INVALID_PANE_COMMAND", `Unknown pane action "${String(request.action)}".`);
		}
		// The new layout is reported once React has rendered the change.
		await new Promise((resolve) => setTimeout(resolve, 60));
		return latestPaneLayout(sessionId) ?? describeSessionPanesRef.current();
	};
	// A file named by an agent: workspace-relative, or absolute inside this
	// session's workspace.
	const resolvePaneFile = async (rawPath: string): Promise<string> => {
		if (session?.cloud) throw new PaneCommandError("UNSUPPORTED", "Files of a Cloud session are not split.");
		const reference = normalizeWorkspaceFileReference(rawPath);
		if (!isAbsoluteFileReference(reference)) {
			const data = await fetchWorkspaceFiles();
			return matchWorkspaceFilePath(rawPath, data.files ?? []);
		}
		const resolved = await resolveSessionWorkspacePath(sessionId, reference, hostId);
		if (resolved?.kind === "workspace") return resolved.path;
		throw new PaneCommandError("FILE_OUTSIDE_WORKSPACE", `${rawPath} is not in this session's workspace.`);
	};
	useEffect(() => {
		if (!paneRelayEnabled) return undefined;
		paneRegistrationRef.current += 1;
		const unregister = registerPaneSession(sessionId, (request) => paneCommandRef.current(request));
		return () => {
			paneRegistrationRef.current += 1;
			unregister();
		};
	}, [paneRelayEnabled, sessionId]);
	// Report after every render (only a changed layout is sent), and when a
	// terminal's agent changes, which this view does not render on.
	useEffect(() => reportSessionPanesRef.current());
	useEffect(() => (paneRelayEnabled ? useShellAgentStore.subscribe(() => reportSessionPanesRef.current()) : undefined), [paneRelayEnabled]);

	const mainWorkspaceTabs = useMemo(
		() => (groupWorkspaceTabs.length ? [...workspaceTabs, ...groupWorkspaceTabs] : workspaceTabs),
		[groupWorkspaceTabs, workspaceTabs],
	);
	useEffect(() => {
		if (!chatViewActive) return;
		const viewId = crypto.randomUUID();
		let left = false;
		let refreshed = false;
		let renewedAt = Date.now();
		let pending = Promise.resolve();
		const setViewActive = async (active: boolean) => {
			try {
				const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/chat-view", {
					params: { path: { sessionId } },
					body: { viewId, active },
				});
				if (error) throw error;
				if (active) renewedAt = Date.now();
				// Remote conversations already refresh every two seconds. A ready or
				// working chat hears every change through events, and a wake emits
				// one, so only a chat still waking or stopped is refreshed here;
				// refreshing a settled one reloaded every page it had on each return.
				if (active && !left && !refreshed && !hostId) {
					refreshed = true;
					if (!conversationSettled(queryClient, sessionId)) {
						void queryClient.invalidateQueries({ queryKey: conversationQueryKey(sessionId, hostId) });
					}
					void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
				}
			} finally {
				// Release a late registration too; leaving must not wait on wake.
				if (active && left) void setViewActive(false).catch(() => {});
			}
		};
		const renewView = () => {
			pending = pending.catch(() => {}).then(() => {
				return left ? undefined : setViewActive(true);
			});
			return pending;
		};
		const refreshAfterWakeError = () => {
			if (left || hostId) return;
			void queryClient.invalidateQueries({ queryKey: conversationQueryKey(sessionId, hostId) });
		};
		const leaseLapsed = () => Date.now() - renewedAt > CHAT_VIEW_LEASE_MS;
		void renewView().catch(refreshAfterWakeError);
		const renewal = window.setInterval(() => {
			if (document.hidden && leaseLapsed()) return;
			void renewView().catch(refreshAfterWakeError);
		}, 10_000);
		const renewOnReturn = () => {
			if (!document.hidden && leaseLapsed()) void renewView().catch(refreshAfterWakeError);
		};
		document.addEventListener("visibilitychange", renewOnReturn);
		return () => {
			left = true;
			window.clearInterval(renewal);
			document.removeEventListener("visibilitychange", renewOnReturn);
			void setViewActive(false).catch(() => {});
		};
	}, [chatViewActive, hostId, queryClient, sessionId]);
	// A session's tab menus hold the interface switch and, in a local terminal, its agent; Cloud cannot switch agent.
	const agentWorking = chatDraftBoundaries.length > 0 || session?.isTerminated === true ||
		session?.activity?.state === "active" || session?.activity?.state === "waiting_input" || session?.activity?.state === "blocked";
	// The agent rows apply while there is a switch to offer, or a local
	// terminal's agent to pick and no switch running.
	const sessionTabAgentMenu = useMemo<SessionTabAgentMenu | undefined>(() => {
		if (!session) return undefined;
		const picksAgent = !session.cloud && interfaceUi.target === "chat";
		if (!interfaceUi.switchAction && !(picksAgent && !interfaceUi.switchControl)) return undefined;
		return { session, hostId, blocked: agentWorking, picksAgent, switchTarget: interfaceUi.target, switchAction: interfaceUi.switchAction };
	}, [agentWorking, hostId, interfaceUi.switchAction, interfaceUi.switchControl, interfaceUi.target, session]);
	// Cmd+K offers the same switch for the session on screen.
	useEffect(() => {
		if (!session) return;
		const id = session.id;
		useSessionSwitchStore.getState().publish(id, sessionTabAgentMenu);
		return () => useSessionSwitchStore.getState().publish(id, undefined);
	}, [session?.id, sessionTabAgentMenu]);
	// The session tab always shows its ⋮ menu after the title; the other tabs
	// show a close button on hover. While an interface switch is requested or
	// running, the switch's status (cancel on hover) takes the ⋮'s place.
	const sessionTabActionPinned = Boolean(session);
	const sessionTabActions = !session
		? null
		: interfaceUi.switchPending
			? interfaceUi.switchControl
			: <SessionTabMenuButton key={uiSessionId} />;
	const sessionHeaderActions = (
		<div
			className="session-topbar-session-chrome workspace-topbar-actions flex shrink-0 items-center"
			data-compact-session-chrome="false"
		>
			{hostLabel ? <span className="max-w-40 truncate px-3 text-xs text-muted-foreground" title={hostId}>{hostLabel}</span> : null}
			{/* On a session shared with this person: what they can do in it, and through which team. */}
			{session ? <SessionAccessTag session={session} /> : null}
			{/* A shared project's pull requests and merge requests, on its orchestrator. */}
			{isOrchestrator && session?.cloud && routedWorkspace ? (
				<SharedProjectPanel hostId={hostId} orchestrator={session} sessions={routedWorkspace.sessions} />
			) : null}
			{/* Who else is here, or Share; only the session's own organization can share it, and only once WorkOS enables sharing for it. */}
			{session?.cloud && session.isTerminated !== true ? (
				<SessionPeopleControl
					orgId={session.cloud.orgId}
					projectId={session.workspaceId}
					sessionId={session.id}
					// A standalone agent shared with this person is its owner's to share.
					canShare={session.cloud.orgId === cloudOrgId && sessionOffering.features.includes(SHARE_FEATURE) &&
						!(sessionWorkspace?.standaloneAgent && sessionWorkspace.shared)}
					sessionName={session.title}
					projectName={sessionWorkspace?.name}
					standaloneAgent={sessionWorkspace?.standaloneAgent}
				/>
			) : null}
			<SessionTopbarActions route={sessionTopbarRoute} />
		</div>
	);

	// The pane shows one terminal at a time, so selecting a shell or the reviewer
	// takes the agent's terminal off screen while the route still points here.
	// Publish which one is showing: the notification runtime lives outside this
	// subtree and must not treat "on the session route" as "watching the agent".
	useEffect(() => {
		setVisibleTerminalKind(uiSessionId, reviewerChatId ? "reviewer" : routedTerminalTarget.kind);
		return () => clearVisibleTerminalKind(uiSessionId);
	}, [clearVisibleTerminalKind, reviewerChatId, routedTerminalTarget.kind, uiSessionId, setVisibleTerminalKind]);

	const prepareFilesInspector = useCallback(() => {
		if (browserOnly) return;
		setBrowserPopOutState({ sessionId, phase: "docked" });
		setFilesPoppedOut(false);
		setFilesChangedOnly(uiSessionId, true);
		transitionInspectorView("files");
		setInspectorOpenForSession(uiSessionId, true);
	}, [browserOnly, sessionId, uiSessionId, setFilesChangedOnly, setInspectorOpenForSession, transitionInspectorView]);

	const fetchWorkspaceFiles = useCallback(async () => {
		return queryClient.fetchQuery(
			sessionWorkspaceFilesQueryOptions(sessionId, t("files.error.loadWorkspace"), hostId),
		);
	}, [hostId, queryClient, sessionId, t]);

	const handleOpenArtifact = useCallback(
		(target: { feedback?: boolean; path: string }) => {
			setBrowserPopOutState({ sessionId, phase: "docked" });
			setFilesPoppedOut(false);
			transitionInspectorView("artifacts");
			setInspectorOpenForSession(uiSessionId, true);
			setArtifactRequestsBySession((current) => ({
				...current,
				[uiSessionId]: { feedback: target.feedback, path: target.path, key: (current[uiSessionId]?.key ?? 0) + 1 },
			}));
		},
		[sessionId, setInspectorOpenForSession, transitionInspectorView, uiSessionId],
	);

	// A deliverable the agent registers with `ao artifact add` while the user
	// watches opens in the Artifacts tab, whatever its file type; agents no
	// longer hand artifacts to the Browser panel. Artifacts already registered
	// when the session is entered are the baseline and never reveal anything.
	// While the user is watching a live page in Browser or reviewing Files,
	// docked or popped out, a registration only badges Artifacts instead of
	// taking the view, so an agent registering screenshots as it iterates on a
	// UI does not steal the running app.
	// A remote host's session can first show what the sidebar kept from an
	// earlier read, which can predate artifacts registered since. Fresh data is
	// authoritative at once; stale data is refetched on mount, and until that
	// read lands the baseline keeps absorbing what it sees. A cloud session's
	// artifact list loads after the session itself, so it waits for that too.
	// A local session whose listing failed reads as having none; the next good
	// read can then open an existing artifact, which is harmless.
	const registeredArtifactsBaselineRef = useRef<{ sessionId: string; keys: Set<string>; settled: boolean } | null>(null);
	const artifactFiles = session?.artifactFiles;
	const artifactListUnsettled = (Boolean(hostId) && remoteSessionQuery.isStale) || Boolean(session?.artifactFilesPending);
	useEffect(() => {
		if (!hasInspector) return;
		const registered = (artifactFiles ?? []).filter((file) => file.registered);
		const keyOf = (file: SessionArtifact) => file.id ?? file.path;
		const baseline = registeredArtifactsBaselineRef.current;
		if (!baseline || baseline.sessionId !== uiSessionId) {
			registeredArtifactsBaselineRef.current = { sessionId: uiSessionId, keys: new Set(registered.map(keyOf)), settled: !artifactListUnsettled };
			return;
		}
		if (!baseline.settled) {
			for (const file of registered) baseline.keys.add(keyOf(file));
			baseline.settled = !artifactListUnsettled;
			return;
		}
		// A rename keeps its id; a file without one counts as known under an old path.
		const added = registered.filter(
			(file) => !baseline.keys.has(keyOf(file)) && !(file.previousPaths ?? []).some((previous) => baseline.keys.has(previous)),
		);
		for (const file of registered) baseline.keys.add(keyOf(file));
		if (added.length === 0 || terminated) return;
		const inspector = useUiStore.getState().inspectorSessions[uiSessionId];
		const busy = filesPoppedOut || (browserPoppedOut && hasBrowserContent)
			|| Boolean(inspector?.isOpen && (inspector.view === "files" || (inspector.view === "browser" && hasBrowserContent)));
		const newest = added.reduce((latest, file) => ((Date.parse(file.updatedAt) || 0) > (Date.parse(latest.updatedAt) || 0) ? file : latest));
		if (busy) {
			setArtifactsUnseen(uiSessionId, true);
			// The badge sits on the rail, which a maximized Browser covers and a
			// screen reader does not announce; the toast stays above overlays and
			// is a polite status. One per session, so a burst of screenshots
			// replaces it rather than stacking.
			showGlobalToast(t("artifacts.newArtifact", { name: newest.name }), undefined, {
				tone: "info",
				dedupeKey: `artifact-registered:${uiSessionId}`,
				action: { label: t("artifacts.openNew"), run: () => handleOpenArtifact({ path: newest.path }) },
			});
			return;
		}
		handleOpenArtifact({ path: newest.path });
	}, [artifactFiles, browserPoppedOut, filesPoppedOut, handleOpenArtifact, hasBrowserContent, artifactListUnsettled, hasInspector, setArtifactsUnseen, showGlobalToast, t, terminated, uiSessionId]);

	// The toast's Open writes this session's inspector state through its own
	// closure, so it must not outlive the session it was raised for.
	useEffect(() => () => {
		const dedupeKey = `artifact-registered:${uiSessionId}`;
		const { dismissGlobalToast, globalToasts } = useUiStore.getState();
		for (const toast of globalToasts) if (toast.dedupeKey === dedupeKey) dismissGlobalToast(toast.nonce);
	}, [uiSessionId]);

	// A file written moments ago, or one past the artifact list cap, is not in
	// the cached session, and the Files inspector can only show listed
	// artifacts. Refresh once before deciding it cannot be opened.
	const revealResolvedArtifact = useCallback(
		async (path: string) => {
			const listed = (artifacts: SessionArtifact[] | undefined) => artifactAtPath(artifacts, path) !== undefined;
			if (!listed(session?.artifactFiles)) {
				await refreshWorkspaces();
				const refreshed = queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKeyForHost(hostId))
					?.flatMap((workspace) => workspace.sessions)
					.find((candidate) => candidate.id === sessionId);
				if (!listed(refreshed?.artifactFiles)) return false;
			}
			handleOpenArtifact({ path });
			return true;
		},
		[handleOpenArtifact, hostId, queryClient, refreshWorkspaces, session?.artifactFiles, sessionId],
	);

	const revealResolvedWorkspaceFile = useCallback(
		async (rawPath: string, options?: FileOpenOptions) => {
			const reference = normalizeWorkspaceFileReference(rawPath);
			if (!isAbsoluteFileReference(reference)) {
				const data = await fetchWorkspaceFiles();
				openCenterFile(matchWorkspaceFilePath(rawPath, data.files ?? []), options);
				return;
			}
			// The renderer is never told where a session's worktree lives, so the
			// daemon decides which root an absolute path belongs to.
			const resolved = await resolveSessionWorkspacePath(sessionId, reference, hostId);
			if (resolved?.kind === "workspace") {
				openCenterFile(resolved.path, options);
				return;
			}
			if (resolved?.kind === "artifact" && await revealResolvedArtifact(resolved.path)) return;
			// Outside the session, or the daemon could not say. Nothing shows the
			// path belongs here, so open it as written and let the file pane say
			// so, rather than a workspace file that happens to share its name.
			openCenterFile(reference, options);
		},
		[fetchWorkspaceFiles, hostId, openCenterFile, revealResolvedArtifact, sessionId],
	);

	const handleOpenFiles = useCallback(() => {
		markFileViewerPerformance("files-click");
		// Opens on the checked-out branch's default review: its uncommitted
		// changes, or its commits when the worktree is clean.
		resetFilesSelection(uiSessionId);
		prepareFilesInspector();
		void fetchWorkspaceFiles();
	}, [fetchWorkspaceFiles, prepareFilesInspector, resetFilesSelection, uiSessionId]);

	const handleOpenReviewFile = useCallback(
		(target: { line?: number; path: string }) => {
			void revealResolvedWorkspaceFile(target.path, { line: target.line, mode: "diff" });
		},
		[revealResolvedWorkspaceFile],
	);

	const handleOpenFile = useCallback(
		(path: string, line?: number) => {
			void revealResolvedWorkspaceFile(path, { line, mode: "file" });
		},
		[revealResolvedWorkspaceFile],
	);

	const handleArtifactRequestConsumed = useCallback((key: number) => {
		setArtifactRequestsBySession((current) => {
			const request = current[uiSessionId];
			if (!request || request.key !== key || !request.feedback) return current;
			return {
				...current,
				[uiSessionId]: { ...request, feedback: undefined },
			};
		});
	}, [uiSessionId]);
	useEffect(() => {
		if (!workspaceFileOpenRequest || !session) return;
		if (sessionUiKey(workspaceFileOpenRequest.sessionId, workspaceFileOpenRequest.hostId) !== uiSessionId) return;
		const { line, nonce, path, source } = workspaceFileOpenRequest;
		if (source === "artifact") {
			handleOpenArtifact({ path });
		} else if (session.cloud) {
			prepareFilesInspector();
			openCenterFile(path, { mode: "file" });
		} else {
			handleOpenFile(path, line);
		}
		clearWorkspaceFileOpenRequest(nonce);
	}, [
		clearWorkspaceFileOpenRequest,
		handleOpenArtifact,
		handleOpenFile,
		openCenterFile,
		prepareFilesInspector,
		session,
		uiSessionId,
		workspaceFileOpenRequest,
	]);

	const handleToggleFilesPopOut = useCallback(
		(next: boolean) => {
			if (next) setBrowserPopOutState({ sessionId, phase: "docked" });
			setFilesPoppedOut(next);
			transitionInspectorView("files");
			setInspectorOpenForSession(uiSessionId, true);
		},
		[sessionId, uiSessionId, setInspectorOpenForSession, transitionInspectorView],
	);

	const handleToggleBrowserPopOut = useCallback(
		(next: boolean) => {
			if (next) setFilesPoppedOut(false);
			setBrowserPopOutState((current) => {
				if (next) {
					if (current.sessionId === sessionId && current.phase !== "docked") return current;
					return { sessionId, phase: "mounting" };
				}
				if (current.sessionId !== sessionId || current.phase === "docked") return current;
				return { sessionId, phase: "docked" };
			});
		},
		[sessionId],
	);

	useEffect(() => {
		if (!hasInspector) return;
		const current = useUiStore.getState().inspectorSessions[uiSessionId];
		if (browserOnly) {
			if (terminated && current?.browserUnseen) setBrowserUnseen(uiSessionId, false);
			return;
		}
		if (!hasBrowserContent) {
			if (current?.browserContentRevealed) setBrowserContentRevealed(uiSessionId, false);
			else if (current?.browserUnseen) setBrowserUnseen(uiSessionId, false);
			return;
		}
		if (current?.browserContentRevealed) return;
		setBrowserContentRevealed(uiSessionId, true);
	}, [
		hasBrowserContent,
		browserOnly,
		hasInspector,
		previewRevision,
		uiSessionId,
		setBrowserContentRevealed,
		setBrowserUnseen,
		terminated,
	]);

	const revealBrowserPanel = useCallback(() => {
		setBrowserContentRevealed(uiSessionId, true);
		setBrowserUnseen(uiSessionId, false);
		if (browserIsVisible(uiSessionId, browserPoppedOut)) return;
		setFilesPoppedOut(false);
		transitionInspectorView("browser");
		setInspectorOpenForSession(uiSessionId, true);
	}, [browserPoppedOut, uiSessionId, setBrowserContentRevealed, setBrowserUnseen, transitionInspectorView, setInspectorOpenForSession]);

	useEffect(() => {
		if (!hasInspector) return;
		const previewKey = previewRevealKey(previewUrl, previewRevision);
		const baseline = previewBaselineRef.current;
		if (!baseline || baseline.sessionId !== uiSessionId) {
			previewBaselineRef.current = { sessionId: uiSessionId, key: previewKey };
			return;
		}
		if (baseline.key === previewKey) return;
		previewBaselineRef.current = { sessionId: uiSessionId, key: previewKey };
		if (!previewKey || terminated) return;
		// Only the routed session mounts SessionView. A new explicit preview
		// reveals its panel; entering a session with an existing target does not.
		revealBrowserPanel();
	}, [
		hasInspector,
		previewRevision,
		previewUrl,
		uiSessionId,
		terminated,
		revealBrowserPanel,
	]);

	// Explicit open commands reveal once per command. A fast command's start
	// and finish can share a React render, so either event can consume its ID.
	// Later completion and UI changes must not override a user's close. Only a
	// command whose start arrived while this session was on screen counts: one
	// begun while the user was elsewhere would otherwise pop Browser open a
	// moment after they switch in.
	useEffect(() => {
		if (revealedBrowserCommandsRef.current.sessionId !== uiSessionId) {
			revealedBrowserCommandsRef.current = { sessionId: uiSessionId, commands: new Set() };
		}
		if (!hasInspector || terminated) return;
		const activity = browserView.agentBrowserActivity;
		if (activity && BROWSER_REVEAL_ACTIONS.has(activity.action)) {
			if (!activity.commandId || activity.viewId !== browserView.viewId || !activity.startObserved) {
				if (browserView.agentBrowserActive && !browserIsVisible(uiSessionId, browserPoppedOut)) setBrowserUnseen(uiSessionId, true);
				return;
			}
			const commands = revealedBrowserCommandsRef.current.commands;
			if (commands.has(activity.commandId)) return;
			commands.add(activity.commandId);
			// Bound deduplication memory while retaining recent overlapping commands.
			if (commands.size > 64) commands.delete(commands.values().next().value!);
			revealBrowserPanel();
			return;
		}
		if (!browserView.agentBrowserActive) return;
		// Passive activity only badges Browser, including an orchestrator's.
		if (browserOnly && !useUiStore.getState().inspectorSessions[uiSessionId]?.browserContentRevealed) {
			setBrowserContentRevealed(uiSessionId, true);
		}
		if (!browserIsVisible(uiSessionId, browserPoppedOut)) setBrowserUnseen(uiSessionId, true);
	}, [
		browserPoppedOut,
		browserView.agentBrowserActive,
		browserView.agentBrowserActivity,
		browserView.viewId,
		hasInspector,
		inspectorView,
		isInspectorOpen,
		uiSessionId,
		setBrowserUnseen,
		terminated,
		browserOnly,
		setBrowserContentRevealed,
		revealBrowserPanel,
	]);

	// Opening Browser consumes the pending activity indicator, including the
	// case where the inspector was collapsed while already parked on Browser.
	useEffect(() => {
		if (hasInspector && browserIsVisible(uiSessionId, browserPoppedOut)) {
			setBrowserUnseen(uiSessionId, false);
		}
	}, [browserPoppedOut, hasInspector, inspectorView, isInspectorOpen, uiSessionId, setBrowserUnseen]);

	const handleToggleInspector = useCallback(() => {
		if (browserOnly && inspectorView === "browser") setBrowserContentRevealed(uiSessionId, true);
		toggleInspector(uiSessionId);
	}, [browserOnly, inspectorView, uiSessionId, toggleInspector, setBrowserContentRevealed]);

	// Keyboard commands scoped to the session on screen. The main process matches
	// the chord (see main/app-shortcuts.ts), so these also work from xterm and the
	// native Browser view.
	useSessionShortcut("toggle-inspector", handleToggleInspector, hasInspector);
	useSessionMenuAvailable("toggle-inspector", hasInspector);
	useSessionMenuAvailable("new-shell-terminal", Boolean(session) && !isOrchestrator);
	useSessionShortcut(
		"toggle-diff",
		() => {
			if (browserOnly) return;
			const showingDiff =
				isInspectorOpen &&
				inspectorView === "files" &&
				(useUiStore.getState().inspectorSessions[uiSessionId]?.filesChangedOnly ?? true);
			if (showingDiff) setInspectorOpenForSession(uiSessionId, false);
			else prepareFilesInspector();
		},
		hasInspector,
	);
	useSessionShortcut(
		"toggle-terminal",
		() => {
			if (terminalTarget.kind === "shell") {
				selectSessionTerminal();
				focusAfterPaint("[aria-label='Message the agent']");
				return;
			}
			const shell = shellTerminals[shellTerminals.length - 1];
			if (shell) selectShellTerminal(shell.handleId);
			else if (!isOrchestrator) addShellTerminal();
			focusAfterPaint(".xterm-helper-textarea");
		},
		hasInspector,
	);
	useSessionShortcut(
		"reopen-closed-tab",
		() => {
			const path = closedFilePathsRef.current[uiSessionId]?.pop();
			if (path) {const request=centerFileRequestsBySession[uiSessionId]?.[path];openCenterFile(request?.path ?? path, { ...request, editing:false, mode: "file" });}
		},
		hasInspector,
	);
	useSessionShortcut(
		"copy-session-path",
		() => {
			if (!session || session.cloud || hostId) return;
			aoBridge.editorHandoff.copyWorkspacePath(sessionId).then(
				() => showGlobalToast(t("shortcut.sessionPathCopied")),
				(error: unknown) =>
					showGlobalToast(t("shortcut.sessionPathCopyFailed"), error instanceof Error ? error.message : undefined, "error"),
			);
		},
		hasInspector,
	);
	useSessionShortcut("model-picker", openComposerModelPicker, hasInspector);
	useSessionShortcut("dictate", toggleComposerDictation, hasInspector);

	const inspectorMotionReadyRef = useRef<string | null>(null);
	const handleInspectorCloseAnimationComplete = useCallback(() => {
		setInspectorSettledClosed(true);
	}, []);
	useLayoutEffect(() => {
		if (!hasInspector) {
			setInspectorSettledClosed(true);
			stopTerminalLiveResize();
			return;
		}
		if (inspectorMotionReadyRef.current !== uiSessionId) {
			setInspectorSettledClosed(!isInspectorOpen);
			stopTerminalLiveResize();
			if (workspaceResizeTimerRef.current !== null) window.clearTimeout(workspaceResizeTimerRef.current);
			workspaceResizeTimerRef.current = null;
			sessionSplitRef.current?.removeAttribute("data-workspace-resizing");
			browserEntryWidthFloorRef.current = null;
		}
	}, [hasInspector, isInspectorOpen, uiSessionId, stopTerminalLiveResize]);
	useEffect(() => {
		if (!hasInspector || inspectorMotionReadyRef.current !== uiSessionId) return;
		if (isInspectorOpen) {
			setInspectorSettledClosed(false);
			const groupWidth = sessionSplitRef.current?.clientWidth || window.innerWidth;
			const availableWidth = Math.max(0, groupWidth - INSPECTOR_SEPARATOR_RESERVE_PX);
			const targetInspectorWidth = Number.parseFloat(initialInspectorSize(sizing, availableWidth));
			startTerminalLiveResize(
				targetInspectorWidth <= INSPECTOR_COMPACT_MAX_PX ? "compact" : "expanded",
				topbarSecondaryLabelMode(Math.max(0, availableWidth - targetInspectorWidth)),
			);
			return;
		}
		const groupWidth = sessionSplitRef.current?.clientWidth || window.innerWidth;
		startTerminalLiveResize("expanded", topbarSecondaryLabelMode(groupWidth));
	}, [hasInspector, isInspectorOpen, uiSessionId, sizing, startTerminalLiveResize]);
	useEffect(() => {
		if (!hasInspector) {
			inspectorMotionReadyRef.current = null;
			return;
		}
		inspectorMotionReadyRef.current = uiSessionId;
		return () => {
			inspectorMotionReadyRef.current = null;
		};
	}, [hasInspector, uiSessionId]);
	// A Cloud tab may arrive before the paginated workspace cache contains its
	// row. Keep the session surface (and its switch control) mounted while the
	// direct control-plane lookup is in flight; only show "not found" after
	// both sources have settled.
	if (!session && (hostId ? !remoteSessionQuery.isLoading : !workspaceQuery.isLoading && !workspaceSessionQuery.isLoading && !cloudSessionResolving)) {
		const remoteError = hostId ? t(remoteCode === "BAD_PASSWORD" ? "remote.hostUnauthorized"
			: remoteCode === "HOST_API_INCOMPATIBLE" ? "remote.hostIncompatible"
			: hostUnreachable
				? "remote.hostOffline" : remoteSessionQuery.isError ? "remote.loadSessionFailed" : "session.notFound") : undefined;
		// A shared session's route names no organization, and once its sharing
		// ends it is gone from the shared list too: it reads as shared because
		// the list showed it earlier in this run.
		const routeError = cloudRouteSession.error;
		const noLongerShared = !hostId && (
			sessionWasSharedWithYou(cloudCpBaseUrl, cloudCpUserId, sessionId) ||
			(Boolean(routeCloudOrgId) && (
				(ownCloudOrg !== undefined && routeCloudOrgId !== ownCloudOrg.id) ||
				(routeError instanceof CloudCpError && routeError.status === 403)
			))
		);
		return (
			<div className="flex h-full min-h-0 flex-col bg-background" data-testid="session-missing">
				<PageTopbar />
				<div className="grid min-h-0 flex-1 place-items-center p-6 text-center font-mono text-xs text-passive" role={remoteError ? "alert" : undefined}>
					{remoteError ?? t(noLongerShared ? "session.noLongerShared" : "session.notFound")}
				</div>
			</div>
		);
	}

	return (
		<div className="relative flex h-full min-h-0 flex-col bg-background text-foreground" data-testid="session-detail">
			{!hostId && !session?.cloud && session?.mode !== "chat" && sessionAgentExited(session) && autoResume.variables === sessionId && autoResume.isError ? (
				<p className="px-4 py-2 text-xs text-error" role="alert">
					{apiErrorMessage(autoResume.error)}
				</p>
			) : null}
			<div
				className="session-split relative flex min-h-0 flex-1 overflow-hidden"
				data-testid="panel-group"
				data-workspace-mode={sizing.mode}
				id={SESSION_WORKSPACE_ID}
				ref={sessionSplitRef}
				style={
					{
						"--session-inspector-max-width": inspectorMaxWidthCss(
							sizing.maxPercent,
							sizing.chatMinWidth,
						),
						"--session-inspector-motion-duration": `${INSPECTOR_SPRING_MS}ms`,
						"--session-inspector-motion-easing": INSPECTOR_SPRING_EASING,
					} as CSSProperties
				}
			>
				<div
					className="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden"
					data-panel=""
					id="terminal"
				>
					<div className="relative flex h-full min-h-0 flex-col" ref={centerColumnRef}>
						<SessionTopbarHost
							className="relative z-chrome flex h-toolbar w-full shrink-0 overflow-hidden"
							data-testid="session-topbar-host"
						/>
						<div
							className="relative min-h-0 flex-1"
							data-split-group={shownGroup?.id}
							data-testid="center-split-area"
							ref={splitScreenRef}
							style={shownLayout?.vars as CSSProperties | undefined}
						>
							{isCloudPausedStage(cloudStage) ? <CloudPausedStatus stage={cloudStage} /> : chatWaking ? <CloudPausedStatus stage="paused" /> : null}
							{/* In Chat, a failed checkout or Setup shows in the conversation, as a failed local start does. */}
							{startupFailure && session?.cloud && !(showChatSurface && startupFailure.phase === "failed" && isWorkspaceStartupError(startupFailure.code)) ? (
								<CloudSessionStartupError coderWorkspaceUrl={session.cloud.coderWorkspaceUrl} ended={terminated} orgId={session.cloud.orgId} problem={startupFailure} sessionId={session.id} />
							) : null}
							{/* The committed mode owns the agent surface. Auxiliary shell and
							    reviewer targets remain terminal surfaces in either mode. */}
							<SplitCloseFocusedContext.Provider value={shownGroup ? closeFocusedPane : undefined}>
							<SplitHiddenTabKeysContext.Provider value={groupedKeySet}>
							<TabSplitMenuContext.Provider value={splitTabMenu}>
							<SessionTabAgentMenuContext.Provider value={sessionTabAgentMenu}>
							<div
								className={cn("min-h-0 min-w-0 overflow-hidden", (mainActiveFilePath || mainReviewerChatId || sessionPaneHidden || activePRUrl) && "invisible pointer-events-none")}
								data-split-pane={SESSION_TAB_KEY}
								inert={mainActiveFilePath || mainReviewerChatId || sessionPaneHidden || activePRUrl ? true : undefined}
								style={paneStyle(SESSION_TAB_KEY)}
							>
							{/* Every Cloud Chat target stays on the Cloud surface: the local one
							    asks this machine's daemon, which has never heard of a Cloud
							    session and answers "session not found". */}
							{showChatSurface && session?.cloud ? (
								<>
								<CloudSessionChatSurface
									controllerTransitioning={interfaceUi.controllerTransitioning}
									headerActions={sessionHeaderActions}
									newWorkDisabled={interfaceUi.newWorkDisabled}
									onConversationWorkChange={interfaceUi.onConversationWorkChange}
									onOpenFiles={browserOnly ? undefined : prepareFilesInspector}
									onOpenFile={openCenterFile}
									onOpenArtifact={hasInspector ? handleOpenArtifact : undefined}
									session={session}
									sessionTabAction={sessionTabActions}
									sessionTabActionPinned={sessionTabActionPinned}
									reviewerTerminal={reviewerTerminal}
									onOpenReviewerTerminal={selectReviewerTerminal}
									reviewerTarget={routedTerminalTarget.kind === "reviewer" ? routedTerminalTarget : undefined}
									onSelectChat={selectSessionTerminal}
									shellTerminals={shellTerminals}
									shellTarget={routedTerminalTarget.kind === "shell" ? routedTerminalTarget : undefined}
									onSelectShellTerminal={selectShellTerminal}
									onCloseShellTerminal={closeShellTerminalByHandle}
									onRenameShellTerminal={renameShellTerminalByHandle}
									daemonReady={hostId ? Boolean(remoteBase) : daemonStatus.state === "ready"}
									theme={theme}
									tabStripAction={newShellTerminalAction}
									workspaceTabs={mainWorkspaceTabs}
									workspaceActiveTabKey={activeWorkspaceTabKey}
									workspaceFileActive={Boolean(mainActiveFilePath)}
									auxiliaryTabOrder={mainAuxiliaryTabOrder}
									onAuxiliaryTabOrderChange={setMainAuxiliaryTabOrder}
									reviewerChat={reviewerChat}
									reviewerChatSelected={Boolean(mainReviewerChatId)}
									onOpenReviewerChat={(target) => selectReviewerChat(target.reviewId)}
									onOpenLinkInBrowser={browserView.openLink}
								/>
								</>
							) : showChatSurface ? (
								<>
								<SessionChatSurface
									sideChatContainer={sideChatContainer}
									key={uiSessionId}
									assetBaseUrl={remoteBase}
									hostId={hostId}
									session={session}
									reviewerTerminal={reviewerTerminal}
									reviewerChat={reviewerChat}
									reviewerChatSelected={Boolean(mainReviewerChatId)}
									onOpenReviewerTerminal={selectReviewerTerminal}
									onOpenReviewerChat={(target) => selectReviewerChat(target.reviewId)}
									onSessionRenamed={refreshWorkspaces}
									reviewerTarget={
										routedTerminalTarget.kind === "reviewer" ? routedTerminalTarget : undefined
									}
									onSelectChat={selectSessionTerminal}
									shellTerminals={shellTerminals}
									shellTarget={
										routedTerminalTarget.kind === "shell" ? routedTerminalTarget : undefined
									}
									onSelectShellTerminal={selectShellTerminal}
									onCloseShellTerminal={closeShellTerminalByHandle}
									onRenameShellTerminal={renameShellTerminalByHandle}
									daemonReady={hostId ? !hostUnreachable : daemonStatus.state === "ready"}
									theme={theme}
									headerActions={sessionHeaderActions}
									sessionTabAction={sessionTabActions}
									sessionTabActionPinned={sessionTabActionPinned}
									tabStripAction={newShellTerminalAction}
									workspaceTabs={mainWorkspaceTabs}
									workspaceActiveTabKey={activeWorkspaceTabKey}
									workspaceFileActive={Boolean(mainActiveFilePath)}
									auxiliaryTabOrder={mainAuxiliaryTabOrder}
									onAuxiliaryTabOrderChange={setMainAuxiliaryTabOrder}
									controllerResumeError={autoResumeFailed ? apiErrorMessage(autoResume.error) : undefined}
									controllerResumeOpenElsewhere={autoResumeFailed && resumeOpenElsewhere(autoResume.error)}
									controllerTransitioning={interfaceUi.controllerTransitioning}
									accountMoving={Boolean(interfaceUi.accountMoveTarget)}
									accountMoveQueues={interfaceUi.accountMoveQueues}
									arriving={interfaceUi.optimisticTarget === "chat"}
									agentResuming={quietResume}
									newWorkDisabled={interfaceUi.newWorkDisabled}
									onConversationWorkChange={interfaceUi.onConversationWorkChange}
									interfaceSwitch={{ control: interfaceUi.switchControl, errorNotice: interfaceUi.switchErrorNotice }}
									onOpenShell={addShellTerminal}
									openingShell={openShellTerminal.isPending}
									shellError={
										openShellTerminal.error ? apiErrorMessage(openShellTerminal.error) : undefined
									}
									onOpenFiles={browserOnly ? undefined : handleOpenFiles}
									onOpenFile={handleOpenFile}
									onOpenArtifact={hasInspector ? handleOpenArtifact : undefined}
         onOpenFileAnnotation={(reference,resolution)=>{
          if(reference.sessionId!==sessionId || resolution.state==="unavailable")return;
          const path=resolution.path??reference.path;
          const source: NonNullable<FileOpenOptions["source"]> = reference.source.kind==="pull_request"
           ? {kind:"pull_request",number:reference.source.number!,url:reference.source.url??"",label:`PR #${reference.source.number}`,snapshot:reference.source.snapshot}
           : reference.source.kind==="artifact"
           ? {kind:"artifact",rawUrl:`${(hostId?baseUrlForHost(hostId):getApiBaseUrl())}/api/v1/sessions/${encodeURIComponent(sessionId)}/artifact-files/${path.split("/").map(encodeURIComponent).join("/")}?raw=true`}
           : {kind:"workspace"};
          openCenterFile(path,{source,previousPath:reference.previousPath,commitSha:reference.commitSha,scope:reference.scope as FileOpenOptions["scope"],mode:isFileViewCapture(reference)?"file":"diff",annotationRange:resolution.state==="changed" ? undefined : {...reference,line:resolution.line??reference.line,endLine:resolution.endLine??reference.endLine,lines:reference.lines.map(row=>({...row,oldNo:row.oldNo==null?null:row.oldNo+(resolution.oldLineOffset??0),newNo:row.newNo==null?null:row.newNo+(resolution.newLineOffset??0)})),requestKey:0}});
         }}
									onOpenLinkInBrowser={browserView.openLink}
								/>
								</>
							) : (
								<CenterPane
									hostId={hostId}
									agentInputDisabled={interfaceUi.agentInputDisabled}
									terminalBooting={interfaceUi.optimisticTarget === "tui"}
									terminalSwitchingLabel={accountSwitchingLabel}
									daemonReady={hostId ? Boolean(remoteBase) : daemonStatus.state === "ready"}
									onCloseShellTerminal={closeShellTerminalByHandle}
									onRenameShellTerminal={renameShellTerminalByHandle}
									onSelectSessionTerminal={selectSessionTerminal}
									onSessionTerminalAttached={onSessionTerminalAttached}
									onSelectReviewerTerminal={selectReviewerTerminal}
									onSelectReviewerChat={(target) => selectReviewerChat(target.reviewId)}
									onSelectShellTerminal={selectShellTerminal}
									reviewerTerminal={reviewerTerminal}
									reviewerChat={reviewerChat}
									reviewerChatSelected={Boolean(mainReviewerChatId)}
									session={session}
									shellTerminals={shellTerminals}
									terminalTarget={routedTerminalTarget}
									theme={theme}
									topbarActions={sessionHeaderActions}
									sessionTabAction={sessionTabActions}
									sessionTabActionPinned={sessionTabActionPinned}
									tabStripAction={newShellTerminalAction}
									workspaceTabs={mainWorkspaceTabs}
									workspaceActiveTabKey={activeWorkspaceTabKey}
									auxiliaryTabOrder={mainAuxiliaryTabOrder}
									onAuxiliaryTabOrderChange={setMainAuxiliaryTabOrder}
								/>
							)}
							</div>
							</SessionTabAgentMenuContext.Provider>
							</TabSplitMenuContext.Provider>
							</SplitHiddenTabKeysContext.Provider>
							</SplitCloseFocusedContext.Provider>
							{shownGroup
								? leafKeys(shownGroup.root)
									.filter((key) => key !== SESSION_TAB_KEY && !key.startsWith("file:") && !key.startsWith("reviewer-chat:"))
									.map((key) => {
										const target = paneTerminalTarget(key);
										return (
											<div
												className="min-h-0 min-w-0 overflow-hidden bg-background"
												data-split-pane={key}
												key={key}
												style={paneStyle(key)}
											>
												{target ? (
													hostId && !remoteBase ? null : (
														<SplitPaneTerminal
															createMux={hostId ? paneCreateMux : undefined}
															daemonReady={paneDaemonReady}
															focusRequested={selectedTabKey === key}
															session={session}
															target={target}
															theme={theme}
														/>
													)
												) : null}
											</div>
										);
									})
								: null}
							{retainedReviewerChatId && reviewerPaneKey ? (
								<div
									className={cn("min-h-0 min-w-0 overflow-hidden bg-background", !reviewerVisible && "invisible pointer-events-none [content-visibility:hidden]")}
									data-split-pane={reviewerInPane ? reviewerPaneKey : undefined}
									inert={!reviewerVisible ? true : undefined}
									key={`reviewer-chat:${retainedReviewerChatId}`}
									style={{ ...(reviewerInPane ? paneStyle(reviewerPaneKey) : FULL_PANE_STYLE), visibility: reviewerVisible ? undefined : "hidden" }}
								>
									{renderReviewerChat(retainedReviewerChatId)}
								</div>
							) : null}
							{(session?.cloud ? Boolean(mainActiveFilePath) : fileTabs.openPaths.length > 0) ? (
								<div className="pointer-events-none absolute inset-0">
					{session?.cloud ? (
						<div className="pointer-events-auto" style={FULL_PANE_STYLE}>
						<CloudFileContentPane
							annotation={fileAnnotation}
							commitSha={activeCenterFileRequest?.commitSha}
							initialEditing={activeCenterFileInitialEditing}
							initialLine={activeCenterFileInitialLine}
							initialMode={activeCenterFileRequest?.mode ?? "file"}
							initialRequestKey={activeCenterFileRequest?.key ?? 0}
							onDirtyChange={setCenterFileDirty}
							path={mainActiveFilePath!}
							// Uncommitted is local-only; a cloud review's nearest scope is combined.
							scope={activeCenterFileRequest?.scope === "uncommitted" ? "combined" : activeCenterFileRequest?.scope}
							session={session}
							split={filesSplit}
						/>
						</div>
									) : (
										<>
											{fileTabs.openPaths.map((fileKey) => {
												const request = centerFileRequestsBySession[uiSessionId]?.[fileKey];
												const token = request ? `${uiSessionId}:${fileKey}:${request.key}` : "";
												// A file view stays mounted here and is only placed over its pane.
												const paneKey = `file:${fileKey}`;
												const inPane = Boolean(shownLayout?.leaves.has(paneKey));
												const visible = inPane || mainActiveFilePath === fileKey;
												return (
													<div
														key={fileKey}
														className={cn(visible ? "pointer-events-auto overflow-hidden" : "invisible pointer-events-none [content-visibility:hidden]")}
														data-split-pane={inPane ? paneKey : undefined}
														inert={!visible ? true : undefined}
														style={inPane ? paneStyle(paneKey) : FULL_PANE_STYLE}
													>
														<SessionFileWorkspace
															active={visible && !filesPoppedOut}
															focusRequest={paneFocusRequests[paneKey] ?? 0}
															fileKey={fileKey}
															hostId={hostId}
															source={request?.source} annotationRange={request?.annotationRange}
															previousPath={request?.previousPath}
															annotation={fileAnnotation}
															commitSha={request?.commitSha}
															initialEditing={Boolean(request?.editing && !consumedCenterEditingRequestsRef.current.has(token))}
															initialLine={request?.line != null && !consumedCenterLineRequestsRef.current.has(token) ? request.line : undefined}
															initialMode={request?.mode ?? "file"}
															initialRequestKey={request?.key ?? 0}
															onDirtyChange={setCenterFileDirty}
															onInitialEditingConsumed={markCenterFileEditingConsumed}
															onInitialLineConsumed={markCenterFileLineConsumed}
															path={request?.path ?? fileKey}
															sessionId={sessionId}
															split={filesSplit}
															scope={request?.scope}
														/>
													</div>
												);
											})}
										</>									)}
								</div>
							) : null}
							{/* Every pane of a split has a title bar: it names the pane as the
							    tab it is, is the handle to drag it. */}
							{shownGroup && shownLayout
								? [...shownLayout.leaves.entries()].map(([key, box]) => (
									<SplitPaneHeader
										closable={key !== SESSION_TAB_KEY && !key.startsWith("reviewer")}
										focused={key === selectedTabKey}
										key={`header:${key}`}
										member={groupMember(key)}
										menu={{ kind: "group", onUngroup: () => updateGroups((current) => ungroupById(current, shownGroup.id)) }}
										onPress={() => {
											setPaneFocusRequests((current) => ({ ...current, [key]: (current[key] ?? 0) + 1 }));
											// A terminal or chat composer only autofocuses when it becomes
											// selected, so a press on a selected pane puts the caret in it.
											requestAnimationFrame(() => {
												for (const pane of document.querySelectorAll<HTMLElement>("[data-split-pane]")) {
													if (pane.dataset.splitPane === key) pane.querySelector<HTMLElement>(".xterm-helper-textarea, [contenteditable='true'][role='combobox']")?.focus({ preventScroll: true });
												}
											});
										}}
										// The top row reaches 2px above the screen so no sliver of the
										// panel shows over its title bar.
										style={box.top === "0%"
											? { left: box.left, top: -2, width: box.width, height: PANE_HEADER_PX + 2, paddingTop: 2 }
											: { left: box.left, top: box.top, width: box.width, height: PANE_HEADER_PX }}
									/>
								))
								: null}
							{shownGroup && shownLayout
								? shownLayout.dividers.map((divider) => (
									<SplitDivider
										divider={divider}
										key={`${shownGroup.id}:${divider.path}`}
										onResize={resizeSplit}
										screenRef={splitScreenRef}
									/>
								))
								: null}
							{/* Like a file tab, a PR's tab stays mounted behind the others: coming back finds the same section, scroll and half-written comment. */}
							{session && !session.cloud ? prTabs.open.map((url) => (
								<div className={cn("absolute inset-0 z-10", url !== activePRUrl && "invisible pointer-events-none [content-visibility:hidden]")} key={url}>
									<PRTabPane
										hostId={hostId}
										onViewInDiff={handleOpenReviewFile}
										request={prTabs.request?.url === url ? prTabs.request : undefined}
										session={session}
										url={url}
									/>
								</div>
							)) : null}
							{shownGroup && shownLayout
								? findJunctions(shownLayout.dividers).map(({ row, column }) => (
									<SplitJunction
										column={column}
										key={`${shownGroup.id}:${row.path}:${column.path}`}
										onResize={resizeSplit}
										row={row}
										screenRef={splitScreenRef}
									/>
								))
								: null}
							{interfaceUi.notice}
						</div>
					</div>
				</div>
				{hasInspector ? (
					<SessionInspectorRail
						sessionKey={uiSessionId}
						isOpen={isInspectorOpen}
						onCloseAnimationComplete={handleInspectorCloseAnimationComplete}
						restoreMinWidth={
							sizing.mode === "browser" ? (browserEntryWidthFloorRef.current ?? undefined) : undefined
						}
						sizing={sizing}
						settledClosed={!isInspectorOpen && inspectorSettledClosed}
						splitRef={sessionSplitRef}
					>
						<SessionInspector
							sideChatView={sideChatsAvailable ? <div ref={setSideChatContainer} className="h-full min-h-0 flex-1" /> : undefined}
							hostId={hostId}
							orchestrator={browserOnly}
							browserAnnotationQueue={inspectorView === "browser" ? browserAnnotationQueue : undefined}
							browserPoppedOut={browserPoppedOut}
							filesView={
								inspectorView === "files" && session ? (
									session.cloud ? (
										<CloudWorkspaceDiff annotation={fileAnnotation} onOpenFile={openCenterFile} onSplitChange={setFilesSplit} onToggleMaximized={handleToggleFilesPopOut} session={session} split={filesSplit} />
									) : (
										<SessionFileExplorer
											hostId={hostId}
											onOpenFile={openCenterFile}
											onSplitChange={setFilesSplit}
											onToggleMaximized={handleToggleFilesPopOut}
											checkedOutBranch={session.branch}
											projectKind={filesKind}
											sessionId={session.id}
											split={filesSplit}
										/>
									)
								) : null
							}
							isInspectorVisible={inspectorPanelVisible}
							artifactRequest={artifactRequestsBySession[uiSessionId] ?? null}
							onArtifactRequestConsumed={handleArtifactRequestConsumed}
							onOpenArtifact={handleOpenArtifact}
							onOpenFiles={browserOnly ? undefined : handleOpenFiles}
							onOpenReviewFile={handleOpenReviewFile}
							onOpenReviewerTerminal={selectReviewerTerminal}
							onOpenReviewerChat={selectReviewerChat}
							onWorkerMessageSent={showChatSurface || reviewerChatId ? selectSessionTerminal : undefined}
							onToggleBrowserPopOut={handleToggleBrowserPopOut}
							onViewChange={transitionInspectorView}
							view={inspectorView}
							browserView={hostId || inspectorView === "browser" ? browserView : undefined}
							session={session}
						/>
					</SessionInspectorRail>
				) : null}
			</div>
			<PageTopbarTrailingAction>
				{hasInspector ? <Tooltip>
					<TooltipTrigger asChild>
						<Button type="button" size="icon-sm"
							aria-label={isInspectorOpen ? t("shell.closeInspector") : t("shell.openInspector")}
							aria-pressed={isInspectorOpen}
							onClick={() => {
								captureTopbarAction("inspector", isOrchestrator ? "orchestrator" : "worker");
								handleToggleInspector();
							}}
							style={noDragStyle}
							variant="quiet"
						>
							<span className="relative inline-flex">
								<PanelRight className="size-icon-md" aria-hidden="true" />
								{browserOnly && !isInspectorOpen && browserUnseen ? (
									<span
										aria-hidden="true"
										className="pointer-events-none absolute -right-0.5 -top-0.5 size-1.5 rounded-full bg-primary ring-2 ring-background"
										data-testid="orchestrator-browser-unseen-indicator"
									/>
								) : null}
							</span>
						</Button>
					</TooltipTrigger>
					<TooltipContent shortcut="toggle-inspector" side="bottom">
						{isInspectorOpen ? t("shell.closeInspector") : t("shell.openInspector")}
					</TooltipContent>
				</Tooltip> : null}
			</PageTopbarTrailingAction>
			{interfaceUi.cloudLoader
				? <CloudInterfaceSwitchLoader target={interfaceUi.target} />
				: showLifecycleLoader || showCompletedLoader
					? <CloudSessionLifecycleLoader
						key={`${sessionId}:${cloudReconnecting && !workspaceRestarting ? "terminal" : "startup"}`}
						sessionId={sessionId}
						orgId={session?.cloud?.orgId ?? cloudOrgId ?? ""}
						createdAt={session?.cloud?.observedState === "requested" ? session.createdAt : undefined}
						observedState={session?.cloud?.observedState}
						workerConnected={Boolean(session?.runtimeConnected)}
						terminalOnly={cloudReconnecting && !workspaceRestarting}
						completed={showCompletedLoader}
						startupNote={startupProblem?.phase === "retrying" ? startupProblem.message : undefined}
						coderWorkspaceUrl={session?.cloud?.coderWorkspaceUrl}
						eventsFromChat={!session || Boolean(showChatSurface && session.cloud)}
						queuedMessage={startupQueuedMessage}
					/>
					: null}
			{interfaceUi.dialogs}
			{/* Maximized files wear the maximized browser's chrome: a backdrop, the
          filter pinned in the titlebar band where the browser's address bar
          sits, and an inset frame for the explorer. The explorer mounts once
          the band exists so the filter never renders inline first. */}
			{filesPoppedOut && session
				? createPortal(
						<SessionFilesPopOut onClose={() => handleToggleFilesPopOut(false)}>{(topbarHost) =>
							<FilesTopbarHostContext.Provider value={topbarHost}>
								{session.cloud ? (
									<CloudWorkspaceDiff annotation={fileAnnotation} isMaximized onOpenFile={openCenterFile} onSplitChange={setFilesSplit} onToggleMaximized={handleToggleFilesPopOut} session={session} split={filesSplit} />
								) : (
									<SessionFileExplorer
 onOpenFile={openCenterFile}
										hostId={hostId}
										isMaximized
										onSplitChange={setFilesSplit}
										onToggleMaximized={handleToggleFilesPopOut}
										checkedOutBranch={session.branch}
										projectKind={filesKind}
										sessionId={session.id}
										split={filesSplit}
									/>
								)}
							</FilesTopbarHostContext.Provider>
						}</SessionFilesPopOut>,
						document.body,
					)
				: null}
			{/* Maximized browser: a fixed overlay across the app workspace,
          portaled to <body> so it escapes the shell layout (covering the
          sidebar + topbar, not just the session area) and sits outside any
          `[data-panel]` column, so the native WebContentsView is not clamped
          and fills the window below any native titlebar overlay. */}
			{browserPopOutMounted && session
				? createPortal(
						<SessionBrowserPopOut onClose={() => handleToggleBrowserPopOut(false)} onTopbarHost={setBrowserPopoutTopbarHost} phase={browserPopOutPhase === "open" ? "open" : "mounting"}>
								{browserPoppedOut && browserPopoutTopbarHost ? <BrowserPanelView
									active
									annotationQueue={browserAnnotationQueue}
									browserView={browserView}
									onTogglePopOut={handleToggleBrowserPopOut}
									poppedOut
									session={session}
									topbarHost={browserPopoutTopbarHost}
								/> : null}
						</SessionBrowserPopOut>,
						document.body,
					)
				: null}
		</div>
	);
}

/** The key a terminal target's tab has in the strips and the split layout. */
function terminalTabKey(target: TerminalTarget): string {
	if (target.kind === "shell") return target.handleId;
	if (target.kind === "reviewer") return `reviewer:${target.handleId}`;
	return SESSION_TAB_KEY;
}

const EMPTY_TAB_GROUPS: TabGroup[] = [];
/** The PR tabs while the PR view is off: none. */
const NO_PR_TABS: PRTabsState = { open: [], active: null };
const PENDING_SHELL_KEY_PREFIX = "pending-shell:";
const FULL_PANE_STYLE: CSSProperties = { position: "absolute", left: 0, top: 0, width: "100%", height: "100%" };

type SplitDropState = {
	groups: TabGroup[];
	shownGroup: TabGroup | undefined;
	selectedTabKey: string;
	splittableKeys: ReadonlySet<string>;
	selectTab: (key: string) => void;
	updateGroups: (update: (current: TabGroup[]) => TabGroup[]) => void;
	/** What releasing says, named inside the drop preview. */
	labels: { split: string; join: string; unsplit: string };
};

/**
 * Where a dragged tab would land. Over the screen: beside the pane under the
 * pointer, on the edge it points at (an ungrouped screen counts as one pane).
 * Over the strip: onto another tab to show both together, or off its group
 * to make it a tab of its own again.
 */
function resolveSplitDrop(
	state: SplitDropState,
	screen: HTMLElement | null,
	column: HTMLElement | null,
	key: string,
	x: number,
	y: number,
): PaneDropResolution | null {
	if (!screen) return null;
	// A plain box: a DOMRect's fields are prototype getters, so spreading one
	// (as halfOf does) would drop them.
	const bounds = screen.getBoundingClientRect();
	const rect: ClientRect = { left: bounds.left, top: bounds.top, width: bounds.width, height: bounds.height };
	if (y < rect.top) return resolveStripDrop(state, column, rect, key, x, y);
	if (!containsPoint(rect, x, y)) return null;
	const shown = state.shownGroup;
	if (!shown) {
		const target = state.selectedTabKey;
		if (target === key || !state.splittableKeys.has(target)) return null;
		const edge = edgeAt(rect, x, y);
		if (!edge) return null;
		return {
			rect: halfOf(rect, edge),
			label: state.labels.split,
			commit: () => {
				state.updateGroups((current) => splitBeside(current, target, key, edge));
				state.selectTab(key);
			},
		};
	}
	for (const [paneKey, box] of layoutBoxes(shown.root).leaves) {
		const paneRect = toClientRect(rect, box);
		if (!containsPoint(paneRect, x, y)) continue;
		if (paneKey === key) return null;
		const edge = edgeAt(paneRect, x, y);
		if (!edge) return null;
		return {
			rect: halfOf(paneRect, edge),
			label: state.labels.split,
			commit: () => {
				state.updateGroups((current) => splitBeside(current, paneKey, key, edge));
				state.selectTab(key);
			},
		};
	}
	return null;
}

function resolveStripDrop(
	state: SplitDropState,
	column: HTMLElement | null,
	screen: ClientRect,
	key: string,
	x: number,
	y: number,
): PaneDropResolution | null {
	if (!column || !containsPoint(column.getBoundingClientRect(), x, y)) return null;
	let over: { key: string; rect: ClientRect } | undefined;
	column.querySelectorAll<HTMLElement>("[data-terminal-tab-key]").forEach((element) => {
		const box = element.getBoundingClientRect();
		if (!over && element.dataset.terminalTabKey && containsPoint(box, x, y)) {
			over = { key: element.dataset.terminalTabKey, rect: { left: box.left, top: box.top, width: box.width, height: box.height } };
		}
	});
	const sourceGroup = groupContaining(state.groups, key);
	const targetGroup = over?.key.startsWith(GROUP_KEY_PREFIX)
		? state.groups.find((group) => groupTabKey(group) === over?.key)
		: undefined;
	if (sourceGroup && targetGroup === sourceGroup) return null;
	if (sourceGroup && !over) {
		// Off its group: the tab stands alone again and the rest stays on screen.
		const remaining = leafKeys(sourceGroup.root).find((candidate) => candidate !== key);
		return {
			rect: screen,
			label: state.labels.unsplit,
			commit: () => {
				state.updateGroups((current) => withoutTab(current, key));
				if (remaining && state.selectedTabKey === key) state.selectTab(remaining);
			},
		};
	}
	if (!over || over.key === key) return null;
	if (targetGroup) {
		// Joining a group adds the tab as a new column beside its layout.
		return {
			rect: over.rect,
			label: state.labels.join,
			commit: () => {
				state.updateGroups((current) =>
					placeInGroup(current, targetGroup, key, (root) => {
						const count = leafKeys(root).length;
						return { type: "split", dir: "row", ratio: count / (count + 1), a: root, b: { type: "leaf", key } };
					}),
				);
				state.selectTab(key);
			},
		};
	}
	const target = over.key;
	if (!state.splittableKeys.has(target)) return null;
	return {
		rect: over.rect,
		label: state.labels.join,
		commit: () => {
			state.updateGroups((current) => [
				...withoutTab(current, key),
				{ id: newGroupId(), root: { type: "split", dir: "row", ratio: 0.5, a: { type: "leaf", key: target }, b: { type: "leaf", key } } },
			]);
			state.selectTab(key);
		},
	};
}
