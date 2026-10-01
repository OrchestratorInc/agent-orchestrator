import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useBlocker } from "@tanstack/react-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import { createPortal } from "react-dom";
import { Globe2, PanelRight, Plus } from "lucide-react";
import { apiErrorCode, apiErrorMessage } from "../lib/api-client";
import { baseUrlForHost, clientForHost, labelForHost, subscribeConnectedHosts } from "../lib/host-clients";
import { refKey, sessionUiKey } from "../lib/hosts";
import { sessionReviewsQueryKey, type ReviewsResponse } from "../lib/session-reviews";
import { aoBridge } from "../lib/bridge";
import { chatDraftDialogCopy, confirmDiscardChatDrafts, getChatDraftBoundaries, subscribeChatDraftBoundaries } from "../lib/chat-draft-boundary";
import { useWorkspaceSession, remoteWorkspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { sessionWorkspaceFilesQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import { useFileAnnotation } from "../hooks/useFileAnnotation";
import { useBrowserView } from "../hooks/useBrowserView";
import { useSessionHandoffMenu } from "../hooks/useSessionHandoffMenu";
import { useSessionInterfaceSwitch } from "../hooks/useSessionInterfaceSwitch";
import { useCloseShellTerminal, useOpenShellTerminal, useRenameShellTerminal, useShellTerminals } from "../hooks/useShellTerminals";
import { clearSwitchAgentState } from "../hooks/useSwitchAgent";
import { activateSessionFile, closeSessionFile, EMPTY_SESSION_FILE_TABS, openSessionFile } from "../lib/session-file-tabs";
import { matchWorkspaceFilePath } from "../lib/workspace-file-path";
import { cn } from "../lib/utils";
import { matchesRendererShortcut } from "../stores/keybindings-store";
import { isOrchestratorSession, sessionIsActive } from "../types/workspace";
import { inspectorIsOpen, useUiStore } from "../stores/ui-store";
import { inspectorMaxWidthCss } from "../lib/inspector-width";
import { SessionChatSurface } from "./chat/SessionChatSurface";
import { ReviewerChatSurface } from "./chat/ReviewerChatSurface";
import { AgentAvatar } from "./AgentAvatar";
import type { FileOpenOptions } from "./FileContentPane";
import { SessionPaneTab } from "./CenterPane";
import { SessionTopbarHost } from "./SessionTopbarPortal";
import { TopbarButton } from "./TopbarButton";
import { RemoteTerminalView } from "./RemoteTerminalView";
import { SessionFileExplorer } from "./SessionFileExplorer";
import { FilesTopbarHostContext } from "./files-topbar-host";
import { SessionFilesPopOut } from "./SessionFilesPopOut";
import { SessionBrowserPopOut } from "./SessionBrowserPopOut";
import { SessionFileTab } from "./SessionFileTabs";
import { SessionFileWorkspace } from "./SessionFileWorkspace";
import { SessionInspector } from "./SessionInspector";
import { SessionInspectorRail, inspectorSizing, INSPECTOR_SPRING_EASING, INSPECTOR_SPRING_MS } from "./SessionInspectorRail";
import { BrowserPanelView, useBrowserAnnotationQueue } from "./BrowserPanel";
import { ShellTerminalTab } from "./ShellTerminalTab";
import { SessionActionsMenu } from "./SessionActionsMenu";
import { NotificationCenter } from "./NotificationCenter";
import { SwitchAgentDialog, canSwitchAgentHarness } from "./SwitchAgentDialog";
import { TerminalSwitchAgentButton } from "./TerminalSwitchAgentButton";

export function RemoteSessionRoute({ hostId, sessionId }: { hostId: string; sessionId: string }) {
	const uiSessionId = sessionUiKey(sessionId, hostId);
	const draftBoundaries = useSyncExternalStore(subscribeChatDraftBoundaries, () => getChatDraftBoundaries(uiSessionId));
	const remoteHostsEnabled = useUiStore((state) => state.remoteHosts);
	const setRemoteHosts = useUiStore((state) => state.setRemoteHosts);
	useBlocker({
		disabled: draftBoundaries.length === 0,
		enableBeforeUnload: draftBoundaries.length > 0,
		shouldBlockFn: () => {
			const blocked = !confirmDiscardChatDrafts(getChatDraftBoundaries(uiSessionId), (message) => window.confirm(message));
			if (blocked && !remoteHostsEnabled) setRemoteHosts(true);
			return blocked;
		},
	});
	useEffect(() => {
		aoBridge.app.setChatDraftRisk?.(draftBoundaries, chatDraftDialogCopy(draftBoundaries));
		return () => aoBridge.app.setChatDraftRisk?.([]);
	}, [draftBoundaries]);
	return <RemoteSessionView hostId={hostId} sessionId={sessionId} />;
}

/** Host-routed data and actions around the same Chat and terminal surfaces as local sessions. */
export function RemoteSessionView({ hostId, sessionId }: { hostId: string; sessionId: string }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const session = useWorkspaceSession(sessionId, hostId);
	const interfaceUi = useSessionInterfaceSwitch(sessionId, session.data, hostId);
	const proxyBase = useSyncExternalStore(subscribeConnectedHosts, () => baseUrlForHost(hostId));
	const hostLabel = useSyncExternalStore(subscribeConnectedHosts, () => labelForHost(hostId)) ?? hostId;
	const sessionRefKey = refKey({ host: hostId, id: sessionId });
	const reviewsQuery = useQuery({
		queryKey: sessionReviewsQueryKey(sessionId, hostId),
		enabled: Boolean(proxyBase && session.data && sessionIsActive(session.data) && !isOrchestratorSession(session.data) && session.data.prs.length > 0),
		refetchInterval: (query) => (query.state.data as ReviewsResponse | undefined)?.reviews?.some((review) => review.status === "running") ? 2500 : false,
		queryFn: async () => {
			const { data, error } = await clientForHost(hostId).GET("/api/v1/sessions/{sessionId}/reviews", { params: { path: { sessionId } } });
			if (error) throw new Error(apiErrorMessage(error, "Unable to load reviews"));
			return data ?? ({ reviewerHandleId: "", reviews: [], runs: [] } satisfies ReviewsResponse);
		},
	});
	const reviewData = reviewsQuery.data;
	const reviewerRun = reviewData?.reviews?.find((review) => review.latestRun)?.latestRun;
	const reviewerTerminal = reviewData?.reviewerSurface?.mode === "chat" || !reviewData?.reviewerHandleId?.trim() ? undefined : {
		handleId: reviewData.reviewerHandleId.trim(), harness: reviewData.reviewerHarness || reviewerRun?.harness || "codex",
	};
	const reviewerChat = reviewData?.reviewerSurface?.mode === "chat" && reviewData.reviewerSurface.reviewId ? {
		reviewId: reviewData.reviewerSurface.reviewId, harness: reviewData.reviewerSurface.harness || "codex",
	} : undefined;
	const [reviewerSelection, setReviewerSelection] = useState<
		{ owner: string; kind: "terminal"; handleId: string; harness: string } | { owner: string; kind: "chat"; reviewId: string } | null
	>(null);
	const selectedReviewer = reviewerSelection?.owner === sessionRefKey ? reviewerSelection : null;
	const [shellSelection, setShellSelection] = useState<{ owner: string; handleId: string } | null>(null);
	const shellTerminals = (useShellTerminals(hostId).data ?? []).filter((shell) => shell.sessionId === sessionId);
	const openShellTerminal = useOpenShellTerminal(hostId);
	const closeShellTerminal = useCloseShellTerminal(hostId);
	const renameShellTerminal = useRenameShellTerminal(hostId);
	const selectedShell = shellSelection?.owner === sessionRefKey ? shellTerminals.find((shell) => shell.handleId === shellSelection.handleId) : undefined;
	const shellTarget = selectedShell && !selectedShell.optimistic ? { kind: "shell" as const, generation: selectedShell.createdAt, handleId: selectedShell.handleId, sessionId, title: selectedShell.title } : undefined;
	const uiSessionId = sessionUiKey(sessionId, hostId);
	const browserOnly = Boolean(session.data && isOrchestratorSession(session.data));
	const inspectorOpen = useUiStore((state) => inspectorIsOpen(state.inspectorSessions, uiSessionId));
	const inspectorView = useUiStore((state) => browserOnly ? "browser" : state.inspectorSessions[uiSessionId]?.view ?? "summary");
	const setInspectorOpen = useUiStore((state) => state.setInspectorOpen);
	const setInspectorView = useUiStore((state) => state.setInspectorView);
	const initializeInspectorSession = useUiStore((state) => state.initializeInspectorSession);
	const [inspectorSettledClosed, setInspectorSettledClosed] = useState(!inspectorOpen);
	const sessionSplitRef = useRef<HTMLDivElement | null>(null);
	const sizing = useMemo(() => inspectorSizing(inspectorView), [inspectorView]);
	useEffect(() => {
		if (!session.data) return;
		initializeInspectorSession(uiSessionId, Boolean(session.data.previewUrl), true);
		if (browserOnly) setInspectorView(uiSessionId, "browser");
	}, [browserOnly, initializeInspectorSession, session.data, setInspectorView, uiSessionId]);
	useEffect(() => {
		if (inspectorOpen) setInspectorSettledClosed(false);
	}, [inspectorOpen]);
	useEffect(() => {
		const onKeyDown = (event: KeyboardEvent) => {
			if (!session.data || !matchesRendererShortcut("toggle-inspector", event)) return;
			event.preventDefault();
			setInspectorOpen(uiSessionId, !inspectorIsOpen(useUiStore.getState().inspectorSessions, uiSessionId));
		};
		window.addEventListener("keydown", onKeyDown);
		return () => window.removeEventListener("keydown", onKeyDown);
	}, [session.data, setInspectorOpen, uiSessionId]);
	const [handoffDialogOpen, setHandoffDialogOpen] = useState(false);
	const [browserPopOut, setBrowserPopOut] = useState<{ owner: string; phase: "docked" | "mounting" | "open" }>({ owner: sessionRefKey, phase: "docked" });
	const browserPopOutPhase = browserPopOut.owner === sessionRefKey ? browserPopOut.phase : "docked";
	const browserPoppedOut = browserPopOutPhase === "open";
	const [browserPopoutTopbarHost, setBrowserPopoutTopbarHost] = useState<HTMLDivElement | null>(null);
	useLayoutEffect(() => {
		if (browserPopOutPhase === "mounting" && browserPopoutTopbarHost) setBrowserPopOut({ owner: sessionRefKey, phase: "open" });
	}, [browserPopOutPhase, browserPopoutTopbarHost, sessionRefKey]);
	const [handoffDialogContainer, setHandoffDialogContainer] = useState<HTMLDivElement | null>(null);
	const { agentSwitch: handoffAgentSwitch, switchControlPresentation: handoffPresentation, switchError: handoffSwitchError } = useSessionHandoffMenu(session.data);
	const handleHandoffDialogOpenChange = useCallback((open: boolean) => {
		setHandoffDialogOpen(open);
		if (!open && handoffSwitchError) clearSwitchAgentState(queryClient, sessionId, hostId);
	}, [handoffSwitchError, hostId, queryClient, sessionId]);
	useEffect(() => setHandoffDialogOpen(false), [sessionRefKey]);
	useEffect(() => {
		if (proxyBase && inspectorOpen && inspectorView === "browser") void session.refetch();
	}, [proxyBase, inspectorOpen, inspectorView, session.refetch]);
	const browserView = useBrowserView({
		sessionId: uiSessionId,
		origin: { hostId, sessionId, proxyBase: proxyBase ?? "" },
		active: Boolean(proxyBase && (browserPoppedOut || (inspectorOpen && inspectorView === "browser"))),
		poppedOut: browserPoppedOut,
		terminated: Boolean(session.data && !sessionIsActive(session.data)),
		previewUrl: session.data?.previewUrl,
		previewRevision: session.data?.previewRevision,
	});
	const browserAnnotationQueue = useBrowserAnnotationQueue({ sessionId, hostId, sourcePreviewUrl: session.data?.previewUrl, navUrl: browserView.navState.url });
	const [fileTabs, setFileTabs] = useState(EMPTY_SESSION_FILE_TABS);
	const [fileRequests, setFileRequests] = useState<Record<string, FileOpenOptions & { key: number }>>({});
	const [dirtyFiles, setDirtyFiles] = useState<Record<string, true>>({});
	const [filesPoppedOut, setFilesPoppedOut] = useState(false);
	const [filesSplit, setFilesSplit] = useState(() => window.localStorage.getItem("ao.files.diffStyle") === "split");
	const consumedEditingRequests = useRef(new Set<string>());
	const fileAnnotation = useFileAnnotation(sessionId, { hostId });
	const openCenterFile = useCallback((path: string, options?: FileOpenOptions) => {
		setShellSelection(null);
		setFileRequests((current) => ({ ...current, [path]: { ...options, key: (current[path]?.key ?? 0) + 1 } }));
		setFileTabs((current) => openSessionFile(current, path));
	}, []);
	const openReferencedFile = useCallback((rawPath: string) => {
		void queryClient.fetchQuery(sessionWorkspaceFilesQueryOptions(sessionId, t("files.error.loadWorkspace"), hostId))
			.then((data) => openCenterFile(matchWorkspaceFilePath(rawPath, data.files ?? [])));
	}, [hostId, openCenterFile, queryClient, sessionId, t]);
	const selectWorker = useCallback(() => {
		setReviewerSelection(null);
		setShellSelection(null);
		setFileTabs((current) => activateSessionFile(current, null));
	}, []);
	const selectReviewerTerminal = useCallback((target: { handleId: string; harness: string }) => {
		setReviewerSelection({ owner: sessionRefKey, kind: "terminal", ...target });
		setShellSelection(null);
		setFileTabs((current) => activateSessionFile(current, null));
	}, [sessionRefKey]);
	const selectReviewerChat = useCallback((reviewId: string) => {
		setReviewerSelection({ owner: sessionRefKey, kind: "chat", reviewId });
		setShellSelection(null);
		setFileTabs((current) => activateSessionFile(current, null));
	}, [sessionRefKey]);
	const selectShell = useCallback((handleId: string) => {
		setShellSelection({ owner: sessionRefKey, handleId });
		setReviewerSelection(null);
		setFileTabs((current) => activateSessionFile(current, null));
	}, [sessionRefKey]);
	const addShell = useCallback(() => {
		openShellTerminal.open({ projectId: session.data?.workspaceId, sessionId }, { onSuccess: (shell) => selectShell(shell.handleId) });
	}, [openShellTerminal, selectShell, session.data?.workspaceId, sessionId]);
	const closeShell = useCallback((handleId: string) => {
		if (selectedShell?.handleId === handleId) selectWorker();
		closeShellTerminal.mutate(handleId);
	}, [closeShellTerminal, selectWorker, selectedShell?.handleId]);
	const renameShell = useCallback((handleId: string, title: string) => renameShellTerminal.mutate({ handleId, title }), [renameShellTerminal]);
	const closeCenterFile = useCallback((path: string) => setFileTabs((current) => closeSessionFile(current, path)), []);
	const markCenterFileDirty = useCallback((path: string, dirty: boolean) => setDirtyFiles((current) => {
		if (Boolean(current[path]) === dirty) return current;
		if (dirty) return { ...current, [path]: true };
		const next = { ...current };
		delete next[path];
		return next;
	}), []);
	const showFiles = useCallback(() => {
		setFilesPoppedOut(false);
		setInspectorOpen(uiSessionId, true);
		setInspectorView(uiSessionId, "files");
	}, [setInspectorOpen, setInspectorView, uiSessionId]);
	const toggleFilesPopOut = useCallback((next: boolean) => {
		if (next) setBrowserPopOut({ owner: sessionRefKey, phase: "docked" });
		setFilesPoppedOut(next);
		if (next) {
			setInspectorView(uiSessionId, "files");
			setInspectorOpen(uiSessionId, true);
		}
	}, [sessionRefKey, setInspectorOpen, setInspectorView, uiSessionId]);
	const toggleBrowserPopOut = useCallback((next: boolean) => {
		if (next) setFilesPoppedOut(false);
		setBrowserPopOut({ owner: sessionRefKey, phase: next ? "mounting" : "docked" });
	}, [sessionRefKey]);
	const centerFileTabs = fileTabs.openPaths.map((path) => ({
		key: `file:${path}`,
		content: <SessionFileTab active={fileTabs.activePath === path} dirty={Boolean(dirtyFiles[path])} onActivate={() => setFileTabs((current) => activateSessionFile(current, path))} onClose={() => closeCenterFile(path)} path={path} />,
		onSelect: () => setFileTabs((current) => activateSessionFile(current, path)),
		onClose: () => closeCenterFile(path),
	}));
	const activeFilePath = fileTabs.activePath;
	const activeFileRequest = activeFilePath ? fileRequests[activeFilePath] : undefined;
	const activeEditingToken = activeFilePath && activeFileRequest ? `${activeFilePath}:${activeFileRequest.key}` : undefined;
	const initialFileEditing = Boolean(activeFileRequest?.editing && activeEditingToken && !consumedEditingRequests.current.has(activeEditingToken));
	const markFileEditingConsumed = useCallback((path: string, requestKey: number) => {
		consumedEditingRequests.current.add(`${path}:${requestKey}`);
	}, []);
	const refreshRemoteWorkspaces = useCallback(() => queryClient.invalidateQueries({ queryKey: remoteWorkspaceQueryKey(hostId) }), [hostId, queryClient]);
	const centerFileWorkspace = activeFilePath ? <div className="absolute inset-0"><SessionFileWorkspace
		annotation={fileAnnotation}
		commitSha={activeFileRequest?.commitSha}
		hostId={hostId}
		initialEditing={initialFileEditing}
		initialMode={activeFileRequest?.mode ?? "file"}
		initialRequestKey={activeFileRequest?.key ?? 0}
		onDirtyChange={markCenterFileDirty}
		onInitialEditingConsumed={markFileEditingConsumed}
		path={activeFilePath}
		scope={activeFileRequest?.scope}
		sessionId={sessionId}
		split={filesSplit}
	/></div> : null;
	const title = session.data?.title ?? sessionId;
	const handoffMenuItem = session.data?.kind === "worker" && canSwitchAgentHarness(session.data.provider) && (sessionIsActive(session.data) || handoffPresentation)
		? <TerminalSwitchAgentButton
			agentSwitch={handoffAgentSwitch}
			onOpenChange={handleHandoffDialogOpenChange}
			open={handoffDialogOpen}
			presentation={handoffPresentation}
			session={session.data}
			switchError={handoffSwitchError}
			variant="menu-item"
		/>
		: null;
	const sessionTabActions = interfaceUi.unsupported ? null : <SessionActionsMenu inlineStatus={interfaceUi.inlineStatus}>
		{interfaceUi.menuItem}
		{handoffMenuItem}
	</SessionActionsMenu>;
	const newShellAction = session.data && !isOrchestratorSession(session.data) ? <TopbarButton aria-label={t("shortcut.new-shell-terminal")} onClick={addShell} title={t("shortcut.new-shell-terminal")} type="button" variant="icon"><Plus aria-hidden="true" className="size-icon-md" /></TopbarButton> : null;
	const hostActions = <div className="flex items-center gap-3">
		<span className="max-w-40 truncate text-xs text-muted-foreground" title={hostId}>{hostLabel}</span>
		<div className="session-pinned-actions-reserve" data-state={inspectorOpen ? "collapsed" : "expanded"} data-testid="session-pinned-actions-reserve" aria-hidden="true" />
	</div>;
	const sessionErrorCode = apiErrorCode(session.error);
	const sessionErrorMessage = apiErrorMessage(session.error);
	const loadErrorKey = sessionErrorCode === "BAD_PASSWORD" ? "remote.hostUnauthorized"
		: sessionErrorCode === "HOST_API_INCOMPATIBLE" ? "remote.hostIncompatible"
		: !proxyBase || sessionErrorCode === "UPSTREAM_UNAVAILABLE" || sessionErrorMessage === "remote daemon unreachable" || sessionErrorMessage === "remote host identity not verified"
			? "remote.hostOffline" : "remote.loadSessionFailed";

	return <div className="relative flex h-full min-h-0 flex-col bg-background text-foreground" data-testid="remote-session-view" data-host-id={hostId}>
		<div
			className="session-split relative flex min-h-0 flex-1 overflow-hidden"
			data-testid="panel-group"
			data-workspace-mode={sizing.mode}
			id="session-workspace"
			ref={sessionSplitRef}
			style={{
				"--session-inspector-max-width": inspectorMaxWidthCss(sizing.maxPercent, sizing.chatMinWidth),
				"--session-inspector-motion-duration": `${INSPECTOR_SPRING_MS}ms`,
				"--session-inspector-motion-easing": INSPECTOR_SPRING_EASING,
			} as CSSProperties}
		>
		<div className="relative flex min-w-0 flex-1 flex-col overflow-hidden" data-panel="" id="terminal">
		{proxyBase && !session.isError && interfaceUi.renderedMode === "chat" && <SessionTopbarHost className="relative z-chrome flex h-inspector-tabs w-full shrink-0 overflow-hidden" data-testid="session-topbar-host" />}
		{(session.isError || !proxyBase) && <p role="alert" className="px-4 py-2 text-sm text-destructive">{t(loadErrorKey)}</p>}
		<div className="relative min-h-0 flex-1" ref={setHandoffDialogContainer}>
			{session.data && handoffDialogContainer ? <SwitchAgentDialog agentSwitch={handoffAgentSwitch} container={handoffDialogContainer} onOpenChange={handleHandoffDialogOpenChange} open={handoffDialogOpen} session={session.data} /> : null}
			{proxyBase && !session.isError && session.data && interfaceUi.renderedMode === "chat" ? <>
			<div className={cn("h-full min-h-0", activeFilePath && "invisible pointer-events-none")} inert={activeFilePath ? true : undefined}><SessionChatSurface
				key={sessionRefKey}
				assetBaseUrl={proxyBase}
				daemonReady={Boolean(proxyBase) && !session.isError}
				headerActions={hostActions}
				hostId={hostId}
				controllerTransitioning={interfaceUi.controllerTransitioning}
				newWorkDisabled={session.data.isTerminated || session.isError || interfaceUi.newWorkDisabled}
				onConversationWorkChange={interfaceUi.onConversationWorkChange}
				reviewerTerminal={reviewerTerminal}
				reviewerChat={reviewerChat}
				reviewerChatSelected={Boolean(selectedReviewer)}
				onOpenReviewerTerminal={selectReviewerTerminal}
				onOpenReviewerChat={(target) => selectReviewerChat(target.reviewId)}
				onOpenFile={openReferencedFile}
				onOpenFiles={showFiles}
				onOpenShell={addShell}
				openingShell={openShellTerminal.isPending}
				shellError={openShellTerminal.error ? apiErrorMessage(openShellTerminal.error) : undefined}
				onSelectChat={selectWorker}
				shellTerminals={shellTerminals}
				shellTarget={shellTarget}
				onSelectShellTerminal={selectShell}
				onCloseShellTerminal={closeShell}
				onRenameShellTerminal={renameShell}
				onSessionRenamed={refreshRemoteWorkspaces}
				session={session.data}
				sessionTabAction={sessionTabActions}
				tabStripAction={newShellAction}
				handoffDialogOpen={handoffDialogOpen}
				workspaceActiveTabKey={activeFilePath ? `file:${activeFilePath}` : undefined}
				workspaceFileActive={Boolean(activeFilePath)}
				workspaceTabs={centerFileTabs}
			/></div>
			{selectedReviewer && !activeFilePath ? <div className="absolute inset-0" data-testid="remote-reviewer-panel">
				{selectedReviewer.kind === "chat" ? <ReviewerChatSurface hideHeader hostId={hostId} reviewId={selectedReviewer.reviewId} /> : <RemoteTerminalView hostId={hostId} proxyBase={proxyBase} terminalHandleId={selectedReviewer.handleId} />}
			</div> : null}
			{centerFileWorkspace}
			</> : proxyBase && !session.isError && session.data && interfaceUi.renderedMode === "tui" ? <div className="flex h-full min-h-0 flex-col">
				<header className="flex h-inspector-tabs shrink-0 items-stretch justify-between bg-sidebar">
					<div className="flex min-w-0 items-stretch" role="tablist"><SessionPaneTab isActive={!activeFilePath && !selectedReviewer && !shellTarget} label={title} onRenamed={refreshRemoteWorkspaces} onSelect={selectWorker} session={session.data} tabAction={sessionTabActions} />
						{reviewerTerminal || reviewerChat ? <SessionPaneTab appearance="connected" icon={<AgentAvatar className="size-terminal-agent-icon" decorative provider={(reviewerTerminal ?? reviewerChat)?.harness ?? "codex"} />} isActive={Boolean(selectedReviewer && !activeFilePath)} label={t("terminal.reviewer")} onSelect={() => reviewerTerminal ? selectReviewerTerminal(reviewerTerminal) : reviewerChat && selectReviewerChat(reviewerChat.reviewId)} /> : null}
						{shellTerminals.map((shell) => <ShellTerminalTab key={shell.handleId} appearance="connected" isActive={shellTarget?.handleId === shell.handleId && !activeFilePath} onClose={() => closeShell(shell.handleId)} onRename={(name) => renameShell(shell.handleId, name)} onSelect={() => selectShell(shell.handleId)} shell={shell} />)}
						{centerFileTabs.map((tab) => <div key={tab.key}>{tab.content}</div>)}</div>
					<div className="flex items-center">{newShellAction}{hostActions}</div>
				</header>
				<div className="relative min-h-0 flex-1">
					<div className={cn("h-full min-h-0", (activeFilePath || selectedReviewer || shellTarget) && "invisible pointer-events-none")} inert={activeFilePath || selectedReviewer || shellTarget ? true : undefined}><RemoteTerminalView hostId={hostId} inputDisabled={interfaceUi.agentInputDisabled} proxyBase={proxyBase} terminalHandleId={session.data.terminalHandleId} terminalGeneration={session.data.terminalGeneration} /></div>
					{selectedReviewer && !activeFilePath ? <div className="absolute inset-0" data-testid="remote-reviewer-panel">
						{selectedReviewer.kind === "chat" ? <ReviewerChatSurface hideHeader hostId={hostId} reviewId={selectedReviewer.reviewId} /> : <RemoteTerminalView hostId={hostId} proxyBase={proxyBase} terminalHandleId={selectedReviewer.handleId} />}
					</div> : null}
					{shellTarget && !activeFilePath ? <div className="absolute inset-0" data-testid="remote-shell-panel"><RemoteTerminalView hostId={hostId} proxyBase={proxyBase} terminalHandleId={shellTarget.handleId} /></div> : null}
					{centerFileWorkspace}
				</div>
			</div> : proxyBase && session.isLoading ? <div className="grid h-full place-items-center text-sm text-muted-foreground">{t("remote.loadingSession")}</div>
				: proxyBase && !session.data && !session.isError ? <div className="grid h-full place-items-center text-sm text-muted-foreground">{t("session.notFound")}</div>
				: null}
			{interfaceUi.notice}
		</div>
		</div>
		{session.data && proxyBase && !session.isError ? <SessionInspectorRail
			showCollapsedHandle={!browserOnly}
			isOpen={inspectorOpen}
			onExpand={() => setInspectorOpen(uiSessionId, true)}
			onCloseAnimationComplete={() => setInspectorSettledClosed(true)}
			sizing={sizing}
			settledClosed={!inspectorOpen && inspectorSettledClosed}
			splitRef={sessionSplitRef}
		>
			<SessionInspector key={sessionRefKey} browserOnly={browserOnly} browserPoppedOut={browserPoppedOut} browserAnnotationQueue={browserAnnotationQueue} browserView={browserView} hostId={hostId} isInspectorVisible={inspectorOpen || !inspectorSettledClosed} session={session.data} filesView={inspectorView === "files" ? <SessionFileExplorer hostId={hostId} onOpenFile={openCenterFile} onSplitChange={setFilesSplit} onToggleMaximized={toggleFilesPopOut} sessionId={sessionId} split={filesSplit} /> : null} onOpenReviewFile={({ path }) => openReferencedFile(path)} onOpenReviewerTerminal={selectReviewerTerminal} onOpenReviewerChat={selectReviewerChat} onWorkerMessageSent={selectWorker} onToggleBrowserPopOut={toggleBrowserPopOut} onViewChange={(view) => setInspectorView(uiSessionId, view)} view={inspectorView} />
		</SessionInspectorRail> : null}
		</div>
		{session.data && proxyBase && !session.isError ? <div className="session-pinned-actions" data-testid="session-pinned-actions">
			<TopbarButton
				aria-label={inspectorOpen ? t("shell.closeInspector") : t("shell.openInspector")}
				aria-pressed={inspectorOpen}
				onClick={() => setInspectorOpen(uiSessionId, !inspectorOpen)}
				variant="icon"
			>{browserOnly ? <Globe2 aria-hidden="true" className="size-icon-md" /> : <PanelRight aria-hidden="true" className="size-icon-md" />}</TopbarButton>
			<NotificationCenter />
		</div> : null}
		{interfaceUi.dialogs}
		{filesPoppedOut && session.data ? createPortal(
			<SessionFilesPopOut>{(topbarHost) =>
				<FilesTopbarHostContext.Provider value={topbarHost}>
					<SessionFileExplorer hostId={hostId} isMaximized onSplitChange={setFilesSplit} onToggleMaximized={toggleFilesPopOut} sessionId={sessionId} split={filesSplit} />
				</FilesTopbarHostContext.Provider>
			}</SessionFilesPopOut>, document.body,
		) : null}
		{browserPopOutPhase !== "docked" && session.data ? createPortal(
			<SessionBrowserPopOut onTopbarHost={setBrowserPopoutTopbarHost} phase={browserPopOutPhase}>
				{browserPoppedOut && browserPopoutTopbarHost ? <BrowserPanelView active annotationQueue={browserAnnotationQueue} browserView={browserView} onTogglePopOut={toggleBrowserPopOut} poppedOut session={session.data} topbarHost={browserPopoutTopbarHost} /> : null}
			</SessionBrowserPopOut>, document.body,
		) : null}
	</div>;
}
