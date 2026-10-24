import { Feather } from "@expo/vector-icons";
import { XtermJsWebView, type XtermWebViewHandle } from "@fressh/react-native-xtermjs-webview";
import { useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Alert, Keyboard, Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { useKeyboardState } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useCloudAuth } from "../cloud/authStore";
import { cloudLifecycleStage, cloudStartupProblem, cloudStartupErrorCopy, isResumable, shouldShowCloudStartup, stageLabel } from "../cloud/lifecycle";
import { cloudRequestErrorCopy } from "../cloud/errorCopy";
import { createCloudTerminal, isCloudReviewerTerminalId, type CloudTerminalStatus } from "../cloud/terminal";
import { haptics } from "../haptics";
import { resetHeaderRightForSwap } from "../headerRightSwap";
import { terminalTheme, type Theme } from "../theme";
import { useApp } from "../store";
import type { SourceRef } from "../environment/scopedBoard";
import { filesRoute } from "../files/navigation";
import { previewRoute } from "../preview/navigation";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { Composer } from "./Composer";
import { dockInset, rootKeyboardPad } from "./keyboardInset";
import { KeyRow } from "./KeyRow";
import { terminalPayload } from "./sendRoute";
import { useVoiceInput } from "../voice/useVoiceInput";
import type { RouteSession } from "./sessionRoute";
import { useChatViewLease } from "../chat/useChatViewLease";
import { useCloudInterfaceTransition, type CloudInterfaceControls } from "../cloud/useCloudInterfaceTransition";
import { cloudInterfaceDrainWaiting, cloudInterfaceInputLocked, cloudInterfaceSettledTarget } from "../cloud/interfaceTransition";
import { mobileInterfaceTransitionIsCancellable } from "./interfaceTransition";
import { SessionStartupHeader, SessionStartupStepRow } from "../chat/SessionStartup";
import { space, type } from "../tokens";

const CLOUD_TERMINAL_JS = `
(function () {
  // Leave terminal input to the native composer and touch scrolling to the
  // viewport. The xterm canvas otherwise consumes drags and focuses its hidden
  // textarea, keeping the keyboard open after a terminal tap.
  var style = document.createElement('style');
  style.textContent =
    '.xterm-screen,.xterm-helper-textarea{pointer-events:none !important;}' +
    '.xterm-viewport{pointer-events:auto !important;-webkit-overflow-scrolling:touch !important;scrollbar-width:none !important;}' +
    '.xterm-viewport::-webkit-scrollbar{display:none !important;}';
  document.head.appendChild(style);

  function hardenInput() {
    var input = document.querySelector('.xterm-helper-textarea');
    if (!input) return;
    input.disabled = true;
    input.setAttribute('inputmode', 'none');
    input.setAttribute('readonly', 'readonly');
  }
  hardenInput();
  setTimeout(hardenInput, 400);
  setTimeout(hardenInput, 1500);
  setInterval(hardenInput, 3000);

  var last = '';
  function report() {
    try {
      var fit = window.fitAddon;
      if (!fit || !fit.proposeDimensions || !window.ReactNativeWebView) return;
      var viewport = window.terminal && window.terminal._core && window.terminal._core.viewport;
      if (viewport) viewport.scrollBarWidth = 0;
      var size = fit.proposeDimensions();
      if (!size || size.cols < 20 || size.rows < 4) return;
      var value = size.cols + ' ' + size.rows;
      if (value === last) return;
      last = value;
      window.ReactNativeWebView.postMessage(JSON.stringify({ type: 'debug', message: 'FRESSH_DIMS ' + value }));
    } catch (_) {}
  }
  setTimeout(report, 300);
  window.addEventListener('resize', report);
  setInterval(report, 1000);

  // Full-screen agent TUIs use the alternate buffer (no xterm scrollback).
  // Route those drags through xterm's wheel handling so the agent receives
  // mouse-wheel or cursor input; ordinary shell output scrolls the viewport.
  function appDrivesScroll() {
    try {
      var term = window.terminal;
      return term.buffer.active.type === 'alternate' ||
        term.modes.mouseTrackingMode !== 'none';
    } catch (_) { return false; }
  }
  function wheelTick(up, x, y) {
    var root = document.querySelector('.xterm');
    if (!root) return;
    root.dispatchEvent(new WheelEvent('wheel', {
      bubbles: true, cancelable: true, deltaX: 0, deltaY: up ? -1 : 1,
      deltaMode: 1, clientX: x, clientY: y
    }));
  }
  function lineAt(y) {
    var term = window.terminal;
    var screen = document.querySelector('.xterm-screen');
    if (!term || !screen || !term.rows) return 0;
    var rect = screen.getBoundingClientRect();
    var row = Math.floor((y - rect.top) / (rect.height / term.rows));
    row = Math.max(0, Math.min(term.rows - 1, row));
    return term.buffer.active.viewportY + row;
  }
  var startX = 0, startY = 0, startScroll = 0, mode = 'idle', longPress = 0;
  var viewport = null, wheelLines = 0, anchor = 0;
  function cancelLongPress() { if (longPress) clearTimeout(longPress); longPress = 0; }
  document.addEventListener('touchstart', function (event) {
    if (!event.touches || event.touches.length !== 1) { mode = 'idle'; cancelLongPress(); return; }
    startX = event.touches[0].clientX;
    startY = event.touches[0].clientY;
    viewport = document.querySelector('.xterm-viewport');
    startScroll = viewport ? viewport.scrollTop : 0;
    wheelLines = 0;
    mode = 'pending';
    cancelLongPress();
    longPress = setTimeout(function () {
      if (mode !== 'pending') return;
      mode = 'select';
      anchor = lineAt(startY);
      try { window.terminal.selectLines(anchor, anchor); } catch (_) {}
    }, 350);
  }, { capture: true, passive: true });
  document.addEventListener('touchmove', function (event) {
    if (!event.touches || event.touches.length !== 1) return;
    var touch = event.touches[0];
    if (mode === 'pending') {
      if (Math.abs(touch.clientX - startX) <= 10 && Math.abs(touch.clientY - startY) <= 10) return;
      mode = 'scroll';
      cancelLongPress();
    }
    if (mode === 'select') {
      if (event.cancelable) event.preventDefault();
      var row = lineAt(touch.clientY);
      try { window.terminal.selectLines(Math.min(anchor, row), Math.max(anchor, row)); } catch (_) {}
      return;
    }
    if (mode !== 'scroll') return;
    if (appDrivesScroll()) {
      if (event.cancelable) event.preventDefault();
      var distance = (touch.clientY - startY) / 24;
      var wanted = distance > 0 ? Math.floor(distance) : Math.ceil(distance);
      var change = wanted - wheelLines;
      for (var i = 0; i < Math.abs(change); i++) wheelTick(change > 0, touch.clientX, touch.clientY);
      wheelLines = wanted;
    } else if (IS_ANDROID && viewport) {
      viewport.scrollTop = startScroll - (touch.clientY - startY);
      if (event.cancelable) event.preventDefault();
    }
    // iOS normal-buffer scrolling keeps native WebView momentum.
  }, { capture: true, passive: false });
  document.addEventListener('touchend', function () {
    cancelLongPress();
    if (mode === 'pending' && window.ReactNativeWebView) {
      window.ReactNativeWebView.postMessage(JSON.stringify({ type: 'debug', message: 'AO_TERMINAL_TAP' }));
    }
    if (mode === 'select') {
      try {
        var selected = window.terminal.getSelection();
        if (selected && navigator.clipboard) navigator.clipboard.writeText(selected);
      } catch (_) {}
    }
    mode = 'idle';
  }, { capture: true, passive: true });
})();`;

const labels: Record<CloudTerminalStatus, string> = {
	connecting: "Connecting to Cloud terminal…",
	waiting: "Waiting for the Cloud worker…",
	attaching: "Connecting to Cloud terminal…",
	ready: "Connected to Cloud terminal",
	disconnected: "Reconnecting to Cloud terminal…",
	exited: "Agent terminal exited",
	error: "Cloud terminal unavailable",
};

const startupSteps = [
	"Creating workspace",
	"Connecting to worker",
	"Preparing repository and agent",
	"Connecting terminal",
] as const;

export function CloudTerminalSessionScreen({ session, source, kind = "agent", reviewerTerminalId, showSpawnStartup = false, interfaceSwitch }: { session: RouteSession; source: SourceRef; kind?: "agent" | "workspace"; reviewerTerminalId?: string; showSpawnStartup?: boolean; interfaceSwitch?: CloudInterfaceControls }) {
	const workspace = kind === "workspace";
	const reviewer = reviewerTerminalId !== undefined;
	const separateTerminal = workspace || reviewer;
	const workspaceUnsupported = workspace && "projectName" in session;
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const { client, orgId, sessionEpoch, isCurrentSession } = useCloudAuth();
	const { refreshSource, sourceFor } = useApp();
	const sessionSource = sourceFor(source);
	const reviewerInvalid = reviewer && (!isCloudReviewerTerminalId(reviewerTerminalId) || workspace || !sessionSource?.scopeKey);
	const transportIdentity = JSON.stringify([sessionSource?.scopeKey, sessionEpoch, session.id, session.cloud?.workerEpoch, scheme, kind, reviewerTerminalId]);
	const viewOwner = useMemo(() => ({ active: true }), [client, orgId, sessionSource, transportIdentity]);
	const liveViewOwner = useRef(viewOwner); liveViewOwner.current = viewOwner;
	useEffect(() => { viewOwner.active = true; return () => { viewOwner.active = false; }; }, [viewOwner]);
	const ownsView = useCallback(() => viewOwner.active && liveViewOwner.current === viewOwner && source.kind === "cloud" && source.id === orgId &&
		!!sessionSource && sourceFor(source) === sessionSource && isCurrentSession?.() !== false,
	[isCurrentSession, orgId, sessionSource, source.kind, source.id, sourceFor, viewOwner]);
	const refreshBoard = useCallback(() => ownsView() ? refreshSource(source) : Promise.resolve(), [ownsView, refreshSource, source.kind, source.id]);
	const ownInterfaceSwitch = useCloudInterfaceTransition(!separateTerminal && !interfaceSwitch ? sessionSource : undefined, session.id, refreshBoard);
	const handoff = separateTerminal ? undefined : interfaceSwitch ?? ownInterfaceSwitch;
	const handoffLocked = Boolean(handoff?.loading || handoff?.starting || cloudInterfaceInputLocked(handoff?.status));
	const drainWaiting = cloudInterfaceDrainWaiting(handoff?.status) || Boolean(handoff?.starting && handoff.startingPolicy === "drain" && handoff.status?.currentMode === "tui" && !cloudInterfaceInputLocked(handoff.status));
	const terminalInputLocked = Boolean(handoff?.loading || handoffLocked && !drainWaiting);
	const recoveryRequired = handoff?.transition?.phase === "recovery_required";
	const stage = cloudLifecycleStage(session);
	const paused = isResumable(stage);
	const archived = "projectName" in session ? !!session.isTerminal : !!session.isTerminated;
	const startupProblem = cloudStartupProblem(session);
	const startupCopy = startupProblem ? cloudStartupErrorCopy(startupProblem, !session.cloud?.coderWorkspaceUrl) : undefined;
	const startupStopped = startupProblem?.phase === "failed" || startupProblem?.phase === "unavailable";
	useChatViewLease(null, session.id, !reviewer && !workspaceUnsupported && !paused && !archived && !startupStopped && stage === "connected" ? sessionSource : undefined);
	const liveTransportIdentity = useRef(transportIdentity);
	liveTransportIdentity.current = transportIdentity;
	const router = useRouter();
	const navigation = useNavigation();
	const insets = useSafeAreaInsets();
	const keyboardHeight = useKeyboardState((state) => state.height);
	const keyboardVisible = useKeyboardState((state) => state.isVisible);
	const xtermRef = useRef<XtermWebViewHandle | null>(null);
	const terminalRef = useRef<ReturnType<typeof createCloudTerminal> | null>(null);
	const terminalOwner = useRef<typeof viewOwner | null>(null);
	const xtermReady = useRef(false);
	const firstReadySession = useRef<string | null>(null);
	const pendingOutput = useRef<Uint8Array[]>([]);
	const lastSize = useRef<{ cols: number; rows: number } | null>(null);
	const [terminalState, setTerminalState] = useState<{ identity: string; status: CloudTerminalStatus }>({ identity: transportIdentity, status: "connecting" });
	const status = terminalState.identity === transportIdentity ? terminalState.status : "connecting";
	const [headerRightReady, setHeaderRightReady] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [draft, setDraft] = useState("");
	const liveDraft = useRef(draft); liveDraft.current = draft;
	const [sending, setSending] = useState(false);
	const [resuming, setResuming] = useState(false);
	const [shellAttempt, setShellAttempt] = useState(0);
	// A shell is a separate PTY, not an agent interface switch. As on desktop,
	// explicit shell entry first wakes the workspace; polling/epoch changes do not
	// issue another resume. Retries are explicit and ownership-scoped.
	const viewIdentity = JSON.stringify([sessionSource?.scopeKey, sessionEpoch, source.kind, source.id, session.id, kind, reviewerTerminalId]);
	const shellEntryIdentity = JSON.stringify([viewIdentity, shellAttempt]);
	const liveShellEntry = useRef({ identity: shellEntryIdentity, source: sessionSource });
	liveShellEntry.current = { identity: shellEntryIdentity, source: sessionSource };
	const shellRequest = useRef<{ identity: string; source: typeof sessionSource; promise: Promise<void> } | null>(null);
	const [shellEntry, setShellEntry] = useState<{ identity: string; source: typeof sessionSource; ready: boolean; error?: string }>({ identity: shellEntryIdentity, source: sessionSource, ready: false });
	const shellEntryOwned = shellEntry.identity === shellEntryIdentity && shellEntry.source === sessionSource;
	const shellReady = !workspace || shellEntryOwned && shellEntry.ready;
	const shellError = shellEntryOwned ? shellEntry.error : undefined;
	const liveInputAvailable = useRef(false);
	liveInputAvailable.current = !paused && !archived && !startupStopped && !terminalInputLocked && shellReady && !workspaceUnsupported && !reviewerInvalid;
	useEffect(() => {
		if (!workspace || reviewer || workspaceUnsupported || archived || startupStopped) return;
		let active = true;
		const owns = () => active && ownsView() && liveShellEntry.current.identity === shellEntryIdentity && liveShellEntry.current.source === sessionSource;
		setShellEntry({ identity: shellEntryIdentity, source: sessionSource, ready: false });
		if (!orgId || source.kind !== "cloud" || orgId !== source.id || !sessionSource) {
			setShellEntry({ identity: shellEntryIdentity, source: sessionSource, ready: false, error: "Your Cloud workspace is not ready. Sign in again and retry." });
			return;
		}
		if (shellRequest.current?.identity !== shellEntryIdentity || shellRequest.current.source !== sessionSource) {
			shellRequest.current = { identity: shellEntryIdentity, source: sessionSource, promise: Promise.resolve().then(() => {
				if (!ownsView() || liveShellEntry.current.identity !== shellEntryIdentity || liveShellEntry.current.source !== sessionSource) return;
				return sessionSource.resumeSession(session.id);
			}) };
		}
		void shellRequest.current.promise.then(async () => {
			if (!owns()) return;
			await refreshBoard();
			if (owns()) setShellEntry({ identity: shellEntryIdentity, source: sessionSource, ready: true });
		}).catch((cause) => {
			if (owns()) setShellEntry({ identity: shellEntryIdentity, source: sessionSource, ready: false, error: cloudRequestErrorCopy(cause) });
		});
		return () => { active = false; };
	}, [archived, orgId, ownsView, refreshBoard, reviewer, session.id, sessionSource, shellEntryIdentity, source.kind, source.id, startupStopped, workspace, workspaceUnsupported]);
	const [switchingChat, setSwitchingChat] = useState(false);
	const chatIntent = useRef({ version: 0, busy: false });
	useEffect(() => {
		chatIntent.current.version++; chatIntent.current.busy = false; setSwitchingChat(false); setSending(false); setResuming(false);
		return () => { chatIntent.current.version++; chatIntent.current.busy = false; };
	}, [transportIdentity, sessionSource]);
	useEffect(() => { setDraft(""); setSending(false); }, [viewIdentity, sessionSource]);
	const [size, setSize] = useState<{ cols: number; rows: number } | null>(null);
	const sessionTitle = "displayName" in session ? session.displayName : "Orchestrator";
	const filesOwner = JSON.stringify([viewIdentity, orgId]);
	const liveFilesOwner = useRef({ identity: filesOwner, source: sessionSource });
	liveFilesOwner.current = { identity: filesOwner, source: sessionSource };
	const filesAvailable = source.kind === "cloud" && source.id === orgId && Boolean(sessionSource?.scopeKey && sessionSource.workspace?.isCurrent());
	const openFiles = useCallback(() => {
		if (!ownsView() || !filesAvailable || liveFilesOwner.current.identity !== filesOwner || liveFilesOwner.current.source !== sessionSource || sourceFor(source) !== sessionSource || !sessionSource?.scopeKey || !sessionSource.workspace?.isCurrent()) return;
		router.push(filesRoute(session.id, undefined, sessionTitle || undefined, { source: { kind: source.kind, id: source.id }, scopeKey: sessionSource.scopeKey }));
	}, [filesAvailable, filesOwner, ownsView, router, session.id, sessionSource, sessionTitle, sourceFor, source.kind, source.id]);
	const preview = sessionSource?.preview;
	const browserAvailable = !reviewer && source.kind === "cloud" && source.id === orgId && !!sessionSource?.scopeKey && preview?.scopeKey === sessionSource.scopeKey && preview.isCurrent();
	const openBrowser = useCallback(() => {
		if (!browserAvailable || !ownsView() || !sessionSource?.scopeKey || sessionSource.preview !== preview || !preview?.isCurrent()) return;
		router.push(previewRoute(session.id, undefined, sessionTitle || undefined, { source, scopeKey: sessionSource.scopeKey }));
	}, [browserAvailable, ownsView, preview, router, session.id, sessionSource, sessionTitle, source.kind, source.id]);
	const changeDraft = useCallback((value: string) => { if (ownsView()) setDraft(value); }, [ownsView]);
	const voice = useVoiceInput({
		onTranscript: useCallback((text: string) => {
			if (!ownsView()) return;
			setDraft((current) => current ? `${current} ${text}` : text);
		}, [ownsView]),
	});

	const openChat = useCallback(async () => {
		if (!ownsView() || separateTerminal || chatIntent.current.busy || handoffLocked) return;
		const version = ++chatIntent.current.version; chatIntent.current.busy = true;
		const current = () => ownsView() && chatIntent.current.version === version && liveTransportIdentity.current === transportIdentity;
		setSwitchingChat(true);
		try {
			if (sessionSource?.startInterfaceTransition && handoff) {
				const begin = async (policy: "drain" | "interrupt") => {
					if (!current()) return;
					try { await handoff.start("chat", policy); }
					catch (cause) { if (current()) Alert.alert("Couldn’t switch to Chat", cloudRequestErrorCopy(cause)); }
				};
				// A cached idle row may predate terminal input. Match desktop by
				// checking authoritative activity before stopping without a prompt.
				const read = await handoff.refresh();
				if (!current() || !read || read.ok && read.stale) return;
				if (read?.ok && cloudInterfaceInputLocked(read.status)) return;
				// Opening the selected interface is navigation, not a handoff.
				// Paused/offline workers can still show Chat history and Resume.
				if (read?.ok && read.status.currentMode === "chat") {
					await refreshBoard();
					if (current()) router.replace({ pathname: "/session/[id]", params: { id: session.id, source: source.kind, sourceId: source.id } });
					return;
				}
				if (read?.ok && !read.status.supported) {
					Alert.alert("Chat unavailable", read.status.reason || "This agent does not support native Chat handoff."); return;
				}
				const activityKnown = Boolean(read?.ok && read.status.currentMode === "tui" && read.status.runtimeConnected && read.status.sourceBusy !== undefined);
				if (activityKnown && read?.ok && !read.status.sourceBusy && !liveDraft.current.trim()) { await begin("interrupt"); return; }
				const waiting = read?.ok && read.status.sourceWaitingForInput;
				const message = !activityKnown
					? "Couldn’t confirm whether this terminal is busy. Stopping may interrupt its current work."
					: waiting ? "This turn is waiting for your input. Finish waits for your answer; stop cancels it and switches now."
						: "Keep the same Orchestrator session, worktree, and native agent conversation. Stop cancels any current work and switches now.";
				const choices: NonNullable<Parameters<typeof Alert.alert>[2]> = [{ text: "Keep Terminal", style: "cancel" }];
				if (activityKnown && read?.ok && read.status.sourceBusy) choices.push({ text: "Finish, then switch", onPress: () => void begin("drain") });
				choices.push({ text: "Stop and switch", style: "destructive", onPress: () => void begin("interrupt") });
				Alert.alert("Switch to Chat?", message + (liveDraft.current.trim() ? " Unsent text in this phone’s composer will not be sent." : ""), choices);
				return;
			}
			if (!sessionSource?.switchToChat) throw new Error("Your Cloud workspace is not ready. Sign in again and retry.");
			await sessionSource.switchToChat(session.id);
			await refreshBoard();
			if (current()) router.replace({ pathname: "/session/[id]", params: { id: session.id, source: source.kind, sourceId: source.id } });
		} catch (cause) {
			if (current()) Alert.alert("Couldn’t open Chat", cloudRequestErrorCopy(cause));
		} finally {
			if (current()) { chatIntent.current.busy = false; setSwitchingChat(false); }
		}
	}, [handoff, handoffLocked, ownsView, refreshBoard, router, separateTerminal, session.id, sessionSource, source.kind, source.id, transportIdentity]);
	const returnToSession = useCallback(() => {
		if (!ownsView()) return;
		router.replace({ pathname: "/session/[id]", params: { id: session.id, source: source.kind, sourceId: source.id } });
	}, [ownsView, router, session.id, source.kind, source.id]);
	useEffect(() => {
		if (ownsView() && !reviewer && !interfaceSwitch && cloudInterfaceSettledTarget(handoff?.status) === "chat") router.replace({ pathname: "/session/[id]", params: { id: session.id, source: source.kind, sourceId: source.id } });
	}, [handoff?.status, interfaceSwitch, ownsView, reviewer, router, session.id, source.kind, source.id]);

	useLayoutEffect(() => resetHeaderRightForSwap(
		() => navigation.setOptions({ headerRight: undefined }),
		() => setHeaderRightReady(true),
	), [navigation]);

	useLayoutEffect(() => {
		if (!headerRightReady) return;
		navigation.setOptions({
			title: reviewer ? "Reviewer terminal" : workspace ? "Workspace shell" : sessionTitle || "Cloud terminal",
			headerRight: () => <View style={styles.headerActions}>
				{browserAvailable ? <Pressable accessibilityRole="button" accessibilityLabel="Open Cloud browser" onPress={openBrowser} style={styles.filesButton}>
					<Feather name="globe" size={16} color={t.accent} />
				</Pressable> : null}
				{filesAvailable ? <Pressable accessibilityRole="button" accessibilityLabel="Files" onPress={openFiles} style={styles.filesButton}>
					<Feather name="folder" size={16} color={t.accent} />
				</Pressable> : null}
				<Pressable accessibilityRole="button" accessibilityLabel={separateTerminal ? "Return to session" : "Open Chat UI"} disabled={!separateTerminal && (switchingChat || handoffLocked)} onPress={separateTerminal ? returnToSession : openChat} style={styles.chatButton}>
					{!separateTerminal && switchingChat ? <ActivityIndicator size="small" color={t.accent} /> : <Feather name={separateTerminal ? "arrow-left" : "message-square"} size={16} color={t.accent} />}
					<Text style={styles.chatButtonText}>{separateTerminal ? "Session" : switchingChat ? "Switching…" : "Chat"}</Text>
				</Pressable>
			</View>,
		});
	}, [browserAvailable, filesAvailable, handoffLocked, headerRightReady, navigation, openBrowser, openChat, openFiles, returnToSession, reviewer, separateTerminal, sessionTitle, styles, switchingChat, t.accent, workspace]);

	useEffect(() => {
		xtermReady.current = false;
		pendingOutput.current = [];
		return () => { xtermReady.current = false; pendingOutput.current = []; };
	}, [transportIdentity]);
	useEffect(() => {
		let active = true;
		pendingOutput.current = [];
		setSending(false);
		setTerminalState({ identity: transportIdentity, status: "connecting" });
		setError(null);
		if (paused || archived || startupStopped || terminalInputLocked || !shellReady || workspaceUnsupported || reviewerInvalid) return;
		if (!orgId || source.kind !== "cloud" || orgId !== source.id || !sessionSource) {
			setTerminalState({ identity: transportIdentity, status: "error" });
			setError("Your Cloud workspace is not ready. Sign in again and retry.");
			return;
		}
		if (!ownsView()) return;
		const terminal = createCloudTerminal({
			client, orgId, sessionId: session.id, kind, ...(reviewer ? { terminalId: reviewerTerminalId } : {}),
			onStatus: (next) => {
				if (!active || !ownsView() || liveTransportIdentity.current !== transportIdentity) return;
				if (next === "ready") firstReadySession.current = session.id;
				setTerminalState({ identity: transportIdentity, status: next });
			},
			onError: (message) => { if (active && ownsView() && liveTransportIdentity.current === transportIdentity) setError(message); },
			onOutput: (bytes) => {
				if (!active || !ownsView() || liveTransportIdentity.current !== transportIdentity) return;
				if (!xtermReady.current || !xtermRef.current) {
					pendingOutput.current.push(bytes);
					return;
				}
				xtermRef.current.write(bytes);
			},
		});
		terminalRef.current = terminal;
		terminalOwner.current = viewOwner;
		if (lastSize.current) terminal.resize(lastSize.current.cols, lastSize.current.rows);
		void terminal.connect();
		return () => {
			active = false;
			terminal.disconnect();
			if (terminalRef.current === terminal) { terminalRef.current = null; terminalOwner.current = null; }
			pendingOutput.current = [];
		};
	}, [client, orgId, ownsView, paused, archived, startupStopped, terminalInputLocked, scheme, session.id, session.cloud?.workerEpoch, source.kind, source.id, sessionSource, transportIdentity, kind, reviewer, reviewerInvalid, reviewerTerminalId, shellReady, viewOwner, workspaceUnsupported]);

	const resumeSandbox = useCallback(async () => {
		if (!ownsView() || reviewer || resuming) return;
		setResuming(true);
		try {
			if (!sessionSource) throw new Error("No cloud session source available");
			if (archived) {
				if (!sessionSource.restoreSession) throw new Error("Cloud Restore is unavailable. Refresh and try again.");
				await sessionSource.restoreSession(session.id);
			} else if (paused) await sessionSource.resumeSession(session.id);
			else if (startupProblem?.phase === "failed") {
				if (!sessionSource.retryStartup) throw new Error("Cloud startup retry is unavailable. Refresh and try again.");
				await sessionSource.retryStartup(session.id);
			} else throw new Error("This session has no supported recovery action. Refresh its status.");
			if (ownsView()) await refreshBoard();
		} catch (cause) {
			if (ownsView()) Alert.alert("Could not recover session", cloudRequestErrorCopy(cause));
		} finally {
			if (ownsView()) {
				await refreshBoard().catch(() => {});
				if (ownsView()) setResuming(false);
			}
		}
	}, [ownsView, refreshBoard, resuming, reviewer, session.id, sessionSource, archived, paused, startupProblem?.phase]);

	const onInitialized = useCallback(() => {
		if (!ownsView() || liveTransportIdentity.current !== transportIdentity) return;
		xtermReady.current = true;
		for (const bytes of pendingOutput.current) xtermRef.current?.write(bytes);
		pendingOutput.current = [];
		if (lastSize.current) xtermRef.current?.resize(lastSize.current);
	}, [ownsView, transportIdentity]);

	const applySize = useCallback((cols: number, rows: number) => {
		// Ignore unsettled bridge fits before caching or resizing either end.
		if (!ownsView() || !Number.isInteger(cols) || !Number.isInteger(rows) || cols < 20 || rows < 4) return;
		lastSize.current = { cols, rows };
		setSize({ cols, rows });
		terminalRef.current?.resize(cols, rows);
		xtermRef.current?.resize({ cols, rows });
	}, [ownsView]);
	const logger = useMemo(() => ({ log: (...args: unknown[]) => {
		if (!ownsView()) return;
		const message = args.at(-1);
		if (typeof message !== "string") return;
		if (message === "AO_TERMINAL_TAP") {
			Keyboard.dismiss();
			return;
		}
		const match = /^FRESSH_DIMS (\d+) (\d+)$/.exec(message);
		if (match) applySize(Number(match[1]), Number(match[2]));
	} }), [applySize, ownsView]);
	const xtermOptions = useMemo(() => ({
		fontSize: 12,
		cursorBlink: true,
		scrollback: 5000,
		scrollSensitivity: 3,
		fastScrollSensitivity: 8,
		theme: terminalTheme(scheme),
		minimumContrastRatio: 4.5,
	}), [scheme]);
	const webViewOptions = useMemo(() => ({
		hideKeyboardAccessoryView: true,
		injectedJavaScript: `var IS_ANDROID=${Platform.OS === "android"};\n${CLOUD_TERMINAL_JS}`,
		nestedScrollEnabled: true,
		onRenderProcessGone: () => { if (ownsView()) setError("Terminal renderer crashed. Reopen the worker to reconnect."); },
	}), [ownsView]);
	const sendInput = useCallback((data: string) => {
		if (!ownsView() || !liveInputAvailable.current || terminalOwner.current !== viewOwner) return;
		if (!terminalRef.current?.sendInput(data)) setError("Terminal is not ready yet.");
	}, [ownsView, viewOwner]);

	const sendPrompt = useCallback(async () => {
		const text = draft.trim();
		if (!ownsView() || !liveInputAvailable.current || terminalOwner.current !== viewOwner || !text || sending || terminalInputLocked) return;
		const terminal = terminalRef.current;
		const owns = () => ownsView() && liveTransportIdentity.current === transportIdentity && terminalRef.current === terminal;
		setSending(true);
		try {
			const line = terminalPayload(text);
			const sent = await terminal?.sendPrompt(line.slice(0, -1));
			if (!owns()) return;
			if (!sent) {
				setError("Terminal is not ready yet. Your text may not have been submitted.");
				haptics.error();
				return;
			}
			setDraft("");
			setError(null);
			haptics.success();
		} finally {
			if (owns()) setSending(false);
		}
	}, [draft, ownsView, sending, terminalInputLocked, transportIdentity, viewOwner]);

	const rootPad = rootKeyboardPad(Platform.OS === "android" ? "android" : "ios", keyboardHeight, insets.bottom);
	const bottomPad = dockInset(keyboardHeight, insets.bottom, keyboardVisible);
	const observedState = session.cloud?.observedState;
	// A normal revisit reconnects the terminal; it does not recreate the workspace.
	const showStartup = shouldShowCloudStartup({ spawnIntent: !separateTerminal && showSpawnStartup && firstReadySession.current !== session.id,
		stage, startupProblem, terminalReady: status === "ready" }) && !archived
		&& status !== "error" && status !== "exited" && !error;
	const startupStep = status === "attaching" ? 3
		: (observedState === "bootstrapping" || observedState === "running")
			? session.runtimeConnected ? 2 : 1
			: 0;
	const recoveryTitle = archived ? "Session archived" : paused ? stageLabel(stage!) : startupProblem?.phase === "failed" ? "Session failed to start" : "Cloud runtime unavailable";
	const recoveryAction = archived ? "Restore" : paused ? "Resume" : startupProblem?.phase === "failed" ? "Retry startup" : undefined;
	if (reviewer && (reviewerInvalid || paused || archived || startupStopped)) return <View style={styles.screen}><View style={styles.pausedBody}>
		<Text style={styles.pausedTitle}>Reviewer terminal unavailable</Text>
		<Text style={styles.pausedCopy}>{reviewerInvalid ? "Open its review again to reconnect." : "Open the session to check its workspace, then return to the review."}</Text>
	</View></View>;
	if (workspaceUnsupported) return <View style={styles.screen}><View style={styles.pausedBody}>
		<Text style={styles.pausedTitle}>Workspace shell unavailable</Text>
		<Text style={styles.pausedCopy}>Open a worker to use its workspace shell.</Text>
	</View></View>;
	if (workspace && !archived && !startupStopped && (!shellReady || paused)) return <View style={styles.screen}><View style={styles.pausedBody}>
		{!shellError ? <ActivityIndicator color={t.accent} /> : <Feather name="cloud" size={22} color={t.amber} />}
		<Text style={styles.pausedTitle}>{shellError ? "Could not open workspace shell" : "Opening workspace shell…"}</Text>
		<Text selectable style={styles.pausedCopy}>{shellError || "Resuming the workspace and connecting to its shell. Your agent’s selected interface stays unchanged."}</Text>
		{shellError || paused && shellReady ? <Pressable accessibilityRole="button" accessibilityLabel="Retry workspace shell" onPress={() => { if (ownsView()) setShellAttempt((attempt) => attempt + 1); }} style={styles.resumeButton}>
			<Text style={styles.resumeButtonText}>Retry</Text>
		</Pressable> : null}
	</View></View>;
	if (paused || archived || startupStopped) return <View style={styles.screen}>
		<View style={styles.statusBar}>
			<View style={[styles.dot, { backgroundColor: t.amber }]} />
			<Text style={styles.statusText}>{recoveryTitle}</Text>
		</View>
		<View style={styles.pausedBody}>
			<Feather name="cloud" size={22} color={t.amber} />
			<Text style={styles.pausedTitle}>{recoveryTitle}</Text>
			<Text style={styles.pausedCopy}>{archived ? "Its conversation and workspace are preserved." : paused ? "The sandbox paused after a period of inactivity." : startupCopy?.message}</Text>
			{startupCopy?.detail ? <Text selectable style={styles.pausedCopy}>{startupCopy.detail}</Text> : null}
			{recoveryAction ? <Pressable accessibilityRole="button" accessibilityLabel={archived ? "Restore session" : paused ? "Resume sandbox" : "Retry startup"} disabled={resuming} onPress={() => void resumeSandbox()} style={styles.resumeButton}>
				<Text style={styles.resumeButtonText}>{resuming ? "Recovering…" : recoveryAction}</Text>
			</Pressable> : null}
		</View>
	</View>;
	return <View style={[styles.screen, !showStartup && rootPad > 0 && { paddingBottom: rootPad }]}>
		{handoffLocked || handoff?.error || handoff?.transition?.phase === "failed" && !handoff.transition.noticeAcknowledgedAt ? <View style={styles.statusBar}>
			<Text style={styles.statusText}>{recoveryRequired ? handoff?.transition?.errorDetail || "The interface needs recovery. Both controllers are unavailable; your history is preserved." : handoff?.error || handoff?.transition?.errorDetail || (drainWaiting ? "Waiting to switch to Chat…" : handoff?.transition ? "Switching to Chat · " + handoff.transition.phase.replaceAll("_", " ") : "Checking Cloud interface…")}</Text>
			{mobileInterfaceTransitionIsCancellable(handoff?.transition) ? <Pressable accessibilityRole="button" accessibilityLabel="Cancel interface switch" onPress={() => { if (ownsView()) void handoff?.cancel().catch(() => {}); }} style={styles.chatButton}><Text style={styles.chatButtonText}>Cancel</Text></Pressable> : null}
			{recoveryRequired || handoff?.transition?.phase === "failed" ? <Pressable accessibilityRole="button" accessibilityLabel="Dismiss interface notice" onPress={() => { if (ownsView()) void handoff?.acknowledgeNotice(handoff.transition!.id).catch(() => {}); }} style={styles.chatButton}><Text style={styles.chatButtonText}>Dismiss</Text></Pressable> : handoff?.error ? <Pressable accessibilityRole="button" accessibilityLabel="Retry interface status" onPress={() => { if (ownsView()) void handoff.refresh(); }} style={styles.chatButton}><Text style={styles.chatButtonText}>Retry</Text></Pressable> : null}
		</View> : null}
		{!showStartup ? <View style={styles.statusBar}>
			<View style={[styles.dot, { backgroundColor: status === "ready" ? t.green : status === "error" || status === "exited" ? t.red : t.amber }]} />
			<Text style={styles.statusText}>{reviewer ? status === "exited" ? "Reviewer terminal exited" : labels[status].replace("Cloud terminal", "reviewer terminal") : workspace ? status === "exited" ? "Workspace shell exited" : labels[status].replace("Cloud terminal", "workspace shell") : labels[status]}</Text>
			{status === "connecting" || status === "waiting" || status === "attaching" || status === "disconnected" ? <ActivityIndicator size="small" color={t.amber} /> : null}
			{size ? <Text style={styles.dimensions}>{size.cols}×{size.rows}</Text> : null}
		</View> : null}
		{!showStartup && (error || voice.error) ? <Text style={styles.error} selectable>{error || voice.error}</Text> : null}
		<View style={styles.terminal}>
			<XtermJsWebView
				key={`cloud-terminal-${transportIdentity}`}
				ref={xtermRef}
				autoFit={false}
				xtermOptions={xtermOptions}
				webViewOptions={webViewOptions}
				logger={logger}
				onInitialized={onInitialized}
				onData={sendInput}
				style={styles.xterm}
			/>
			{showStartup ? <View style={styles.startupOverlay} accessibilityLabel={`Starting Cloud terminal: ${startupSteps[startupStep]}`}>
				<View style={styles.startupSteps}>
					<SessionStartupHeader />
					{startupProblem?.phase === "retrying" ? <Text style={styles.startupRetryCopy}>{startupCopy?.message} Cloud is still retrying.</Text> : null}
					<View accessibilityLabel="Setup steps" style={styles.startupList}>
						{startupSteps.map((step, index) => <SessionStartupStepRow key={step} label={step}
							status={index < startupStep ? "done" : index === startupStep ? "running" : "pending"} />)}
					</View>
				</View>
			</View> : null}
		</View>
		{!showStartup && !terminalInputLocked ? <View style={[styles.dock, { paddingBottom: bottomPad }]}>
			<KeyRow onKey={sendInput} />
			<Composer
				value={draft}
				onChangeText={changeDraft}
				onSend={sendPrompt}
				sending={sending}
				target="terminal"
				onTargetChange={() => {}}
				voice={voice}
				keyboardVisible={keyboardVisible}
				onDismissKeyboard={Keyboard.dismiss}
				targetLocked
			/>
		</View> : null}
	</View>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	headerActions: { flexDirection: "row", alignItems: "center" },
	filesButton: { minHeight: 44, minWidth: 44, alignItems: "center", justifyContent: "center" },
	chatButton: { minHeight: 44, minWidth: 44, flexDirection: "row", alignItems: "center", gap: 6, paddingHorizontal: 10, paddingVertical: 6 },
	chatButtonText: { color: t.accent, fontSize: 14, fontWeight: "600" },
	statusBar: { flexDirection: "row", alignItems: "center", paddingHorizontal: 14, paddingVertical: 7, borderBottomWidth: 1, borderBottomColor: t.borderSubtle },
	dot: { width: 8, height: 8, borderRadius: 4, marginRight: 8 },
	statusText: { color: t.textSecondary, fontSize: 12, flex: 1 },
	dimensions: { color: t.textTertiary, fontSize: 11, fontFamily: t.fontMono },
	error: { color: t.red, paddingHorizontal: 14, paddingVertical: 9, fontSize: 12 },
	pausedBody: { flex: 1, alignItems: "center", justifyContent: "center", gap: 14, paddingHorizontal: 28 },
	pausedTitle: { color: t.textPrimary, fontSize: 18, fontWeight: "600" },
	pausedCopy: { color: t.textSecondary, fontSize: 14, textAlign: "center" },
	resumeButton: { minHeight: 44, justifyContent: "center", backgroundColor: t.accent, borderRadius: 12, paddingHorizontal: 20, marginTop: 8 },
	resumeButtonText: { color: t.onAccent, fontSize: 14, fontWeight: "600" },
	terminal: { flex: 1 },
	xterm: { flex: 1, backgroundColor: t.bgBase },
	startupOverlay: { position: "absolute", top: 0, right: 0, bottom: 0, left: 0, paddingHorizontal: space.xl, paddingTop: space.md, backgroundColor: t.bgBase },
	startupSteps: { width: "100%", paddingBottom: space.sm },
	startupList: { paddingTop: space.xxs },
	startupRetryCopy: { fontFamily: "Geist_400Regular", fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, color: t.textSecondary, marginBottom: space.sm },
	dock: { borderTopWidth: 1, borderTopColor: t.borderSubtle, backgroundColor: t.bgSurface },
});
