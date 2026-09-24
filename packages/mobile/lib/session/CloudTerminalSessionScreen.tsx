import { Feather } from "@expo/vector-icons";
import { XtermJsWebView, type XtermWebViewHandle } from "@fressh/react-native-xtermjs-webview";
import { useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Keyboard, Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { useKeyboardState } from "react-native-keyboard-controller";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useCloudAuth } from "../cloud/authStore";
import { createCloudTerminal, type CloudTerminalStatus } from "../cloud/terminal";
import { haptics } from "../haptics";
import { resetHeaderRightForSwap } from "../headerRightSwap";
import { terminalTheme, type Theme } from "../theme";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { Composer } from "./Composer";
import { dockInset, rootKeyboardPad } from "./keyboardInset";
import { KeyRow } from "./KeyRow";
import { terminalPayload } from "./sendRoute";
import { useVoiceInput } from "../voice/useVoiceInput";
import type { RouteSession } from "./sessionRoute";

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
      if (!size || size.cols < 1 || size.rows < 1) return;
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
	ready: "Connected to Cloud terminal",
	disconnected: "Reconnecting to Cloud terminal…",
	exited: "Agent terminal exited",
	error: "Cloud terminal unavailable",
};

export function CloudTerminalSessionScreen({ session }: { session: RouteSession }) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const { client, orgId } = useCloudAuth();
	const router = useRouter();
	const navigation = useNavigation();
	const insets = useSafeAreaInsets();
	const keyboardHeight = useKeyboardState((state) => state.height);
	const keyboardVisible = useKeyboardState((state) => state.isVisible);
	const xtermRef = useRef<XtermWebViewHandle | null>(null);
	const terminalRef = useRef<ReturnType<typeof createCloudTerminal> | null>(null);
	const xtermReady = useRef(false);
	const pendingOutput = useRef<Uint8Array[]>([]);
	const lastSize = useRef<{ cols: number; rows: number } | null>(null);
	const [status, setStatus] = useState<CloudTerminalStatus>("connecting");
	const [headerRightReady, setHeaderRightReady] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [draft, setDraft] = useState("");
	const [sending, setSending] = useState(false);
	const [size, setSize] = useState<{ cols: number; rows: number } | null>(null);
	const sessionTitle = "displayName" in session ? session.displayName : "Orchestrator";
	const voice = useVoiceInput({
		onTranscript: useCallback((text: string) => {
			setDraft((current) => current ? `${current} ${text}` : text);
		}, []),
	});

	const openChat = useCallback(() => {
		router.replace({ pathname: "/session/[id]", params: { id: session.id } });
	}, [router, session.id]);

	useLayoutEffect(() => resetHeaderRightForSwap(
		() => navigation.setOptions({ headerRight: undefined }),
		() => setHeaderRightReady(true),
	), [navigation]);

	useLayoutEffect(() => {
		if (!headerRightReady) return;
		navigation.setOptions({
			title: sessionTitle || "Cloud terminal",
			headerRight: () => <Pressable accessibilityRole="button" accessibilityLabel="Open Chat UI" onPress={openChat} style={styles.chatButton}>
				<Feather name="message-square" size={16} color={t.accent} />
				<Text style={styles.chatButtonText}>Chat</Text>
			</Pressable>,
		});
	}, [headerRightReady, navigation, openChat, sessionTitle, styles, t.accent]);

	useEffect(() => {
		xtermReady.current = false;
		pendingOutput.current = [];
		setStatus("connecting");
		setError(null);
		if (!orgId) {
			setStatus("error");
			setError("Your Cloud workspace is not ready. Sign in again and retry.");
			return;
		}
		const terminal = createCloudTerminal({
			client, orgId, sessionId: session.id,
			onStatus: setStatus,
			onError: setError,
			onOutput: (bytes) => {
				if (!xtermReady.current || !xtermRef.current) {
					pendingOutput.current.push(bytes);
					return;
				}
				xtermRef.current.write(bytes);
			},
		});
		terminalRef.current = terminal;
		if (lastSize.current) terminal.resize(lastSize.current.cols, lastSize.current.rows);
		void terminal.connect();
		return () => {
			terminal.disconnect();
			terminalRef.current = null;
			xtermReady.current = false;
			pendingOutput.current = [];
		};
	}, [client, orgId, scheme, session.id]);

	const onInitialized = useCallback(() => {
		xtermReady.current = true;
		for (const bytes of pendingOutput.current) xtermRef.current?.write(bytes);
		pendingOutput.current = [];
		if (lastSize.current) xtermRef.current?.resize(lastSize.current);
	}, []);

	const applySize = useCallback((cols: number, rows: number) => {
		lastSize.current = { cols, rows };
		setSize({ cols, rows });
		terminalRef.current?.resize(cols, rows);
		xtermRef.current?.resize({ cols, rows });
	}, []);
	const logger = useMemo(() => ({ log: (...args: unknown[]) => {
		const message = args.at(-1);
		if (typeof message !== "string") return;
		if (message === "AO_TERMINAL_TAP") {
			Keyboard.dismiss();
			return;
		}
		const match = /^FRESSH_DIMS (\d+) (\d+)$/.exec(message);
		if (match) applySize(Number(match[1]), Number(match[2]));
	} }), [applySize]);
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
		onRenderProcessGone: () => setError("Terminal renderer crashed. Reopen the worker to reconnect."),
	}), []);

	const sendPrompt = useCallback(async () => {
		const text = draft.trim();
		if (!text || sending) return;
		setSending(true);
		try {
			const line = terminalPayload(text);
			if (!await terminalRef.current?.sendPrompt(line.slice(0, -1))) {
				setError("Terminal is not ready yet. Your text may not have been submitted.");
				haptics.error();
				return;
			}
			setDraft("");
			setError(null);
			haptics.success();
		} finally {
			setSending(false);
		}
	}, [draft, sending]);

	const rootPad = rootKeyboardPad(Platform.OS === "android" ? "android" : "ios", keyboardHeight, insets.bottom);
	const bottomPad = dockInset(keyboardHeight, insets.bottom, keyboardVisible);
	return <View style={[styles.screen, rootPad > 0 && { paddingBottom: rootPad }]}>
		<View style={styles.statusBar}>
			<View style={[styles.dot, { backgroundColor: status === "ready" ? t.green : status === "error" || status === "exited" ? t.red : t.amber }]} />
			<Text style={styles.statusText}>{labels[status]}</Text>
			{status === "connecting" || status === "waiting" || status === "disconnected" ? <ActivityIndicator size="small" color={t.amber} /> : null}
			{size ? <Text style={styles.dimensions}>{size.cols}×{size.rows}</Text> : null}
		</View>
		{error || voice.error ? <Text style={styles.error} selectable>{error || voice.error}</Text> : null}
		<View style={styles.terminal}>
			<XtermJsWebView
				key={`cloud-terminal-${scheme}`}
				ref={xtermRef}
				autoFit={false}
				xtermOptions={xtermOptions}
				webViewOptions={webViewOptions}
				logger={logger}
				onInitialized={onInitialized}
				onData={(data) => { if (!terminalRef.current?.sendInput(data)) setError("Terminal is not ready yet."); }}
				style={styles.xterm}
			/>
		</View>
		<View style={[styles.dock, { paddingBottom: bottomPad }]}>
			<KeyRow onKey={(key) => { if (!terminalRef.current?.sendInput(key)) setError("Terminal is not ready yet."); }} />
			<Composer
				value={draft}
				onChangeText={setDraft}
				onSend={sendPrompt}
				sending={sending}
				target="terminal"
				onTargetChange={() => {}}
				voice={voice}
				keyboardVisible={keyboardVisible}
				onDismissKeyboard={Keyboard.dismiss}
				targetLocked
			/>
		</View>
	</View>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	chatButton: { flexDirection: "row", alignItems: "center", gap: 6, paddingHorizontal: 10, paddingVertical: 6 },
	chatButtonText: { color: t.accent, fontSize: 14, fontWeight: "600" },
	statusBar: { flexDirection: "row", alignItems: "center", paddingHorizontal: 14, paddingVertical: 7, borderBottomWidth: 1, borderBottomColor: t.borderSubtle },
	dot: { width: 8, height: 8, borderRadius: 4, marginRight: 8 },
	statusText: { color: t.textSecondary, fontSize: 12, flex: 1 },
	dimensions: { color: t.textTertiary, fontSize: 11, fontFamily: t.fontMono },
	error: { color: t.red, paddingHorizontal: 14, paddingVertical: 9, fontSize: 12 },
	terminal: { flex: 1 },
	xterm: { flex: 1, backgroundColor: t.bgBase },
	dock: { borderTopWidth: 1, borderTopColor: t.borderSubtle, backgroundColor: t.bgSurface },
});
