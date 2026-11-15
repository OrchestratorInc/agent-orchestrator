import { XtermJsWebView, type XtermWebViewHandle } from "@fressh/react-native-xtermjs-webview";
import { Stack } from "expo-router";
import { type Ref, useCallback, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Keyboard, LayoutAnimation, Platform, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { haptics } from "../haptics";
import { Composer } from "../session/Composer";
import { dockInset, rootKeyboardPad } from "../session/keyboardInset";
import { KeyRow } from "../session/KeyRow";
import { RouteToggle } from "../session/RouteToggle";
import { ScrollTopButton } from "../session/ScrollTopButton";
import { TERMINAL_UNAVAILABLE_NOTICE, terminalPayload, type SendTarget } from "../session/sendRoute";
import { terminalMenuSections, type TerminalMenuAction } from "../session/terminalMenuModel";
import { TerminalNotice, TerminalNoticeHost, type TerminalNoticeMessage, type TerminalNoticeTone } from "../session/TerminalNotice";
import { adjustTerminalViewport, fitTerminalViewport, scrollTerminalToTop, scrollTopReport } from "../session/terminalViewport";
import { Spinner } from "../Spinner";
import type { PlatformSymbol } from "../symbols";
import { terminalTheme, type Theme } from "../theme";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { font, fontScaleCap, iconSize, radius, space, touchTarget, type } from "../tokens";
import { toolbarIcon } from "../toolbarIcon";
import { Button, EmptyState } from "../ui";
import { useVoiceInput } from "../voice/useVoiceInput";
import type { CloudClient } from "./client";
import { connectCloudTerminal, type CloudTerminalConnection, type CloudTerminalStatus } from "./terminal";
import { cloudTerminalDisplay, READ_ONLY_COPY } from "./terminalCopy";
import { TERMINAL_ENHANCE_JS } from "../session/terminalEnhance";
import type { CloudTerminalKind } from "./types";

// The local terminal's size and glyphs (lib/session/TerminalSessionScreen).
const FONT_SIZE = 12;
/** A terminal with nothing running behind it: power, switched off. */
const TERMINATED_GLYPH: PlatformSymbol = { ios: "power", android: "power_settings_new" };
const READ_ONLY_GLYPH: PlatformSymbol = { ios: "lock", android: "lock" };

// The server replays the whole screen from sequence 0 after `reset`. A full
// reset (RIS), not only a clear: it also leaves the alternate screen and drops
// the mouse modes a TUI negotiated, which the replay negotiates again.
const RESET_BYTES = new Uint8Array([0x1b, 0x63]);

/** How long after one refused keystroke the next ones stay quiet. */
const REFUSAL_QUIET_MS = 4_000;

/** What the screen that embeds the terminal can ask of it, for its own header menu. */
export type CloudTerminalHandle = {
	/** The phone's view only: the grid and the PTY keep their size. */
	zoom(direction: -1 | 1): void;
	fitWidth(): void;
	/** A fresh ticket and a full replay. */
	reconnect(): void;
};

export type CloudTerminalViewProps = {
	client: CloudClient;
	orgId: string;
	sessionId: string;
	kind: CloudTerminalKind;
	/** A new worker epoch (resume, restore) means a new agent terminal: it reconnects fresh. */
	workerEpoch?: number;
	/**
	 * Shows the composer's Chat/Terminal switch, on (Terminal); turning it off
	 * calls this. For the agent terminal a session screen draws in place of its chat.
	 */
	onShowChat?: () => void;
	/**
	 * Puts the terminal's "⋯" (Smaller, Larger, Fit width) in the screen's
	 * header, as the local terminal does. For a page the terminal owns; an
	 * embedding screen keeps its own header and can reach the same actions
	 * through `ref`.
	 */
	headerMenu?: boolean;
	ref?: Ref<CloudTerminalHandle>;
};

/**
 * A cloud session's terminal: the agent's own TUI (`agent`) or a shell in the
 * session's sandbox (`workspace`), drawn as the local terminal is
 * (lib/session/TerminalSessionScreen): the same xterm WebView and palette, the
 * key row and the chat's composer in one dock, notices as the platform shows
 * them, the connection as a floating pill and a scroll-to-top control over the
 * grid. The phone sizes the grid to fit itself; the server replays the screen
 * from the start on every (re)connect.
 *
 * It reserves the keyboard itself, so it expects to reach the bottom of the
 * screen. It does not inset itself under a clear glass bar: the page does.
 */
export function CloudTerminalView({ client, orgId, sessionId, kind, workerEpoch, onShowChat, headerMenu = false, ref }: CloudTerminalViewProps) {
	const t = useTheme();
	const { scheme, style } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();

	const xtermRef = useRef<XtermWebViewHandle | null>(null);
	// A theme change or a reload replaces xterm. Bytes arriving between the old
	// WebView's unmount and the new one's onInitialized wait here, in order.
	const xtermReadyRef = useRef(false);
	const pendingOutputRef = useRef<Uint8Array[]>([]);
	const initializedOnceRef = useRef(false);
	const connectionRef = useRef<CloudTerminalConnection | null>(null);
	// The phone's natural fit, as the WebView last reported it: applied to xterm
	// and sent to the PTY, here the phone owns the grid.
	const dimsRef = useRef<{ cols: number; rows: number } | null>(null);

	const [status, setStatus] = useState<CloudTerminalStatus>("connecting");
	const [statusNote, setStatusNote] = useState<string | undefined>();
	const [canOperate, setCanOperate] = useState(true);
	const display = cloudTerminalDisplay(status, statusNote, kind);
	const live = display.state === "live";
	const covered = display.state === "ended" || display.state === "failed";

	// One notice at a time, the newest winning: an inline row on iOS, a snackbar
	// on Android (see TerminalNotice).
	const [notice, setNotice] = useState<TerminalNoticeMessage | null>(null);
	const noticeId = useRef(0);
	const showNotice = useCallback((text: string, tone: TerminalNoticeTone = "error") => {
		noticeId.current += 1;
		setNotice({ id: noticeId.current, text, tone });
	}, []);
	const clearNotice = useCallback(() => setNotice(null), []);
	// When the server last refused a keystroke: said once, not once per key.
	const refusedAtRef = useRef(0);

	const [dockHeight, setDockHeight] = useState(0);
	const [kbHeight, setKbHeight] = useState(0);
	const [kbVisible, setKbVisible] = useState(false);
	const [draft, setDraft] = useState("");

	// The local terminal's keyboard handling, unchanged: neither platform shrinks
	// the layout for the keyboard, so the root pads itself above it and the dock
	// adds nothing on top (see keyboardInset). It stays on the platform
	// listeners for the same reasons TerminalSessionScreen gives: didShow
	// corrects a willShow height that still counts the hidden accessory bar, and
	// iOS alone gets the didHide backup.
	useEffect(() => {
		const isIOS = Platform.OS === "ios";
		const showEvt = isIOS ? "keyboardWillShow" : "keyboardDidShow";
		const hideEvt = isIOS ? "keyboardWillHide" : "keyboardDidHide";
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

	const writeOutput = useCallback((bytes: Uint8Array) => {
		if (!xtermReadyRef.current || !xtermRef.current) {
			pendingOutputRef.current.push(bytes);
			return;
		}
		xtermRef.current.write(bytes);
	}, []);

	useEffect(() => {
		// Events from a connection this effect has closed (on the way out, or for
		// the next epoch's) must not reach the screen.
		let current = true;
		const connection = connectCloudTerminal({
			client,
			orgId,
			sessionId,
			kind,
			handlers: {
				onStatus: (next, note) => {
					if (!current) return;
					setStatus(next);
					setStatusNote(note);
				},
				onOutput: (bytes) => {
					if (current) writeOutput(bytes);
				},
				onReset: () => {
					if (!current) return;
					// Anything still queued is superseded by the replay.
					pendingOutputRef.current = [];
					writeOutput(RESET_BYTES);
				},
				// Someone else holds a shared session's input.
				onRefused: (note) => {
					if (!current) return;
					const now = Date.now();
					if (now - refusedAtRef.current < REFUSAL_QUIET_MS) return;
					refusedAtRef.current = now;
					showNotice(note);
				},
				onAccess: (allowed) => {
					if (current) setCanOperate(allowed);
				},
			},
		});
		connectionRef.current = connection;
		if (dimsRef.current) connection.resize(dimsRef.current.cols, dimsRef.current.rows);
		return () => {
			current = false;
			connection.close();
			if (connectionRef.current === connection) connectionRef.current = null;
		};
	}, [client, kind, orgId, sessionId, workerEpoch, showNotice, writeOutput]);

	// Whether there is older output to jump to, as the WebView last reported it.
	const [canScrollTop, setCanScrollTop] = useState(false);
	// Android killed the WebView's renderer: the grid is black for good until
	// the view is rebuilt.
	const [viewGone, setViewGone] = useState(false);
	// Bumped to rebuild the WebView after its renderer died.
	const [mount, setMount] = useState(0);

	useLayoutEffect(() => {
		xtermReadyRef.current = false;
		// The replacement WebView reports its own answer once it is up, and
		// starts with a renderer of its own.
		setCanScrollTop(false);
		setViewGone(false);
	}, [scheme, style, mount]);

	// fressh routes WebView {type:'debug'} messages to logger.log(prefix, message).
	// The enhance script reports the phone's natural fit (FRESSH_DIMS) and the
	// scroll-to-top button's visibility (AO_SCROLLTOP) through it.
	const logger = useMemo(
		() => ({
			log: (...args: unknown[]) => {
				const m = args[args.length - 1];
				if (typeof m === "string" && m.startsWith("FRESSH_DIMS ")) {
					const [, cols, rows] = m.split(" ").map((part) => parseInt(part, 10));
					if (cols > 0 && rows > 0) {
						dimsRef.current = { cols, rows };
						xtermRef.current?.resize({ cols, rows });
						connectionRef.current?.resize(cols, rows);
					}
					return;
				}
				const scrollTop = scrollTopReport(m);
				if (scrollTop !== undefined) setCanScrollTop(scrollTop);
			},
		}),
		[],
	);

	const onInitialized = useCallback(() => {
		xtermReadyRef.current = true;
		const queued = pendingOutputRef.current;
		pendingOutputRef.current = [];
		for (const bytes of queued) xtermRef.current?.write(bytes);
		if (dimsRef.current) xtermRef.current?.resize(dimsRef.current);
		// A replacement WebView (theme change, reload) starts blank. The server
		// replays the whole screen on a fresh connection, so ask for one rather
		// than leaving the grid empty until the next output.
		if (initializedOnceRef.current) connectionRef.current?.reconnect();
		initializedOnceRef.current = true;
	}, []);

	const send = useCallback(
		(data: string) => {
			if (canOperate) connectionRef.current?.send(data);
		},
		[canOperate],
	);

	// The composer's text goes to the PTY as one submitted line (terminalPayload),
	// as the local terminal's terminal route does.
	const sendDraft = useCallback(() => {
		const text = draft.trim();
		if (!text) return;
		if (!connectionRef.current || status !== "open") {
			haptics.error();
			showNotice(TERMINAL_UNAVAILABLE_NOTICE);
			return;
		}
		connectionRef.current.send(terminalPayload(text));
		haptics.success();
		setDraft("");
	}, [draft, showNotice, status]);

	// Dictation on the phone, landing in the composer as an editable draft.
	const voice = useVoiceInput({
		onTranscript: useCallback((text: string) => {
			setDraft((m) => (m ? `${m} ${text}` : text));
			haptics.success();
		}, []),
	});
	useEffect(() => {
		if (voice.error) showNotice(voice.error);
	}, [voice.error, showNotice]);

	// The composer's switch is on while this is the terminal; turning it off
	// asks the embedding screen for its chat.
	const onTargetChange = useCallback(
		(target: SendTarget) => {
			if (target === "agent") onShowChat?.();
		},
		[onShowChat],
	);

	const reconnect = useCallback(() => {
		clearNotice();
		connectionRef.current?.reconnect();
	}, [clearNotice]);
	const reloadView = useCallback(() => setMount((n) => n + 1), []);

	// Only the phone's CSS viewport changes; the grid and the PTY keep their size.
	const zoom = useCallback((direction: -1 | 1) => {
		adjustTerminalViewport(xtermRef.current, direction);
	}, []);
	const fitWidth = useCallback(() => {
		fitTerminalViewport(xtermRef.current);
	}, []);
	const scrollToTop = useCallback(() => {
		scrollTerminalToTop(xtermRef.current);
	}, []);

	useImperativeHandle(ref, () => ({ zoom, fitWidth, reconnect }), [fitWidth, reconnect, zoom]);

	// The local terminal's "⋯" with what applies to a cloud PTY: the view's
	// Smaller / Larger / Fit width. Preview, files and archive need a paired
	// machine, and the switch to Chat is the embedding screen's.
	const menuActions: Partial<Record<TerminalMenuAction, () => void>> = {
		zoom_out: () => zoom(-1),
		zoom_in: () => zoom(1),
		fit_width: fitWidth,
	};
	const menuActionsRef = useRef(menuActions);
	menuActionsRef.current = menuActions;
	const canZoom = !covered && !viewGone;
	const menuSections = useMemo(
		() =>
			headerMenu
				? terminalMenuSections({ agentSession: false, chatReady: false, checkingChat: false, switchingToChat: false, artifactCount: 0, canZoom })
				: [],
		[canZoom, headerMenu],
	);
	const headerItems = useMemo(() => {
		if (!menuSections.length) return null;
		const ios = Platform.OS === "ios";
		const sections = menuSections.map((section) => ({
			...section,
			actions: section.items.map((item) => (
				<Stack.Toolbar.MenuAction
					key={item.action}
					icon={ios ? item.icon : undefined}
					subtitle={item.subtitle}
					destructive={item.destructive}
					disabled={item.disabled}
					onPress={() => menuActionsRef.current[item.action]?.()}
				>
					{item.label}
				</Stack.Toolbar.MenuAction>
			)),
		}));
		// As the local terminal lays it out: on iOS the view's three steps share
		// one row as a palette of glyphs inside a plain inline section; an M3 menu
		// has no such row, so Android lists them.
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
					{ios ? <Stack.Toolbar.Label>Terminal actions</Stack.Toolbar.Label> : null}
					{menuItems}
				</Stack.Toolbar.Menu>
			</Stack.Toolbar>
		);
	}, [menuSections]);

	const xtermOptions = useMemo(
		() => ({
			fontSize: FONT_SIZE,
			cursorBlink: true,
			scrollback: 5000,
			scrollSensitivity: 3,
			fastScrollSensitivity: 8,
			// The terminal's own palette, not the product one; xterm nudges any
			// pair a TUI picks below this ratio until it is readable.
			theme: terminalTheme(scheme, style),
			minimumContrastRatio: 4.5,
		}),
		[scheme, style],
	);

	const webViewOptions = useMemo(
		() => ({
			hideKeyboardAccessoryView: true,
			injectedJavaScript: `var IS_ANDROID=${Platform.OS === "android"};\n${TERMINAL_ENHANCE_JS}`,
			// Not androidLayerType "hardware": a hardware layer blanks xterm's canvas
			// on many Android devices (see TerminalSessionScreen).
			nestedScrollEnabled: true,
			onRenderProcessGone: () => setViewGone(true),
		}),
		[],
	);

	const bottomPad = dockInset(kbHeight, insets.bottom);
	const rootPad = rootKeyboardPad(Platform.OS === "android" ? "android" : "ios", kbHeight, insets.bottom);

	return (
		<View style={[styles.screen, rootPad > 0 && { paddingBottom: rootPad }]}>
			{headerItems}
			<TerminalNotice notice={notice} onDismiss={clearNotice} />

			<View style={styles.termWrap}>
				<XtermJsWebView
					// Remount on theme changes (xterm applies its palette at
					// construction) and on a reload after the renderer died.
					key={`cloud-term-${style}-${scheme}-${mount}`}
					ref={xtermRef}
					autoFit={false}
					xtermOptions={xtermOptions}
					webViewOptions={webViewOptions}
					logger={logger}
					onInitialized={onInitialized}
					// xterm's own replies (device attributes, mouse reports from the
					// scroll gestures) belong to the PTY too.
					onData={send}
					style={{ flex: 1, backgroundColor: t.bgBase }}
				/>
				{/* The connection says something only while it is not live, floating
				    over the grid so the grid keeps its rows. */}
				{display.state === "pending" && !viewGone ? (
					<View pointerEvents="none" style={styles.statusSlot}>
						<View style={styles.statusPill} accessibilityLiveRegion="polite">
							<Spinner size={iconSize.sm} />
							<Text style={styles.statusText} numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome}>
								{display.label}
							</Text>
						</View>
					</View>
				) : null}
				{canScrollTop && live && !viewGone ? <ScrollTopButton onPress={scrollToTop} /> : null}
				{viewGone ? (
					<View style={styles.coverOverlay} accessibilityRole="alert">
						<EmptyState
							icon="warning"
							iconColor={t.red}
							title="The terminal view stopped working"
							message="Reload it to reconnect and replay the screen."
							action={<Button title="Reload" icon="refresh" onPress={reloadView} />}
						/>
					</View>
				) : display.state === "ended" ? (
					<View style={styles.coverOverlay}>
						<EmptyState
							icon={TERMINATED_GLYPH}
							title={display.title}
							message={display.message}
							action={display.action ? <Button title={display.action} icon="refresh" onPress={reconnect} /> : undefined}
						/>
					</View>
				) : display.state === "failed" ? (
					<View style={styles.coverOverlay} accessibilityRole="alert">
						<EmptyState
							icon="offline"
							iconColor={t.red}
							title={display.title}
							message={display.message}
							action={<Button title={display.action} icon="refresh" onPress={reconnect} />}
						/>
					</View>
				) : null}
			</View>

			{/* The input dock: one container, one bottom inset. */}
			<View style={[styles.dock, { paddingBottom: bottomPad }]} onLayout={(e) => setDockHeight(Math.round(e.nativeEvent.layout.height))}>
				{canOperate ? (
					<>
						<KeyRow onKey={send} />
						<Composer
							value={draft}
							onChangeText={setDraft}
							onSend={sendDraft}
							sending={false}
							target="terminal"
							onTargetChange={onTargetChange}
							targetLocked={!onShowChat}
							voice={voice}
							keyboardVisible={kbVisible}
							onDismissKeyboard={Keyboard.dismiss}
						/>
					</>
				) : (
					// Watching only: no keys and no field, but the way back to the chat stays.
					<View style={styles.readOnly}>
						<AppSymbol name={READ_ONLY_GLYPH} size={iconSize.md} color={t.textSecondary} />
						<Text style={styles.readOnlyText} maxFontSizeMultiplier={fontScaleCap.body}>
							{READ_ONLY_COPY}
						</Text>
						{onShowChat ? (
							<RouteToggle
								on
								onChange={(on) => {
									if (on) return;
									haptics.select();
									onShowChat();
								}}
							/>
						) : null}
					</View>
				)}
			</View>
			{/* Absolutely placed, so it comes last and draws over the dock's
			    neighbours; the offset clears the keyboard pad and the dock. */}
			<TerminalNoticeHost notice={notice} onDismiss={clearNotice} bottomOffset={rootPad + dockHeight + space.sm} />
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		screen: { flex: 1, backgroundColor: t.bgBase },
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
		// iOS draws the grid's first column against the screen's rounded edge; a
		// few points in keeps every cell whole.
		termWrap: { flex: 1, backgroundColor: t.bgBase, paddingHorizontal: Platform.OS === "ios" ? space.xs : 0 },
		dock: {
			borderTopWidth: rowDividerWidth,
			borderTopColor: t.borderSubtle,
			backgroundColor: t.bgBase,
		},
		readOnly: { flexDirection: "row", alignItems: "center", gap: space.sm, minHeight: touchTarget, paddingLeft: space.lg, paddingRight: space.sm, paddingTop: space.xs },
		readOnlyText: { flex: 1, paddingVertical: space.sm, fontFamily: font.regular, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		coverOverlay: { ...StyleSheet.absoluteFill, backgroundColor: t.bgBase },
	});
