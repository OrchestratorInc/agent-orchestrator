import { Feather } from "@expo/vector-icons";
import { XtermJsWebView, type XtermWebViewHandle } from "@fressh/react-native-xtermjs-webview";
import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Alert, Keyboard, LayoutAnimation, Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { WebView } from "react-native-webview";
import { ApiError, getPreview, isTerminalStatus, killSession, sendMessage } from "../api";
import { authHeaders, isConfigured, loadConfig, type ServerConfig } from "../config";
import { terminalTheme, type Theme } from "../theme";
import { haptics } from "../haptics";
import { resetHeaderRightForSwap } from "../headerRightSwap";
import { MinimalBackButton } from "../MinimalBackButton";
import { MuxClient, type MuxStatus } from "../mux";
import { Composer } from "./Composer";
import { dockInset, rootKeyboardPad } from "./keyboardInset";
import { KeyRow } from "./KeyRow";
import {
	REROUTED_NOTICE,
	routeForSend,
	terminalPayload,
	TERMINAL_MODE_NOTICE,
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
import { adjustTerminalViewport } from "./terminalViewport";
import { terminalEnhanceScript } from "./terminalEnhanceScript";
import type { RouteSession } from "./sessionRoute";

const FONT_SIZE = 12;

// Injected into the xterm WebView after load. xterm has its own touch handlers
// that scroll by discrete lines (the janky "1 line per swipe"). We intercept in
// the CAPTURE phase and stopPropagation so those handlers never fire, then drive
// the viewport's scrollTop in proportion to finger movement (+ momentum). Taps
// (no significant movement) are left alone so tap-to-focus / keyboard still work.

const statusLabel: Record<MuxStatus, string> = {
	connecting: "connecting...",
	open: "live",
	closed: "disconnected",
	error: "error",
};
const statusColorFor = (t: Theme): Record<MuxStatus, string> => ({
	connecting: t.attention,
	open: t.green,
	closed: t.textTertiary,
	error: t.red,
});

function terminalInterfacePhaseLabel(phase?: string): string {
	switch (phase) {
		case "draining":
			return "Waiting for the current terminal turn to finish. New AO messages are queued safely.";
		case "source_stopping":
			return "Stopping the terminal controller before Chat starts.";
		case "source_stopped":
			return "Terminal controller stopped. The worktree and native conversation are unchanged.";
		case "target_starting":
			return "Resuming the same native conversation in Chat.";
		case "activating":
			return "Opening the Chat interface.";
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
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const params = useLocalSearchParams<{ id?: string; handleId?: string; projectId?: string; sessionId?: string; title?: string }>();
	const shellOnly = Boolean(params.handleId);
	const id = String(params.handleId ?? params.id ?? "");
	const sessionId = shellOnly ? String(params.sessionId ?? "") : id;
	const projectId = params.projectId ? String(params.projectId) : undefined;
	const router = useRouter();
	const navigation = useNavigation();
	const [headerRightReady, setHeaderRightReady] = useState(false);
	useLayoutEffect(
		() => resetHeaderRightForSwap(
			() => navigation.setOptions({ headerRight: undefined }),
			() => setHeaderRightReady(true),
		),
		[navigation],
	);
	const insets = useSafeAreaInsets();

	// Leaving the screen: pop when there's history, otherwise go to the board.
	// Guards against a missing/broken back button when this route was cold-started
	// with no back-stack - e.g. a reload while on the terminal, or a deep link.
	const leave = useCallback(() => {
		if (router.canGoBack()) router.back();
		else router.replace("/");
	}, [router]);

	const xtermRef = useRef<XtermWebViewHandle | null>(null);
	// Theme changes still replace xterm. Retain bytes arriving between the old
	// WebView unmount and the replacement's onInitialized callback in wire order.
	const xtermReadyRef = useRef(false);
	const pendingOutputRef = useRef<Uint8Array[]>([]);
	const muxRef = useRef<MuxClient | null>(null);
	const openedRef = useRef(false);
	// Last grid size reported by the WebView's FitAddon, so we can send it to the
	// PTY the moment the terminal opens (dims may arrive before or after open).
	const lastDimsRef = useRef<{ cols: number; rows: number } | null>(null);
	// The authoritative grid the daemon told us the shared PTY is actually using
	// (driven by the largest/primary client — e.g. a co-viewing desktop). We render
	// THIS grid, not the phone's own fit, so the display matches the PTY and a
	// full-screen TUI doesn't mis-render. The WebView crops/scales it locally.
	const authRef = useRef<{ cols: number; rows: number } | null>(null);

	const [cfg, setCfg] = useState<ServerConfig | null>(null);
	const [status, setStatus] = useState<MuxStatus>("connecting");
	const [size, setSize] = useState<{ cols: number; rows: number } | null>(null);
	const [banner, setBanner] = useState<string | null>(null);
	const [kbHeight, setKbHeight] = useState(0); // iOS: space to reserve for keyboard
	const [kbVisible, setKbVisible] = useState(false); // both platforms
	const [msg, setMsg] = useState("");
	const [sending, setSending] = useState(false);
	const [sendTarget, setSendTarget] = useState<SendTarget>(shellOnly ? "terminal" : "agent");
	// A terminated session has no live PTY (the mux answers "Session not found").
	// Track that + the known status so we can offer Restore instead of a dead term.
	const [notFound, setNotFound] = useState(false);
	const [restoring, setRestoring] = useState(false);
	// In-app browser: shows the static preview file the agent generated (an
	// index.html). We poll the daemon's on-demand detector while the terminal is
	// open, but we deliberately DO NOT auto-open the overlay: the detector falls back
	// to any previewable file (e.g. a repo's README.md), so auto-popping would steal
	// the screen with an unbuilt/blank page. Instead the globe button lights up with a
	// green dot when the agent has produced something to view (any previewable file
	// except the repo README); the user taps it to open.
	const [browserOpen, setBrowserOpen] = useState(false);
	const [preview, setPreview] = useState<{ entry: string; url: string } | null>(null);
	const previewWebRef = useRef<WebView>(null);

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
	const interfaceSwitch = useInterfaceTransition(cfg, shellOnly ? "" : sessionId, refresh);
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
			? "AO restored the session in its last committed interface."
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
	const dead = notFound || (!shellOnly && known ? isTerminalStatus(known.status) : false);
	// What counts as a live preview: any file the daemon surfaces (an .html build, or
	// a generated doc like plan.md / a report) EXCEPT a repo's README, which the
	// detector's markdown fallback always matches on a fresh checkout. Filtering the
	// README out keeps the globe's green dot meaningful — it means "there's something
	// the agent produced to view", not just "this repo has a README".
	const previewBase = (preview?.entry ?? "").split("/").pop() ?? "";
	const isReadme = /^readme\.(md|markdown)$/i.test(previewBase);
	const hasPreview = !!preview && !isReadme;

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

	// Header shows just the short id; Kill lives in our own status bar below so we
	// fully control its shape/alignment (iOS draws its own box behind header
	// buttons, which fights any custom background).
	useLayoutEffect(() => {
		navigation.setOptions({
			title: shellOnly ? (params.title || "Worktree shell") : id.length > 22 ? `${id.slice(0, 20)}...` : id,
			// Always render our own Back control so it works even when the app was
			// cold-started directly on this route (reload/deep link) and the stack
			// has no history for the default back button to use.
			headerLeft: () => <MinimalBackButton onPress={leave} />,
		});
	}, [navigation, id, leave, params.title, shellOnly]);

	// Load config, then connect the mux socket.
	// Rebuilt whenever the active endpoint changes, not only when the session
	// does. The app re-races its endpoints when the network moves — losing
	// Wi-Fi hands the session to Tailscale or the tunnel — and a mux still
	// pointed at the previous address stays disconnected on a blank screen
	// until the screen is closed and reopened.
	const activeBaseUrl = activeConfig
		? `${activeConfig.secure ? "https" : "http"}://${activeConfig.host}:${activeConfig.httpPort}`
		: "";

	useEffect(() => {
		let disposed = false;
		(async () => {
			// Prefer the endpoint the race settled on; fall back to storage on the
			// first render, before the store has resolved one.
			const config = activeConfig ?? (await loadConfig());
			if (disposed) return;
			setCfg(config);
			if (!isConfigured(config)) return;

			const mux = new MuxClient(config, {
				onStatus: (s) => setStatus(s),
				onTerminalData: (tid, bytes) => {
					if (tid !== terminalHandleId) return;
					if (!xtermReadyRef.current || !xtermRef.current) {
						pendingOutputRef.current.push(bytes.slice());
						return;
					}
					xtermRef.current.write(bytes);
				},
				onTerminalExited: (tid, code) => {
					if (tid === terminalHandleId) {
						setBanner(`Session exited (code ${code})`);
						setNotFound(true);
					}
				},
				onTerminalError: (tid, msg) => {
					if (tid !== terminalHandleId) return;
					// A missing PTY means the session is terminated - offer Restore
					// instead of surfacing it as a raw error banner.
					if (/not found/i.test(msg)) setNotFound(true);
					else setBanner(msg);
				},
				onTerminalResize: (tid, cols, rows) => {
					if (tid !== terminalHandleId) return;
					// Render the daemon's authoritative grid exactly. The WebView applies
					// only local crop/zoom, so the phone cannot disturb a desktop owner.
					authRef.current = { cols, rows };
					setSize({ cols, rows });
					xtermRef.current?.resize({ cols, rows });
				},
			});
			muxRef.current = mux;
			mux.connect();
		})();
		return () => {
			disposed = true;
			muxRef.current?.disconnect();
			muxRef.current = null;
			xtermReadyRef.current = false;
			pendingOutputRef.current = [];
		};
		// activeBaseUrl, not activeConfig: the object identity changes on every
		// resolve, and rebuilding the mux on each one would tear down a healthy
		// terminal for no reason.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [terminalHandleId, activeBaseUrl]);

	useLayoutEffect(() => {
		xtermReadyRef.current = false;
	}, [scheme]);

	// Poll the daemon's on-demand preview detector while the terminal is open, just
	// to keep `preview` current for the globe button. We never auto-open the overlay
	// (see the note by the state above): the detector's markdown fallback matches a
	// repo README, so auto-popping would surface a blank/unbuilt page.
	useEffect(() => {
		if (shellOnly) return;
		if (!cfg || !isConfigured(cfg)) return;
		let cancelled = false;
		let timer: ReturnType<typeof setTimeout> | null = null;
		const tick = async () => {
			try {
				const p = await getPreview(cfg, id);
				if (cancelled) return;
				setPreview(p);
			} catch {
				/* transient - keep polling */
			}
			if (!cancelled) timer = setTimeout(tick, 5000);
		};
		tick();
		return () => {
			cancelled = true;
			if (timer) clearTimeout(timer);
		};
	}, [cfg, id, shellOnly]);

	// The WebView reports the phone's NATURAL fit (proposeDimensions, measure-only).
	// We forward it to the daemon as this client's requested size — used only when
	// the phone is the sole viewer (a co-viewing desktop, being primary, wins). The
	// render grid comes back via onTerminalResize; until it does, render the fit so
	// the terminal isn't blank.
	const applyDims = useCallback(
		(cols: number, rows: number) => {
			lastDimsRef.current = { cols, rows };
			if (openedRef.current) muxRef.current?.resize(terminalHandleId, cols, rows, projectId);
			if (!authRef.current) {
				setSize({ cols, rows });
				xtermRef.current?.resize({ cols, rows });
			}
		},
		[terminalHandleId, projectId],
	);

	// fressh routes WebView {type:'debug'} messages to logger.log(prefix, message).
	// We piggyback on it for the FRESSH_DIMS report (using a custom onMessage would
	// clobber fressh's own bridge).
	const logger = useMemo(
		() => ({
			log: (...args: unknown[]) => {
				const m = args[args.length - 1];
				if (typeof m === "string" && m.startsWith("FRESSH_DIMS ")) {
					const parts = m.split(" ");
					const cols = parseInt(parts[1], 10);
					const rows = parseInt(parts[2], 10);
					if (cols > 0 && rows > 0) applyDims(cols, rows);
				}
			},
		}),
		[applyDims],
	);

	const onInitialized = useCallback(() => {
		xtermReadyRef.current = true;
		const pending = pendingOutputRef.current;
		pendingOutputRef.current = [];
		for (const bytes of pending) xtermRef.current?.write(bytes);
		// A fresh xterm (first mount, or a remount after a font-zoom) starts at its
		// default grid — restore the daemon's authoritative grid onto it so it keeps
		// mirroring the shared PTY rather than snapping to the default.
		if (authRef.current) xtermRef.current?.resize(authRef.current);
		// Guard against a second open if the WebView re-fires onInitialized (e.g.
		// remount on orientation change) - that would attach the PTY twice.
		if (openedRef.current) return;
		openedRef.current = true;
		muxRef.current?.openTerminal(terminalHandleId, projectId);
		// If the FitAddon already reported dims before open, send them to the PTY now.
		const d = lastDimsRef.current;
		if (d) muxRef.current?.resize(terminalHandleId, d.cols, d.rows, projectId);
	}, [terminalHandleId, projectId]);

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
	// The reroute is announced in the banner rather than done silently, and the
	// text stays in the field on any failure we did NOT handle, so nothing typed
	// is ever lost.
	const sendPrompt = useCallback(async () => {
		const text = msg.trim();
		if (!text) return;
		if (routeForSend(sendTarget) === "terminal") {
			if (muxRef.current && status === "open") {
				muxRef.current.sendInput(terminalHandleId, terminalPayload(text), projectId);
				haptics.success();
				setMsg("");
				setBanner(TERMINAL_MODE_NOTICE);
			} else {
				haptics.error();
				setBanner(TERMINAL_UNAVAILABLE_NOTICE);
			}
			return;
		}
		setSending(true);
		try {
			const config = cfg ?? (await loadConfig());
			await sendMessage(config, id, text);
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
				setSendTarget("terminal");
				setBanner(REROUTED_NOTICE);
			} else {
				haptics.error();
				setBanner(`Send failed: ${e instanceof Error ? e.message : String(e)}`);
			}
		} finally {
			setSending(false);
		}
	}, [msg, sendTarget, cfg, id, terminalHandleId, projectId, status]);

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

	// Recognition failures surface in the existing banner rather than new UI.
	useEffect(() => {
		if (voice.error) setBanner(voice.error);
	}, [voice.error]);

	// Toggle the in-app browser. The poll above keeps `preview` current, so a tap
	// just shows/hides the overlay. A bare README (the detector's markdown fallback)
	// reports "no preview yet" instead of surfacing an unbuilt repo doc.
	const toggleBrowser = useCallback(() => {
		haptics.tap();
		if (browserOpen) {
			setBrowserOpen(false);
			return;
		}
		if (!hasPreview) {
			setBanner("No preview yet - waiting for the agent to generate a page or document...");
			return;
		}
		setBrowserOpen(true);
	}, [browserOpen, hasPreview]);

	const startInterfaceSwitch = useCallback(
		async (policy: "drain" | "interrupt") => {
			try {
				await interfaceSwitch.start("chat", policy);
				setBanner(null);
			} catch (cause) {
				setBanner(`Switch failed: ${cause instanceof Error ? cause.message : String(cause)}`);
			}
		},
		[interfaceSwitch],
	);

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
				// on the tap's payload would alert "Could not reach AO" (or "Not ready
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
		Alert.alert(
			"Switch to Chat?",
			known?.activity === "waiting_input" || known?.activity === "blocked"
				? "This turn is waiting for your input. Finish waits for your answer; stop cancels it and switches now."
				: "Keep the same AO session, worktree, and native agent conversation.",
			[
				{ text: "Keep Terminal UI", style: "cancel" },
				{ text: "Finish, then switch", onPress: () => void startInterfaceSwitch("drain") },
				{ text: "Stop and switch", style: "destructive", onPress: () => void startInterfaceSwitch("interrupt") },
			],
		);
	}, [interfaceBusy, interfaceSwitch, interfaceTransitionActive, known?.activity, startInterfaceSwitch]);

	// The poll keeps retrying on its own at up to 8s; this is for the user who can
	// see the network is back and does not want to wait for the tick. The header
	// button is disabled for the whole transition, so this is the only tap that
	// re-asks while one is live. Same guard as the header recheck: it is the same
	// request, and two of them against a slow link is what the guard prevents.
	const retryInterfaceCheck = useCallback(async () => {
		if (recheckingRef.current) return;
		haptics.tap();
		recheckingRef.current = true;
		setRechecking(true);
		try {
			await interfaceSwitch.refresh();
		} finally {
			recheckingRef.current = false;
			if (mountedRef.current) setRechecking(false);
		}
	}, [interfaceSwitch]);

	const requestInterfaceFailureRecovery = useCallback(() => {
		if (!interfaceFailureRecovery) return;
		interfaceFailureRecovery.confirm((policy) => void startInterfaceSwitch(policy));
	}, [interfaceFailureRecovery, startInterfaceSwitch]);

	// The browser toggle lives in the nav bar, beside the session name, to keep the
	// status row uncluttered. Separate from the headerLeft effect above because
	// `toggleBrowser` is declared here — referencing it in that effect's dep array
	// would read it before initialisation.
	useLayoutEffect(() => {
		if (shellOnly || !headerRightReady) {
			navigation.setOptions({ headerRight: undefined });
			return;
		}
		navigation.setOptions({
			headerRight: () => (
				<View style={styles.headerActions}>
					<Pressable
						hitSlop={10}
						accessibilityLabel="Open Chat interface"
						accessibilityState={{
							busy: interfaceTransitionActive || interfaceSwitch.starting || rechecking,
						}}
						disabled={rechecking || interfaceSwitch.starting || interfaceTransitionActive}
						onPress={() => void requestInterfaceSwitch()}
						style={({ pressed }) => [styles.headerBrowserBtn, pressed && { opacity: 0.6 }]}
					>
						{/* A spinner rather than a dimmed glyph: the recheck only runs while
						    the icon is already faint, so fading it further reads as broken.
						    `starting` shows it too — that POST is disabled-but-silent for up
						    to REQUEST_TIMEOUT_MS otherwise, the same dead-button shape the
						    recheck spinner exists to remove. */}
						{rechecking || interfaceSwitch.starting ? (
							<ActivityIndicator size="small" color={t.blue} />
						) : (
							<Feather
								name={interfaceTransitionActive ? "repeat" : "message-square"}
								size={18}
								color={interfaceSwitch.status?.supported ? t.blue : t.textFaint}
							/>
						)}
					</Pressable>
					<Pressable
						hitSlop={12}
						accessibilityLabel={browserOpen ? "Close preview" : "Open preview"}
						onPress={toggleBrowser}
						style={({ pressed }) => [styles.headerBrowserBtn, pressed && { opacity: 0.6 }]}
					>
						<Feather
							name="globe"
							size={19}
							color={browserOpen ? t.blue : hasPreview ? t.green : t.textSecondary}
						/>
						{hasPreview && !browserOpen && <View style={styles.browserReadyDot} />}
					</Pressable>
				</View>
			),
		});
	}, [headerRightReady, navigation, browserOpen, hasPreview, toggleBrowser, styles, t, shellOnly, interfaceTransitionActive, interfaceSwitch.starting, rechecking, interfaceSwitch.status?.supported, requestInterfaceSwitch]);

	const confirmKill = useCallback(() => {
		const doKill = async () => {
			try {
				const config = cfg ?? (await loadConfig());
				if (shellOnly) await closeShellTerminal(config, id);
				else await killSession(config, id);
				haptics.success();
				leave();
			} catch (e) {
				haptics.error();
				setBanner(`Kill failed: ${e instanceof Error ? e.message : String(e)}`);
			}
		};
		if (Platform.OS === "web") {
			doKill();
			return;
		}
		// Cautionary buzz as the destructive confirmation dialog is raised.
		haptics.warning();
		Alert.alert(shellOnly ? "Close shell?" : "Kill session?", shellOnly ? "This stops the worktree shell." : `This stops ${id}.`, [
			{ text: "Cancel", style: "cancel" },
			{ text: shellOnly ? "Close" : "Kill", style: "destructive", onPress: doKill },
		]);
	}, [cfg, id, leave, shellOnly]);

	// Restore a terminated session: the daemon re-attaches its worktree agent and
	// its PTY comes back, so we re-open the terminal once restore succeeds.
	const onRestore = useCallback(async () => {
		setRestoring(true);
		try {
			await restore(id);
			setBanner(null);
			setNotFound(false);
			openedRef.current = false;
			// Give the daemon a moment to bring the PTY up, then re-attach.
			setTimeout(() => {
				if (openedRef.current) return;
				openedRef.current = true;
				muxRef.current?.openTerminal(terminalHandleId, projectId);
				const d = lastDimsRef.current;
				if (d) muxRef.current?.resize(terminalHandleId, d.cols, d.rows, projectId);
			}, 1200);
		} catch (e) {
			setBanner(`Restore failed: ${e instanceof Error ? e.message : String(e)}`);
		} finally {
			setRestoring(false);
		}
	}, [restore, id, terminalHandleId, projectId]);

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
			theme: terminalTheme(scheme),
			// Agent TUIs pick their colours assuming a dark canvas, so on the light
			// theme they can land white-on-white — the prompt row is exactly that
			// once its background band is collapsed away. xterm nudges any pair that
			// falls below this ratio until it is readable, which is cheaper and far
			// more reliable than trying to anticipate every colour a TUI might emit.
			minimumContrastRatio: 4.5,
		}),
		// `scheme` matters: without it the terminal keeps the palette it was built
		// with and stays dark after a theme switch.
		[scheme],
	);

	// Adjust only the phone's CSS viewport. The xterm renderer, mux attachment,
	// replay buffer, and daemon-owned PTY dimensions all remain untouched.
	const zoom = useCallback((delta: number) => {
		adjustTerminalViewport(xtermRef.current, delta > 0 ? 1 : -1);
	}, []);

	const webViewOptions = useMemo(
		() => ({
			// Removes the extra "< > Done" / autofill bar iOS shows above the keyboard.
			hideKeyboardAccessoryView: true,
			// Custom drag/momentum scroll + input hardening (see TERMINAL_ENHANCE_JS).
			// Prepend the platform flag the enhance script branches on for scrolling.
			injectedJavaScript: terminalEnhanceScript(Platform.OS === "android" ? "android" : "ios"),
			// NOTE: do NOT force androidLayerType:"hardware" here. xterm renders into a
			// <canvas>, and a hardware layer makes the Android WebView's render process
			// composite/crash blank on many devices (black terminal, no dims ever
			// reported). Leaving it at the default keeps the canvas visible.
			nestedScrollEnabled: true,
			// Surface an Android WebView render-process crash instead of a silent black
			// screen, so the user can tell the terminal died vs. never loaded.
			onRenderProcessGone: () => setBanner("Terminal renderer crashed - reopen the session (Back, then tap it again)."),
		}),
		[],
	);

	if (cfg && !isConfigured(cfg)) {
		return (
			<View style={styles.center}>
				<Text style={styles.bannerText}>No server configured.</Text>
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
			<View style={styles.statusBar}>
				<View style={[styles.statusDot, { backgroundColor: statusColorFor(t)[status] }]} />
				<Text style={styles.statusText}>{statusLabel[status]}</Text>
				{size && !dead && (
					<Text style={styles.dims}>
						{size.cols}x{size.rows}
					</Text>
				)}
				{/* Zoom only the mobile viewport; the shared PTY grid stays unchanged. */}
				{!dead && (
					<View style={styles.zoomGroup}>
						<Pressable
							hitSlop={6}
							accessibilityLabel="Smaller text"
							onPress={() => { haptics.tap(); zoom(-1); }}
							style={({ pressed }) => [styles.zoomBtn, pressed && { opacity: 0.6 }]}
						>
							<Feather name="minus" size={13} color={t.textSecondary} />
						</Pressable>
						<View style={styles.zoomDivider} />
						<Pressable
							hitSlop={6}
							accessibilityLabel="Larger text"
							onPress={() => { haptics.tap(); zoom(1); }}
							style={({ pressed }) => [styles.zoomBtn, pressed && { opacity: 0.6 }]}
						>
							<Feather name="plus" size={13} color={t.textSecondary} />
						</Pressable>
					</View>
				)}
				{dead && !shellOnly ? (
					<Pressable
						hitSlop={8}
						onPress={() => { haptics.tap(); void onRestore(); }}
						disabled={restoring}
						style={({ pressed }) => [styles.restoreBtn, (pressed || restoring) && { opacity: 0.7 }]}
					>
						<Feather name="rotate-ccw" size={12} color={t.blue} />
						<Text style={styles.restoreText}>{restoring ? "Restoring..." : "Restore"}</Text>
					</Pressable>
				) : (
					// Icon-only: the trash glyph reads as "destroy this session" on its
					// own, so the label would only cost width. Hence accessibilityLabel.
					<Pressable
						hitSlop={8}
						accessibilityLabel={shellOnly ? "Close shell" : "Kill session"}
						onPress={confirmKill}
						style={({ pressed }) => [styles.killBtn, pressed && { opacity: 0.7 }]}
					>
						<Feather name={shellOnly ? "x" : "trash-2"} size={14} color={t.red} />
					</Pressable>
				)}
			</View>

			{banner && (
				<Pressable onPress={() => { haptics.tap(); setBanner(null); }} style={styles.banner}>
					<Text style={styles.bannerText}>{banner} (tap to dismiss)</Text>
				</Pressable>
			)}
			{interfaceTransitionNotice ? (
				<View style={styles.banner}>
					<Text style={styles.bannerText}>
						{interfaceTransitionNoticeText}
						{interfaceSwitch.acknowledgeNoticeError
							? ` Could not dismiss: ${interfaceSwitch.acknowledgeNoticeError}`
							: ""}
					</Text>
					<View style={styles.interfaceFailureActions}>
						{interfaceFailureRecovery ? (
							<Pressable
								accessibilityRole="button"
								onPress={() => {
									haptics.tap();
									requestInterfaceFailureRecovery();
								}}
							>
								<Text style={styles.interfaceFailureActionText}>{interfaceFailureRecovery.actionLabel}</Text>
							</Pressable>
						) : null}
						<Pressable
							accessibilityRole="button"
							accessibilityLabel="Dismiss interface switch error"
							disabled={interfaceSwitch.acknowledgingNotice}
							onPress={() => {
								haptics.tap();
								void interfaceSwitch
									.acknowledgeNotice(interfaceTransitionNotice.id)
									.catch(() => {});
							}}
						>
							<Text style={styles.interfaceFailureDismissText}>
								{interfaceSwitch.acknowledgingNotice ? "Dismissing…" : "Dismiss"}
							</Text>
						</Pressable>
					</View>
				</View>
			) : null}

			<View style={styles.termWrap}>
				<XtermJsWebView
					// Remount only on theme changes: xterm applies its palette at
					// construction, so already-painted rows otherwise keep old colours.
					key={`term-${scheme}`}
					ref={xtermRef}
					autoFit={false}
					xtermOptions={xtermOptions}
					webViewOptions={webViewOptions}
					logger={logger}
					onInitialized={onInitialized}
					onData={onData}
					style={{ flex: 1, backgroundColor: t.bgBase }}
				/>
				{interfaceTransitionActive ? (
					<View style={styles.interfaceOverlay}>
						<View style={styles.interfaceCard}>
							<Feather name={interfaceRecoveryMessage ? "alert-triangle" : "repeat"} size={22} color={t.blue} />
							<Text style={styles.interfaceTitle}>{interfaceRecoveryMessage ? "Interface recovery blocked" : "Switching to Chat"}</Text>
							<Text style={styles.interfaceCopy}>{interfaceRecoveryMessage || terminalInterfacePhaseLabel(interfaceSwitch.transition?.phase)}</Text>
							{mobileInterfaceTransitionIsCancellable(interfaceSwitch.transition) ? (
								<Pressable
									disabled={interfaceSwitch.cancelling}
									onPress={() => { haptics.tap(); void interfaceSwitch.cancel().catch(() => {}); }}
									style={styles.interfaceCancel}
								>
									<Text style={styles.interfaceCancelText}>{interfaceSwitch.cancelling ? "Cancelling…" : "Cancel switch"}</Text>
								</Pressable>
							) : null}
							{interfaceSwitch.error ? <Text style={styles.interfaceError}>{interfaceSwitch.error}</Text> : null}
							{interfaceSwitch.fetchFailed || interfaceRecoveryMessage ? (
								<Pressable
									disabled={rechecking}
									onPress={() => void retryInterfaceCheck()}
									style={styles.interfaceCancel}
								>
									<Text style={styles.interfaceCancelText}>{rechecking ? "Retrying…" : "Retry"}</Text>
								</Pressable>
							) : null}
						</View>
					</View>
				) : null}
				{dead && (
					<View style={styles.deadOverlay}>
						<View style={styles.deadIcon}>
							<Feather name="power" size={24} color={t.textTertiary} />
						</View>
						<Text style={styles.deadTitle}>{shellOnly ? "Shell closed" : "Session terminated"}</Text>
						<Text style={styles.deadMsg}>{shellOnly ? "This worktree shell is no longer running." : "This session has no live terminal. Restore it to bring the agent back."}</Text>
						{!shellOnly ? <Pressable
								onPress={() => { haptics.tap(); void onRestore(); }}
							disabled={restoring}
							style={({ pressed }) => [styles.restoreCta, (pressed || restoring) && { opacity: 0.8 }]}
						>
							<Feather name="rotate-ccw" size={16} color={t.onAccent} />
							<Text style={styles.restoreCtaText}>{restoring ? "Restoring..." : "Restore session"}</Text>
						</Pressable> : null}
					</View>
				)}

				{/* In-app browser overlay: the agent's generated preview file. Sits over
				    the terminal (which keeps running underneath) with its own bar. */}
				{browserOpen && preview && (
					<View style={styles.browserOverlay}>
						<View style={styles.browserBar}>
							<Feather name="globe" size={13} color={t.textTertiary} />
							<Text style={styles.browserPath} numberOfLines={1}>
								{preview.entry}
							</Text>
							<Pressable hitSlop={8} onPress={() => { haptics.tap(); previewWebRef.current?.reload(); }} style={styles.browserAction}>
								<Feather name="rotate-cw" size={15} color={t.blue} />
							</Pressable>
							<Pressable hitSlop={8} onPress={() => { haptics.tap(); setBrowserOpen(false); }} style={styles.browserAction}>
								<Feather name="x" size={17} color={t.textSecondary} />
							</Pressable>
						</View>
						<WebView
							ref={previewWebRef}
							// The preview route lives behind the daemon's connection-password
							// auth (Bearer). Without this header the WebView's request 401s and
							// renders the JSON error body instead of the page. cfg carries the
							// password we paired with; authHeaders() turns it into the Bearer.
							source={{ uri: preview.url, headers: cfg ? authHeaders(cfg) : undefined }}
							originWhitelist={["*"]}
							style={styles.browserWeb}
							onError={() => setBanner("Preview failed to load.")}
						/>
					</View>
				)}
			</View>

			{/* The input dock. One container, one bottom inset, fixed slots — every
			    control sits in the same place in every state the screen can reach. */}
			<View style={[styles.dock, { paddingBottom: bottomPad }]}>
				{/* Live dictation readout. Deliberately not inside the field: the
				    partial transcript changes on every syllable, and rewriting the
				    input under the user's caret is hostile. */}
				{(voice.state === "starting" || voice.state === "recording" || voice.state === "transcribing") && (
					<View style={[styles.voiceStrip, voice.state === "starting" && styles.voiceStripWarmup]}>
						<Feather name="mic" size={13} color={voice.state === "starting" ? t.textTertiary : t.blue} />
						<Text style={styles.voiceText} numberOfLines={2}>
							{voice.partial ||
								(voice.state === "starting"
									? // The mic is not capturing yet. Saying "Listening" here would
										// invite speech that gets dropped during warm-up.
										"Keep holding..."
									: voice.state === "transcribing"
										? "Transcribing..."
										: voice.mode === "latched"
											? "Recording hands-free - tap the mic to finish"
											: "Speak now - release to insert")}
						</Text>
					</View>
				)}

				<KeyRow onKey={sendKey} />

				<Composer
					value={msg}
					onChangeText={setMsg}
					onSend={sendPrompt}
					sending={sending || interfaceTransitionActive}
					target={sendTarget}
					onTargetChange={setSendTarget}
					voice={voice}
					keyboardVisible={kbVisible}
					onDismissKeyboard={Keyboard.dismiss}
					targetLocked={shellOnly}
				/>
			</View>
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
	statusBar: {
		flexDirection: "row",
		alignItems: "center",
		paddingHorizontal: 14,
		paddingVertical: 6,
		borderBottomWidth: 1,
		borderBottomColor: t.borderSubtle,
	},
	statusDot: { width: 8, height: 8, borderRadius: 4, marginRight: 8 },
	statusText: { color: t.textSecondary, fontSize: 12, flex: 1 },
	dims: { color: t.textTertiary, fontSize: 11, fontFamily: t.fontMono },
	zoomGroup: {
		flexDirection: "row",
		alignItems: "center",
		marginLeft: 10,
		borderWidth: 1,
		borderColor: t.borderDefault,
		borderRadius: 7,
		backgroundColor: t.bgElevated,
		overflow: "hidden",
	},
	zoomBtn: { width: 28, height: 24, alignItems: "center", justifyContent: "center" },
	zoomDivider: { width: 1, height: 24, backgroundColor: t.borderDefault },
	banner: {
		backgroundColor: t.bgElevated,
		paddingHorizontal: 14,
		paddingVertical: 8,
		borderBottomWidth: 1,
		borderBottomColor: t.borderDefault,
	},
	bannerText: { color: t.attention, fontSize: 12 },
	interfaceFailureActions: {
		marginTop: 8,
		flexDirection: "row",
		alignItems: "center",
		justifyContent: "flex-end",
		gap: 16,
	},
	interfaceFailureActionText: { color: t.red, fontSize: 12, fontWeight: "700" },
	interfaceFailureDismissText: { color: t.textSecondary, fontSize: 12, fontWeight: "600" },
	termWrap: { flex: 1, backgroundColor: t.bgBase },
	dock: {
		borderTopWidth: 1,
		borderTopColor: t.borderSubtle,
		backgroundColor: t.bgSurface,
	},
	// Square-ish now that it holds a glyph and no label.
	killBtn: {
		alignItems: "center",
		justifyContent: "center",
		backgroundColor: t.tintRed,
		borderRadius: 12,
		paddingHorizontal: 8,
		paddingVertical: 5,
		marginLeft: 12,
	},
	// Bare glyph in the nav bar: iOS draws its own round box behind header buttons,
	// so a tinted pill here would fight it. Colour carries the state instead.
	// A fixed square with centred content, not padding — asymmetric padding leaves
	// the glyph visibly off-centre inside the circle iOS draws around it.
	headerBrowserBtn: {
		width: 35,
		height: 30,
		alignItems: "center",
		justifyContent: "center",
	},
	headerActions: { flexDirection: "row", alignItems: "center" },
	interfaceOverlay: {
		...StyleSheet.absoluteFill,
		alignItems: "center",
		justifyContent: "center",
		paddingHorizontal: 24,
		backgroundColor: t.scrim,
	},
	interfaceCard: {
		width: "100%",
		maxWidth: 340,
		alignItems: "center",
		gap: 10,
		paddingHorizontal: 22,
		paddingVertical: 24,
		borderRadius: 16,
		borderWidth: 1,
		borderColor: t.borderDefault,
		backgroundColor: t.bgSurface,
	},
	interfaceTitle: { color: t.textPrimary, fontSize: 16, fontWeight: "700" },
	interfaceCopy: { color: t.textSecondary, fontSize: 12, lineHeight: 18, textAlign: "center" },
	interfaceCancel: {
		marginTop: 4,
		borderRadius: 9,
		borderWidth: 1,
		borderColor: t.borderDefault,
		paddingHorizontal: 13,
		paddingVertical: 8,
	},
	interfaceCancelText: { color: t.textPrimary, fontSize: 12, fontWeight: "600" },
	interfaceError: { color: t.red, fontSize: 11, lineHeight: 16, textAlign: "center" },
	// Small green badge on the globe when a real preview is available. Offsets are
	// from the square's centre, so it rides the glyph rather than the button frame.
	browserReadyDot: {
		position: "absolute",
		top: 4,
		right: 3,
		width: 8,
		height: 8,
		borderRadius: 4,
		backgroundColor: t.green,
		borderWidth: 1,
		borderColor: t.bgSurface,
	},
	browserOverlay: { ...StyleSheet.absoluteFill, backgroundColor: t.bgBase },
	browserBar: {
		flexDirection: "row",
		alignItems: "center",
		gap: 10,
		paddingHorizontal: 12,
		paddingVertical: 8,
		backgroundColor: t.bgSurface,
		borderBottomWidth: 1,
		borderBottomColor: t.borderSubtle,
	},
	browserPath: { flex: 1, color: t.textSecondary, fontFamily: t.fontMono, fontSize: 12 },
	browserAction: { paddingHorizontal: 4, paddingVertical: 2 },
	browserWeb: { flex: 1, backgroundColor: "#ffffff" },
	restoreBtn: {
		flexDirection: "row",
		alignItems: "center",
		gap: 4,
		backgroundColor: t.tintBlue,
		borderRadius: 12,
		paddingHorizontal: 11,
		paddingVertical: 4,
		marginLeft: 12,
	},
	restoreText: { color: t.blue, fontWeight: "700", fontSize: 12 },
	deadOverlay: {
		...StyleSheet.absoluteFill,
		alignItems: "center",
		justifyContent: "center",
		padding: 32,
		gap: 10,
		backgroundColor: t.bgBase,
	},
	deadIcon: {
		width: 64,
		height: 64,
		borderRadius: 18,
		backgroundColor: t.bgElevated,
		borderWidth: 1,
		borderColor: t.borderSubtle,
		alignItems: "center",
		justifyContent: "center",
		marginBottom: 6,
	},
	deadTitle: { color: t.textPrimary, fontSize: 17, fontWeight: "700", textAlign: "center" },
	deadMsg: { color: t.textSecondary, fontSize: 13, lineHeight: 20, textAlign: "center", maxWidth: 300 },
	restoreCta: {
		flexDirection: "row",
		alignItems: "center",
		gap: 8,
		backgroundColor: t.blue,
		borderRadius: 10,
		paddingVertical: 12,
		paddingHorizontal: 20,
		marginTop: 10,
	},
	restoreCtaText: { color: t.onAccent, fontSize: 15, fontWeight: "700" },
	// Accent-blue like the rest of the chrome. Red is reserved for the mic button
	// alone: one small saturated element reads as "recording", a whole red panel
	// reads as an error. It sits at the top of the dock, so it divides itself from
	// the keys below rather than redrawing the dock's own top border.
	voiceStrip: {
		flexDirection: "row",
		alignItems: "center",
		gap: 8,
		paddingHorizontal: 12,
		paddingVertical: 7,
		backgroundColor: t.tintBlue,
		borderBottomWidth: 1,
		borderBottomColor: t.blue,
	},
	// Muted while the mic warms up, so "ready to speak" is a visible state change
	// and not just a wording difference.
	voiceStripWarmup: { backgroundColor: t.bgElevated, borderBottomColor: t.borderDefault },
	voiceText: { flex: 1, color: t.textPrimary, fontSize: 13 },
});
