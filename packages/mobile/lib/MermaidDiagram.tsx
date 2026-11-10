import { useEffect, useMemo, useState, type ReactNode } from "react";
import { ActivityIndicator, Linking, StyleSheet, View } from "react-native";
import { WebView, type WebViewMessageEvent } from "react-native-webview";
import type { ShouldStartLoadRequest } from "react-native-webview/lib/WebViewTypes";
import { isRenderableDiagram } from "../../shared/files/file-formats";
import { mermaidPageHtml, parseDiagramMessage } from "./mermaidPage";
import { loadMermaidRuntime } from "./mermaidRuntime";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles, useThemeState } from "./ThemeProvider";

/** Loads in the page's own document; anything else is a link, which opens in the browser. */
function stayInPage(request: ShouldStartLoadRequest): boolean {
	if (/^(about|data):/i.test(request.url)) return true;
	if (/^https?:/i.test(request.url)) void Linking.openURL(request.url).catch(() => {});
	return false;
}

/**
 * A Mermaid diagram file drawn as its diagram, as the desktop draws it. The
 * engine runs in its own WebView (see mermaidPage), themed with the app.
 * Anything it cannot draw — a syntax error, an oversized or empty file, an
 * engine that will not load — shows `fallback` (the source) instead, never a
 * blank box or an error box that reads as content.
 */
export function MermaidDiagram({ code, fallback }: { code: string; fallback: ReactNode }) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const renderable = isRenderableDiagram(code);
	const [runtime, setRuntime] = useState<string>();
	const [failed, setFailed] = useState(false);
	const [rendered, setRendered] = useState(false);
	useEffect(() => {
		if (!renderable) return;
		let live = true;
		loadMermaidRuntime().then((source) => { if (live) setRuntime(source); }, () => { if (live) setFailed(true); });
		return () => { live = false; };
	}, [renderable]);
	const colors = useMemo(() => ({ scheme: scheme === "light" ? "light" as const : "dark" as const, background: t.bgBase }), [scheme, t.bgBase]);
	const html = useMemo(() => runtime ? mermaidPageHtml({ runtime, code, colors }) : undefined, [code, colors, runtime]);
	// A new source or theme draws again.
	useEffect(() => {
		setRendered(false);
		setFailed(false);
	}, [html]);
	if (!renderable || failed) return <>{fallback}</>;
	const onMessage = (event: WebViewMessageEvent) => {
		const message = parseDiagramMessage(event.nativeEvent.data);
		if (message?.type === "rendered") setRendered(true);
		else if (message?.type === "failed") setFailed(true);
	};
	return (
		<View style={styles.screen}>
			{html ? (
				<WebView
					source={{ html }}
					originWhitelist={["*"]}
					onShouldStartLoadWithRequest={stayInPage}
					onMessage={onMessage}
					onError={() => setFailed(true)}
					// Under the clear glass bar, the diagram scrolls on beneath it.
					contentInsetAdjustmentBehavior="automatic"
					nestedScrollEnabled
					javaScriptEnabled
					allowFileAccess={false}
					setSupportMultipleWindows={false}
					style={[styles.web, !rendered && styles.hidden]}
				/>
			) : null}
			{!rendered ? <View style={styles.loading}><ActivityIndicator color={t.accent} /></View> : null}
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	web: { flex: 1, backgroundColor: t.bgBase },
	hidden: { opacity: 0 },
	loading: { position: "absolute", top: 0, right: 0, bottom: 0, left: 0, alignItems: "center", justifyContent: "center" },
});
