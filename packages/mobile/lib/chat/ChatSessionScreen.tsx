import { Feather } from "../icons";
import { useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import {
	ActivityIndicator,
	Alert,
	Keyboard,
	Pressable,
	StyleSheet,
	Text,
	View,
} from "react-native";
import { mobileReachablePreviewURL, restoreSession, resumeSessionAgent, type DashboardSession, type OrchestratorLink } from "../api";
import { machineIdentity } from "../config";
import { haptics } from "../haptics";
import { resetHeaderRightForSwap } from "../headerRightSwap";
import { openGitHub } from "../openGitHub";
import { glassHeaderControl } from "../native-header-items";
import { NativeHeaderButton } from "../native-header-button";
import { useApp } from "../store";
import {
	mobileInterfaceTransitionIsActive,
	mobileInterfaceTransitionIsBusy,
	mobileInterfaceTransitionIsCancellable,
	mobileInterfaceTransitionRecoveryMessage,
	useInterfaceTransition,
} from "../session/useInterfaceTransition";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { getWorkspacePaths, openSessionShell } from "./api";
import { requestDockModel } from "./requestDockModel";
import type { ResponseQuoteRequest } from "./responseQuotes";
import { ChatComposer } from "./ChatComposer";
import { ChatLinkProvider } from "./ChatMarkdown";
import { ChatTimeline } from "./ChatTimeline";
import { useChatKeyboard } from "./useChatKeyboard";
import { ConversationTitle } from "./ConversationTitle";
import { chatSheetRoute, type ConversationActionsEntry } from "./chatSheetRegistry";
import { quotaWarning } from "./conversationChrome";
import { shouldAutoResume } from "./autoResume";
import { controllerStoppedBanner, errorBanner, mcpBanner, quotaBanner, reauthBanner, rolledBackBanner, threadBanner, type BannerCopy } from "./conversationBanners";
import { conversationActionError, conversationActionUnsupported } from "./conversationErrors";
import { conversationMarkers } from "./timelineModel";
import { turnSettingsModelLabel } from "./turnSettingsModel";
import { brokenMcpServers, can } from "./types";
import { useMobileConversation } from "./useConversation";
import { SessionStartupStatus } from "./SessionStartup";
import type { TimelineStartup } from "./ChatTimeline";
import { sessionStartupView, startupVisible } from "../session/sessionStartup";
import { startSerialPoll } from "../session/serialPoll";
import { STARTUP_POLL_MS } from "../session/startupPoll";
import { useStartupSession } from "../session/useStartupSession";
import { useChatViewLease } from "./useChatViewLease";
import { useOpenPage } from "../pageNavigation";
import { usePRSummaries } from "../usePRSummaries";
import { reviewRouteForPR, reviewRouteForSession, sessionPRReadyForReview } from "../reviewView";
import { iconSize, type, space } from "../tokens";
import { backOr } from "../backNavigation";
import { userFacingError, NOT_PAIRED_ACTION_COPY } from "../connectionError";
import { ARCHIVE_SESSION_CONFIRM } from "../worker-action-model";
import { changedPathCount, fileRoute, filesRoute, FilesNavigationContext, type FilesNavigation } from "../files/navigation";
import { useChangeSummary } from "../files/useChangeSummary";

type MobileChatSession = DashboardSession | OrchestratorLink;

async function dismissKeyboardBeforeSheet(keyboardVisible: boolean): Promise<void> {
	Keyboard.dismiss();
	if (!keyboardVisible) return;
	await new Promise<void>((resolve) => {
		let settled = false;
		let subscription: ReturnType<typeof Keyboard.addListener> | undefined;
		const finish = () => {
			if (settled) return;
			settled = true;
			subscription?.remove();
			clearTimeout(timeout);
			resolve();
		};
		const timeout = setTimeout(finish, 320);
		subscription = Keyboard.addListener("keyboardDidHide", finish);
	});
}

export function ChatSessionScreen({ session: boardSession }: { session: MobileChatSession }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const navigation = useNavigation();
	const router = useRouter();
	const openPage = useOpenPage();
	const [headerRightReady, setHeaderRightReady] = useState(false);
	useLayoutEffect(
		() => resetHeaderRightForSwap(
			() => navigation.setOptions(glassHeaderControl("right")),
			() => setHeaderRightReady(true),
		),
		[navigation],
	);
	const { config, currentHostId, connection, unreachable, projects, refresh: refreshBoard, setWorkerPinned, renameWorker, kill } = useApp();
	// The board's row, with its provisioning facts re-read every second while it starts.
	const session = useStartupSession(config, boardSession);
	const conversation = useMobileConversation(config, session.id);
	useChatViewLease(config, session.id);
	// A load that failed while the desktop was unreachable retries as soon as the
	// board's poll reconnects, which is what the offline state promises.
	// Keyed on a failed load, not a missing one, so the first mount doesn't send a
	// second request alongside the hook's own initial load.
	const loadFailed = !conversation.snapshot && Boolean(conversation.error);
	const refreshConversation = conversation.refresh;
	useEffect(() => {
		if (connection === "open" && loadFailed) void refreshConversation();
	}, [connection, loadFailed, refreshConversation]);
	const actionsEntryRef = useRef<ConversationActionsEntry | undefined>(undefined);
	const actionsListeners = useRef(new Set<(entry: ConversationActionsEntry) => void>());
	const interfaceSwitch = useInterfaceTransition(config, session.id, refreshBoard);
	const [menuOpen, setMenuOpen] = useState(false);
	const [collapsedReviewPRKey, setCollapsedReviewPRKey] = useState<string>();
	const [loadedReviewPromptStateKey, setLoadedReviewPromptStateKey] = useState<string>();
	const [jumpToSequence, setJumpToSequence] = useState<number>();
	const clearJumpToSequence = useCallback(() => setJumpToSequence(undefined), []);
	// Which request the user pushed aside to type instead. It lives here because
	// both the composer and the timeline change shape depending on it.
	const [dismissedRequest, setDismissedRequest] = useState<number>();
	const restoreRequest = useCallback(() => setDismissedRequest(undefined), []);
	const [filePaths, setFilePaths] = useState<string[]>([]);
	const [filePathsTruncated, setFilePathsTruncated] = useState(false);
	const filePathsRequest = useRef<Promise<{ paths: string[]; truncated: boolean }> | null>(null);
	const [openingShell, setOpeningShell] = useState(false);
	const [resuming, setResuming] = useState(false);
	const [resumeError, setResumeError] = useState<string | undefined>();
	// Banners closed this visit, by what they report. See conversationBanners.
	const [dismissedBanners, setDismissedBanners] = useState<ReadonlySet<string>>(() => new Set());
	const dismissBanner = useCallback((key: string) => setDismissedBanners((current) => new Set(current).add(key)), []);
	// keyboard-controller's early events, corrected by React Native's when it
	// misses a transition. See useChatKeyboard.
	const chatKeyboard = useChatKeyboard();
	const keyboardVisible = chatKeyboard.keyboardVisible;
	const turnOptionsRequestedFor = useRef<string | undefined>(undefined);
	const terminated = "projectName" in session ? Boolean(session.isTerminal) : Boolean(session.isTerminated);
	const failedStart = "projectName" in session || session.provisionState !== "failed"
		? undefined
		: session.provisionError || "The session did not finish starting.";
	const interfaceTransitionActive = mobileInterfaceTransitionIsActive(interfaceSwitch.transition);
	useLayoutEffect(() => {
		const entry = actionsEntryRef.current;
		const snapshot = conversation.snapshot;
		if (!entry || entry.sessionId !== session.id || !snapshot) return;
		const liveEntry = {
			...entry,
			snapshot,
			modelLabel: turnSettingsModelLabel(snapshot, conversation.models, conversation.configOptions),
			openingShell,
			compacting: conversation.pendingActions.includes("compact"),
			mcpReloading: conversation.pendingActions.includes("mcp"),
			refreshing: conversation.refreshing,
			compactSupported: can(snapshot, "compaction") && !conversationActionUnsupported("compact", conversation.actionCodes.compact),
			mcpReloadSupported: can(snapshot, "mcp_reload") && !conversationActionUnsupported("mcp", conversation.actionCodes.mcp),
			interfaceSupported: Boolean(interfaceSwitch.status?.supported),
			interfaceReason: interfaceSwitch.status?.reason || interfaceSwitch.error,
			interfaceSwitching: interfaceTransitionActive || interfaceSwitch.starting,
		};
		actionsEntryRef.current = liveEntry;
		actionsListeners.current.forEach((listener) => listener(liveEntry));
	}, [conversation.snapshot, conversation.models, conversation.configOptions, conversation.pendingActions, conversation.refreshing, conversation.actionCodes, openingShell, interfaceSwitch.status, interfaceSwitch.error, interfaceSwitch.starting, interfaceTransitionActive, session.id]);
	const interfaceTransitionNotice =
		!interfaceTransitionActive &&
		!interfaceSwitch.transition?.noticeAcknowledgedAt &&
		(interfaceSwitch.transition?.phase === "failed" ||
			interfaceSwitch.transition?.phase === "recovery_required")
			? interfaceSwitch.transition
			: undefined;
	const interfaceTransitionRecovered =
		interfaceTransitionNotice?.phase === "recovery_required" &&
		interfaceTransitionNotice.errorCode === "DAEMON_RESTARTED";
	const interfaceTransitionNoticeText =
		interfaceTransitionNotice?.errorDetail ||
		(interfaceTransitionRecovered
			? "Orchestrator restored the session in its last committed interface."
			: interfaceTransitionNotice?.phase === "recovery_required"
				? "The interface switch needs recovery before more work is sent."
				: "The interface switch failed; the current interface remains available.");
	const turnActive = Boolean(
		conversation.snapshot?.turns.some((turn) => turn.state === "running" || turn.state === "queued"),
	);
	const turnWaiting = Boolean(
		conversation.snapshot?.items.some(
			(item) =>
				item.kind === "activity" &&
				(item.activityKind === "approval" || item.activityKind === "user_input") &&
				item.status === "pending",
		),
	);

	useEffect(() => {
		if (!conversation.snapshot || turnOptionsRequestedFor.current === session.id) return;
		turnOptionsRequestedFor.current = session.id;
		void conversation.loadTurnOptions().catch(() => {});
	}, [conversation.loadTurnOptions, conversation.snapshot, session.id]);

	const sessionName = sessionTitle(session);
	// Codex-style: every surface that shows the agent changed files can open them.
	const filesNavigation = useMemo<FilesNavigation>(() => ({
		openChanges: () => router.push(filesRoute(session.id, currentHostId, sessionName)),
		openFile: (path, previousPath) => router.push(fileRoute(session.id, path, currentHostId, previousPath)),
	}), [currentHostId, router, session.id, sessionName]);
	// Re-read git's totals when a turn reports edits, not on a timer.
	const lastTurn = conversation.snapshot?.turns.at(-1);
	const changeSignal = `${changedPathCount(conversation.snapshot?.turns)}:${lastTurn?.id ?? ""}:${lastTurn?.state ?? ""}:${lastTurn?.diff?.files.length ?? 0}`;
	const changeSummary = useChangeSummary(config, session.id, changeSignal);
	// Desktop derives one name from the session (useWorkspaceQuery: displayName ??
	// issueId ?? id) and its chat header prefers it over the conversation's own
	// title (ChatWorkspace: sessionTitle || session.title || snapshot.title). This
	// had that precedence inverted, so renaming from the board changed the row but
	// left this header on the agent's auto-generated conversation title.
	const title = sessionName || conversation.snapshot?.title || session.id;
	const projectName = "projectName" in session
		? session.projectName
		: session.projectId ? projects.find((project) => project.id === session.projectId)?.name : "Standalone";
	const headerHarness = conversation.snapshot?.harness || session.harness || "Agent";
	const headerState = conversation.snapshot?.controller.state;
	const reviewPromptPRCandidate = "projectName" in session ? undefined : sessionPRReadyForReview(session);
	const reviewPromptKey = reviewPromptPRCandidate ? `${reviewPromptPRCandidate.url}#${reviewPromptPRCandidate.number}` : undefined;
	const reviewPromptHostKey = config ? machineIdentity(config) : currentHostId;
	const reviewPromptStateStorageKey = reviewPromptKey && reviewPromptHostKey
		? `ao.chat.reviewPromptCollapsed:${encodeURIComponent(reviewPromptHostKey)}:${encodeURIComponent(session.id)}:${encodeURIComponent(reviewPromptKey)}`
		: undefined;
	useEffect(() => {
		let cancelled = false;
		setLoadedReviewPromptStateKey(undefined);
		setCollapsedReviewPRKey(undefined);
		if (!reviewPromptKey || !reviewPromptStateStorageKey) {
			setLoadedReviewPromptStateKey(reviewPromptStateStorageKey);
			return () => { cancelled = true; };
		}
		AsyncStorage.getItem(reviewPromptStateStorageKey).then((value) => {
			if (cancelled) return;
			setCollapsedReviewPRKey(value === "1" ? reviewPromptKey : undefined);
			setLoadedReviewPromptStateKey(reviewPromptStateStorageKey);
		}).catch(() => {
			if (cancelled) return;
			setLoadedReviewPromptStateKey(reviewPromptStateStorageKey);
		});
		return () => { cancelled = true; };
	}, [reviewPromptKey, reviewPromptStateStorageKey]);
	const reviewPromptStateReady = !reviewPromptStateStorageKey || loadedReviewPromptStateKey === reviewPromptStateStorageKey;
	const reviewPromptPR = reviewPromptStateReady ? reviewPromptPRCandidate : undefined;
	const reviewPromptCollapsed = Boolean(reviewPromptStateReady && reviewPromptKey && reviewPromptKey === collapsedReviewPRKey);
	const collapseReviewPrompt = useCallback(() => {
		if (!reviewPromptKey || !reviewPromptStateStorageKey) return;
		setCollapsedReviewPRKey(reviewPromptKey);
		void AsyncStorage.setItem(reviewPromptStateStorageKey, "1").catch(() => {});
	}, [reviewPromptKey, reviewPromptStateStorageKey]);
	const expandReviewPrompt = useCallback(() => {
		setCollapsedReviewPRKey(undefined);
		if (reviewPromptStateStorageKey) void AsyncStorage.removeItem(reviewPromptStateStorageKey).catch(() => {});
	}, [reviewPromptStateStorageKey]);
	const reviewSummaries = usePRSummaries(config && reviewPromptPR ? [{ config, sessionId: session.id }] : []);
	const reviewPromptSummary = config && reviewPromptPR ? reviewSummaries.summaryFor(config, session.id, reviewPromptPR.number) : undefined;

	// The blocking request. It takes the composer's place until it is answered.
	// Computed here rather than at render because the back-swipe below is a hook
	// and cannot sit after this screen's early returns.
	const request = requestDockModel(conversation.snapshot, {
		approval: conversation.pendingActions.includes("approval"),
		input: conversation.pendingActions.includes("input"),
	});
	const requestDismissed = request ? dismissedRequest === request.sequence : false;
	// The timeline collapses a request the card is answering to a record of what
	// was asked — one live set of controls, never two.
	const answeredBelow = request && !requestDismissed && request.canAnswerInline ? request.sequence : undefined;
	// On iOS a left-to-right swipe is the screen's back gesture, and it beat the
	// card's own swipe — you got the board instead of the previous question. The
	// whole horizontal axis goes to the card while it is up; the header's back
	// button still leaves the session, and the card's ✕ still returns the
	// composer, so nothing becomes unreachable.
	const cardShowing = Boolean(request && !requestDismissed);
	useLayoutEffect(() => {
		navigation.setOptions({ gestureEnabled: !cardShowing });
	}, [cardShowing, navigation]);
	useLayoutEffect(() => {
		navigation.setOptions({
			headerTitle: () => (
				<ConversationTitle
					title={title}
					subtitle={[projectName, headerHarness].filter(Boolean).join(" · ")}
					harness={headerHarness}
					state={headerState}
				/>
			),
		});
	}, [headerHarness, headerState, navigation, projectName, title, t]);

	// The title is set on the first commit, above. Only the trailing control waits:
	// native-stack measures the replacement for a frame, and deferring the whole
	// options object meant the title arrived with it — iOS then animated the
	// header in from the top, after the screen had already landed.
	useLayoutEffect(() => {
		navigation.setOptions(
			headerRightReady
				? glassHeaderControl("right", (
					<View style={{ flexDirection: "row", alignItems: "center", gap: 4 }}>
						{reviewPromptCollapsed && reviewPromptPR ? (
							<Pressable
								testID="header-pullRequest"
								accessibilityRole="button"
								accessibilityLabel="Show PR card"
								onPress={() => { haptics.tap(); expandReviewPrompt(); }}
								style={({ pressed }) => [styles.reviewHeaderPRButton, { backgroundColor: pressed ? t.accentTint : t.bgElevatedHover }]}
							>
								<Feather name="git-pull-request" size={iconSize.lg} color={t.textSecondary} />
							</Pressable>
						) : null}
						<NativeHeaderButton icon="more" label="Conversation actions" onPress={() => { haptics.tap(); setMenuOpen(true); }} />
					</View>
				))
				: glassHeaderControl("right"),
		);
	}, [expandReviewPrompt, headerRightReady, navigation, reviewPromptCollapsed, reviewPromptPR, t]);

	const composerKey = config ? JSON.stringify([machineIdentity(config), session.id]) : session.id;
	const [quoteRequest, setQuoteRequest] = useState<ResponseQuoteRequest>();
	const quoteSequence = useRef(0);
	const addResponseToChat = useCallback((text: string, source?: ResponseQuoteRequest["source"]) => {
		if (!text.length || !source) return;
		setDismissedRequest(request?.sequence);
		setQuoteRequest({ id: `${Date.now()}-${++quoteSequence.current}`, text, source, composerKey });
	}, [composerKey, request?.sequence]);
	const quoteHandled = useCallback((id: string) => setQuoteRequest((current) => current?.id === id ? undefined : current), []);

	const loadWorkspaceFiles = useCallback(async () => {
		if (!config || !conversation.snapshot) return { paths: filePaths, truncated: filePathsTruncated };
		if (filePathsRequest.current) return filePathsRequest.current;
		const request = getWorkspacePaths(config, session.id)
			.then((result) => {
				setFilePaths(result.paths);
				setFilePathsTruncated(result.truncated);
				return result;
			})
			.catch(() => ({ paths: filePaths, truncated: filePathsTruncated }))
			.finally(() => { filePathsRequest.current = null; });
		filePathsRequest.current = request;
		return request;
	}, [config, conversation.snapshot, filePaths, filePathsTruncated, session.id]);

	const openTurnSettings = useCallback(async () => {
		const current = conversation.snapshot;
		if (!current) return;
		await dismissKeyboardBeforeSheet(keyboardVisible);
		let catalog = { models: conversation.models, configOptions: conversation.configOptions };
		let catalogError = conversation.actionErrors.settings ?? conversation.actionErrors.config;
		try {
			catalog = await conversation.loadTurnOptions();
		} catch (cause) {
			catalogError = conversationActionError(cause);
		}
		router.push(chatSheetRoute({
			kind: "turn-settings",
			snapshot: current,
			models: catalog.models,
			options: catalog.configOptions,
			disabled: current.controller.state === "stopped" || conversation.pendingActions.includes("settings") || conversation.pendingActions.includes("config"),
			error: catalogError,
			onSettings: conversation.chooseSettings,
			onOption: conversation.setConfigOption,
			onRefresh: () => conversation.loadTurnOptions({ refresh: true }),
		}));
	}, [conversation, keyboardVisible, router]);

	const openShell = useCallback(async () => {
		if (!config || openingShell) return;
		setOpeningShell(true);
		try {
			const shell = await openSessionShell(config, session.id, session.projectId);
			setMenuOpen(false);
			router.push({ pathname: "/shell/[handleId]", params: { handleId: shell.handleId, projectId: session.projectId, sessionId: session.id, title: shell.title, hostId: config.hostId } });
		} catch (cause) {
			Alert.alert("Couldn't open shell", userFacingError(cause));
		} finally { setOpeningShell(false); }
	}, [config, openingShell, router, session.id, session.projectId]);

	// Links open in-app, like the desktop Browser inspector. A localhost link from
	// the agent's machine is mapped onto the AO host first (the phone's own
	// localhost is never the dev server the agent named); openGitHub then applies
	// the package's rule for a web page - GitHub app when it has a screen for it,
	// the in-app browser otherwise.
	const openLink = useCallback((url: string) => {
		void openGitHub(mobileReachablePreviewURL(url, config?.host ?? "")?.href ?? url);
	}, [config?.host]);

	const resume = useCallback(async (quiet = false) => {
		if (resuming) return;
		setResuming(true);
		setResumeError(undefined);
		try {
			if (!config) throw new Error(NOT_PAIRED_ACTION_COPY);
			if (terminated) await restoreSession(config, session.id);
			else await resumeSessionAgent(config, session.id);
			await refreshBoard();
			await conversation.refresh();
		} catch (cause) {
			// An automatic attempt has no alert; the stopped banner carries the reason.
			if (quiet) setResumeError(userFacingError(cause));
			else Alert.alert("Couldn't resume the agent", userFacingError(cause));
		} finally { setResuming(false); }
	}, [config, conversation.refresh, refreshBoard, resuming, session.id, terminated]);

	const startupView = useMemo(
		() => ("projectName" in session ? undefined : sessionStartupView(session, conversation.snapshot)),
		[conversation.snapshot, session],
	);
	const startupShown = startupVisible(startupView, conversation.snapshot);
	// The agent coming up is not a conversation event, so over the direct link
	// nothing tells the screen. With an opening brief the turn's own events cover
	// it; a promptless start stayed on "Connecting to the agent…". Once
	// provisioning settles, re-read the conversation until it reports the agent
	// connected, so one failed read cannot leave the screen waiting.
	const provisionState = "projectName" in session ? undefined : session.provisionState;
	const previousProvisionState = useRef(provisionState);
	const [awaitingAgent, setAwaitingAgent] = useState(false);
	useEffect(() => {
		const before = previousProvisionState.current;
		previousProvisionState.current = provisionState;
		if (before === "provisioning" && provisionState === "ready") setAwaitingAgent(true);
	}, [provisionState]);
	const agentConnecting = conversation.snapshot?.controller.state === "connecting";
	const hasSnapshot = Boolean(conversation.snapshot);
	useEffect(() => {
		if (!awaitingAgent) return;
		if (hasSnapshot && !agentConnecting) {
			setAwaitingAgent(false);
			return;
		}
		// One read at a time: the refresh keeps only its newest request, so a
		// fixed interval faster than the link discards every response.
		return startSerialPoll(refreshConversation, STARTUP_POLL_MS);
	}, [agentConnecting, awaitingAgent, hasSnapshot, refreshConversation]);
	const retryStart = useCallback(() => void resume(), [resume]);
	const timelineStartup = useMemo<TimelineStartup | undefined>(
		() => (startupView ? { view: startupView, visible: startupShown, onRetry: retryStart, retrying: resuming } : undefined),
		[resuming, retryStart, startupShown, startupView],
	);

	// The daemon leaves agents stopped after a restart; opening the session here
	// starts them, as the desktop does. One attempt per opening: a failure or a
	// later exit stays stopped behind the Resume banner, which shows the reason.
	const autoResumeTried = useRef<string | undefined>(undefined);
	useEffect(() => {
		if (autoResumeTried.current === session.id) return;
		if (!shouldAutoResume(session, terminated, Boolean(config))) return;
		autoResumeTried.current = session.id;
		void resume(true);
	}, [config, resume, session, terminated]);

	const startInterfaceSwitch = useCallback(
		async (policy: "drain" | "interrupt") => {
			setMenuOpen(false);
			try {
				await interfaceSwitch.start("tui", policy);
			} catch (cause) {
				Alert.alert("Couldn't switch interface", userFacingError(cause));
			}
		},
		[interfaceSwitch],
	);

	const requestInterfaceSwitch = useCallback(() => {
		if (!interfaceSwitch.status?.supported) {
			Alert.alert("Terminal UI unavailable", interfaceSwitch.status?.reason || interfaceSwitch.error || "This agent cannot resume the same native conversation in Terminal UI.");
			return;
		}
		if (!turnActive) {
			void startInterfaceSwitch("drain");
			return;
		}
		Alert.alert(
			"Switch to Terminal UI?",
			turnWaiting
				? "This turn is waiting for your input. Finish waits for your answer; stop cancels it and switches now."
				: "Keep the same Orchestrator session, worktree, and native agent conversation.",
			[
				{ text: "Keep Chat", style: "cancel" },
				{ text: "Finish, then switch", onPress: () => void startInterfaceSwitch("drain") },
				{ text: "Stop and switch", style: "destructive", onPress: () => void startInterfaceSwitch("interrupt") },
			],
		);
	}, [interfaceSwitch, startInterfaceSwitch, turnActive, turnWaiting]);

	useEffect(() => {
		const current = conversation.snapshot;
		if (!menuOpen || !current) return;
		setMenuOpen(false);
		const entry: ConversationActionsEntry = {
			kind: "conversation-actions",
			sessionId: session.id,
			snapshot: current,
			subscribeEntry: (listener) => {
				actionsListeners.current.add(listener);
				listener(actionsEntryRef.current ?? entry);
				return () => { actionsListeners.current.delete(listener); };
			},
			sessionTitle: sessionName,
			modelLabel: turnSettingsModelLabel(current, conversation.models, conversation.configOptions),
			openingShell,
			compacting: conversation.pendingActions.includes("compact"),
			mcpReloading: conversation.pendingActions.includes("mcp"),
			refreshing: conversation.refreshing,
			compactSupported: can(current, "compaction") && !conversationActionUnsupported("compact", conversation.actionCodes.compact),
			mcpReloadSupported: can(current, "mcp_reload") && !conversationActionUnsupported("mcp", conversation.actionCodes.mcp),
			interfaceSupported: Boolean(interfaceSwitch.status?.supported),
			interfaceReason: interfaceSwitch.status?.reason || interfaceSwitch.error,
			interfaceSwitching: interfaceTransitionActive || interfaceSwitch.starting,
			// Orchestrators are not deleted from here; the board owns their lifecycle.
			canDelete: !("projectName" in session),
			// The daemon refuses to rename the project orchestrator.
			canRename: !("projectName" in session),
			canPin: !("projectName" in session),
			pinned: "projectName" in session ? false : Boolean(session.isPinned),
			onFiles: filesNavigation.openChanges,
			onMap: () => router.push(chatSheetRoute({ kind: "conversation-map", markers: conversationMarkers(actionsEntryRef.current?.snapshot ?? current), onSelect: setJumpToSequence })),
			onOpenShell: () => void openShell(),
			onPreview: () => router.push({ pathname: "/preview/[id]", params: { id: session.id, title, hostId: currentHostId } }),
			onPullRequests: () => {
				const route = !("projectName" in session) && (session.prs?.length ?? (session.pr ? 1 : 0)) <= 1
					? reviewRouteForSession(session, currentHostId)
					: undefined;
				if (route) openPage(route);
				else router.push({ pathname: "/(tabs)/prs", params: { hostId: currentHostId, projectId: session.projectId } });
			},
			onSettings: () => void openTurnSettings(),
			onSwitchInterface: requestInterfaceSwitch,
			onCompact: () => void conversation.compact().catch(() => {}),
			onReload: () => void conversation.reloadMcp().catch(() => {}),
			onRename: () => router.push(chatSheetRoute({
				kind: "conversation-rename",
				initialTitle: sessionName,
				onRename: (next) => renameWorker(session.id, next),
			})),
			onTogglePin: () => {
				if ("projectName" in session) return;
				void setWorkerPinned(session.id, !session.isPinned).catch(() => {});
			},
			onRefresh: () => void conversation.refresh(),
			onDelete: () => {
				haptics.warning();
				Alert.alert(
					ARCHIVE_SESSION_CONFIRM.title,
					ARCHIVE_SESSION_CONFIRM.message,
					[
						{ text: ARCHIVE_SESSION_CONFIRM.cancel, style: "cancel" },
						// Leave first: the session this screen is showing is about to stop
						// existing, and the board is where its row disappears from.
						{ text: ARCHIVE_SESSION_CONFIRM.confirm, style: "destructive", onPress: () => { backOr(router); void kill(session.id).catch(() => {}); } },
					],
				);
			},
		};
		actionsEntryRef.current = entry;
		void dismissKeyboardBeforeSheet(keyboardVisible).then(() => router.push(chatSheetRoute(actionsEntryRef.current ?? entry)));
	}, [currentHostId, conversation, filesNavigation, interfaceSwitch, interfaceTransitionActive, keyboardVisible, menuOpen, openPage, openShell, openTurnSettings, openingShell, requestInterfaceSwitch, router, session, sessionName, setWorkerPinned, title]);

	// The poll keeps retrying on its own at up to 8s; this is for the user who can
	// see the network is back and does not want to wait for the tick. Nothing else
	// on this screen re-asks the hook, so without it a transition whose poll had
	// failed could only be checked by leaving and coming back. The ref is the
	// guard (state updates are async); the state drives the label.
	const [recheckingTransition, setRecheckingTransition] = useState(false);
	const recheckingTransitionRef = useRef(false);
	const retryInterfaceCheck = useCallback(async () => {
		if (recheckingTransitionRef.current) return;
		recheckingTransitionRef.current = true;
		setRecheckingTransition(true);
		try {
			await interfaceSwitch.refresh();
		} finally {
			recheckingTransitionRef.current = false;
			setRecheckingTransition(false);
		}
	}, [interfaceSwitch]);
	const interfaceRecoveryMessage = mobileInterfaceTransitionRecoveryMessage(interfaceSwitch.transition);
	const interfaceTransitionPhaseText = interfaceRecoveryMessage || `Switching to Terminal UI · ${interfacePhaseLabel(interfaceSwitch.transition?.phase)}`;
	const interfaceTransitionBanner = {
		text: interfaceSwitch.fetchFailed
			? `${interfaceTransitionPhaseText}. Couldn't check on it${interfaceSwitch.error ? `: ${interfaceSwitch.error}` : ""}`
			: interfaceTransitionPhaseText,
		// Cancel stays put while a check is failing: the phase the hook holds is
		// still cancellable and the cancel is its own request, so a user who wants
		// out should not have to prove the link first. Retry rides alongside it,
		// which is also how the terminal card lays the two out.
		action: mobileInterfaceTransitionIsCancellable(interfaceSwitch.transition) ? (interfaceSwitch.cancelling ? "Cancelling…" : "Cancel") : undefined,
		onPress: interfaceSwitch.cancelling ? undefined : () => void interfaceSwitch.cancel().catch(() => {}),
		secondary: interfaceSwitch.fetchFailed || interfaceRecoveryMessage ? (recheckingTransition ? "Retrying…" : "Retry") : undefined,
		onSecondary: recheckingTransition ? undefined : () => void retryInterfaceCheck(),
	};

	if (failedStart && !conversation.snapshot) return <Centered icon="alert-triangle" title="Session failed to start" message={failedStart} action={resuming ? "Retrying…" : "Retry"} onAction={() => void resume()} />;
	// A session that is still starting shows its checklist straight away rather than a bare spinner.
	if (conversation.loading && !conversation.snapshot && startupView && !startupView.failed) {
		return <View style={styles.startupLoading}><SessionStartupStatus startup={startupView} /></View>;
	}
	if (conversation.loading && !conversation.snapshot) return <Centered icon="message-square" title="Loading conversation…" spinning />;
	if (conversation.unavailable) return <Unavailable message={conversation.unavailable.message} onShell={() => void openShell()} openingShell={openingShell} />;
	// The board's poll is the app's view of the link: when it is down, say so in
	// the board's words instead of echoing whatever this request failed with.
	if (!conversation.snapshot && unreachable) return <Centered icon="wifi-off" title="This machine is offline" message="This conversation loads once the app reconnects." action="Retry" onAction={() => void conversation.refresh()} />;
	if (!conversation.snapshot) return <Centered icon="alert-triangle" title="Couldn't load the conversation" message={conversation.error || "The machine didn't return this conversation. Try again."} action="Retry" onAction={() => void conversation.refresh()} />;

	const snapshot = conversation.snapshot;
	const active = snapshot.turns.some((turn) => turn.state === "running" || turn.state === "queued");
	const brokenServers = brokenMcpServers(snapshot);
	const rolledBack = snapshot.turns.filter((turn) => turn.rolledBack).length;
	const quota = quotaWarning(snapshot.rateLimits);
	const compactSupported = can(snapshot, "compaction") && !conversationActionUnsupported("compact", conversation.actionCodes.compact);
	const mcpReloadSupported = can(snapshot, "mcp_reload") && !conversationActionUnsupported("mcp", conversation.actionCodes.mcp);
	const steerUnsupported = conversationActionUnsupported("steer", conversation.actionCodes.steer);

	return (
		// keyboard-controller's avoider, not React Native's. Both do the same
		// arithmetic; this one interpolates the padding with the keyboard's own
		// animation, so the composer travels with the keys. RN's re-renders once per
		// keyboard event on the JS thread, which is the single-frame jump up when
		// the keyboard opens.
		<KeyboardAvoidingView
			style={[styles.screen, chatKeyboard.rootStyle]}
			behavior={chatKeyboard.behavior}
			keyboardVerticalOffset={chatKeyboard.keyboardVerticalOffset}
		>
			{interfaceTransitionActive ? (
				<InlineBanner
					tone="warning"
					icon="repeat"
					title={interfaceTransitionBanner.text}
					action={interfaceTransitionBanner.action}
					secondary={interfaceTransitionBanner.secondary}
					onPress={interfaceTransitionBanner.onPress}
					onSecondary={interfaceTransitionBanner.onSecondary}
				/>
			) : interfaceTransitionNotice ? (
				<InlineBanner
					tone={interfaceTransitionRecovered ? "warning" : "danger"}
					icon={interfaceTransitionRecovered ? "check-circle" : "alert-triangle"}
					title={`${interfaceTransitionNoticeText}${
						interfaceSwitch.acknowledgeNoticeError
							? ` Couldn't dismiss: ${interfaceSwitch.acknowledgeNoticeError}`
							: ""
					}`}
					action={interfaceSwitch.acknowledgingNotice ? "Dismissing…" : "Dismiss"}
					onPress={
						interfaceSwitch.acknowledgingNotice
							? undefined
							: () =>
									void interfaceSwitch
										.acknowledgeNotice(interfaceTransitionNotice.id)
										.catch(() => {})
					}
				/>
			) : null}
			<ConversationBanners
				snapshot={snapshot}
				// The checklist reports setup, and a failed start with its Retry, in place.
				startFailure={startupShown ? undefined : failedStart}
				starting={startupShown}
				brokenServers={brokenServers}
				resuming={resuming}
				resumeError={resumeError}
				terminated={terminated}
				mcpReloading={conversation.pendingActions.includes("mcp")}
				mcpError={conversation.actionErrors.mcp}
				mcpReloadSupported={mcpReloadSupported}
				turnInFlight={active}
				onResume={() => void resume()}
				onReload={() => void conversation.reloadMcp().catch(() => {})}
				onOpenShell={() => void openShell()}
				dismissed={dismissedBanners}
				onDismiss={dismissBanner}
			/>
			{conversation.error ? <DismissibleBanner copy={errorBanner("load", conversation.error)} dismissed={dismissedBanners} onDismiss={dismissBanner} tone="danger" icon="wifi-off" action="Retry" onPress={() => void conversation.refresh()} /> : null}
			{quota ? <DismissibleBanner copy={quotaBanner(quota)} dismissed={dismissedBanners} onDismiss={dismissBanner} tone={quota.severity === "critical" ? "danger" : "warning"} icon="alert-triangle" action="Details" onPress={() => setMenuOpen(true)} /> : null}
			{conversation.actionError && conversation.actionError !== conversation.error ? <DismissibleBanner copy={errorBanner("action", conversation.actionError)} dismissed={dismissedBanners} onDismiss={dismissBanner} tone="danger" icon="alert-circle" /> : null}
			{rolledBack ? <DismissibleBanner copy={rolledBackBanner(rolledBack)} dismissed={dismissedBanners} onDismiss={dismissBanner} tone="muted" icon="rotate-ccw" /> : null}
			{conversation.pendingSends.map((pendingSend) => pendingSend.state === "failed" ? <InlineBanner key={pendingSend.id} tone="danger" icon="send" title="Message delivery uncertain" body={pendingSend.error || "Delivery failed"} action="Retry" secondary="Discard" onPress={() => void conversation.retrySend(pendingSend.id).catch(() => {})} onSecondary={() => void conversation.discardSend(pendingSend.id).catch(() => {})} /> : null)}
			<ChatLinkProvider onLinkOpen={openLink} onAddToChat={conversation.snapshot?.supportsExcerpts ? addResponseToChat : undefined}>
				<FilesNavigationContext.Provider value={filesNavigation}>
				<ChatTimeline
					snapshot={snapshot}
					loadingOlder={conversation.loadingOlder}
					onLoadOlder={conversation.loadOlder}
					approvalPending={conversation.pendingActions.includes("approval")}
					inputPending={conversation.pendingActions.includes("input")}
					onDecide={conversation.resolveApproval}
					onResolveInput={conversation.resolveInput}
					onRollback={conversation.rollback}
					jumpToSequence={jumpToSequence}
					onJumpHandled={clearJumpToSequence}
					answeredBelow={answeredBelow}
					startup={timelineStartup}
				/>
				</FilesNavigationContext.Provider>
			</ChatLinkProvider>
			<ChatComposer
				key={composerKey}
				quoteRequest={quoteRequest?.composerKey === composerKey ? quoteRequest : undefined}
				onQuoteHandled={quoteHandled}
				reviewPR={reviewPromptPR}
				reviewPRSummary={reviewPromptSummary}
				reviewPRCollapsed={reviewPromptCollapsed}
				onCollapseReviewPR={collapseReviewPrompt}
				onOpenReview={reviewPromptPR ? () => openPage(reviewRouteForPR(session.id, reviewPromptPR, currentHostId)) : undefined}
				sessionId={session.id}
				config={config}
				snapshot={snapshot}
				changes={changeSummary?.files ? { ...changeSummary, onOpen: filesNavigation.openChanges } : undefined}
				quotaActive={Boolean(quota)}
				request={request}
				requestDismissed={requestDismissed}
				onRequestDecide={conversation.resolveApproval}
				onRequestResolveInput={conversation.resolveInput}
				onShowRequest={setJumpToSequence}
				onDismissRequest={() => setDismissedRequest(request?.sequence)}
				onRestoreRequest={restoreRequest}
				skills={conversation.skills}
				filePaths={filePaths}
				filePathsTruncated={filePathsTruncated}
				onLoadSkills={conversation.loadSkills}
				onLoadFiles={loadWorkspaceFiles}
				configOptions={conversation.configOptions}
				models={conversation.models}
				steerUnavailable={steerUnsupported}
				disabled={interfaceTransitionActive || Boolean(failedStart)}
				pending={mobileInterfaceTransitionIsBusy(interfaceSwitch.transition) || conversation.pendingSends.some((item) => item.state === "sending")}
				interrupting={conversation.pendingActions.includes("interrupt")}
				onSend={conversation.send}
				onAcknowledgeSend={conversation.acknowledgeSend}
				completedRetry={conversation.completedRetry}
				onSteer={conversation.steer}
				onPromoteQueuedTurn={conversation.promoteQueuedTurn}
				onCancelQueuedTurn={conversation.cancelQueuedTurn}
				onInterrupt={() => void conversation.interrupt().catch(() => {})}
				onOpenSettings={() => void openTurnSettings()}
				onSettings={conversation.chooseSettings}
				onConfigOption={conversation.setConfigOption}
				restingInset={chatKeyboard.restingInset}
				dockRiseOverride={chatKeyboard.dockRiseOverride}
				openingTurnId={startupView?.openingTurnId}
				startingAgentName={startupShown && !startupView?.failed ? startupView?.agentName : undefined}
			/>
		</KeyboardAvoidingView>
	);
}

function ConversationBanners({ snapshot, startFailure, starting, brokenServers, resuming, resumeError, terminated, mcpReloading, mcpError, mcpReloadSupported, turnInFlight, onResume, onReload, onOpenShell, dismissed, onDismiss }: { snapshot: NonNullable<ReturnType<typeof useMobileConversation>["snapshot"]>; startFailure?: string; starting: boolean; brokenServers: ReturnType<typeof brokenMcpServers>; resuming: boolean; resumeError?: string; terminated: boolean; mcpReloading: boolean; mcpError?: string; mcpReloadSupported: boolean; turnInFlight: boolean; onResume(): void; onReload(): void; onOpenShell(): void; dismissed: ReadonlySet<string>; onDismiss(key: string): void }) {
	const thread = snapshot.threadState;
	const reauthAt = snapshot.account?.reauthRequiredAt;
	return <>
		{reauthAt ? <DismissibleBanner copy={reauthBanner(reauthAt, signInCommand(snapshot.harness))} dismissed={dismissed} onDismiss={onDismiss} tone="danger" icon="key" action="Open shell" onPress={onOpenShell} /> : null}
		{startFailure ? <DismissibleBanner copy={{ key: `start:${startFailure}`, title: "Session failed to start", body: startFailure }} dismissed={dismissed} onDismiss={onDismiss} tone="danger" icon="alert-triangle" action={resuming ? "Retrying…" : "Retry"} secondary="Shell" onPress={resuming ? undefined : onResume} onSecondary={onOpenShell} /> : !starting && snapshot.controller.state === "stopped" ? <DismissibleBanner copy={controllerStoppedBanner(terminated, resumeError ?? snapshot.controller.error)} dismissed={dismissed} onDismiss={onDismiss} tone="danger" icon="power" action={terminated ? (resuming ? "Restoring…" : "Restore") : (resuming ? "Resuming…" : "Resume")} secondary="Shell" onPress={resuming ? undefined : onResume} onSecondary={onOpenShell} /> : null}
		{/* Passing states clear themselves, so there is nothing to close. */}
		{!startFailure && !starting && (snapshot.controller.state === "recovering" || snapshot.controller.state === "connecting") ? <InlineBanner tone="warning" icon="loader" title={snapshot.controller.state === "recovering" ? "Reconnecting to the agent…" : "Starting the agent…"} /> : null}
		{threadBanner(thread?.status) ? <DismissibleBanner copy={threadBanner(thread?.status)!} dismissed={dismissed} onDismiss={onDismiss} tone={thread?.status === "system_error" ? "danger" : "warning"} icon="alert-triangle" /> : null}
		{brokenServers.length ? <DismissibleBanner copy={mcpBanner(brokenServers, mcpError)!} dismissed={dismissed} onDismiss={onDismiss} tone="warning" icon="tool" action={mcpReloadSupported && !turnInFlight ? (mcpReloading ? "Reloading…" : "Reload") : undefined} onPress={mcpReloading ? undefined : onReload} /> : null}
	</>;
}

type InlineBannerProps = { tone: "warning" | "danger" | "muted"; icon: keyof typeof Feather.glyphMap; title: string; body?: string; action?: string; secondary?: string; onPress?(): void; onSecondary?(): void; onDismiss?(): void };

/** A banner the reader can close; it stays closed until what it reports changes. */
function DismissibleBanner({ copy, dismissed, onDismiss, ...props }: Omit<InlineBannerProps, "title" | "body" | "onDismiss"> & { copy: BannerCopy | undefined; dismissed: ReadonlySet<string>; onDismiss(key: string): void }) {
	if (!copy || dismissed.has(copy.key)) return null;
	return <InlineBanner {...props} title={copy.title} body={copy.body} onDismiss={() => onDismiss(copy.key)} />;
}

// Headline plus at most two lines of detail, like the desktop's status banners.
function InlineBanner({ tone, icon, title, body, action, secondary, onPress, onSecondary, onDismiss }: InlineBannerProps) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const color = tone === "danger" ? t.red : tone === "warning" ? t.amber : t.textTertiary;
	const fill = tone === "danger" ? t.tintRed : tone === "warning" ? t.tintAmber : t.bgSubtle;
	return (
		<View style={[styles.banner, { backgroundColor: fill }]}>
			<Feather name={icon} size={12} color={color} style={styles.bannerIcon} />
			<View style={styles.bannerCopy}>
				<Text style={[styles.bannerTitle, { color: tone === "muted" ? t.textSecondary : color }]} numberOfLines={1}>{title}</Text>
				{body ? <Text style={styles.bannerText} numberOfLines={2}>{body}</Text> : null}
			</View>
			{secondary ? <Pressable hitSlop={7} onPress={() => { haptics.tap(); onSecondary?.(); }}><Text style={styles.bannerSecondary}>{secondary}</Text></Pressable> : null}
			{action ? <Pressable hitSlop={7} onPress={() => { haptics.tap(); onPress?.(); }}><Text style={[styles.bannerAction, { color }]}>{action}</Text></Pressable> : null}
			{onDismiss ? <Pressable accessibilityRole="button" accessibilityLabel={`Close: ${title}`} hitSlop={10} onPress={() => { haptics.tap(); onDismiss(); }} style={styles.bannerClose}><Feather name="x" size={15} color={t.textTertiary} /></Pressable> : null}
		</View>
	);
}

function Unavailable({ message, onShell, openingShell }: { message: string; onShell(): void; openingShell: boolean }) { return <Centered icon="alert-triangle" title="Conversation unavailable" message={`${message}\n\nThe worktree is untouched. You can still open a plain shell in it.`} action={openingShell ? "Opening…" : "Open worktree shell"} onAction={onShell} />; }
function Centered({ icon, title, message, spinning, action, onAction }: { icon: keyof typeof Feather.glyphMap; title: string; message?: string; spinning?: boolean; action?: string; onAction?(): void }) { const t = useTheme(); const styles = useThemedStyles(makeStyles); return <View style={styles.center}>{spinning ? <ActivityIndicator color={t.accent} /> : <Feather name={icon} size={20} color={t.amber} />}<Text style={styles.centerTitle}>{title}</Text>{message ? <Text style={styles.centerCopy}>{message}</Text> : null}{action ? <Pressable onPress={() => { haptics.tap(); onAction?.(); }} style={styles.centerAction}><Text style={styles.centerActionText}>{action}</Text></Pressable> : null}</View>; }

function sessionTitle(session: MobileChatSession): string { return "displayName" in session ? session.displayName || session.issueTitle || session.issueLabel || session.id : session.projectName || session.id; }
function interfacePhaseLabel(phase?: string): string {
	switch (phase) {
		case "draining": return "finishing current work";
		case "source_stopping": return "stopping Chat controller";
		case "source_stopped": return "Chat controller stopped";
		case "target_starting": return "resuming terminal controller";
		case "activating": return "opening Terminal UI";
		default: return "preparing native handoff";
	}
}
function signInCommand(harness: string): string | undefined { return harness === "codex" ? "codex login" : harness === "claude-code" || harness === "claude" ? "claude auth login" : undefined; }

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	banner: { minHeight: 35, flexDirection: "row", alignItems: "center", gap: space.sm, paddingLeft: space.md, paddingRight: space.sm, paddingVertical: space.xs, borderBottomWidth: 1, borderBottomColor: t.borderSubtle },
	bannerIcon: { alignSelf: "flex-start", marginTop: space.hair },
	bannerCopy: { flex: 1, minWidth: 0, gap: space.none },
	bannerTitle: { fontFamily: "Geist_600SemiBold", fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "600" },
	bannerText: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
	bannerClose: { width: 26, height: 26, alignItems: "center", justifyContent: "center" },
	bannerAction: { fontFamily: "Geist_600SemiBold", fontSize: type.caption2.fontSize, fontWeight: "600" },
	bannerSecondary: { fontFamily: "Geist_600SemiBold", color: t.textTertiary, fontSize: type.caption2.fontSize, fontWeight: "600" },
	reviewHeaderPRButton: { width: 44, height: 44, borderRadius: 22, borderCurve: "continuous", borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderStrong, alignItems: "center", justifyContent: "center", overflow: "hidden" },
	center: { flex: 1, alignItems: "center", justifyContent: "center", gap: space.md, paddingHorizontal: space.huge, backgroundColor: t.bgBase },
	// Where the timeline will draw the checklist, so the handoff to it does not move it.
	startupLoading: { flex: 1, backgroundColor: t.bgBase, paddingHorizontal: space.xl, paddingTop: space.md },
	centerTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.body.fontSize, fontWeight: "600", textAlign: "center" },
	centerCopy: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, textAlign: "center" },
	centerAction: { minHeight: 42, justifyContent: "center", backgroundColor: t.accent, borderRadius: 12, paddingHorizontal: space.lg, marginTop: space.xxs },
	centerActionText: { fontFamily: "Geist_600SemiBold", color: t.onAccent, fontSize: type.footnote.fontSize, fontWeight: "600" },
});
