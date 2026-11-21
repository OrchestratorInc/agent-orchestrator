import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ActivityIndicator, Linking, StyleSheet, View } from "react-native";
import { WebView, type WebViewMessageEvent } from "react-native-webview";
import type { ShouldStartLoadRequest } from "react-native-webview/lib/WebViewTypes";
import { isRenderableDiagram } from "../../shared/files/file-formats";
import { mermaidPageHtml, parseDiagramMessage } from "./mermaidPage";
import { loadMermaidRuntime } from "./mermaidRuntime";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles, useThemeState } from "./ThemeProvider";

/** Loads in the page's own document; anything else is a link, which opens in the browser. */
export function stayInDiagramPage(request: ShouldStartLoadRequest): boolean {
	if (/^(about|data):/i.test(request.url)) return true;
	if (/^https?:/i.test(request.url)) void Linking.openURL(request.url).catch(() => {});
	return false;
}

// A diagram page whose process keeps dying gives up after this many fresh mounts.
const MAX_PROCESS_RESTARTS = 2;

/**
 * A diagram WebView's mount, for when its web content process dies: iOS ends
 * WKWebView content processes in the background, and Android can lose the
 * renderer under memory pressure, which leaves a blank box. `gone` mounts the
 * page afresh (bump `key` with `mount`), then calls `giveUp` once it has died
 * more than the limit in a row, as the chat's render frames do (useRenderPage).
 * `rendered` says a mount drew: the count starts over, so a diagram that comes
 * back each time is never given up on after several background visits.
 */
export function useDiagramRemount(): { mount: number; gone: (giveUp: () => void) => void; rendered: () => void } {
	const [mount, setMount] = useState(0);
	const restarts = useRef(0);
	const gone = useCallback((giveUp: () => void) => {
		restarts.current += 1;
		if (restarts.current > MAX_PROCESS_RESTARTS) giveUp();
		else setMount((value) => value + 1);
	}, []);
	const rendered = useCallback(() => {
		restarts.current = 0;
	}, []);
	return useMemo(() => ({ mount, gone, rendered }), [mount, gone, rendered]);
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
	const page = useDiagramRemount();
	// A page mounted afresh after its process died draws again before it shows.
	useEffect(() => setRendered(false), [page.mount]);
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
		if (message?.type === "rendered") {
			setRendered(true);
			page.rendered();
		}
		else if (message?.type === "failed") setFailed(true);
	};
	return (
		<View style={styles.screen}>
			{html ? (
				<WebView
					key={page.mount}
					source={{ html }}
					originWhitelist={["*"]}
					onShouldStartLoadWithRequest={stayInDiagramPage}
					onMessage={onMessage}
					onError={() => setFailed(true)}
					onContentProcessDidTerminate={() => page.gone(() => setFailed(true))}
					onRenderProcessGone={() => page.gone(() => setFailed(true))}
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
