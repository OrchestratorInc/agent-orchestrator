import { Feather } from "@expo/vector-icons";
import { XtermJsWebView, type XtermWebViewHandle } from "@fressh/react-native-xtermjs-webview";
import { useFocusEffect, useNavigation, useRouter } from "expo-router";
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
import { terminalEnhanceScript } from "./terminalEnhanceScript";
import { adjustTerminalViewport } from "./terminalViewport";
import { useVoiceInput } from "../voice/useVoiceInput";
import type { RouteSession } from "./sessionRoute";

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
	const authoritativeSize = useRef<{ cols: number; rows: number } | null>(null);
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
				<Feather name="message-square" size={16} color={t.blue} />
				<Text style={styles.chatButtonText}>Chat</Text>
			</Pressable>,
		});
	}, [headerRightReady, navigation, openChat, sessionTitle, styles, t.blue]);

	useEffect(() => {
		xtermReady.current = false;
		pendingOutput.current = [];
		authoritativeSize.current = null;
		setSize(null);
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
			onReset: () => {
				authoritativeSize.current = null;
				pendingOutput.current = [];
				setSize(null);
			},
			onSize: (columns, rows) => {
				const grid = { cols: columns, rows };
				authoritativeSize.current = grid;
				setSize(grid);
				if (!xtermReady.current || !xtermRef.current) return;
				xtermRef.current.resize(grid);
				for (const bytes of pendingOutput.current) xtermRef.current.write(bytes);
				pendingOutput.current = [];
			},
			onOutput: (bytes) => {
				if (!xtermReady.current || !xtermRef.current || !authoritativeSize.current) {
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

	useFocusEffect(useCallback(() => {
		terminalRef.current?.setVisible(true);
		return () => terminalRef.current?.setVisible(false);
	}, []));

	const onInitialized = useCallback(() => {
		xtermReady.current = true;
		if (!authoritativeSize.current) return;
		xtermRef.current?.resize(authoritativeSize.current);
		for (const bytes of pendingOutput.current) xtermRef.current?.write(bytes);
		pendingOutput.current = [];
	}, []);

	const applySize = useCallback((cols: number, rows: number) => {
		lastSize.current = { cols, rows };
		terminalRef.current?.resize(cols, rows);
	}, []);
	const zoom = useCallback((direction: -1 | 1) => {
		adjustTerminalViewport(xtermRef.current, direction);
	}, []);
	const logger = useMemo(() => ({ log: (...args: unknown[]) => {
		const message = args.at(-1);
		if (typeof message !== "string") return;
		const match = /^FRESSH_DIMS (\d+) (\d+)$/.exec(message);
		if (match) applySize(Number(match[1]), Number(match[2]));
	} }), [applySize]);
	const xtermOptions = useMemo(() => ({
		fontSize: 12,
		cursorBlink: true,
		scrollback: 5000,
		theme: terminalTheme(scheme),
		minimumContrastRatio: 4.5,
	}), [scheme]);
	const webViewOptions = useMemo(() => ({
		hideKeyboardAccessoryView: true,
		injectedJavaScript: terminalEnhanceScript(Platform.OS === "android" ? "android" : "ios"),
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
			<View style={styles.zoomGroup}>
				<Pressable hitSlop={6} accessibilityLabel="Smaller text" onPress={() => { haptics.tap(); zoom(-1); }} style={styles.zoomButton}>
					<Feather name="minus" size={13} color={t.textSecondary} />
				</Pressable>
				<View style={styles.zoomDivider} />
				<Pressable hitSlop={6} accessibilityLabel="Larger text" onPress={() => { haptics.tap(); zoom(1); }} style={styles.zoomButton}>
					<Feather name="plus" size={13} color={t.textSecondary} />
				</Pressable>
			</View>
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
	chatButtonText: { color: t.blue, fontSize: 14, fontWeight: "600" },
	statusBar: { flexDirection: "row", alignItems: "center", paddingHorizontal: 14, paddingVertical: 7, borderBottomWidth: 1, borderBottomColor: t.borderSubtle },
	dot: { width: 8, height: 8, borderRadius: 4, marginRight: 8 },
	statusText: { color: t.textSecondary, fontSize: 12, flex: 1 },
	dimensions: { color: t.textTertiary, fontSize: 11, fontFamily: t.fontMono },
	zoomGroup: { flexDirection: "row", alignItems: "center", marginLeft: 10, borderWidth: 1, borderColor: t.borderDefault, borderRadius: 7, backgroundColor: t.bgElevated, overflow: "hidden" },
	zoomButton: { width: 28, height: 24, alignItems: "center", justifyContent: "center" },
	zoomDivider: { width: 1, height: 24, backgroundColor: t.borderDefault },
	error: { color: t.red, paddingHorizontal: 14, paddingVertical: 9, fontSize: 12 },
	terminal: { flex: 1 },
	xterm: { flex: 1, backgroundColor: t.bgBase },
	dock: { borderTopWidth: 1, borderTopColor: t.borderSubtle, backgroundColor: t.bgSurface },
});
