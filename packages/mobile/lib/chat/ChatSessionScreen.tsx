import { Stack, useNavigation, useRouter } from "expo-router";
import { useHeaderHeight } from "expo-router/react-navigation";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { Alert, Keyboard, Platform, StyleSheet, View, type LayoutChangeEvent } from "react-native";
import { mobileReachablePreviewURL, restoreSession, resumeSessionAgent, type DashboardSession, type OrchestratorLink } from "../api";
import { machineIdentity } from "../config";
import { haptics } from "../haptics";
import { openGitHub } from "../openGitHub";
import { InlineBanner, type InlineBannerProps } from "../InlineBanner";
import { useApp } from "../store";
import {
	mobileInterfaceTransitionIsActive,
	mobileInterfaceTransitionIsBusy,
	mobileInterfaceTransitionIsCancellable,
	mobileInterfaceTransitionRecoveryMessage,
	useInterfaceTransition,
} from "../session/useInterfaceTransition";
import type { Theme } from "../theme";
import { Frost, FROST_TAIL, frostStops } from "../frost";
import { stackScreenOptions, SYSTEM_GLASS_HEADER } from "../stackScreenOptions";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { getWorkspacePaths, openSessionShell } from "./api";
import { requestDockModel } from "./requestDockModel";
import type { ResponseQuoteRequest } from "./responseQuotes";
import { ChatComposer } from "./ChatComposer";
import { ChatLinkProvider } from "./ChatMarkdown";
import { ChatTimeline } from "./ChatTimeline";
import { useChatKeyboard } from "./useChatKeyboard";
import { ConversationTitle } from "./ConversationTitle";
import { chatSheetRoute, type ChatSheetEntry } from "./chatSheetRegistry";
import { conversationMenuGroups, type ConversationMenuAction, type ConversationMenuGroup } from "./conversationMenuModel";
import { quotaWarning } from "./conversationChrome";
import { shouldAutoResume } from "./autoResume";
import { controllerStoppedBanner, errorBanner, mcpBanner, quotaBanner, reauthBanner, rolledBackBanner, threadBanner, type BannerCopy } from "./conversationBanners";
import { contextReadout } from "./conversationChrome";
import { conversationActionError, conversationActionUnsupported } from "./conversationErrors";
import { providerCatalogLive, turnOptionsLoadFor } from "./turnOptionsCatalog";
import { brokenMcpServers, can, type ConversationSnapshot } from "./types";
import { useMobileConversation } from "./useConversation";
import { SessionStartupStatus } from "./SessionStartup";
import type { TimelineStartup } from "./ChatTimeline";
import { sessionStartupView, startupVisible } from "../session/sessionStartup";
import { startSerialPoll } from "../session/serialPoll";
import { STARTUP_POLL_MS } from "../session/startupPoll";
import { useStartupSession } from "../session/useStartupSession";
import { sessionHeaderButtons } from "../session/sessionHeaderButtons";
import { useSessionPreview } from "../session/useSessionPreview";
import { toolbarIcon } from "../toolbarIcon";
import { useChatViewLease } from "./useChatViewLease";
import { useOpenPage } from "../pageNavigation";
import { usePRSummaries } from "../usePRSummaries";
import { reviewRouteForPR, reviewRouteForSession, sessionPRReadyForReview } from "../reviewView";
import { space } from "../tokens";
import { Button, EmptyState } from "../ui";
import { backOr } from "../backNavigation";
import { userFacingError, NOT_PAIRED_ACTION_COPY } from "../connectionError";
import { ARCHIVE_SESSION_CONFIRM } from "../worker-action-model";
import { changedPathCount, fileRoute, filesRoute, FilesNavigationContext, type FilesNavigation } from "../files/navigation";
import { useChangeSummary } from "../files/useChangeSummary";
import { sessionLink } from "./automationMessages";
import { readSession } from "./sessionLookup";

type MobileChatSession = DashboardSession | OrchestratorLink;

/** How long the composer's height holds still before the timeline takes it: past its spring. */
const COMPOSER_SETTLE_MS = 120;

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
	const { config, currentHostId, connection, unreachable, linkHealth, projects, sessions, refresh: refreshBoard, setWorkerPinned, kill } = useApp();
	// The board's row, with its provisioning facts re-read every second while it starts.
	const session = useStartupSession(config, boardSession);
	const conversation = useMobileConversation(config, session.id);
	// Opening a hibernated chat wakes it, and only the lease request says when.
	// The catalog is asked for again too if it has none of its own yet: a page
	// cached as ready loads it before the wake, and that load can fail unseen.
	const { refresh: refreshOnView, loadTurnOptions: loadTurnOptionsOnView } = conversation;
	const onViewing = useCallback(() => {
		void refreshOnView();
		void loadTurnOptionsOnView({ refresh: true, ifMissing: true }).catch(() => {});
	}, [loadTurnOptionsOnView, refreshOnView]);
	useChatViewLease(config, session.id, onViewing);
	// A load that failed while the desktop was unreachable retries as soon as the
	// board's poll reconnects, which is what the offline state promises.
	// Keyed on a failed load, not a missing one, so the first mount doesn't send a
	// second request alongside the hook's own initial load.
	const loadFailed = !conversation.snapshot && Boolean(conversation.error);
	const refreshConversation = conversation.refresh;
	useEffect(() => {
		if (connection === "open" && loadFailed) void refreshConversation();
	}, [connection, loadFailed, refreshConversation]);
	const interfaceSwitch = useInterfaceTransition(config, session.id, refreshBoard);
	const [jumpToSequence, setJumpToSequence] = useState<number>();
	const clearJumpToSequence = useCallback(() => setJumpToSequence(undefined), []);
	// An earlier prompt open in its inline editor takes the keyboard, so the
	// composer steps aside (still mounted, keeping its draft and attachments).
	const [editingMessage, setEditingMessage] = useState(false);
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
	const chatKeyboard = useChatKeyboard({ underHeader: CLEAR_HEADER });
	const headerOverlap = useGlassHeaderOverlap();
	// On iOS 26 the banners float under the header, over the list's top, and the
	// list starts below them.
	const [bannersHeight, setBannersHeight] = useState(0);
	const [composerHeight, setComposerHeight] = useState(0);
	// The jump back to the newest message steps aside while the composer is open.
	const [composerOpen, setComposerOpen] = useState(false);
	// The composer springs open and closed, and its height changes every frame
	// of that. The timeline takes the height once it settles, rather than
	// re-rendering the conversation for each frame.
	const composerSettle = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	useEffect(() => () => clearTimeout(composerSettle.current), []);
	const measureComposer = useCallback((event: LayoutChangeEvent) => {
		const height = Math.round(event.nativeEvent.layout.height);
		clearTimeout(composerSettle.current);
		composerSettle.current = setTimeout(() => setComposerHeight(height), COMPOSER_SETTLE_MS);
	}, []);
	const keyboardVisible = chatKeyboard.keyboardVisible;
	const turnOptionsRequestedFor = useRef<string | undefined>(undefined);
	const terminated = "projectName" in session ? Boolean(session.isTerminal) : Boolean(session.isTerminated);
	const failedStart = "projectName" in session || session.provisionState !== "failed"
		? undefined
		: session.provisionError || "The session did not finish starting.";
	const interfaceTransitionActive = mobileInterfaceTransitionIsActive(interfaceSwitch.transition);
	// The usage sheet follows the conversation while it is up: the numbers move
	// with every turn.
	const latestSnapshot = useRef<ConversationSnapshot | undefined>(conversation.snapshot);
	const snapshotListeners = useRef(new Set<(snapshot: ConversationSnapshot) => void>());
	useLayoutEffect(() => {
		latestSnapshot.current = conversation.snapshot;
		const snapshot = conversation.snapshot;
		if (snapshot) snapshotListeners.current.forEach((listener) => listener(snapshot));
	}, [conversation.snapshot]);
	const subscribeSnapshot = useCallback((listener: (snapshot: ConversationSnapshot) => void) => {
		snapshotListeners.current.add(listener);
		if (latestSnapshot.current) listener(latestSnapshot.current);
		return () => { snapshotListeners.current.delete(listener); };
	}, []);
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

	// Loaded again when the provider comes up: a chat opened while hibernated or
	// connecting has no catalog to give, and the composer would otherwise keep the
	// empty one until the screen was reopened.
	const providerLive = conversation.snapshot ? providerCatalogLive(conversation.snapshot) : undefined;
	useEffect(() => {
		const load = turnOptionsLoadFor(turnOptionsRequestedFor.current, session.id, providerLive);
		if (!load) return;
		turnOptionsRequestedFor.current = load.key;
		void conversation.loadTurnOptions(load.refresh ? { refresh: true } : undefined).catch(() => {});
	}, [conversation.loadTurnOptions, providerLive, session.id]);

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
	// The PR ready for review, as a chip over the composer that opens it.
	const reviewPromptPR = "projectName" in session ? undefined : sessionPRReadyForReview(session);
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
	// The conversation scrolls up under a clear bar. iOS 26's is the system's
	// glass, which blurs it beneath the title, as Settings and the board do; the
	// edge effect is the system's own: setting `scrollEdgeEffects` from here
	// turned it off. Android's bar frosts it out with the page's colour (see
	// `frost`), as the board tabs' bars do. The route also holds terminal
	// sessions, so the stack's own bar comes back when this screen goes.
	useLayoutEffect(() => {
		if (!CLEAR_HEADER) return;
		navigation.setOptions({ headerTransparent: true, headerStyle: SYSTEM_GLASS_HEADER ? undefined : { backgroundColor: "transparent" } });
		return () => navigation.setOptions({ headerTransparent: false, headerStyle: stackScreenOptions(t).headerStyle });
	}, [navigation, t]);

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

	const openShell = useCallback(async () => {
		if (!config || openingShell) return;
		setOpeningShell(true);
		try {
			const shell = await openSessionShell(config, session.id, session.projectId);
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
	// An `ao://sessions/…` link opens that session's chat on this conversation's machine.
	const openLink = useCallback((url: string) => {
		const target = sessionLink(url);
		if (target) {
			router.push({ pathname: "/session/[id]", params: { id: target.sessionId, projectId: target.projectId, hostId: currentHostId } });
			return;
		}
		void openGitHub(mobileReachablePreviewURL(url, config?.host ?? "")?.href ?? url);
	}, [config?.host, currentHostId, router]);
	// A session the board leaves out (archived, say) is read once for its name.
	const [readSessionNames, setReadSessionNames] = useState<Readonly<Record<string, string>>>({});
	const nameOfSession = useCallback((sessionId: string) => sessions.find((candidate) => candidate.id === sessionId)?.displayName || readSessionNames[sessionId] || undefined, [readSessionNames, sessions]);
	const wantSessionName = useCallback((sessionId: string) => {
		if (!config) return;
		void readSession(config, sessionId).then((read) => {
			const name = read?.displayName;
			if (name) setReadSessionNames((current) => current[sessionId] === name ? current : { ...current, [sessionId]: name });
		});
	}, [config]);

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

	// Chat sheets take the keyboard's place rather than opening under it.
	const presentSheet = useCallback(async (entry: ChatSheetEntry) => {
		await dismissKeyboardBeforeSheet(keyboardVisible);
		router.push(chatSheetRoute(entry));
	}, [keyboardVisible, router]);
	const openUsage = useCallback(() => {
		const snapshot = latestSnapshot.current;
		if (snapshot) void presentSheet({ kind: "conversation-usage", sessionTitle: sessionName, snapshot, subscribeSnapshot });
	}, [presentSheet, sessionName, subscribeSnapshot]);

	// What the menu shows, as plain values: the conversation streams, and the menu
	// is rebuilt only when one of these changes, not on every event.
	const menuSnapshot = conversation.snapshot;
	const isOrchestrator = "projectName" in session;
	const menuReady = Boolean(menuSnapshot);
	// The daemon refuses to rename the project orchestrator.
	const menuCanCompact = Boolean(menuSnapshot && can(menuSnapshot, "compaction")) && !conversationActionUnsupported("compact", conversation.actionCodes.compact);
	// The ring on Compact: how much of the context window the conversation fills.
	const menuContextPercent = contextReadout(menuSnapshot?.usage)?.percent;
	const menuPinned = "projectName" in session ? false : Boolean(session.isPinned);
	const menuCompacting = conversation.pendingActions.includes("compact");
	const menuArtifactCount = "projectName" in session ? undefined : session.artifacts?.length;
	// Android's bar keeps only the "⋯"; iOS's leads with the switch, preview and files.
	const sessionToolsInMenu = Platform.OS === "android";
	const previewReady = useSessionPreview(config, session.id);
	const interfaceSwitchReady = Boolean(interfaceSwitch.status?.supported);
	const interfaceSwitchBusy = interfaceSwitch.starting;
	const menuSwitchBusy = interfaceSwitchBusy || interfaceTransitionActive;
	const menuGroups = useMemo(() => menuReady ? conversationMenuGroups({
		previewReady,
		artifactCount: menuArtifactCount,
		// Orchestrators are not pinned or archived from here; the board owns their lifecycle.
		canPin: !isOrchestrator,
		canDelete: !isOrchestrator,
		canCompact: menuCanCompact,
		pinned: menuPinned,
		openingShell,
		compacting: menuCompacting,
		turnInFlight: turnActive,
		contextPercent: menuContextPercent,
	}) : undefined, [isOrchestrator, menuArtifactCount, menuCanCompact, menuCompacting, menuContextPercent, menuPinned, menuReady, openingShell, previewReady, turnActive]);
	// The actions page follows what it shows while it is up (see `ConversationActionsEntry`).
	const latestMenuGroups = useRef(menuGroups);
	const menuGroupListeners = useRef(new Set<(groups: readonly ConversationMenuGroup[]) => void>());
	useEffect(() => {
		latestMenuGroups.current = menuGroups;
		if (menuGroups) menuGroupListeners.current.forEach((listener) => listener(menuGroups));
	}, [menuGroups]);
	const subscribeMenuGroups = useCallback((listener: (groups: readonly ConversationMenuGroup[]) => void) => {
		if (latestMenuGroups.current) listener(latestMenuGroups.current);
		menuGroupListeners.current.add(listener);
		return () => { menuGroupListeners.current.delete(listener); };
	}, []);

	// Each menu item's work, read when it is chosen, so the menu does not have to
	// change whenever a callback does.
	const menuActions: Record<ConversationMenuAction, () => void> = {
		preview: () => router.push({ pathname: "/preview/[id]", params: { id: session.id, title, hostId: currentHostId } }),
		shell: () => void openShell(),
		artifacts: () => router.push({ pathname: "/artifacts/[sessionId]", params: { sessionId: session.id, title: sessionName, hostId: currentHostId } }),
		pull_requests: () => {
			const route = !("projectName" in session) && (session.prs?.length ?? (session.pr ? 1 : 0)) <= 1
				? reviewRouteForSession(session, currentHostId)
				: undefined;
			if (route) openPage(route);
			// Several PRs open Pull Requests over this page, so closing it
			// returns here.
			else openPage("/prs");
		},
		pin: () => {
			if ("projectName" in session) return;
			void setWorkerPinned(session.id, !session.isPinned).catch(() => {});
		},
		compact: () => void conversation.compact().catch(() => {}),
		delete: () => {
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
	const menuActionsRef = useRef(menuActions);
	menuActionsRef.current = menuActions;
	const runMenuAction = useCallback((action: ConversationMenuAction) => menuActionsRef.current[action](), []);

	// The header leads with the same buttons as the terminal's (see
	// `sessionHeaderButtons`), and its "⋯" opens the rest of the actions as a
	// page. A folded PR card comes back from the strip it leaves in the composer,
	// not from here. Each button's work is read when it is pressed, so the header
	// is registered again only when what it shows changes.
	const headerPress = useRef({ requestInterfaceSwitch, openPreview: () => {}, openFiles: filesNavigation.openChanges });
	headerPress.current = {
		requestInterfaceSwitch,
		openPreview: () => router.push({ pathname: "/preview/[id]", params: { id: session.id, title, hostId: currentHostId } }),
		openFiles: filesNavigation.openChanges,
	};
	const openActions = useCallback(() => {
		const groups = latestMenuGroups.current;
		if (groups) void presentSheet({ kind: "conversation-actions", title, groups, subscribeGroups: subscribeMenuGroups, onSelect: runMenuAction });
	}, [presentSheet, runMenuAction, subscribeMenuGroups, title]);
	const headerItems = useMemo(() => menuReady ? (
		<Stack.Toolbar placement="right">
			{sessionToolsInMenu ? null : sessionHeaderButtons({
				switchTo: "terminal",
				switchReady: interfaceSwitchReady,
				switchBusy: interfaceSwitchBusy,
				switchBusyLabel: "Switching to Terminal UI",
				switchDisabled: interfaceTransitionActive,
				previewReady,
				onSwitch: () => headerPress.current.requestInterfaceSwitch(),
				onPreview: () => headerPress.current.openPreview(),
				onFiles: () => headerPress.current.openFiles(),
				t,
			})}
			<Stack.Toolbar.Button icon={toolbarIcon("more")} accessibilityLabel="Conversation actions" onPress={openActions} />
		</Stack.Toolbar>
	) : null, [interfaceSwitchBusy, interfaceSwitchReady, interfaceTransitionActive, menuReady, openActions, previewReady, sessionToolsInMenu, t]);

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

	if (failedStart && !conversation.snapshot) {
		return <Failure><EmptyState icon="warning" iconColor={t.red} title="Session failed to start" message={failedStart} action={<Button title="Retry" icon="refresh" loading={resuming} onPress={() => void resume()} />} /></Failure>;
	}
	// A session that is still starting shows its checklist straight away rather than a bare spinner.
	if (conversation.loading && !conversation.snapshot && startupView && !startupView.failed) {
		return <View style={[styles.startupLoading, { paddingTop: space.md + headerOverlap }]}><SessionStartupStatus startup={startupView} /></View>;
	}
	if (conversation.loading && !conversation.snapshot) return <View style={[styles.center, { paddingTop: headerOverlap }]}><EmptyState busy title="Loading conversation…" /></View>;
	if (conversation.unavailable) {
		return <Failure>
			<EmptyState
				icon="warning"
				title="Conversation unavailable"
				message={`${conversation.unavailable.message}\n\nThe worktree is untouched. You can still open a plain shell in it.`}
				action={<Button title="Open worktree shell" icon="terminal" loading={openingShell} onPress={() => void openShell()} />}
			/>
		</Failure>;
	}
	// The board's poll is the app's view of the link: when it is down, say so in
	// the board's words instead of echoing whatever this request failed with.
	if (!conversation.snapshot && linkHealth === "phone-offline") {
		return <Failure><EmptyState icon="offline" title="You're offline" message="This conversation loads once this phone is back online." /></Failure>;
	}
	if (!conversation.snapshot && connection !== "open" && linkHealth === "reconnecting") {
		return <Failure><EmptyState icon="refresh" title="Reconnecting…" message="This conversation loads once the app reconnects." /></Failure>;
	}
	if (!conversation.snapshot && unreachable) {
		return <Failure><EmptyState icon="offline" title="This machine is offline" message="This conversation loads once the app reconnects." action={<Button title="Retry" icon="refresh" onPress={() => void conversation.refresh()} />} /></Failure>;
	}
	if (!conversation.snapshot) {
		return <Failure><EmptyState icon="warning" iconColor={t.red} title="Couldn't load the conversation" message={conversation.error || "The machine didn't return this conversation. Try again."} action={<Button title="Retry" icon="refresh" onPress={() => void conversation.refresh()} />} /></Failure>;
	}

	const snapshot = conversation.snapshot;
	const active = snapshot.turns.some((turn) => turn.state === "running" || turn.state === "queued");
	const brokenServers = brokenMcpServers(snapshot);
	const rolledBack = snapshot.turns.filter((turn) => turn.rolledBack).length;
	const quota = quotaWarning(snapshot.rateLimits);
	const compactSupported = can(snapshot, "compaction") && !conversationActionUnsupported("compact", conversation.actionCodes.compact);
	const mcpReloadSupported = can(snapshot, "mcp_reload") && !conversationActionUnsupported("mcp", conversation.actionCodes.mcp);
	const steerUnsupported = conversationActionUnsupported("steer", conversation.actionCodes.steer);

	const banners = (
		<>
			{interfaceTransitionActive ? (
				<InlineBanner
					tone="warning"
					icon="busy"
					title={interfaceTransitionBanner.text}
					action={interfaceTransitionBanner.action}
					secondary={interfaceTransitionBanner.secondary}
					onPress={interfaceTransitionBanner.onPress}
					onSecondary={interfaceTransitionBanner.onSecondary}
				/>
			) : interfaceTransitionNotice ? (
				<InlineBanner
					tone={interfaceTransitionRecovered ? "warning" : "danger"}
					icon={interfaceTransitionRecovered ? "checkCircle" : "warning"}
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
			{conversation.error ? <DismissibleBanner copy={errorBanner("load", conversation.error)} dismissed={dismissedBanners} onDismiss={dismissBanner} tone="danger" icon="offline" action="Retry" onPress={() => void conversation.refresh()} /> : null}
			{quota ? <DismissibleBanner copy={quotaBanner(quota)} dismissed={dismissedBanners} onDismiss={dismissBanner} tone={quota.severity === "critical" ? "danger" : "warning"} icon="warning" action="Details" onPress={openUsage} /> : null}
			{conversation.actionError && conversation.actionError !== conversation.error ? <DismissibleBanner copy={errorBanner("action", conversation.actionError)} dismissed={dismissedBanners} onDismiss={dismissBanner} tone="danger" icon="error" /> : null}
			{rolledBack ? <DismissibleBanner copy={rolledBackBanner(rolledBack)} dismissed={dismissedBanners} onDismiss={dismissBanner} tone="muted" icon={{ ios: "arrow.uturn.backward", android: "undo" }} /> : null}
			{conversation.pendingSends.map((pendingSend) => pendingSend.state === "failed" ? <InlineBanner key={pendingSend.id} tone="danger" icon="error" title="Message delivery uncertain" body={pendingSend.error || "Delivery failed"} action="Retry" secondary="Discard" onPress={() => void conversation.retrySend(pendingSend.id).catch(() => {})} onSecondary={() => void conversation.discardSend(pendingSend.id).catch(() => {})} /> : null)}
		</>
	);

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
			{headerItems}
			{CLEAR_HEADER ? null : banners}
			<View style={styles.conversation}>
			<ChatLinkProvider onLinkOpen={openLink} sessionName={nameOfSession} onSessionNameNeeded={wantSessionName} onAddToChat={conversation.snapshot?.supportsExcerpts ? addResponseToChat : undefined}>
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
					onEditMessage={conversation.editMessage}
					onEditingChange={setEditingMessage}
					jumpToSequence={jumpToSequence}
					onJumpHandled={clearJumpToSequence}
					answeredBelow={answeredBelow}
					startup={timelineStartup}
					underHeader={headerOverlap}
					topInset={bannersHeight}
					bottomInset={composerHeight}
					jumpHidden={composerOpen}
				/>
				</FilesNavigationContext.Provider>
			</ChatLinkProvider>
			{/* Android's frost behind the clear bar: solid under the title, then
			    dissolving into the page, so the conversation frosts out as it
			    passes under it rather than meeting a hard edge. */}
			{CLEAR_HEADER && !SYSTEM_GLASS_HEADER ? (
				<Frost from="top" stops={frostStops(headerOverlap)} style={[styles.headerFrost, { height: headerOverlap + FROST_TAIL }]} />
			) : null}
			{CLEAR_HEADER ? (
				<View pointerEvents="box-none" style={[styles.floatingBanners, { top: headerOverlap }]} onLayout={(event) => setBannersHeight(event.nativeEvent.layout.height)}>
					{banners}
				</View>
			) : null}
			{/* The composer floats over the conversation, which scrolls on under its
			    glass; the timeline leaves its measured height clear at the end. */}
			<View pointerEvents="box-none" style={styles.floatingComposer} onLayout={measureComposer}>
			<View style={editingMessage ? styles.composerHidden : undefined}>
				<ChatComposer
					key={composerKey}
					quoteRequest={quoteRequest?.composerKey === composerKey ? quoteRequest : undefined}
					onQuoteHandled={quoteHandled}
					reviewPR={reviewPromptPR}
					reviewPRSummary={reviewPromptSummary}
					onOpenReview={reviewPromptPR ? () => openPage(reviewRouteForPR(session.id, reviewPromptPR, currentHostId)) : undefined}
					onExpandedChange={setComposerOpen}
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
					onSettings={conversation.chooseSettings}
					onConfigOption={conversation.setConfigOption}
					restingInset={chatKeyboard.restingInset}
					dockRiseOverride={chatKeyboard.dockRiseOverride}
					openingTurnId={startupView?.openingTurnId}
					startingAgentName={startupShown && !startupView?.failed ? startupView?.agentName : undefined}
				/>
			</View>
			</View>
			</View>
			{editingMessage ? <View style={{ height: chatKeyboard.restingInset }} /> : null}
		</KeyboardAvoidingView>
	);
}

function ConversationBanners({ snapshot, startFailure, starting, brokenServers, resuming, resumeError, terminated, mcpReloading, mcpError, mcpReloadSupported, turnInFlight, onResume, onReload, onOpenShell, dismissed, onDismiss }: { snapshot: NonNullable<ReturnType<typeof useMobileConversation>["snapshot"]>; startFailure?: string; starting: boolean; brokenServers: ReturnType<typeof brokenMcpServers>; resuming: boolean; resumeError?: string; terminated: boolean; mcpReloading: boolean; mcpError?: string; mcpReloadSupported: boolean; turnInFlight: boolean; onResume(): void; onReload(): void; onOpenShell(): void; dismissed: ReadonlySet<string>; onDismiss(key: string): void }) {
	const thread = snapshot.threadState;
	const reauthAt = snapshot.account?.reauthRequiredAt;
	return <>
		{reauthAt ? <DismissibleBanner copy={reauthBanner(reauthAt, signInCommand(snapshot.harness))} dismissed={dismissed} onDismiss={onDismiss} tone="danger" icon={{ ios: "key", android: "key" }} action="Open shell" onPress={onOpenShell} /> : null}
		{startFailure ? <DismissibleBanner copy={{ key: `start:${startFailure}`, title: "Session failed to start", body: startFailure }} dismissed={dismissed} onDismiss={onDismiss} tone="danger" icon="warning" action={resuming ? "Retrying…" : "Retry"} secondary="Shell" onPress={resuming ? undefined : onResume} onSecondary={onOpenShell} /> : !starting && snapshot.controller.state === "stopped" ? <DismissibleBanner copy={controllerStoppedBanner(terminated, resumeError ?? snapshot.controller.error)} dismissed={dismissed} onDismiss={onDismiss} tone="danger" icon={{ ios: "power", android: "power_settings_new" }} action={terminated ? (resuming ? "Restoring…" : "Restore") : (resuming ? "Resuming…" : "Resume")} secondary="Shell" onPress={resuming ? undefined : onResume} onSecondary={onOpenShell} /> : null}
		{/* Passing states clear themselves, so there is nothing to close. */}
		{!startFailure && !starting && (snapshot.controller.state === "recovering" || snapshot.controller.state === "connecting") ? <InlineBanner tone="warning" icon="busy" title={snapshot.controller.state === "recovering" ? "Reconnecting to the agent…" : "Starting the agent…"} /> : null}
		{threadBanner(thread?.status) ? <DismissibleBanner copy={threadBanner(thread?.status)!} dismissed={dismissed} onDismiss={onDismiss} tone={thread?.status === "system_error" ? "danger" : "warning"} icon="warning" /> : null}
		{brokenServers.length ? <DismissibleBanner copy={mcpBanner(brokenServers, mcpError)!} dismissed={dismissed} onDismiss={onDismiss} tone="warning" icon={{ ios: "wrench.and.screwdriver", android: "build" }} action={mcpReloadSupported && !turnInFlight ? (mcpReloading ? "Reloading…" : "Reload") : undefined} onPress={mcpReloading ? undefined : onReload} /> : null}
	</>;
}

/** A banner the reader can close; it stays closed until what it reports changes. */
function DismissibleBanner({ copy, dismissed, onDismiss, ...props }: Omit<InlineBannerProps, "title" | "body" | "onDismiss"> & { copy: BannerCopy | undefined; dismissed: ReadonlySet<string>; onDismiss(key: string): void }) {
	if (!copy || dismissed.has(copy.key)) return null;
	return <InlineBanner {...props} title={copy.title} body={copy.body} onDismiss={() => onDismiss(copy.key)} />;
}

/** A screen-filling state the conversation cannot get past, announced as it appears. */
function Failure({ children }: { children: ReactNode }) {
	const styles = useThemedStyles(makeStyles);
	const headerOverlap = useGlassHeaderOverlap();
	return <View accessibilityRole="alert" style={[styles.center, { paddingTop: headerOverlap }]}>{children}</View>;
}

/**
 * The conversation runs on under a clear bar: iOS 26's glass, and on Android
 * the bar the frost lies behind. Older iOS keeps the stack's solid bar.
 */
const CLEAR_HEADER = SYSTEM_GLASS_HEADER || Platform.OS === "android";

/** How much of the screen the clear header covers; nothing under a solid one. */
function useGlassHeaderOverlap(): number {
	const headerHeight = useHeaderHeight();
	return CLEAR_HEADER ? headerHeight : 0;
}

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
	composerHidden: { display: "none" },
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, backgroundColor: t.bgBase },
	conversation: { flex: 1 },
	// The page's colour, so the conversation scrolling up passes under the
	// banners rather than showing between them.
	floatingComposer: { position: "absolute", left: 0, right: 0, bottom: 0 },
	floatingBanners: { position: "absolute", left: 0, right: 0, backgroundColor: t.bgBase },
	headerFrost: { position: "absolute", top: 0, left: 0, right: 0 },
	// Where the timeline will draw the checklist, so the handoff to it does not move it.
	startupLoading: { flex: 1, backgroundColor: t.bgBase, paddingHorizontal: space.xl, paddingTop: space.md },
});
