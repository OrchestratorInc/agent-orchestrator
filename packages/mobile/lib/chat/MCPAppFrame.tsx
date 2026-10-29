import { useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Alert, Modal, Pressable, Text, View, useWindowDimensions } from "react-native";
import { WebView } from "react-native-webview";
import * as Linking from "expo-linking";
import * as Sharing from "expo-sharing";
import { File, Paths } from "expo-file-system";
import { createMCPAppHost, mcpAppPath, mcpAppStyleVariables, type MCPAppActionResponse, type MCPAppSnapshot } from "../../../product-ui/src/mcp-app";
import { authHeaders, httpBase } from "../config";
import { useApp } from "../store";
import { useTheme, useThemeState } from "../ThemeProvider";
import { renderTheme } from "./renderFrameModel";

// An MCP app normally posts to its parent frame. In a native WebView the
// parent is the view itself; only the main document gets this transport shim.
const BRIDGE = `(() => {
  window.postMessage = (message) => window.ReactNativeWebView.postMessage(JSON.stringify(message));
})(); true;`;

export function MCPAppFrame({ conversationId, activityId, completed }: { conversationId: string; activityId: string; completed: boolean }) {
	const { config } = useApp();
	const theme = useTheme();
	const { scheme } = useThemeState();
	const dimensions = useWindowDimensions();
	const [expanded, setExpanded] = useState(false);
	const [height, setHeight] = useState(360);
	const [attempt, setAttempt] = useState(0);
	const [snapshot, setSnapshot] = useState<MCPAppSnapshot>();
	const [failure, setFailure] = useState<string>();
	const [ready, setReady] = useState(false);
	const [closed, setClosed] = useState(false);
	const web = useRef<WebView>(null);
	const host = useRef<ReturnType<typeof createMCPAppHost> | undefined>(undefined);
	const current = useRef({ theme, scheme, dimensions, expanded });
	current.current = { theme, scheme, dimensions, expanded };
	const pendingConsent = useRef<((answer: boolean) => void) | undefined>(undefined);
	const path = mcpAppPath(conversationId, activityId);
	const baseUrl = config ? httpBase(config) : undefined;
	const uri = baseUrl ? `${baseUrl}${path}/document` : undefined;
	const source = useMemo(() => config && uri ? { uri, headers: authHeaders(config) } : undefined, [config, uri]);
	const snapshotRef = useRef(snapshot);
	snapshotRef.current = snapshot;
	useEffect(() => {
		if (!config || !baseUrl) return;
		const abort = new AbortController();
		setFailure(undefined);
		void fetch(`${baseUrl}${path}`, { headers: authHeaders(config), signal: abort.signal }).then(async response => {
			const value = await response.json();
			if (!response.ok) throw new Error(value.message ?? "Could not load app.");
			if (!abort.signal.aborted) { setSnapshot(value); setFailure(value.error); }
		}).catch(error => { if (!abort.signal.aborted) setFailure(error.message); });
		return () => abort.abort();
	}, [config, baseUrl, path, attempt, completed]);
	useEffect(() => {
		if (!config || !baseUrl || !snapshot?.ready || closed) return;
		const abort = new AbortController();
		setReady(false);
		const confirm = (title: string, detail: string) => new Promise<boolean>(resolve => {
			if (pendingConsent.current) { resolve(false); return; }
			pendingConsent.current = resolve;
			const finish = (approved: boolean) => { if (pendingConsent.current === resolve) pendingConsent.current = undefined; resolve(approved && !abort.signal.aborted); };
			Alert.alert(title, detail, [{ text: "Decline", style: "cancel", onPress: () => finish(false) }, { text: "Allow once", onPress: () => finish(true) }], { cancelable: true, onDismiss: () => finish(false) });
		});
		let timer: ReturnType<typeof setTimeout>;
		const bridge = createMCPAppHost({
			post: message => web.current?.injectJavaScript(`window.dispatchEvent(new MessageEvent('message', {data:${JSON.stringify(message)},source:window,origin:'null'})); true;`),
			context: () => {
				const state = current.current;
				return { theme: state.scheme === "light" ? "light" : "dark", styles: { variables: mcpAppStyleVariables(renderTheme(state.theme, state.scheme === "light" ? "light" : "dark").variables) }, displayMode: state.expanded ? "fullscreen" : "inline", availableDisplayModes: ["inline", "fullscreen"], platform: "mobile", containerDimensions: { width: state.dimensions.width - 32, ...(state.expanded ? { height: state.dimensions.height - 120 } : { maxHeight: 2000 }) }, locale: Intl.DateTimeFormat().resolvedOptions().locale, timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone };
			},
			confirm,
			action: async request => {
				const response = await fetch(`${baseUrl}${path}/actions`, { method: "POST", headers: { ...authHeaders(config), "Content-Type": "application/json" }, body: JSON.stringify(request), signal: abort.signal });
				const result = await response.json();
				if (!response.ok) throw new Error(result.message ?? "App action failed.");
				return result as MCPAppActionResponse;
			},
			openLink: async url => { await Linking.openURL(url); },
			download: async files => {
				for (const file of files) {
					if (file.uri) { await Linking.openURL(file.uri); continue; }
					if (!await Sharing.isAvailableAsync()) throw new Error("File sharing is unavailable on this device.");
					const local = new File(Paths.cache, `app-${Date.now()}-${file.name}`);
					try {
						local.write(file.blob === undefined ? file.text ?? "" : Uint8Array.from(atob(file.blob), c => c.charCodeAt(0)));
						await Sharing.shareAsync(local.uri, { mimeType: file.mimeType, dialogTitle: file.name });
					} finally { if (local.exists) local.delete(); }
				}
			},
			displayMode: async mode => {
				if (mode === "fullscreen" && !current.current.expanded && !await confirm("Expand app?", "This app wants to use the full screen.")) return "inline";
				setExpanded(mode === "fullscreen"); return mode;
			},
			height: next => { if (!current.current.expanded) setHeight(next); }, ready: () => { clearTimeout(timer); setReady(true); setFailure(undefined); }, close: () => { void bridge.teardown().then(() => setClosed(true)); },
		});
		host.current = bridge;
		if (snapshotRef.current) bridge.setCall(snapshotRef.current);
		timer = setTimeout(() => setFailure("This app has not connected. Retry or inspect the tool result."), 20_000);
		return () => { clearTimeout(timer); abort.abort(); pendingConsent.current?.(false); pendingConsent.current = undefined; void bridge.teardown(); host.current = undefined; };
	}, [config, baseUrl, path, snapshot?.ready, closed, attempt, expanded]);
	useEffect(() => { if (snapshot) host.current?.setCall(snapshot); }, [snapshot]);
	useEffect(() => host.current?.updateContext(), [theme, scheme, dimensions]);
	const retry = async () => { pendingConsent.current?.(false); await host.current?.teardown(); setFailure(undefined); setClosed(false); setAttempt(n => n + 1); };
	const body = <View style={{ backgroundColor: theme.bgBase, borderColor: theme.borderDefault, borderWidth: 1, borderRadius: 8, overflow: "hidden", ...(expanded ? { flex: 1, marginTop: 55, marginBottom: 24 } : {}) }}>
		<View style={{ flexDirection: "row", alignItems: "center", padding: 10, gap: 12 }}>
			<Text style={{ color: theme.textSecondary, flex: 1, fontSize: 12 }}>{snapshot?.server ?? "Interactive tool"}{snapshot && !snapshot.live ? " · Saved view" : ""}</Text>
			<Pressable accessibilityRole="button" accessibilityLabel="Reload app" onPress={retry}><Text style={{ color: theme.textPrimary }}>Retry</Text></Pressable>
			<Pressable accessibilityRole="button" accessibilityLabel={expanded ? "Collapse app" : "Expand app"} onPress={() => setExpanded(v => !v)}><Text style={{ color: theme.textPrimary }}>{expanded ? "Done" : "Expand"}</Text></Pressable>
		</View>
		{(failure || closed) && <Text accessibilityRole="alert" style={{ color: theme.textSecondary, padding: 12 }}>{closed ? "The app closed its view." : failure}</Text>}
		{!ready && !failure && !closed && <ActivityIndicator accessibilityLabel="Loading app" style={{ padding: 12 }} color={theme.textSecondary} />}
		{snapshot?.ready && source && !closed && <WebView key={attempt} ref={web} source={source} injectedJavaScriptBeforeContentLoaded={BRIDGE} injectedJavaScriptBeforeContentLoadedForMainFrameOnly
			onMessage={event => { try { host.current?.receive(JSON.parse(event.nativeEvent.data)); } catch { /* Ignore malformed page messages. */ } }}
			onShouldStartLoadWithRequest={request => request.isTopFrame === false || request.url.split("#")[0] === uri}
			onError={() => setFailure("The app document could not load.")} onHttpError={() => setFailure("The app document could not load.")}
			onContentProcessDidTerminate={() => setFailure("The app stopped. Tap Retry to reload.")} onRenderProcessGone={() => setFailure("The app stopped. Tap Retry to reload.")}
			setSupportMultipleWindows={false} javaScriptCanOpenWindowsAutomatically={false} allowsLinkPreview={false} allowFileAccess={false} allowUniversalAccessFromFileURLs={false}
			style={expanded ? { flex: 1 } : { height, backgroundColor: theme.bgBase }} />}
	</View>;
	return expanded ? <Modal visible animationType="slide" onRequestClose={() => setExpanded(false)}>{body}</Modal> : body;
}
