import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useBlocker } from "@tanstack/react-router";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { PanelRight, Plus } from "lucide-react";
import { apiErrorCode, apiErrorMessage } from "../lib/api-client";
import { baseUrlForHost, clientForHost, labelForHost, subscribeConnectedHosts } from "../lib/host-clients";
import { refKey, sessionUiKey } from "../lib/hosts";
import { sessionReviewsQueryKey, type ReviewsResponse } from "../lib/session-reviews";
import { aoBridge } from "../lib/bridge";
import { chatDraftDialogCopy, confirmDiscardChatDrafts, getChatDraftBoundaries, subscribeChatDraftBoundaries } from "../lib/chat-draft-boundary";
import { useWorkspaceSession, remoteWorkspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { useFileAnnotation } from "../hooks/useFileAnnotation";
import { useBrowserView } from "../hooks/useBrowserView";
import { useSessionHandoffMenu } from "../hooks/useSessionHandoffMenu";
import { useSessionInterfaceSwitch } from "../hooks/useSessionInterfaceSwitch";
import { useCloseShellTerminal, useOpenShellTerminal, useRenameShellTerminal, useShellTerminals } from "../hooks/useShellTerminals";
import { clearSwitchAgentState } from "../hooks/useSwitchAgent";
import { activateSessionFile, closeSessionFile, EMPTY_SESSION_FILE_TABS, openSessionFile } from "../lib/session-file-tabs";
import { cn } from "../lib/utils";
import { isOrchestratorSession, sessionIsActive } from "../types/workspace";
import type { InspectorView } from "../stores/ui-store";
import { useUiStore } from "../stores/ui-store";
import { SessionChatSurface } from "./chat/SessionChatSurface";
import { ReviewerChatSurface } from "./chat/ReviewerChatSurface";
import { AgentAvatar } from "./AgentAvatar";
import type { FileOpenOptions } from "./FileContentPane";
import { SessionPaneTab } from "./CenterPane";
import { SessionTopbarHost } from "./SessionTopbarPortal";
import { TopbarButton } from "./TopbarButton";
import { RemoteTerminalView } from "./RemoteTerminalView";
import { SessionFileExplorer } from "./SessionFileExplorer";
import { SessionFileTab } from "./SessionFileTabs";
import { SessionFileWorkspace } from "./SessionFileWorkspace";
import { SessionInspector } from "./SessionInspector";
import { useBrowserAnnotationQueue } from "./BrowserPanel";
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
	const [inspectorOpen, setInspectorOpen] = useState(true);
	const [handoffDialogOpen, setHandoffDialogOpen] = useState(false);
	const [handoffDialogContainer, setHandoffDialogContainer] = useState<HTMLDivElement | null>(null);
	const { agentSwitch: handoffAgentSwitch, switchControlPresentation: handoffPresentation, switchError: handoffSwitchError } = useSessionHandoffMenu(session.data);
	const handleHandoffDialogOpenChange = useCallback((open: boolean) => {
		setHandoffDialogOpen(open);
		if (!open && handoffSwitchError) clearSwitchAgentState(queryClient, sessionId, hostId);
	}, [handoffSwitchError, hostId, queryClient, sessionId]);
	useEffect(() => setHandoffDialogOpen(false), [sessionRefKey]);
	const [inspectorView, setInspectorView] = useState<InspectorView>("summary");
	useEffect(() => {
		if (proxyBase && inspectorOpen && inspectorView === "browser") void session.refetch();
	}, [proxyBase, inspectorOpen, inspectorView, session.refetch]);
	const browserView = useBrowserView({
		sessionId: sessionUiKey(sessionId, hostId),
		origin: { hostId, sessionId, proxyBase: proxyBase ?? "" },
		active: Boolean(proxyBase && inspectorOpen && inspectorView === "browser"),
		poppedOut: false,
		terminated: Boolean(session.data && !sessionIsActive(session.data)),
		previewUrl: session.data?.previewUrl,
		previewRevision: session.data?.previewRevision,
	});
	const browserAnnotationQueue = useBrowserAnnotationQueue({ sessionId, hostId, sourcePreviewUrl: session.data?.previewUrl, navUrl: browserView.navState.url });
	const [fileTabs, setFileTabs] = useState(EMPTY_SESSION_FILE_TABS);
	const [fileRequests, setFileRequests] = useState<Record<string, FileOpenOptions & { key: number }>>({});
	const [dirtyFiles, setDirtyFiles] = useState<Record<string, true>>({});
	const consumedEditingRequests = useRef(new Set<string>());
	const fileAnnotation = useFileAnnotation(sessionId, { hostId });
	const openCenterFile = useCallback((path: string, options?: FileOpenOptions) => {
		setShellSelection(null);
		setFileRequests((current) => ({ ...current, [path]: { ...options, key: (current[path]?.key ?? 0) + 1 } }));
		setFileTabs((current) => openSessionFile(current, path));
	}, []);
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
		setInspectorOpen(true);
		setInspectorView("files");
	}, []);
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
		split={false}
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
		<TopbarButton
			aria-label={inspectorOpen ? t("shell.closeInspector") : t("shell.openInspector")}
			aria-pressed={inspectorOpen}
			onClick={() => setInspectorOpen((open) => !open)}
			variant="icon"
		><PanelRight aria-hidden="true" className="size-icon-md" /></TopbarButton>
		<NotificationCenter />
	</div>;
	const hostUnavailable = !proxyBase || (session.isError && (
		apiErrorCode(session.error) === "UPSTREAM_UNAVAILABLE" || apiErrorMessage(session.error) === "remote daemon unreachable"
	));

	return <div className="relative flex h-full min-h-0 bg-background text-foreground" data-testid="remote-session-view" data-host-id={hostId}>
		<div className="flex min-w-0 flex-1 flex-col">
		{proxyBase && !session.isError && interfaceUi.renderedMode === "chat" && <SessionTopbarHost className="relative z-chrome flex h-inspector-tabs w-full shrink-0 overflow-hidden" data-testid="session-topbar-host" />}
		{(session.isError || !proxyBase) && <p role="alert" className="px-4 py-2 text-sm text-destructive">{t(hostUnavailable ? "remote.hostOffline" : "remote.loadSessionFailed")}</p>}
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
				onOpenFile={openCenterFile}
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
		{interfaceUi.dialogs}
		{session.data && proxyBase && !session.isError && inspectorOpen ? <div className="w-[min(20rem,40%)] shrink-0 overflow-hidden border-l border-border-strong bg-background 2xl:w-[min(24rem,40%)]" data-testid="panel-inspector">
			<SessionInspector key={sessionRefKey} browserAnnotationQueue={browserAnnotationQueue} browserView={browserView} hostId={hostId} session={session.data} filesView={<SessionFileExplorer hostId={hostId} onOpenFile={openCenterFile} sessionId={sessionId} />} onOpenReviewFile={({ path }) => openCenterFile(path)} onOpenReviewerTerminal={selectReviewerTerminal} onOpenReviewerChat={selectReviewerChat} onWorkerMessageSent={selectWorker} onViewChange={setInspectorView} view={inspectorView} />
		</div> : null}
	</div>;
}
