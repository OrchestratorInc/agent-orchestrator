import { deliverableArtifacts } from "../../../shared/artifacts/deliverables";
import { XtermJsWebView, type XtermWebViewHandle } from "@fressh/react-native-xtermjs-webview";
import { Stack, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Alert, Keyboard, LayoutAnimation, Platform, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { ApiError, killSession, killSessionReviewer, sendMessage } from "../api";
import { isConfigured } from "../config";
import { terminalTheme, type Theme } from "../theme";
import { haptics } from "../haptics";
import { resetHeaderRightForSwap } from "../headerRightSwap";
import type { MuxStatus } from "../mux";
import { filesRoute } from "../files/navigation";
import { glassHeaderControl } from "../native-header-items";
import { Composer } from "./Composer";
import { dockInset, rootKeyboardPad } from "./keyboardInset";
import { KeyRow } from "./KeyRow";
import {
	REROUTED_NOTICE,
	routeForSend,
	terminalPayload,
	TERMINAL_UNAVAILABLE_NOTICE,
	type SendTarget,
} from "./sendRoute";
import { useApp } from "../store";
import { useVoiceInput } from "../voice/useVoiceInput";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { closeShellTerminal } from "../chat/api";
import {
	interfaceSwitchAlert,
	type InterfaceSwitchRecheck,
	mobileInterfaceTransitionIsActive,
	mobileInterfaceTransitionIsCancellable,
	mobileInterfaceTransitionRecoveryMessage,
	useInterfaceTransition,
} from "./useInterfaceTransition";
import { terminalInterfaceFailureRecovery } from "./terminalInterfaceRecovery";
import { adjustTerminalViewport, fitTerminalViewport, scrollTerminalToTop, scrollTopReport } from "./terminalViewport";
import { TERMINAL_ENHANCE_JS } from "./terminalEnhance";
import { ScrollTopButton } from "./ScrollTopButton";
import { terminalMenuSections, type TerminalMenuAction } from "./terminalMenuModel";
import { useSessionPreview } from "./useSessionPreview";
import { isRuntimeGone, type RouteSession } from "./sessionRoute";
import { font, fontScaleCap, iconSize, radius, space, touchTarget, type } from "../tokens";
import { toolbarIcon } from "../toolbarIcon";
import { Spinner } from "../Spinner";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { Button, EmptyState } from "../ui";
import type { PlatformSymbol } from "../symbols";
import { TerminalNotice, TerminalNoticeHost, type TerminalNoticeMessage, type TerminalNoticeTone } from "./TerminalNotice";
import { InterfaceSwitchConfirm, type InterfaceSwitchPolicy } from "./InterfaceSwitchConfirm";
import { NoticeDismissButton } from "./NoticeDismissButton";
import { backOr } from "../backNavigation";
import { userFacingError } from "../connectionError";
import { ARCHIVE_SESSION_CONFIRM } from "../worker-action-model";
import { isTerminalGoneError, TERMINAL_STATUS_LABEL, terminalErrorCopy, terminalExitedCopy } from "./terminalCopy";
import { useTerminalMux } from "./useTerminalMux";
import { InterfaceSwitchCard } from "./InterfaceSwitchCard";
import { createPaintDetector, TERMINAL_PAINT_WAIT_MS } from "./terminalPaint";
import { useInterfaceHandoff } from "./useInterfaceHandoff";

const FONT_SIZE = 12;

/** A terminal with nothing running behind it: power, switched off. */
const TERMINATED_GLYPH: PlatformSymbol = { ios: "power", android: "power_settings_new" };

function terminalInterfacePhaseLabel(phase?: string): string {
	switch (phase) {
		case "draining":
			return "Waiting for the current terminal turn to finish. New messages are queued safely.";
		case "source_stopping":
			return "Stopping the terminal controller before Chat starts.";
		case "source_stopped":
			return "Terminal controller stopped. The worktree and native conversation are unchanged.";
		case "target_starting":
			return "Resuming the same native conversation in Chat.";
		case "activating":
			return "Opening the Chat interface.";
		case "completed":
			return "Chat is ready. Opening it now.";
		default:
			return "Checking that Chat can resume this agent's native conversation.";
	}
}

/**
 * `session` is what the route resolved when the board's lists do not hold this
 * id (see `sessionRouteView`); the lists still win whenever they have it, since
 * they are refreshed on every poll. A session from that lookup is not refreshed.
 * The shell route passes nothing.
 */
export default function TerminalScreen({ session: resolved }: { session?: RouteSession }) {
	const t = useTheme();
	const { scheme, style } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const params = useLocalSearchParams<{ id?: string; handleId?: string; projectId?: string; sessionId?: string; title?: string; kind?: string; hostId?: string }>();
	const shellOnly = Boolean(params.handleId);
	// A reviewer pane is attached by handle like a shell, but the daemon does not
	// own it as a shell terminal: closing it means stopping the worker's reviewer.
	const reviewerPane = params.kind === "reviewer" && Boolean(params.sessionId);
	const id = String(params.handleId ?? params.id ?? "");
	const sessionId = shellOnly ? String(params.sessionId ?? "") : id;
	const projectId = params.projectId ? String(params.projectId) : undefined;
	const router = useRouter();
	const navigation = useNavigation();
	const [headerRightReady, setHeaderRightReady] = useState(false);
	useLayoutEffect(
		() => resetHeaderRightForSwap(
			() => navigation.setOptions(glassHeaderControl("right")),
			() => setHeaderRightReady(true),
		),
		[navigation],
	);
	const insets = useSafeAreaInsets();

	// Leaving the screen. This route is cold-started with no back stack often
	// enough — a reload while on the terminal, or a deep link — that the rule lives
	// in one place; see `backOr`.
	const leave = useCallback(() => backOr(router), [router]);

	const xtermRef = useRef<XtermWebViewHandle | null>(null);
	// The authoritative grid the daemon told us the shared PTY is actually using
	// (driven by the largest/primary client — e.g. a co-viewing desktop). We render
	// THIS grid, not the phone's own fit, so the display matches the PTY and a
	// full-screen TUI doesn't mis-render. The WebView crops/scales it locally.
	const authRef = useRef<{ cols: number; rows: number } | null>(null);

	const [status, setStatus] = useState<MuxStatus>("connecting");
	// One notice at a time, the newest winning: an inline row on iOS, a snackbar
	// on Android (see TerminalNotice).
	const [notice, setNotice] = useState<TerminalNoticeMessage | null>(null);
	const noticeId = useRef(0);
	const showNotice = useCallback((text: string, tone: TerminalNoticeTone = "error") => {
		noticeId.current += 1;
		setNotice({ id: noticeId.current, text, tone });
	}, []);
	const clearNotice = useCallback(() => setNotice(null), []);
	const [dockHeight, setDockHeight] = useState(0);
	const [kbHeight, setKbHeight] = useState(0); // iOS: space to reserve for keyboard
	const [kbVisible, setKbVisible] = useState(false); // both platforms
	const [msg, setMsg] = useState("");
	const [sending, setSending] = useState(false);
	// A plain shell has no agent to message. Otherwise each send goes to the agent
	// unless the composer's toggle routes it to the terminal, and falls back to the
	// terminal while the agent waits on a prompt (routeForSend).
	const [sendTarget, setSendTarget] = useState<SendTarget>(shellOnly ? "terminal" : "agent");
	// A terminated session has no live PTY (the mux answers "Session not found").
	// Track that + the known status so we can offer Restore instead of a dead term.
	const [notFound, setNotFound] = useState(false);
	const [restoring, setRestoring] = useState(false);
	// Set while the session is switching to Chat. The switch stops this
	// terminal's PTY on purpose, so its exit is part of the handoff, not news.
	const switchingRef = useRef(false);
	// Whether the terminal has drawn anything yet. A fresh attach is empty until
	// the agent's TUI paints, which after a switch from Chat takes a few seconds.
	const [painted, setPainted] = useState(false);
	const [paintWaitOver, setPaintWaitOver] = useState(false);
	const paintedRef = useRef(false);
	const detectPaintRef = useRef(createPaintDetector());
	// Bumped whenever a fresh PTY is about to attach (Restore, another handle),
	// which restarts the wait below: the new agent boots onto an empty grid too.
	const [paintEpoch, setPaintEpoch] = useState(0);
	const restartPaintWait = useCallback(() => {
		paintedRef.current = false;
		detectPaintRef.current = createPaintDetector();
		setPainted(false);
		setPaintWaitOver(false);
		setPaintEpoch((epoch) => epoch + 1);
	}, []);

	const { sessions, orchestrators, restore, refresh, config: activeConfig } = useApp();
	const known =
		sessions.find((s) => s.id === sessionId) ??
		orchestrators.find((o) => o.id === sessionId) ??
		(!shellOnly && resolved?.id === sessionId ? resolved : null);
	// Runtime handles are opaque. Native macOS PTYs are versioned (ptyhost-v1:),
	// so using the session id here would incorrectly route the attach to legacy
	// tmux. Older daemons omit terminalHandleId and retain the historical
	// session-id handle, which keeps the fallback backward-compatible.
	const terminalHandleId = shellOnly ? id : known?.terminalHandleId || id;
	// The mux socket and the PTY attached on it. Theme changes still replace the
	// xterm; output arriving between the old WebView and the replacement's
	// onInitialized is held in wire order (see useTerminalMux).
	const { muxRef, openedRef, lastDimsRef, xtermReadyRef, markXtermReady, openTerminal } = useTerminalMux({
		config: activeConfig,
		terminalHandleId,
		projectId,
		xtermRef,
		events: {
			onStatus: setStatus,
			onExited: (code) => {
				if (!switchingRef.current) showNotice(terminalExitedCopy(code), code === 0 ? "info" : "error");
				setNotFound(true);
			},
			onOutput: (bytes) => {
				if (paintedRef.current || !detectPaintRef.current(bytes)) return;
				paintedRef.current = true;
				setPainted(true);
			},
			// A missing PTY means the session is terminated - offer Restore
			// instead of surfacing it as a raw error notice.
			onError: (message) => {
				if (isTerminalGoneError(message)) setNotFound(true);
				else showNotice(terminalErrorCopy(message));
			},
			// Render the daemon's authoritative grid exactly. The WebView applies
			// only local crop/zoom, so the phone cannot disturb a desktop owner.
			onResize: (cols, rows) => {
				authRef.current = { cols, rows };
				xtermRef.current?.resize({ cols, rows });
			},
		},
	});
	const interfaceSwitch = useInterfaceTransition(activeConfig, shellOnly ? "" : sessionId, refresh);
	// Owned by the screen rather than the hook: the background poll calls the same
	// `refresh()`, so a hook-wide busy flag would let a poll tick disable the
	// user's own button and swallow the very tap the recheck exists to serve.
	// The ref is the guard (state updates are async); the state drives the spinner.
	const [rechecking, setRechecking] = useState(false);
	const recheckingRef = useRef(false);
	// A recheck can be in flight for up to REQUEST_TIMEOUT_MS, long enough for the
	// user to leave. Alerting from a screen they already navigated away from would
	// interrupt whatever they went to instead.
	const mountedRef = useRef(true);
	useEffect(() => {
		mountedRef.current = true;
		return () => {
			mountedRef.current = false;
		};
	}, []);
	// The status the hook holds right now, readable after an await. The tap's
	// closure is from the render it started in, which is the pre-tap status.
	const interfaceStatusRef = useRef(interfaceSwitch.status);
	interfaceStatusRef.current = interfaceSwitch.status;
	const interfaceTransitionActive = mobileInterfaceTransitionIsActive(interfaceSwitch.transition);
	// The switch has finished but the route still shows this screen until the
	// board's next poll; the card stays up through that gap.
	const handoff = useInterfaceHandoff(interfaceSwitch.transition, "tui");
	const switchCardShown = interfaceTransitionActive || handoff.pending;
	switchingRef.current = switchCardShown || interfaceSwitch.starting;
	const interfaceRecoveryMessage = mobileInterfaceTransitionRecoveryMessage(interfaceSwitch.transition);
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
				: "The interface switch failed; Terminal UI remains available.");
	const interfaceFailureRecovery = useMemo(
		() => terminalInterfaceFailureRecovery(interfaceSwitch.transition),
		[interfaceSwitch.transition?.errorCode],
	);
	const interfaceBusy = Boolean(
		known &&
		(known.status === "working" ||
			known.status === "needs_input" ||
			known.activity === "active" ||
			known.activity === "waiting_input" ||
			known.activity === "blocked"),
	);
	const dead = notFound || (!shellOnly && known ? isRuntimeGone(known) : false);
	// Polled so the globe can advertise the optional App preview tab.
	const hasPreview = useSessionPreview(activeConfig, id, !shellOnly);
	// The terminal's artifacts, in its "⋯" once the agent has written something.
	const artifactCount = !shellOnly && known && "artifacts" in known ? deliverableArtifacts(known.artifacts ?? []).length : 0;
	const openArtifacts = useCallback(() => {
		router.push({ pathname: "/artifacts/[sessionId]", params: { sessionId, title: params.title, hostId: params.hostId } });
	}, [params.hostId, params.title, router, sessionId]);

	// Neither platform shrinks the layout for the keyboard: iOS never has, and on
	// Android edge-to-edge (edgeToEdgeEnabled) defeats windowSoftInputMode=adjustResize
	// so the window no longer resizes - the keyboard just draws over our content.
	// So reserve the keyboard on BOTH platforms and let the screen pad itself above
	// it, else the input dock (and its send button) hide behind it. What the event
	// reports is not the same quantity on the two platforms — rootKeyboardPad owns
	// that difference. This is the ONLY place the keyboard height is applied — the
	// dock adds nothing on top of it (see dockInset), because doing both is what
	// made the bar kick.
	// Deliberately still on the platform listeners, unlike the spawn sheet, the
	// Workers board and the chat screen, which all moved to keyboard-controller's
	// useKeyboardState.
	//
	// That hook subscribes to keyboardWillShow and keyboardDidHide only. This
	// screen needs keyboardDidShow as well — see the corrector below, which exists
	// because willShow reports a height that still includes the accessory bar we
	// hide. Migrating here would reinstate exactly the gap that corrector removes,
	// and would lose the iOS-only didHide backup whose absence on Android is itself
	// a fix (registering it there subscribed the same handler twice and collapsed
	// the dock). The gain would be earlier Android events on the one screen that
	// reserves keyboard space by hand, which is not worth reopening two fixed bugs.
	useEffect(() => {
		const isIOS = Platform.OS === "ios";
		const showEvt = isIOS ? "keyboardWillShow" : "keyboardDidShow";
		const hideEvt = isIOS ? "keyboardWillHide" : "keyboardDidHide";
		// Ride the system keyboard curve. Without this the relayout lands as an
		// instant jump while the keyboard is still sliding, which is most of why
		// the dock looked like it kicked.
		const animate = (duration?: number) =>
			LayoutAnimation.configureNext({
				duration: duration || 250,
				update: { type: LayoutAnimation.Types.keyboard },
			});
		const show = Keyboard.addListener(showEvt, (e) => {
			animate(e.duration);
			setKbVisible(true);
			setKbHeight(e.endCoordinates.height);
		});
		const hide = Keyboard.addListener(hideEvt, (e) => {
			animate(e?.duration);
			setKbVisible(false);
			setKbHeight(0);
		});
		// willShow can report a height that still includes the accessory bar we hid,
		// leaving a gap. didShow reports the actual final frame - use it to correct.
		// Guarded so the common case where the two agree is a no-op instead of a
		// second visible nudge.
		const didShow = isIOS
			? Keyboard.addListener("keyboardDidShow", (e) => {
					const next = e.endCoordinates.height;
					setKbHeight((h) => {
						if (h === next) return h;
						animate(e.duration);
						return next;
					});
				})
			: null;
		// Backup: guarantee the reserved space collapses even if willHide is missed.
		// iOS-only, like didShow above — Android's hide listener is already bound to
		// keyboardDidHide, so registering this there subscribed the same handler to
		// the same event twice and collapsed the dock on both.
		const didHide = isIOS
			? Keyboard.addListener("keyboardDidHide", () => {
					setKbVisible(false);
					setKbHeight(0);
				})
			: null;
		return () => {
			show.remove();
			hide.remove();
			didShow?.remove();
			didHide?.remove();
		};
	}, []);

	// The session's name, as the chat and the board call it; the short id only
	// when it has none. Everything else the bar holds is a toolbar item.
	const named = sessions.find((session) => session.id === id);
	const sessionName = named?.displayName || named?.issueTitle || params.title;
	useLayoutEffect(() => {
		navigation.setOptions({
			title: shellOnly ? (params.title || "Worktree shell") : sessionName || (id.length > 22 ? `${id.slice(0, 20)}...` : id),
		});
	}, [navigation, id, params.title, sessionName, shellOnly]);

	// Whether there is older output to jump to, as the WebView last reported it.
	const [canScrollTop, setCanScrollTop] = useState(false);
	// Android killed the WebView's renderer: the grid is black for good. Held as
	// state, not a notice, because a snackbar would leave before the problem does.
	const [viewGone, setViewGone] = useState(false);
	// A terminal that really is blank (a cleared shell, an empty alt screen)
	// must not hide behind the loader for good, so it gives up after a while.
	useEffect(() => {
		const timer = setTimeout(() => setPaintWaitOver(true), TERMINAL_PAINT_WAIT_MS);
		return () => clearTimeout(timer);
	}, [paintEpoch]);
	// A different PTY (the board reported a new handle) attaches empty as well.
	const paintHandleRef = useRef(terminalHandleId);
	useEffect(() => {
		if (paintHandleRef.current === terminalHandleId) return;
		paintHandleRef.current = terminalHandleId;
		restartPaintWait();
	}, [restartPaintWait, terminalHandleId]);
	const startLoaderShown = !painted && !paintWaitOver && !dead && !viewGone && !switchCardShown && status !== "error";

	useLayoutEffect(() => {
		xtermReadyRef.current = false;
		// The replacement WebView reports its own answer once it is up, and
		// starts with a renderer of its own.
		setCanScrollTop(false);
		setViewGone(false);
	}, [scheme, style]);


	// The WebView reports the phone's NATURAL fit (proposeDimensions, measure-only).
	// We forward it to the daemon as this client's requested size — used only when
	// the phone is the sole viewer (a co-viewing desktop, being primary, wins). The
	// render grid comes back via onTerminalResize; until it does, render the fit so
	// the terminal isn't blank.
	const applyDims = useCallback(
		(cols: number, rows: number) => {
			lastDimsRef.current = { cols, rows };
			if (openedRef.current) muxRef.current?.resize(terminalHandleId, cols, rows, projectId);
			if (!authRef.current) xtermRef.current?.resize({ cols, rows });
		},
		[terminalHandleId, projectId],
	);

	// fressh routes WebView {type:'debug'} messages to logger.log(prefix, message).
	// We piggyback on it for the FRESSH_DIMS and AO_SCROLLTOP reports (using a
	// custom onMessage would clobber fressh's own bridge).
	const logger = useMemo(
		() => ({
			log: (...args: unknown[]) => {
				const m = args[args.length - 1];
				if (typeof m === "string" && m.startsWith("FRESSH_DIMS ")) {
					const parts = m.split(" ");
					const cols = parseInt(parts[1], 10);
					const rows = parseInt(parts[2], 10);
					if (cols > 0 && rows > 0) applyDims(cols, rows);
					return;
				}
				const scrollTop = scrollTopReport(m);
				if (scrollTop !== undefined) setCanScrollTop(scrollTop);
			},
		}),
		[applyDims],
	);

	const onInitialized = useCallback(() => {
		markXtermReady();
		// A fresh xterm (first mount, or a remount after a font-zoom) starts at its
		// default grid — restore the daemon's authoritative grid onto it so it keeps
		// mirroring the shared PTY rather than snapping to the default.
		if (authRef.current) xtermRef.current?.resize(authRef.current);
		// Opens at most once per socket, so a WebView re-firing onInitialized (e.g.
		// remount on orientation change) does not attach the PTY twice. With no
		// socket yet, the socket opens it when it connects.
		openTerminal();
	}, [markXtermReady, openTerminal]);

	const onData = useCallback(
		(data: string) => {
			muxRef.current?.sendInput(terminalHandleId, data, projectId);
		},
		[terminalHandleId, projectId],
	);

	const sendKey = useCallback(
		(seq: string) => {
			muxRef.current?.sendInput(terminalHandleId, seq, projectId);
		},
		[terminalHandleId, projectId],
	);

	// Send the composed text to the selected route. The agent route can still
	// auto-engage the terminal route when the daemon reports a blocked prompt.
	//
	// AO's /send is the right route for a message: the daemon hands it to the
	// harness and submits it. But it sanitises control characters and refuses
	// outright while a session is paused on a permission prompt — answering 409
	// SESSION_AWAITING_DECISION with the advice "answer it in the session
	// terminal first". So on exactly that code we do what it says and write the
	// line to the PTY instead. `shouldRetryOnTerminal` keeps that narrow: a
	// terminated or exited session has no PTY, and rerouting there would swallow
	// the user's text and report success.
	//
	// The reroute is announced in a notice rather than done silently, and the
	// text stays in the field on any failure we did NOT handle, so nothing typed
	// is ever lost.
	const sendPrompt = useCallback(async () => {
		const text = msg.trim();
		if (!text) return;
		if (routeForSend(sendTarget) === "terminal") {
			if (muxRef.current && status === "open") {
				muxRef.current.sendInput(terminalHandleId, terminalPayload(text), projectId);
				haptics.success();
				// No notice: the composer's toggle already shows the route.
				setMsg("");
			} else {
				haptics.error();
				showNotice(TERMINAL_UNAVAILABLE_NOTICE);
			}
			return;
		}
		setSending(true);
		try {
			if (!activeConfig) throw new Error("No active machine connection");
			await sendMessage(activeConfig, id, text, { userAuthored: true });
			haptics.success();
			setMsg("");
		} catch (e) {
			const failure = e instanceof ApiError ? e : null;
			// Only reroute onto a mux we actually hold open — otherwise the write is
			// a no-op and we would clear the field having sent nothing.
			if (routeForSend(sendTarget, failure) === "terminal" && muxRef.current && status === "open") {
				muxRef.current.sendInput(terminalHandleId, terminalPayload(text), projectId);
				haptics.success();
				setMsg("");
				// Reroute this one send only. Leaving the toggle alone keeps later
				// messages on the agent's delivery path once the prompt is answered.
				showNotice(REROUTED_NOTICE, "info");
			} else {
				haptics.error();
				showNotice(`Message not sent. ${userFacingError(e)}`);
			}
		} finally {
			setSending(false);
		}
	}, [msg, sendTarget, activeConfig, id, terminalHandleId, projectId, status, showNotice]);

	// Push-to-talk dictation, captured on the PHONE rather than by the harness.
	//
	// Driving a harness's own voice mode from here (Claude Code's /voice) looks
	// easy — the key row already sends arbitrary bytes to the PTY — but it records
	// through the *daemon host's* microphone, which is a machine the user is not
	// sitting at. It would also only ever work for the harnesses that ship a voice
	// mode at all.
	//
	// Capturing here inverts both: the transcript is just text, so it leaves
	// through sendPrompt like anything typed, and every harness gets it for free.
	const voice = useVoiceInput({
		onTranscript: useCallback((text: string) => {
			// Land in the composer as an editable draft rather than sending. This
			// text reaches an agent with tool access and speech-to-text mishears code
			// vocabulary often enough that silent submission isn't acceptable.
			//
			// The composer is always mounted now, so there is nothing to open — and
			// nothing focuses the field either, which would pop the keyboard over the
			// terminal mid-phrase.
			// Append, so several held phrases build one prompt (the same way holding
			// the key again appends in Claude Code's own dictation).
			setMsg((m) => (m ? `${m} ${text}` : text));
			haptics.success();
		}, []),
	});

	// Recognition failures surface as a notice rather than new UI.
	useEffect(() => {
		if (voice.error) showNotice(voice.error);
	}, [voice.error, showNotice]);

	// Preview is one session-level destination: the live desktop browser first,
	// with the generated app/document preview available as a second tab.
	const toggleBrowser = useCallback(() => {
		router.push({
			pathname: "/preview/[id]",
			params: {
				id: sessionId,
				hostId: params.hostId,
				previewUrl: known && "previewUrl" in known ? known.previewUrl ?? undefined : undefined,
			},
		});
	}, [known, params.hostId, router, sessionId]);

	const startInterfaceSwitch = useCallback(
		async (policy: "drain" | "interrupt") => {
			try {
				await interfaceSwitch.start("chat", policy);
				clearNotice();
			} catch (cause) {
				showNotice(`Couldn't switch interface. ${userFacingError(cause)}`);
			}
		},
		[clearNotice, interfaceSwitch, showNotice],
	);

	// The detail of the "Switch to Chat?" question while it is being asked.
	const [switchQuestion, setSwitchQuestion] = useState<string | null>(null);
	const requestInterfaceSwitch = useCallback(async () => {
		// A second tap would start a second recheck against a link already slow
		// enough for the first one to be visible. `starting` and an active
		// transition are guarded too, so the button blocks exactly what it
		// announces as busy — otherwise a tap during the start POST fires a
		// duplicate the daemon answers with 409.
		if (recheckingRef.current || interfaceSwitch.starting || interfaceTransitionActive) return;
		haptics.tap();
		let status = interfaceSwitch.status;
		let recheck: InterfaceSwitchRecheck = { outcome: "answered" };
		if (!status?.supported) {
			// A tap can land between two polls, or before the first answer: ask
			// once more before telling the user it is unavailable.
			recheckingRef.current = true;
			setRechecking(true);
			try {
				const result = await interfaceSwitch.refresh();
				if (!mountedRef.current) return;
				if (!result) recheck = { outcome: "not-attempted" };
				// A superseded answer is not a verdict, same rule as the hook's own
				// loop: the readiness tick can overtake a slow tap request, and acting
				// on the tap's payload would alert "Couldn't reach AO" (or "Not ready
				// yet" off an older body) while the button had just enabled itself.
				// The newer request owns the answer; use whatever the hook adopted.
				else if (result.stale) status = interfaceStatusRef.current;
				else if (result.ok) status = result.status;
				else recheck = { outcome: "failed", error: result.error, status: result.status };
			} finally {
				recheckingRef.current = false;
				if (mountedRef.current) setRechecking(false);
			}
		}
		if (!status?.supported) {
			const { title, message } = interfaceSwitchAlert(status, interfaceSwitch.error, recheck);
			Alert.alert(title, message);
			return;
		}
		// The guard at the top saw the pre-tap render. The fresh answer can carry
		// a transition someone else started while the recheck was in flight (the
		// desktop button, say); starting another gets a 409 and a "Switch failed"
		// banner for a switch that is in fact proceeding. The card takes over and
		// explains it — but after a visible spinner, returning in silence reads
		// as a tap that was dropped, so confirm the outcome the user asked for
		// actually happened rather than leaving the card to arrive unannounced.
		if (mobileInterfaceTransitionIsActive(status.transition)) {
			haptics.success();
			return;
		}
		if (!interfaceBusy) {
			void startInterfaceSwitch("drain");
			return;
		}
		// Asked by InterfaceSwitchConfirm: an action sheet on iOS, the system
		// dialog on Android.
		setSwitchQuestion(
			known?.activity === "waiting_input" || known?.activity === "blocked"
				? "This turn is waiting for your input. Finish waits for your answer; stop cancels it and switches now."
				: "Keep the same Orchestrator session, worktree, and native agent conversation.",
		);
	}, [interfaceBusy, interfaceSwitch, interfaceTransitionActive, known?.activity, startInterfaceSwitch]);
	const closeSwitchQuestion = useCallback(() => setSwitchQuestion(null), []);
	const answerSwitchQuestion = useCallback((policy: InterfaceSwitchPolicy) => {
		setSwitchQuestion(null);
		void startInterfaceSwitch(policy);
	}, [startInterfaceSwitch]);

	// The poll keeps retrying on its own at up to 8s; this is for the user who can
	// see the network is back and does not want to wait for the tick. The header
	// button is disabled for the whole transition, so this is the only tap that
	// re-asks while one is live. Same guard as the header recheck: it is the same
	// request, and two of them against a slow link is what the guard prevents.
	const retryInterfaceCheck = useCallback(async () => {
		if (recheckingRef.current) return;
		recheckingRef.current = true;
		setRechecking(true);
		try {
			await interfaceSwitch.refresh();
		} finally {
			recheckingRef.current = false;
			if (mountedRef.current) setRechecking(false);
		}
	}, [interfaceSwitch]);

	// A finished switch the route has not caught up with: ask the board again,
	// which is what moves the route on to Chat.
	const [refreshingBoard, setRefreshingBoard] = useState(false);
	const retryHandoff = useCallback(async () => {
		setRefreshingBoard(true);
		try {
			await refresh();
		} catch {
			// The card stays up with Retry; the board's own poll reports the link.
		} finally {
			if (mountedRef.current) setRefreshingBoard(false);
		}
	}, [refresh]);

	const requestInterfaceFailureRecovery = useCallback(() => {
		if (!interfaceFailureRecovery) return;
		interfaceFailureRecovery.confirm((policy) => void startInterfaceSwitch(policy));
	}, [interfaceFailureRecovery, startInterfaceSwitch]);

	const openFiles = useCallback(() => {
		router.push(filesRoute(sessionId, params.hostId, params.title));
	}, [params.hostId, params.title, router, sessionId]);

	const confirmKill = useCallback(() => {
		const doKill = async () => {
			try {
				if (!activeConfig) throw new Error("No active machine connection");
				if (reviewerPane) await killSessionReviewer(activeConfig, String(params.sessionId));
				else if (shellOnly) await closeShellTerminal(activeConfig, id);
				else await killSession(activeConfig, id);
				haptics.success();
				leave();
			} catch (e) {
				haptics.error();
				showNotice(`${reviewerPane ? "Couldn't stop the reviewer" : shellOnly ? "Couldn't close the shell" : "Couldn't archive the session"}. ${userFacingError(e)}`);
			}
		};
		if (Platform.OS === "web") {
			doKill();
			return;
		}
		if (reviewerPane) {
			haptics.warning();
			Alert.alert("Stop reviewer session?", "This closes the persistent reviewer and cancels any review it is currently running. Review history is preserved.", [
				{ text: "Keep reviewer", style: "cancel" },
				{ text: "Stop reviewer", style: "destructive", onPress: doKill },
			]);
			return;
		}
		haptics.warning();
		Alert.alert(shellOnly ? "Close shell?" : ARCHIVE_SESSION_CONFIRM.title, shellOnly ? "This stops the worktree shell." : ARCHIVE_SESSION_CONFIRM.message, [
			{ text: shellOnly ? "Cancel" : ARCHIVE_SESSION_CONFIRM.cancel, style: "cancel" },
			{ text: shellOnly ? "Close" : ARCHIVE_SESSION_CONFIRM.confirm, style: "destructive", onPress: doKill },
		]);
	}, [activeConfig, id, leave, params.sessionId, reviewerPane, shellOnly, showNotice]);

	// Restore a terminated session: the daemon re-attaches its worktree agent and
	// its PTY comes back, so we re-open the terminal once restore succeeds.
	const onRestore = useCallback(async () => {
		setRestoring(true);
		try {
			await restore(id);
			clearNotice();
			setNotFound(false);
			restartPaintWait();
			openedRef.current = false;
			// Give the daemon a moment to bring the PTY up, then re-attach.
			setTimeout(openTerminal, 1200);
		} catch (e) {
			showNotice(`Couldn't restore the session. ${userFacingError(e)}`);
		} finally {
			setRestoring(false);
		}
	}, [restore, id, openTerminal, clearNotice, showNotice, restartPaintWait]);

	const xtermOptions = useMemo(
		() => ({
			fontSize: FONT_SIZE,
			cursorBlink: true,
			scrollback: 5000,
			// Move more rows per swipe so touch scrolling feels responsive.
			scrollSensitivity: 3,
			fastScrollSensitivity: 8,
			// The terminal's own palette, not the product one: agent TUIs own the
			// meaning of the ANSI slots.
			theme: terminalTheme(scheme, style),
			// Agent TUIs pick their colours assuming a dark canvas, so on the light
			// theme they can land white-on-white — the prompt row is exactly that
			// once its background band is collapsed away. xterm nudges any pair that
			// falls below this ratio until it is readable, which is cheaper and far
			// more reliable than trying to anticipate every colour a TUI might emit.
			minimumContrastRatio: 4.5,
		}),
		// `scheme` and `style` matter: without them the terminal keeps the palette
		// it was built with and stays dark after a theme switch.
		[scheme, style],
	);

	// Adjust only the phone's CSS viewport. The xterm renderer, mux attachment,
	// replay buffer, and daemon-owned PTY dimensions all remain untouched.
	const zoom = useCallback((delta: number) => {
		adjustTerminalViewport(xtermRef.current, delta > 0 ? 1 : -1);
	}, []);
	const fitWidth = useCallback(() => {
		fitTerminalViewport(xtermRef.current);
	}, []);
	const scrollToTop = useCallback(() => {
		scrollTerminalToTop(xtermRef.current);
	}, []);

	// The menu's rows reach their handlers through a ref. The transition hook
	// returns a new object every render, so `requestInterfaceSwitch` is new every
	// render too, and a new toolbar element registers the header's options again:
	// keyed on it, the header would rebuild on every keystroke.
	const menuActions: Record<TerminalMenuAction, () => void> = {
		switch_chat: () => void requestInterfaceSwitch(),
		preview: toggleBrowser,
		files: openFiles,
		artifacts: openArtifacts,
		zoom_out: () => zoom(-1),
		zoom_in: () => zoom(1),
		fit_width: fitWidth,
		archive: confirmKill,
		close_shell: confirmKill,
		stop_reviewer: confirmKill,
	};
	const menuActionsRef = useRef(menuActions);
	menuActionsRef.current = menuActions;
	// A machine picked but never paired: no terminal, and no notice row to say
	// why an archive failed, so the menu stays out (see the early return below).
	const unpaired = Boolean(activeConfig && !isConfigured(activeConfig));
	const menuSections = useMemo(() => unpaired ? [] : terminalMenuSections({
		agentSession: !shellOnly,
		chatReady: Boolean(interfaceSwitch.status?.supported),
		checkingChat: rechecking,
		switchingToChat: interfaceSwitch.starting || switchCardShown,
		previewReady: hasPreview,
		artifactCount,
		// Zoom only the mobile viewport; the shared PTY grid stays unchanged.
		// Pinch and double-tap zoom too.
		canZoom: !dead,
		// A dead session's way back is Restore, in the overlay; a dead shell can
		// still be closed.
		close: reviewerPane ? "reviewer" : shellOnly ? "shell" : dead ? undefined : "archive",
	}), [artifactCount, dead, hasPreview, interfaceSwitch.starting, interfaceSwitch.status?.supported, rechecking, reviewerPane, shellOnly, switchCardShown, unpaired]);
	// The bar holds only "⋯", as Chat's does: switch, preview and files buttons
	// beside it cut the iOS title to about nine characters. Chat opens its
	// actions as a sheet; the terminal keeps the platform's menu.
	const headerItems = useMemo(() => {
		// Held back for the frame `resetHeaderRightForSwap` keeps the slot empty,
		// so the bar never draws these over what the Chat renderer left there.
		if (!headerRightReady || !menuSections.length) return null;
		const ios = Platform.OS === "ios";
		const sections = menuSections.map((section) => ({ ...section, actions: section.items.map((item) => (
			<Stack.Toolbar.MenuAction
				key={item.action}
				icon={ios ? item.icon : undefined}
				subtitle={item.subtitle}
				destructive={item.destructive}
				disabled={item.disabled}
				onPress={() => menuActionsRef.current[item.action]()}
			>
				{item.label}
			</Stack.Toolbar.MenuAction>
		)) }));
		// The first section sits at the top, each later one in an inline section
		// (a divider on Android), so no menu opens on a divider. On iOS Smaller,
		// Larger and Fit width share one row as a palette of glyphs, their labels
		// read by VoiceOver; an M3 menu has no such row, so Android lists them.
		// UIKit draws no separator around a palette of its own, so the row sits
		// inside a plain inline section, which has one above and below.
		const menuItems = sections.flatMap((section, index) => {
			if (ios && section.kind === "view") {
				return [
					<Stack.Toolbar.Menu key={section.kind} inline>
						<Stack.Toolbar.Menu inline palette>{section.actions}</Stack.Toolbar.Menu>
					</Stack.Toolbar.Menu>,
				];
			}
			return index === 0 ? section.actions : [<Stack.Toolbar.Menu key={section.kind} inline>{section.actions}</Stack.Toolbar.Menu>];
		});
		return (
			<Stack.Toolbar placement="right">
				<Stack.Toolbar.Menu icon={toolbarIcon("more")} accessibilityLabel="Terminal actions">
					{/* The bar button's own title, which UIKit shows in the Large
					    Content Viewer and an overflowing bar. */}
					{ios ? <Stack.Toolbar.Label>Terminal actions</Stack.Toolbar.Label> : null}
					{menuItems}
				</Stack.Toolbar.Menu>
			</Stack.Toolbar>
		);
	}, [headerRightReady, menuSections]);

	const webViewOptions = useMemo(
		() => ({
			// Removes the extra "< > Done" / autofill bar iOS shows above the keyboard.
			hideKeyboardAccessoryView: true,
			// Custom drag/momentum scroll + input hardening (see TERMINAL_ENHANCE_JS).
			// Prepend the platform flag the enhance script branches on for scrolling.
			injectedJavaScript: `var IS_ANDROID=${Platform.OS === "android"};\n${TERMINAL_ENHANCE_JS}`,
			// NOTE: do NOT force androidLayerType:"hardware" here. xterm renders into a
			// <canvas>, and a hardware layer makes the Android WebView's render process
			// composite/crash blank on many devices (black terminal, no dims ever
			// reported). Leaving it at the default keeps the canvas visible.
			nestedScrollEnabled: true,
			// Surface an Android WebView render-process crash instead of a silent black
			// screen, so the user can tell the terminal died vs. never loaded.
			onRenderProcessGone: () => setViewGone(true),
		}),
		[],
	);

	if (unpaired) {
		return (
			<View style={styles.center}>
				{headerItems}
				<EmptyState icon="devices" title="No machine paired" />
			</View>
		);
	}

	// One inset for the whole dock. The root already reserves the keyboard, so the
	// dock owes nothing more while the keyboard is up — see dockInset.
	const bottomPad = dockInset(kbHeight, insets.bottom);
	// Android's reported kbHeight excludes the nav bar our root view draws under,
	// so rootKeyboardPad adds it back; on iOS it passes the height through.
	const rootPad = rootKeyboardPad(Platform.OS === "android" ? "android" : "ios", kbHeight, insets.bottom);

	return (
		<View style={[styles.screen, rootPad > 0 && { paddingBottom: rootPad }]}>
			{headerItems}
			<TerminalNotice notice={notice} onDismiss={clearNotice} />
			{interfaceTransitionNotice ? (
				// Not a transient notice: it waits for the user to acknowledge it, and
				// it can carry a recovery action, so it is a row on both platforms.
				<View style={[styles.transitionNotice, !interfaceTransitionRecovered && styles.transitionNoticeFailed]} accessibilityRole="alert">
					<View style={styles.transitionNoticeRow}>
						<AppSymbol
							name={interfaceTransitionRecovered ? "info" : "error"}
							size={iconSize.md}
							color={interfaceTransitionRecovered ? t.textSecondary : t.red}
						/>
						<Text maxFontSizeMultiplier={fontScaleCap.body} style={[styles.transitionNoticeText, !interfaceTransitionRecovered && styles.transitionNoticeTextFailed]}>
							{interfaceTransitionNoticeText}
							{interfaceSwitch.acknowledgeNoticeError
								? ` Couldn't dismiss: ${interfaceSwitch.acknowledgeNoticeError}`
								: ""}
						</Text>
						<NoticeDismissButton
							accessibilityLabel="Dismiss interface switch error"
							busy={interfaceSwitch.acknowledgingNotice}
							onPress={() => {
								void interfaceSwitch
									.acknowledgeNotice(interfaceTransitionNotice.id)
									.catch(() => {});
							}}
						/>
					</View>
					{interfaceFailureRecovery ? (
						<View style={styles.transitionNoticeActions}>
							<Button variant="danger" title={interfaceFailureRecovery.actionLabel} onPress={requestInterfaceFailureRecovery} />
						</View>
					) : null}
				</View>
			) : null}

			<View style={styles.termWrap}>
				<XtermJsWebView
					// Remount only on theme changes: xterm applies its palette at
					// construction, so already-painted rows otherwise keep old colours.
					key={`term-${style}-${scheme}`}
					ref={xtermRef}
					autoFit={false}
					xtermOptions={xtermOptions}
					webViewOptions={webViewOptions}
					logger={logger}
					onInitialized={onInitialized}
					onData={onData}
					style={{ flex: 1, backgroundColor: t.bgBase }}
				/>
				{/* The connection says something only while it is not live. It floats
				    over the grid rather than taking a row, so the grid keeps its
				    rows when the link comes and goes. */}
				{status !== "open" && !dead && !switchCardShown && !startLoaderShown ? (
					<View pointerEvents="none" style={styles.statusSlot}>
						<View style={styles.statusPill} accessibilityLiveRegion="polite">
							{status === "error" ? <AppSymbol name="offline" size={iconSize.sm} color={t.red} /> : <Spinner size={iconSize.sm} />}
							<Text style={styles.statusText} numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome}>
								{TERMINAL_STATUS_LABEL[status]}
							</Text>
						</View>
					</View>
				) : null}
				{canScrollTop && !dead && !viewGone ? <ScrollTopButton onPress={scrollToTop} /> : null}
				{startLoaderShown ? (
					<View pointerEvents="none" style={styles.startLoader} accessibilityLiveRegion="polite">
						<Spinner size="large" />
						<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.startLoaderText}>
							{status === "open" ? "Starting terminal…" : TERMINAL_STATUS_LABEL[status]}
						</Text>
					</View>
				) : null}
				{switchCardShown && handoff.stalled ? (
					<InterfaceSwitchCard
						title="Chat is ready"
						message="This screen hasn't switched over yet. Retry, or go back and open the session again."
						ready
						retryable
						retrying={refreshingBoard}
						onRetry={() => void retryHandoff()}
					/>
				) : switchCardShown ? (
					<InterfaceSwitchCard
						title={interfaceRecoveryMessage ? "Interface recovery blocked" : "Switching to Chat"}
						message={interfaceRecoveryMessage || terminalInterfacePhaseLabel(interfaceSwitch.transition?.phase)}
						blocked={Boolean(interfaceRecoveryMessage)}
						cancellable={mobileInterfaceTransitionIsCancellable(interfaceSwitch.transition)}
						cancelling={interfaceSwitch.cancelling}
						onCancel={() => void interfaceSwitch.cancel().catch(() => {})}
						error={interfaceSwitch.error}
						retryable={interfaceSwitch.fetchFailed || Boolean(interfaceRecoveryMessage)}
						retrying={rechecking}
						onRetry={() => void retryInterfaceCheck()}
					/>
				) : null}
				{viewGone ? (
					// Ahead of a dead session's overlay: Restore would reattach to a
					// renderer that is no longer there.
					<View style={styles.deadOverlay} accessibilityRole="alert">
						<EmptyState
							icon="warning"
							iconColor={t.red}
							title="The terminal view stopped working"
							message={shellOnly ? "Go back and open the shell again." : "Go back and open the session again."}
							action={<Button title="Go back" onPress={leave} />}
						/>
					</View>
				) : dead && !switchCardShown && (
					<View style={styles.deadOverlay}>
						<EmptyState
							icon={TERMINATED_GLYPH}
							title={shellOnly ? "Shell closed" : "Session terminated"}
							message={shellOnly ? "This worktree shell is no longer running." : "This session has no live terminal. Restore it to bring the agent back."}
							action={shellOnly ? undefined : (
								<Button title="Restore session" icon="refresh" loading={restoring} onPress={() => void onRestore()} />
							)}
						/>
					</View>
				)}
			</View>

			{/* The input dock. One container, one bottom inset, fixed slots — every
			    control sits in the same place in every state the screen can reach. */}
			<View style={[styles.dock, { paddingBottom: bottomPad }]} onLayout={(e) => setDockHeight(Math.round(e.nativeEvent.layout.height))}>
				<KeyRow onKey={sendKey} />

				<Composer
					value={msg}
					onChangeText={setMsg}
					onSend={sendPrompt}
					sending={sending || switchCardShown}
					target={sendTarget}
					onTargetChange={setSendTarget}
					targetLocked={shellOnly}
					voice={voice}
					keyboardVisible={kbVisible}
					onDismissKeyboard={Keyboard.dismiss}
				/>
			</View>
			{/* Absolutely placed, so it comes last and draws over the dock's
			    neighbours; the offset clears the keyboard pad and the dock. */}
			<TerminalNoticeHost notice={notice} onDismiss={clearNotice} bottomOffset={rootPad + dockHeight + space.sm} />
			<InterfaceSwitchConfirm message={switchQuestion} onChoose={answerSwitchQuestion} onCancel={closeSwitchQuestion} />
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: {
		flex: 1,
		alignItems: "center",
		justifyContent: "center",
		backgroundColor: t.bgBase,
	},
	statusSlot: { position: "absolute", top: space.sm, left: space.lg, right: space.lg, alignItems: "center" },
	statusPill: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.xs,
		maxWidth: "100%",
		paddingHorizontal: space.md,
		paddingVertical: space.xs,
		borderRadius: radius.pill,
		backgroundColor: t.bgElevated,
		borderWidth: rowDividerWidth,
		borderColor: t.borderDefault,
	},
	statusText: { flexShrink: 1, fontFamily: font.medium, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	// Laid out like TerminalNotice's row, closed by the same button, with the
	// recovery action on a line of its own when there is one.
	transitionNotice: {
		backgroundColor: t.bgElevated,
		borderBottomWidth: rowDividerWidth,
		borderBottomColor: t.borderSubtle,
	},
	transitionNoticeFailed: { backgroundColor: t.tintRed },
	transitionNoticeRow: { flexDirection: "row", alignItems: "center", gap: space.sm, minHeight: touchTarget, paddingLeft: space.lg },
	transitionNoticeText: { flex: 1, paddingVertical: space.sm, fontFamily: font.regular, color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	transitionNoticeTextFailed: { color: t.red },
	transitionNoticeActions: { flexDirection: "row", justifyContent: "flex-end", paddingHorizontal: space.lg, paddingBottom: space.sm },
	// iOS draws the grid's first column against the screen's rounded edge,
	// which cut it off; a few points in keeps every cell whole.
	termWrap: { flex: 1, backgroundColor: t.bgBase, paddingHorizontal: Platform.OS === "ios" ? space.xs : 0 },
	// The page's own surface, as under the chat composer: the composer's
	// elevated fill and the keys' tonal fill both need it to stand apart from.
	dock: {
		borderTopWidth: rowDividerWidth,
		borderTopColor: t.borderSubtle,
		backgroundColor: t.bgBase,
	},
	// Over the empty grid until the terminal first draws; the page's own
	// surface, so the blank grid behind it never shows.
	startLoader: {
		...StyleSheet.absoluteFill,
		alignItems: "center",
		justifyContent: "center",
		gap: space.sm,
		backgroundColor: t.bgBase,
	},
	startLoaderText: { ...type.footnote, color: t.textSecondary, textAlign: "center" },
	deadOverlay: { ...StyleSheet.absoluteFill, backgroundColor: t.bgBase },
});
