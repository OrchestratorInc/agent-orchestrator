import { Stack, useNavigation, useRouter } from "expo-router";
import { useHeaderHeight, useIsFocused } from "expo-router/react-navigation";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Alert, Keyboard, Platform, StyleSheet, View, type LayoutChangeEvent } from "react-native";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { backOr } from "../backNavigation";
import { ChatComposer } from "../chat/ChatComposer";
import { sessionLink } from "../chat/automationMessages";
import { ChatLinkProvider } from "../chat/ChatMarkdown";
import { ChatTimeline, type TimelineStartup } from "../chat/ChatTimeline";
import { chatSheetRoute } from "../chat/chatSheetRegistry";
import type { ConversationMenuAction, ConversationMenuGroup } from "../chat/conversationMenuModel";
import { ConversationTitle } from "../chat/ConversationTitle";
import { KeyboardFollower } from "../chat/KeyboardFollower";
import { MessageFileSourceContext } from "../chat/messageFiles";
import { requestDockModel } from "../chat/requestDockModel";
import { SessionStartupStatus } from "../chat/SessionStartup";
import type { ChatConfigOption, ChatImage, ChatResource, MessageFile, TurnSettings } from "../chat/types";
import { useChatKeyboard } from "../chat/useChatKeyboard";
import { cloudFilesBackend } from "../files/cloudBackend";
import { changedPathCount, fileRoute, filesRoute, FilesNavigationContext, type FilesNavigation } from "../files/navigation";
import { useBackendChangeSummary } from "../files/useChangeSummary";
import { Frost, FROST_TAIL, frostStops } from "../frost";
import { haptics } from "../haptics";
import { InlineBanner } from "../InlineBanner";
import { openGitHub } from "../openGitHub";
import { useOpenPage } from "../pageNavigation";
import { reviewRouteForPR, sessionPRReadyForReview } from "../reviewView";
import { InterfaceSwitchCard } from "../session/InterfaceSwitchCard";
import { startupVisible } from "../session/sessionStartup";
import {
	mobileInterfaceTransitionIsActive,
	mobileInterfaceTransitionIsBusy,
	mobileInterfaceTransitionIsCancellable,
	mobileInterfaceTransitionRecoveryMessage,
	useInterfaceTransitionWith,
} from "../session/useInterfaceTransition";
import { Spinner } from "../Spinner";
import { stackScreenOptions, SYSTEM_GLASS_HEADER } from "../stackScreenOptions";
import { useApp } from "../store";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { space } from "../tokens";
import { toolbarIcon } from "../toolbarIcon";
import { Button, EmptyState } from "../ui";
import { cloudAgentLabel } from "./agents";
import { cloudAttachmentUri, cloudUploadDeps } from "./attachmentFiles";
import { cloudAttachmentLimit, composerUploadSources, MAX_CLOUD_ATTACHMENTS, uploadCloudSource } from "./attachments";
import { useCloud } from "./CloudProvider";
import { CloudSignIn } from "./components";
import { CloudTerminalView } from "./CloudTerminalView";
import { cloudErrorMessage } from "./errors";
import { cloudInterfaceTransitionBackend, cloudSwitchCovers, cloudSwitchNoticeShown, cloudTerminalBusy, useCloudSwitchHandoff } from "./interfaceTransition";
import { interfaceModeName, switchNotice, switchPhaseLabel, switchPhaseShortLabel } from "./interfaceSwitchCopy";
import { cloudChatStageTitle, cloudLifecycleStage, cloudStartupProblem } from "./lifecycle";
import { CLOUD_HOST_ID, cloudSessionRow } from "./mapping";
import { ARCHIVE_CLOUD_SESSION_CONFIRM, cloudSessionMenuGroups } from "./sessionMenu";
import { useCloudStartup } from "./startup";
import { useCloudConversation } from "./useCloudConversation";
import { useCloudPRSummaries } from "./useCloudPRSummaries";

/** A Terminal UI session shows its agent's terminal or its conversation; the sandbox shell is its own page. */
type SessionView = "chat" | "agent";

/** How long the composer's height holds still before the timeline takes it: past its spring. */
const COMPOSER_SETTLE_MS = 120;

/**
 * The conversation runs on under a clear bar: iOS 26's glass, and on Android
 * the bar the frost lies behind. Older iOS keeps the stack's solid bar.
 */
const CLEAR_HEADER = SYSTEM_GLASS_HEADER || Platform.OS === "android";

/** The worker's Plan mode, as the composer's settings show a provider's switch. */
const PLAN_OPTION_ID = "plan";

/**
 * An Orchestrator Cloud session, drawn with the local chat's own parts: the
 * glass header with the agent's name and state, the timeline, the floating
 * composer (model, effort, approvals and attachments), the request card in its
 * place, banners for what the sandbox is doing, and "⋯" opening the same
 * actions sheet. A Terminal UI session shows its agent's terminal instead, with
 * the composer's Chat switch to come back. "⋯" switches the session itself
 * between Chat and Terminal UI, as a machine session's screens do. Opening it
 * wakes a paused sandbox and keeps it awake while read.
 */
export function CloudSessionScreen({ sessionId }: { sessionId: string }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const navigation = useNavigation();
	const router = useRouter();
	const openPage = useOpenPage();
	const focused = useIsFocused();
	const cloud = useCloud();
	const { kill, restore: restoreWorker, setWorkerPinned } = useApp();
	const { client, org, status, sessions, projects } = cloud;
	const listed = useMemo(() => sessions.find((item) => item.id === sessionId), [sessionId, sessions]);
	const conversation = useCloudConversation({ client, orgId: org?.id, sessionId, initial: listed, active: focused && status === "ready" });
	const session = conversation.session ?? listed ?? null;
	const snapshot = conversation.snapshot;
	const startup = useCloudStartup(session, snapshot);
	const [view, setView] = useState<SessionView | null>(null);
	const [deciding, setDeciding] = useState(false);
	const [busyAction, setBusyAction] = useState<"restore" | "retry" | null>(null);
	const [jumpToSequence, setJumpToSequence] = useState<number>();
	const clearJumpToSequence = useCallback(() => setJumpToSequence(undefined), []);
	const [dismissedRequest, setDismissedRequest] = useState<number>();
	const restoreRequest = useCallback(() => setDismissedRequest(undefined), []);
	const chatKeyboard = useChatKeyboard({ underHeader: CLEAR_HEADER });
	const headerHeight = useHeaderHeight();
	const headerOverlap = CLEAR_HEADER ? headerHeight : 0;
	const [bannersHeight, setBannersHeight] = useState(0);
	const [composerHeight, setComposerHeight] = useState(0);
	const [composerOpen, setComposerOpen] = useState(false);
	const composerSettle = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	useEffect(() => () => clearTimeout(composerSettle.current), []);
	const measureComposer = useCallback((event: LayoutChangeEvent) => {
		const height = Math.round(event.nativeEvent.layout.height);
		clearTimeout(composerSettle.current);
		composerSettle.current = setTimeout(() => setComposerHeight(height), COMPOSER_SETTLE_MS);
	}, []);

	const tui = session?.interfaceMode === "tui";
	const current: SessionView = view ?? (tui ? "agent" : "chat");
	// A switch of interface (from here or the desktop) shows the new one's own view.
	useEffect(() => { setView(null); }, [tui]);
	const terminated = Boolean(session?.isTerminated);
	const project = session ? projects.find((item) => item.id === session.projectId) : undefined;
	const harness = session?.harness ?? "";
	const title = session?.displayName?.trim() || (session?.kind === "orchestrator" ? "Orchestrator" : cloudAgentLabel(harness));
	const headerState = snapshot?.controller.state ?? (terminated ? "stopped" : undefined);
	// Whether this view has seen the agent connected or working: after that, the
	// session row lagging the live chat on the runtime link is not a reconnect
	// worth a banner (cloudChatStageTitle).
	const agentWorking = snapshot?.controller.state === "busy";
	const reachedConnected = session ? cloudLifecycleStage(session) === "connected" : false;
	const [connectedOnce, setConnectedOnce] = useState(false);
	useEffect(() => {
		if (agentWorking || reachedConnected) setConnectedOnce(true);
	}, [agentWorking, reachedConnected]);

	const runAction = useCallback(async (key: "restore" | "retry", action: () => Promise<void>) => {
		setBusyAction(key);
		try { await action(); }
		catch (error) {
			haptics.error();
			Alert.alert(key === "restore" ? "Couldn't restore this session" : "Couldn't start this session", cloudErrorMessage(error));
		}
		finally { setBusyAction(null); }
	}, []);
	const restore = useCallback(() => {
		void runAction("restore", async () => {
			await restoreWorker(sessionId, CLOUD_HOST_ID);
			await conversation.refresh();
		});
	}, [conversation, restoreWorker, runAction, sessionId]);
	const retryStart = useCallback(() => {
		void runAction("retry", async () => { await conversation.retryStartup(); await cloud.refreshSessions(); });
	}, [cloud, conversation, runAction]);

	// Switching the session between Chat and Terminal UI, as a machine
	// session's screens do and the desktop's switch asks Cloud. A finished
	// switch reads the session again, which moves this screen to the new view.
	const interfaceBackend = useMemo(() => org?.id && !terminated ? cloudInterfaceTransitionBackend(client, org.id) : null, [client, org?.id, terminated]);
	const { refresh: refreshConversation } = conversation;
	const { refreshSessions } = cloud;
	const readSwitchedSession = useCallback(async () => {
		await Promise.all([refreshConversation(), refreshSessions()]);
	}, [refreshConversation, refreshSessions]);
	const interfaceSwitch = useInterfaceTransitionWith(interfaceBackend, sessionId, readSwitchedSession);
	const switchTarget = tui ? "tui" as const : "chat" as const;
	const switchTo = tui ? "chat" as const : "tui" as const;
	const transition = interfaceSwitch.transition;
	const switchActive = mobileInterfaceTransitionIsActive(transition);
	// Holds the card until this screen shows the new interface (Cloud drops a finished switch from its status).
	const handoff = useCloudSwitchHandoff(transition, switchTarget, readSwitchedSession);
	const chatBusy = Boolean(snapshot?.turns.some((turn) => turn.state === "running" || turn.state === "queued"));
	// From the tap until Cloud answers with the switch, the card is already up.
	const [launchingSwitch, setLaunchingSwitch] = useState(false);
	const switchSourceBusy = tui ? Boolean(session && cloudTerminalBusy(session)) : chatBusy;
	// The card covers the screen from the tap; a banner says it only while a
	// drain waits on the agent's turn, with the conversation still readable.
	const switchCardShown = cloudSwitchCovers(transition, { launching: launchingSwitch, sourceBusy: switchSourceBusy }) || handoff.pending;
	const switchRecovery = mobileInterfaceTransitionRecoveryMessage(transition);
	const notice = switchActive || !cloudSwitchNoticeShown(transition) ? undefined : switchNotice(transition);
	const canSwitch = !terminated && (Boolean(interfaceSwitch.status?.supported) || switchActive);
	const switching = interfaceSwitch.starting || launchingSwitch || switchActive || handoff.pending;
	// Terminal UI starts on the model and effort chosen in the chat, as the desktop's switch sends them.
	const chosenModel = conversation.settings.model;
	const chosenEffort = conversation.settings.reasoningEffort;
	// `cover`: put the card up now. Not for "Finish, then switch", whose banner waits on the turn.
	const startSwitch = useCallback(async (policy: "drain" | "interrupt", cover = true) => {
		if (cover) setLaunchingSwitch(true);
		try {
			await interfaceSwitch.start(switchTo, policy, { model: chosenModel, reasoningEffort: chosenEffort });
		} catch (error) {
			Alert.alert("Couldn't switch interface", cloudErrorMessage(error));
		} finally {
			setLaunchingSwitch(false);
		}
	}, [chosenEffort, chosenModel, interfaceSwitch, switchTo]);
	// Work in flight is finished or stopped first, as the person chooses.
	const askSwitchPolicy = useCallback(() => {
		Alert.alert(`Switch to ${interfaceModeName(switchTo)}?`, "Keep the same Orchestrator session, worktree, and native agent conversation.", [
			{ text: `Keep ${interfaceModeName(switchTarget)}`, style: "cancel" },
			{ text: "Finish, then switch", onPress: () => void startSwitch("drain", false) },
			{ text: "Stop and switch", style: "destructive", onPress: () => void startSwitch("interrupt") },
		]);
	}, [startSwitch, switchTarget, switchTo]);
	const switchOrgId = org?.id;
	const requestSwitch = useCallback(() => {
		if (switching || !switchOrgId) return;
		haptics.tap();
		if (switchTo === "tui") {
			if (chatBusy) askSwitchPolicy();
			else void startSwitch("drain");
			return;
		}
		// From Terminal UI the list can lag what was typed in the terminal: read
		// the session first. An idle terminal is stopped directly (cloudTerminalBusy).
		setLaunchingSwitch(true);
		void client.getSession(switchOrgId, sessionId).then(
			({ session: latest }) => {
				if (cloudTerminalBusy(latest)) {
					setLaunchingSwitch(false);
					askSwitchPolicy();
				} else void startSwitch("interrupt");
			},
			() => { setLaunchingSwitch(false); askSwitchPolicy(); },
		);
	}, [askSwitchPolicy, chatBusy, client, sessionId, startSwitch, switchOrgId, switchTo, switching]);
	const cancelSwitch = useCallback(() => void interfaceSwitch.cancel().catch(() => {}), [interfaceSwitch]);
	const [recheckingSwitch, setRecheckingSwitch] = useState(false);
	const recheckSwitch = useCallback(async () => {
		setRecheckingSwitch(true);
		try { await interfaceSwitch.refresh(); } finally { setRecheckingSwitch(false); }
	}, [interfaceSwitch]);
	const [readingSwitched, setReadingSwitched] = useState(false);
	const retryHandoff = useCallback(async () => {
		setReadingSwitched(true);
		try { await readSwitchedSession(); } catch { /* the card stays with Retry */ } finally { setReadingSwitched(false); }
	}, [readSwitchedSession]);

	// Files go up to the session when the message is sent, where the
	// organization's plan takes them; a sent one shows from a copy kept on the phone.
	const attachmentLimit = cloudAttachmentLimit(org);
	const orgId = org?.id;
	const fileSource = useMemo(() => orgId
		? (file: MessageFile) => cloudAttachmentUri(client, orgId, sessionId, file)
		: null, [client, orgId, sessionId]);
	const { send: sendMessage, steer: steerTurn } = conversation;
	const send = useCallback(async (text: string, images?: ChatImage[], resources?: ChatResource[]) => {
		const sources = composerUploadSources(images, resources, cloudUploadDeps.readBytes);
		if (sources.length && (!attachmentLimit || !orgId)) throw new Error("This organization's plan doesn't take attachments.");
		if (sources.length > MAX_CLOUD_ATTACHMENTS) throw new Error(`A message can carry up to ${MAX_CLOUD_ATTACHMENTS} files.`);
		const attachmentIds: string[] = [];
		for (const source of sources) {
			const uploaded = await uploadCloudSource(client, { orgId: orgId!, sessionId }, source, attachmentLimit!, cloudUploadDeps.sha256);
			attachmentIds.push(uploaded.id);
		}
		await sendMessage(text, attachmentIds);
		return `${Date.now()}`;
	}, [attachmentLimit, client, orgId, sendMessage, sessionId]);
	const steer = useCallback(async (text: string) => {
		await steerTurn(text);
		return `${Date.now()}`;
	}, [steerTurn]);

	// The worker's Plan mode (Claude Code), as a switch among the composer's settings.
	const planOn = Boolean(conversation.settings.plan);
	const configOptions = useMemo<ChatConfigOption[] | undefined>(() => conversation.planAvailable ? [{
		id: PLAN_OPTION_ID,
		name: "Plan mode",
		description: "The agent plans first and changes nothing",
		type: "boolean",
		currentBoolean: planOn,
		choices: [],
	}] : undefined, [conversation.planAvailable, planOn]);
	const { chooseSettings, settings } = conversation;
	const chooseOption = useCallback(async (id: string, value: { value: string } | { enabled: boolean }) => {
		if (id === PLAN_OPTION_ID && "enabled" in value) await chooseSettings({ ...settings, plan: value.enabled });
		return (configOptions ?? []).map((option) => option.id === PLAN_OPTION_ID && "enabled" in value ? { ...option, currentBoolean: value.enabled } : option);
	}, [chooseSettings, configOptions, settings]);
	const chooseTurnSettings = useCallback((next: TurnSettings) => chooseSettings({ ...settings, ...next }), [chooseSettings, settings]);

	const decide = useCallback(async (requestId: string, decisionId: string) => {
		setDeciding(true);
		try { await conversation.decide(requestId, decisionId); }
		catch (error) { Alert.alert("Couldn't answer", cloudErrorMessage(error)); }
		finally { setDeciding(false); }
	}, [conversation]);
	const resolveInput = useCallback(async () => {
		Alert.alert("Answer on the desktop", "Orchestrator Cloud doesn't take answers to this kind of question from the phone yet.");
	}, []);

	// The blocking request takes the composer's place until it is answered.
	const request = requestDockModel(snapshot ?? undefined, { approval: deciding, input: false });
	const requestDismissed = request ? dismissedRequest === request.sequence : false;
	const answeredBelow = request && !requestDismissed && request.canAnswerInline ? request.sequence : undefined;
	const cardShowing = Boolean(request && !requestDismissed);
	useLayoutEffect(() => {
		navigation.setOptions({ gestureEnabled: !cardShowing });
	}, [cardShowing, navigation]);

	useLayoutEffect(() => {
		navigation.setOptions({
			headerTitle: () => (
				<ConversationTitle
					title={title || "Cloud session"}
					// The project and the agent, as a local chat's bar reads; the state
					// is the dot's alone (the sandbox's own steps are banners below).
					subtitle={[project?.displayName ?? "Cloud", harness ? cloudAgentLabel(harness) : ""].filter(Boolean).join(" · ")}
					harness={harness || undefined}
					state={headerState}
				/>
			),
		});
	}, [harness, headerState, navigation, project?.displayName, title]);
	// The conversation scrolls up under a clear bar, as a local chat's does.
	useLayoutEffect(() => {
		if (!CLEAR_HEADER) return;
		navigation.setOptions({ headerTransparent: true, headerStyle: SYSTEM_GLASS_HEADER ? undefined : { backgroundColor: "transparent" } });
		return () => navigation.setOptions({ headerTransparent: false, headerStyle: stackScreenOptions(t).headerStyle });
	}, [navigation, t]);

	// What the actions sheet shows, as plain values, so a poll that changes
	// nothing it shows does not rebuild it.
	const pinned = (listed ?? session)?.pinned === true;
	const pr = session?.prs?.find((item) => item.state === "open" || item.state === "draft") ?? session?.prs?.[0];
	const prNumber = pr?.number;
	const prUrl = pr?.url;
	// The pull request ready for review, as a chip over the composer, with the
	// detail its card shows (title, checks, review) once Cloud has answered.
	const reviewPromptPR = useMemo(() => session ? sessionPRReadyForReview(cloudSessionRow(session)) : undefined, [session]);
	const reviewSummaries = useCloudPRSummaries(reviewPromptPR && session ? [session.id] : NO_IDS);
	const reviewPromptSummary = reviewPromptPR && session ? reviewSummaries.summaryFor(session.id, reviewPromptPR.number) : undefined;
	const hasSession = Boolean(session);
	// Its artifact files open from "⋯" where the organization has artifacts.
	const artifactsOffered = Boolean((org?.offering ?? cloud.me?.offering)?.artifacts);
	const artifactCount = session?.artifactCount;
	const menuGroups = useMemo(() => hasSession ? cloudSessionMenuGroups({
		terminated,
		pinned,
		canShowAgentTerminal: tui && current === "chat",
		interfaceSwitch: canSwitch ? { to: switchTo, switching } : undefined,
		pullRequest: prNumber === undefined ? undefined : { number: prNumber },
		projectName: project?.displayName,
		artifactCount: artifactsOffered ? artifactCount : undefined,
	}) : undefined, [artifactCount, artifactsOffered, canSwitch, current, hasSession, pinned, prNumber, project?.displayName, switchTo, switching, terminated, tui]);
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

	const archive = useCallback(() => {
		haptics.warning();
		Alert.alert(ARCHIVE_CLOUD_SESSION_CONFIRM.title, ARCHIVE_CLOUD_SESSION_CONFIRM.message, [
			{ text: ARCHIVE_CLOUD_SESSION_CONFIRM.cancel, style: "cancel" },
			// Leave first, as a local chat does: the board is where its row goes.
			{ text: ARCHIVE_CLOUD_SESSION_CONFIRM.confirm, style: "destructive", onPress: () => {
				backOr(router);
				void kill(sessionId, CLOUD_HOST_ID).catch((error: unknown) => Alert.alert("Couldn't archive this session", cloudErrorMessage(error)));
			} },
		]);
	}, [kill, router, sessionId]);

	// Each item's work, read when it is chosen.
	const menuActions: Partial<Record<ConversationMenuAction, () => void>> = {
		shell: () => router.push({ pathname: "/cloud/terminal", params: { sessionId, kind: "workspace" } }),
		agent_terminal: () => setView("agent"),
		project: () => { if (project) router.push({ pathname: "/project/[id]", params: { id: project.id, hostId: CLOUD_HOST_ID } }); },
		pin: () => void setWorkerPinned(sessionId, !pinned, CLOUD_HOST_ID).then(() => haptics.success(), (error: unknown) => {
			haptics.error();
			Alert.alert("Couldn't update pin", cloudErrorMessage(error));
		}),
		artifacts: () => router.push({ pathname: "/artifacts/[sessionId]", params: { sessionId, title, hostId: CLOUD_HOST_ID } }),
		pull_requests: () => { if (prNumber !== undefined && prUrl) openPage(reviewRouteForPR(sessionId, { number: prNumber, url: prUrl }, CLOUD_HOST_ID)); },
		delete: archive,
		restore,
		switch_tui: requestSwitch,
		switch_chat: requestSwitch,
	};
	const menuActionsRef = useRef(menuActions);
	menuActionsRef.current = menuActions;
	const openActions = useCallback(() => {
		const groups = latestMenuGroups.current;
		if (!groups) return;
		// The sheet takes the keyboard's place rather than opening under it.
		Keyboard.dismiss();
		router.push(chatSheetRoute({ kind: "conversation-actions", title, groups, subscribeGroups: subscribeMenuGroups, onSelect: (action) => menuActionsRef.current[action]?.() }));
	}, [router, subscribeMenuGroups, title]);
	const headerItems = useMemo(() => hasSession ? (
		<Stack.Toolbar placement="right">
			<Stack.Toolbar.Button icon={toolbarIcon("more")} accessibilityLabel="Session actions" onPress={openActions} />
		</Stack.Toolbar>
	) : null, [hasSession, openActions]);

	// An `ao://sessions/…` link (an orchestrator naming the worker it spawned)
	// opens that session's chat here, as the local chat does; any other link
	// goes to the browser, or GitHub's app for a GitHub page.
	const openLink = useCallback((url: string) => {
		const target = sessionLink(url);
		if (target) {
			router.push({ pathname: "/session/[id]", params: { id: target.sessionId, projectId: target.projectId, hostId: CLOUD_HOST_ID } });
			return;
		}
		void openGitHub(url);
	}, [router]);
	// Every surface that shows the agent changed files opens them, as a local
	// chat's does: the turn's changed-files card and the composer's chip.
	const filesNavigation = useMemo<FilesNavigation>(() => ({
		openChanges: () => router.push(filesRoute(sessionId, CLOUD_HOST_ID, title)),
		openFile: (path, previousPath) => router.push(fileRoute(sessionId, path, CLOUD_HOST_ID, previousPath)),
	}), [router, sessionId, title]);
	// git's totals, re-read when a turn reports edits. Only while the sandbox
	// runs: the read goes to its worker, and would wake a paused one.
	const lastTurn = snapshot?.turns.at(-1);
	const changeSignal = `${changedPathCount(snapshot?.turns)}:${lastTurn?.id ?? ""}:${lastTurn?.state ?? ""}:${lastTurn?.diff?.files.length ?? 0}`;
	const filesBackend = useMemo(() => org?.id ? cloudFilesBackend(client, org.id) : null, [client, org?.id]);
	const workspaceLive = Boolean(session?.runtimeConnected && !session.isTerminated);
	const changeSummary = useBackendChangeSummary(workspaceLive ? filesBackend : null, sessionId, changeSignal);
	const nameOfSession = useCallback((id: string) => {
		const named = sessions.find((item) => item.id === id);
		return named?.displayName?.trim() || (named ? cloudAgentLabel(named.harness) : undefined);
	}, [sessions]);

	if (status === "signed-out") return <CloudSignIn />;
	// Cloud failed before it chose an organization: no session can load, so its
	// own error, with Retry, rather than a session that never arrives.
	if (status === "error" && !org) {
		return (
			<View accessibilityRole="alert" style={[styles.center, { paddingTop: headerOverlap }]}>
				<EmptyState icon="warning" iconColor={t.red} title="Can't reach Orchestrator Cloud" message={cloud.error ?? "Try again in a moment."} action={<Button title="Retry" icon="refresh" onPress={() => void cloud.refresh()} />} />
			</View>
		);
	}
	if (!session || !org) {
		return (
			<View accessibilityRole={conversation.error ? "alert" : undefined} style={[styles.center, { paddingTop: headerOverlap }]}>
				{conversation.error ? (
					<EmptyState icon="warning" iconColor={t.red} title="Couldn't open this session" message={conversation.error} action={<Button title="Retry" icon="refresh" onPress={() => void conversation.refresh()} />} />
				) : <EmptyState busy title="Loading session…" />}
			</View>
		);
	}

	const stage = cloudLifecycleStage(session);
	const problem = cloudStartupProblem(session);
	const wake = conversation.wake;
	const stageTitle = cloudChatStageTitle(stage, {
		agentLive: connectedOnce || agentWorking,
		hasHistory: Boolean(snapshot?.turns.some((turn) => turn.state !== "queued" && turn.state !== "running")),
	});
	// The setup checklist lives in the conversation, so only the chat shows it.
	const checklist = current === "chat" && Boolean(startup);
	const timelineStartup: TimelineStartup | undefined = startup ? { view: startup, visible: startupVisible(startup, snapshot ?? undefined), onRetry: retryStart, retrying: busyAction === "retry" } : undefined;
	const startingAgentName = timelineStartup?.visible && !startup?.failed ? startup?.agentName : undefined;

	const banners = (
		<>
			{terminated ? (
				<InlineBanner tone="muted" icon="archive" title="This session is archived" body="Restore it to continue where it left off." action={busyAction === "restore" ? "Restoring…" : "Restore"} onPress={busyAction ? undefined : restore} />
			) : problem?.phase === "failed" ? (
				checklist ? null : <InlineBanner accessibilityRole="alert" tone="danger" icon="warning" title="The session failed to start" body={problem.detail ? `${problem.message} ${problem.detail.slice(-200)}` : problem.message} action={busyAction === "retry" ? "Retrying…" : "Retry"} onPress={busyAction ? undefined : retryStart} />
			) : wake.phase === "refused" ? (
				// The plan refused the wake: say so and when it lifts, rather than spin.
				<InlineBanner tone="warning" icon="timer" title="Paused: usage limit reached" body={[wake.message, wake.reset].filter(Boolean).join(" ")} action="Try again" onPress={conversation.retryWake} />
			) : wake.phase === "failed" ? (
				<InlineBanner tone="danger" icon="warning" title="Couldn't wake this session" body={wake.message} action="Retry" onPress={conversation.retryWake} />
			) : stage === "paused" ? (
				<InlineBanner tone="muted" icon={wake.phase === "waking" ? "busy" : "idle"} title={wake.phase === "waking" ? "Waking up" : "Paused"} body={wake.phase === "waking" ? "Starting its sandbox so the agent can continue." : "It wakes when you send a message."} />
			) : stageTitle ? (
				// A first start reads as the checklist; only a retry's reason joins it.
				checklist ? (problem?.phase === "retrying" ? <InlineBanner tone="muted" icon="busy" title="Still starting" body={problem.message} /> : null)
					: <InlineBanner tone="muted" icon="busy" title={stageTitle} body={problem?.phase === "retrying" ? problem.message : "Messages you send now are delivered once the agent is ready."} />
			) : null}
			{switchActive && !switchCardShown ? (
				<InlineBanner
					tone="warning"
					icon="busy"
					title={switchRecovery || `Switching to ${interfaceModeName(switchTo)} · ${switchPhaseShortLabel(transition?.phase)}`}
					body={interfaceSwitch.fetchFailed ? `Couldn't check on it${interfaceSwitch.error ? `: ${interfaceSwitch.error}` : "."}` : undefined}
					action={mobileInterfaceTransitionIsCancellable(transition) ? (interfaceSwitch.cancelling ? "Cancelling…" : "Cancel") : undefined}
					onPress={interfaceSwitch.cancelling ? undefined : cancelSwitch}
					secondary={interfaceSwitch.fetchFailed || switchRecovery ? (recheckingSwitch ? "Retrying…" : "Retry") : undefined}
					onSecondary={recheckingSwitch ? undefined : () => void recheckSwitch()}
				/>
			) : notice && transition ? (
				<InlineBanner
					tone={notice.recovered ? "warning" : "danger"}
					icon={notice.recovered ? "checkCircle" : "warning"}
					title={`${notice.text}${interfaceSwitch.acknowledgeNoticeError ? ` Couldn't dismiss: ${interfaceSwitch.acknowledgeNoticeError}` : ""}`}
					action={interfaceSwitch.acknowledgingNotice ? "Dismissing…" : "Dismiss"}
					onPress={interfaceSwitch.acknowledgingNotice ? undefined : () => void interfaceSwitch.acknowledgeNotice(transition.id).catch(() => {})}
				/>
			) : null}
			{tui && current === "chat" && !switchActive ? (
				<InlineBanner
					tone="muted"
					icon="terminal"
					title="This session runs in its terminal"
					body="Messages are typed into the agent's terminal."
					action="Terminal"
					onPress={() => setView("agent")}
					secondary={canSwitch ? "Switch to Chat" : undefined}
					onSecondary={canSwitch ? requestSwitch : undefined}
				/>
			) : null}
			{conversation.error && snapshot ? <InlineBanner tone="danger" icon="offline" title="Showing the last update" body={conversation.error} action="Retry" onPress={() => void conversation.refresh()} /> : null}
		</>
	);

	// Over whichever view is showing, from the moment the current interface
	// starts stopping until this screen shows the new one.
	const switchCard = !switchCardShown ? null : handoff.stalled ? (
		<InterfaceSwitchCard
			title={`${interfaceModeName(switchTo)} is ready`}
			message="This screen hasn't switched over yet. Retry, or go back and open the session again."
			ready
			retryable
			retrying={readingSwitched}
			onRetry={() => void retryHandoff()}
		/>
	) : (
		<InterfaceSwitchCard
			title={switchRecovery ? "Interface recovery blocked" : `Switching to ${switchTo === "tui" ? "Terminal" : "Chat"}`}
			message={switchRecovery || switchPhaseLabel(switchTo, transition?.phase)}
			blocked={Boolean(switchRecovery)}
			cancellable={mobileInterfaceTransitionIsCancellable(transition)}
			cancelling={interfaceSwitch.cancelling}
			onCancel={cancelSwitch}
			error={interfaceSwitch.error}
			retryable={interfaceSwitch.fetchFailed || Boolean(switchRecovery)}
			retrying={recheckingSwitch}
			onRetry={() => void recheckSwitch()}
		/>
	);

	if (current === "agent") {
		return (
			<View style={[styles.screen, { paddingTop: headerOverlap }]}>
				{headerItems}
				{banners}
				<CloudTerminalView client={client} orgId={org.id} sessionId={session.id} kind="agent" workerEpoch={session.workerEpoch} onShowChat={() => setView("chat")} />
				{switchCard}
			</View>
		);
	}

	if (!snapshot) {
		return (
			<View style={[styles.screen, { paddingTop: headerOverlap }]}>
				{headerItems}
				{banners}
				{startup ? (
					<View style={styles.startupLoading}><SessionStartupStatus startup={startup} onRetry={retryStart} retrying={busyAction === "retry"} /></View>
				) : (
					<View style={styles.center}><Spinner accessibilityLabel="Loading conversation" /></View>
				)}
				{switchCard}
			</View>
		);
	}

	return (
		<View style={styles.screen}>
			<KeyboardAvoidingView style={[styles.screen, chatKeyboard.rootStyle]} behavior={chatKeyboard.behavior} keyboardVerticalOffset={chatKeyboard.keyboardVerticalOffset}>
				{headerItems}
				{CLEAR_HEADER ? null : banners}
				<View style={styles.conversation}>
					<FilesNavigationContext.Provider value={filesNavigation}>
					<ChatLinkProvider onLinkOpen={openLink} sessionName={nameOfSession}>
						<MessageFileSourceContext.Provider value={fileSource}>
							<ChatTimeline
								snapshot={snapshot}
								loadingOlder={conversation.loadingOlder}
								onLoadOlder={conversation.loadOlder}
								approvalPending={deciding}
								inputPending={false}
								onDecide={decide}
								onResolveInput={resolveInput}
								jumpToSequence={jumpToSequence}
								onJumpHandled={clearJumpToSequence}
								answeredBelow={answeredBelow}
								startup={timelineStartup}
								underHeader={headerOverlap}
								topInset={bannersHeight}
								bottomInset={composerHeight}
								jumpHidden={composerOpen}
							/>
						</MessageFileSourceContext.Provider>
					</ChatLinkProvider>
					</FilesNavigationContext.Provider>
					{CLEAR_HEADER && !SYSTEM_GLASS_HEADER ? (
						<Frost from="top" stops={frostStops(headerOverlap)} style={[styles.headerFrost, { height: headerOverlap + FROST_TAIL }]} />
					) : null}
					{CLEAR_HEADER ? (
						<View pointerEvents="box-none" style={[styles.floatingBanners, { top: headerOverlap }]} onLayout={(event) => setBannersHeight(event.nativeEvent.layout.height)}>
							{banners}
						</View>
					) : null}
				</View>
			</KeyboardAvoidingView>
			{/* The composer floats over the conversation and rides the keyboard, as a local chat's does. */}
			<KeyboardFollower lift={chatKeyboard.composerLift} style={styles.floatingComposer} onLayout={measureComposer}>
				<ChatComposer
					key={session.id}
					onExpandedChange={setComposerOpen}
					sessionId={`cloud.${session.id}`}
					// Keys the unsent draft and attachments, as a machine's does, so a
					// cloud chat keeps what was typed across leaving it.
					machine={CLOUD_HOST_ID}
					config={null}
					snapshot={snapshot}
					// The starting session's brief reads as sent in the timeline; told
					// the same here, the queue beside the composer leaves it out.
					openingTurnId={startup?.openingTurnId}
					request={request}
					requestDismissed={requestDismissed}
					onRequestDecide={decide}
					onRequestResolveInput={resolveInput}
					onShowRequest={setJumpToSequence}
					onDismissRequest={() => setDismissedRequest(request?.sequence)}
					onRestoreRequest={restoreRequest}
					skills={NO_SKILLS}
					filePaths={NO_PATHS}
					onLoadSkills={loadNoSkills}
					onLoadFiles={loadNoFiles}
					configOptions={configOptions}
					models={conversation.models}
					disabled={terminated || wake.phase === "refused" || tui}
					// Held while the switch stops this interface: the next one takes the message.
					pending={conversation.sending || mobileInterfaceTransitionIsBusy(transition)}
					interrupting={conversation.stopping}
					onSend={send}
					onAcknowledgeSend={acknowledge}
					onSteer={steer}
					// Cloud can't steer with or delete a queued message, so the
					// queue beside the composer offers neither.
					onInterrupt={() => void conversation.stop().catch((error: unknown) => Alert.alert("Couldn't stop the turn", cloudErrorMessage(error)))}
					onSettings={chooseTurnSettings}
					onConfigOption={chooseOption}
					restingInset={chatKeyboard.restingInset}
					dockRiseOverride={chatKeyboard.dockRiseOverride}
					startingAgentName={startingAgentName}
					fileUploads={attachmentLimit ? { maxBytes: attachmentLimit } : undefined}
					attachmentsUnavailable={!attachmentLimit}
					changes={changeSummary?.files ? { ...changeSummary, onOpen: filesNavigation.openChanges } : undefined}
					reviewPR={reviewPromptPR}
					reviewPRSummary={reviewPromptSummary}
					onOpenReview={reviewPromptPR ? () => openPage(reviewRouteForPR(session.id, reviewPromptPR, CLOUD_HOST_ID)) : undefined}
				/>
			</KeyboardFollower>
			{switchCard}
		</View>
	);
}

const NO_SKILLS: never[] = [];
const NO_IDS: string[] = [];
const NO_PATHS: string[] = [];
async function acknowledge() {}
async function loadNoSkills() { return NO_SKILLS; }
async function loadNoFiles() { return { paths: NO_PATHS, truncated: false }; }

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, justifyContent: "center", backgroundColor: t.bgBase },
	conversation: { flex: 1 },
	floatingComposer: { position: "absolute", left: 0, right: 0, bottom: 0 },
	floatingBanners: { position: "absolute", left: 0, right: 0, backgroundColor: t.bgBase },
	headerFrost: { position: "absolute", top: 0, left: 0, right: 0 },
	// Where the timeline will draw the checklist, so the handoff to it does not move it.
	startupLoading: { flex: 1, paddingHorizontal: space.xl, paddingTop: space.md },
});
