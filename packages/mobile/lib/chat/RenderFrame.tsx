import * as Linking from "expo-linking";
import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Modal, Pressable, StyleSheet, Text, View, useWindowDimensions } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useIsFocused } from "expo-router/react-navigation";
import { WebView } from "react-native-webview";
import { authHeaders, httpBase } from "../config";
import { haptics } from "../haptics";
import { Feather } from "../icons";
import { useApp } from "../store";
import type { Theme } from "../theme";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { space, type } from "../tokens";
import {
	RENDER_BRIDGE_SCRIPT,
	RENDER_ZOOM_SCRIPT,
	REPORT_SETTLE_LIMIT_MS,
	clampRenderHeight,
	initialRenderHeight,
	isRenderPageLoad,
	readRenderBridgeMessage,
	rememberRenderHeight,
	rememberedRenderHeight,
	renderFrameLive,
	renderTheme,
	renderThemeFragment,
	renderThemeScript,
	renderViewerStyleScript,
	subscribeRenderViewport,
	type RenderDisplayMode,
	type RenderRef,
	type RenderTheme,
} from "./renderFrameModel";
import { RenderSkeleton } from "./RenderSkeleton";
import { startRenderHeightStore } from "./renderHeightStore";

startRenderHeightStore();

// A link opens only from a touch the viewer just received, so a page cannot
// open one on its own by posting to the bridge.
const LINK_TAP_WINDOW_MS = 1000;
// A page whose process the OS keeps killing stops reloading after this many.
const MAX_PROCESS_RESTARTS = 2;
// A page this device has already loaded usually comes back within this; only
// a slower load shows the skeleton, so a cached page does not flash one.
const SKELETON_DELAY_MS = 250;
// A frame that measures out of range lets its page go only after this. Content
// added above (earlier history) lays the list out once before the scroll anchor
// restores the position, and a measure in between would unload and reload pages
// that never left the screen.
const LEAVE_RANGE_DELAY_MS = 400;
const VIEWER_STYLE_SCRIPT = renderViewerStyleScript(space.xl);

function useRenderTheme(): RenderTheme {
	const t = useTheme();
	const { scheme } = useThemeState();
	return useMemo(() => renderTheme(t, scheme === "light" ? "light" : "dark"), [t, scheme]);
}

/**
 * The page in a WebView. The daemon serves it with its images inlined, so the
 * one authenticated request is all it needs; its CSP sandbox keeps it on an
 * opaque origin. Theme rides in the fragment for first paint and is posted
 * after, so the page never reloads.
 */
function RenderDocument({ path, displayMode, onHeight, onReady, onGone, onFailed }: {
	path: string;
	displayMode: RenderDisplayMode;
	onHeight?: (height: number) => void;
	onReady: () => void;
	/** The page's process died; the WebView is blank until it is mounted again. */
	onGone: () => void;
	onFailed: () => void;
}) {
	const styles = useThemedStyles(makeStyles);
	const { config } = useApp();
	const web = useRef<WebView>(null);
	const lastTouch = useRef(0);
	const loaded = useRef(false);
	const theme = useRenderTheme();
	const themeRef = useRef(theme);
	themeRef.current = theme;
	const pageUrl = config ? `${httpBase(config)}${path}` : undefined;
	// The fragment only seeds first paint; a theme change is posted instead.
	const uri = useMemo(() => pageUrl && `${pageUrl}${renderThemeFragment(themeRef.current, displayMode)}`, [pageUrl, displayMode]);
	const fullscreen = displayMode === "fullscreen";
	// A stable source: a new object each render would make the WebView re-check it.
	const source = useMemo(() => uri && config ? { uri, headers: authHeaders(config) } : undefined, [uri, config]);

	useEffect(() => {
		if (loaded.current) web.current?.injectJavaScript(renderThemeScript(theme));
	}, [theme]);

	if (!uri || !source) return null;
	return <View style={styles.document} onTouchStart={() => { lastTouch.current = Date.now(); }} onTouchEnd={() => { lastTouch.current = Date.now(); }}>
		<WebView
			ref={web}
			source={source}
			injectedJavaScriptBeforeContentLoaded={fullscreen ? VIEWER_STYLE_SCRIPT : undefined}
			injectedJavaScript={fullscreen ? `${RENDER_ZOOM_SCRIPT}\n${RENDER_BRIDGE_SCRIPT}` : RENDER_BRIDGE_SCRIPT}
			onMessage={(event) => {
				const message = readRenderBridgeMessage(event.nativeEvent.data);
				if (message?.type === "height") onHeight?.(message.height);
				else if (message?.type === "link" && fullscreen && Date.now() - lastTouch.current < LINK_TAP_WINDOW_MS) void Linking.openURL(message.url);
			}}
			// Only the page itself loads in the frame; a frame inside the page may load its own.
			onShouldStartLoadWithRequest={(request) => request.isTopFrame === false || isRenderPageLoad(request.url, uri)}
			onLoadEnd={() => {
				loaded.current = true;
				web.current?.injectJavaScript(renderThemeScript(themeRef.current));
				onReady();
			}}
			onHttpError={onFailed}
			onError={onFailed}
			onContentProcessDidTerminate={onGone}
			onRenderProcessGone={onGone}
			setSupportMultipleWindows={false}
			javaScriptCanOpenWindowsAutomatically={false}
			allowsLinkPreview={false}
			// Inline, the page sits still in the thread; expanded, it scrolls and pinch-zooms.
			scrollEnabled={fullscreen}
			bounces={fullscreen}
			setBuiltInZoomControls={fullscreen}
			setDisplayZoomControls={false}
			overScrollMode={fullscreen ? "always" : "never"}
			showsVerticalScrollIndicator={false}
			showsHorizontalScrollIndicator={false}
			style={styles.web}
		/>
	</View>;
}

/**
 * One mounted page's lifecycle: loading until its document finishes, then
 * ready. A page whose process dies is mounted afresh, up to a limit.
 */
function useRenderPage() {
	const [mount, setMount] = useState(0);
	const [loading, setLoading] = useState(true);
	const restarts = useRef(0);
	const ready = useCallback(() => setLoading(false), []);
	const unload = useCallback(() => setLoading(true), []);
	const reset = useCallback(() => {
		restarts.current = 0;
		setLoading(true);
		setMount((value) => value + 1);
	}, []);
	const gone = useCallback((giveUp: () => void) => {
		restarts.current += 1;
		if (restarts.current > MAX_PROCESS_RESTARTS) return giveUp();
		setLoading(true);
		setMount((value) => value + 1);
	}, []);
	return useMemo(() => ({ mount, loading, ready, unload, reset, gone }), [mount, loading, ready, unload, reset, gone]);
}

/** The page over the whole screen, scrollable and pinch-zoomable. */
function RenderViewer({ render, visible, onClose }: { render: RenderRef; visible: boolean; onClose: () => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const [failed, setFailed] = useState(false);
	const page = useRenderPage();
	const close = () => { haptics.tap(); onClose(); };
	// Reset on close, not on show: iOS fires onShow after the fade-in, and a reset
	// then remounts a page that is already showing, so it flashes blank and reloads.
	const { reset } = page;
	useEffect(() => {
		if (visible) return;
		setFailed(false);
		reset();
	}, [visible, reset]);
	return <Modal visible={visible} animationType="fade" statusBarTranslucent onRequestClose={onClose}>
		<View style={[styles.viewer, { paddingTop: insets.top }]}>
			<View style={styles.viewerHeader}>
				<Text numberOfLines={1} accessibilityRole="header" style={styles.viewerTitle}>{render.title}</Text>
				<Pressable accessibilityRole="button" accessibilityLabel="Close" hitSlop={10} onPress={close} style={styles.viewerClose}>
					<Feather name="x" size={18} color={t.textPrimary} />
				</Pressable>
			</View>
			{failed
				? <View style={styles.viewerError}><Text style={styles.fallbackTitle}>Couldn't load this page.</Text></View>
				: visible ? <View style={styles.viewerBody}>
					<RenderDocument key={page.mount} path={render.path} displayMode="fullscreen" onReady={page.ready} onGone={() => page.gone(() => setFailed(true))} onFailed={() => setFailed(true)} />
					{page.loading ? <RenderSkeleton height={Number.POSITIVE_INFINITY} label={render.title} style={styles.viewerSkeleton} delayMs={SKELETON_DELAY_MS} /> : null}
				</View> : null}
		</View>
	</Modal>;
}

/**
 * An agent's HTML page inline in its turn, as desktop shows it; a tap expands
 * it. Only a frame in or near view holds a WebView: a page can run to megabytes
 * of inlined images, and a thread can carry many. Out of range, a placeholder
 * keeps the page's last height so the thread does not jump when it returns.
 */
export const RenderFrame = memo(function RenderFrame({ render }: { render: RenderRef }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { config } = useApp();
	const frame = useRef<View>(null);
	const { height: viewportHeight } = useWindowDimensions();
	const [width, setWidth] = useState<number>();
	const [contentHeight, setContentHeight] = useState<number>();
	const [live, setLive] = useState(false);
	const [failed, setFailed] = useState(false);
	const page = useRenderPage();
	const [expanded, setExpanded] = useState(false);
	// Sized on this device already: by a report this mount, or one saved earlier.
	const measured = contentHeight !== undefined || rememberedRenderHeight(render.path, width) !== undefined;
	const liveRef = useRef(live);
	liveRef.current = live;
	// A screen pushed over the session leaves these frames laid out "in view";
	// they let their pages go anyway and load them again on the way back.
	const focused = useIsFocused();
	const focusedRef = useRef(focused);
	focusedRef.current = focused;
	const leaveRange = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	useEffect(() => () => clearTimeout(leaveRange.current), []);
	const measure = useCallback(() => {
		frame.current?.measureInWindow((_x, y, _width, height) => {
			// A cell the list has clipped measures as zero; leave it as it was.
			if (height <= 0) return;
			if (focusedRef.current && renderFrameLive(y, height, viewportHeight, liveRef.current, measured)) {
				clearTimeout(leaveRange.current);
				leaveRange.current = undefined;
				if (!liveRef.current) setLive(true);
				return;
			}
			if (!liveRef.current || leaveRange.current) return;
			leaveRange.current = setTimeout(() => {
				leaveRange.current = undefined;
				setLive(false);
			}, LEAVE_RANGE_DELAY_MS);
		});
	}, [viewportHeight, measured]);
	useEffect(() => subscribeRenderViewport(measure), [measure]);
	useEffect(() => { measure(); }, [focused, measure]);
	// A page that leaves range reloads when it returns, so it is loading again.
	const { unload } = page;
	useEffect(() => { if (!live) unload(); }, [live, unload]);
	// The skeleton stays until the page has a settled height, so the page appears
	// at its size instead of resizing in view. A page that never reports one is
	// shown anyway once it has had time to.
	const { ready } = page;
	const readyFallback = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	// The fallback belongs to the page that armed it. When that page goes (out of
	// range, or remounted after its process died), so does the timer; otherwise it
	// would mark the next page ready before its height, which then resizes in view.
	useEffect(() => () => clearTimeout(readyFallback.current), [page.mount]);
	useEffect(() => { if (!live) clearTimeout(readyFallback.current); }, [live]);
	const onLoaded = useCallback(() => {
		clearTimeout(readyFallback.current);
		readyFallback.current = setTimeout(ready, REPORT_SETTLE_LIMIT_MS + 500);
	}, [ready]);
	const onHeight = useCallback((height: number) => {
		if (width) rememberRenderHeight(render.path, width, height);
		setContentHeight(height);
		clearTimeout(readyFallback.current);
		ready();
	}, [render.path, width, ready]);

	if (!config || failed) {
		return <Pressable
			disabled={!config}
			accessibilityRole={config ? "button" : undefined}
			accessibilityLabel={config ? `Retry loading ${render.title}` : undefined}
			onPress={() => { haptics.tap(); setFailed(false); page.reset(); }}
			style={styles.fallback}
		>
			<Feather name={config ? "refresh-cw" : "file-text"} size={12} color={t.textTertiary} />
			<Text numberOfLines={1} style={styles.fallbackTitle}>{render.title}</Text>
			{config ? <Text style={styles.fallbackAction}>Couldn't load · Tap to retry</Text> : null}
		</Pressable>;
	}

	const height = clampRenderHeight(contentHeight ?? rememberedRenderHeight(render.path, width) ?? initialRenderHeight(render, width));
	return <View ref={frame} onLayout={(event) => { setWidth(Math.round(event.nativeEvent.layout.width)); measure(); }} style={[styles.frame, { height }]}>
		{width && live ? <RenderDocument key={page.mount} path={render.path} displayMode="inline" onHeight={onHeight} onReady={onLoaded} onGone={() => page.gone(() => setFailed(true))} onFailed={() => setFailed(true)} /> : null}
		{/* Over the frame until the page has loaded, and in its place while it is out of range. */}
		{!live || page.loading ? <RenderSkeleton height={height} label={render.title} style={StyleSheet.absoluteFill} delayMs={measured ? SKELETON_DELAY_MS : 0} /> : null}
		{/* Inline, a tap means "show me this larger"; links and zoom live in the expanded view. */}
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`Expand ${render.title}`}
			onPress={() => { haptics.tap(); setExpanded(true); }}
			style={StyleSheet.absoluteFill}
		>
			<View style={styles.expandChip}><Feather name="maximize" size={13} color={t.textSecondary} /></View>
		</Pressable>
		<RenderViewer render={render} visible={expanded} onClose={() => setExpanded(false)} />
	</View>;
}, (previous, next) => sameRender(previous.render, next.render));

/** The timeline re-reads each row on every update; the same page must not re-render. */
function sameRender(a: RenderRef, b: RenderRef): boolean {
	return a.id === b.id && a.path === b.path && a.title === b.title && a.height === b.height && JSON.stringify(a.heights) === JSON.stringify(b.heights);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	frame: { marginVertical: space.xs, overflow: "hidden", backgroundColor: t.bgBase },
	document: { flex: 1 },
	web: { flex: 1, backgroundColor: t.bgBase },
	expandChip: { position: "absolute", top: space.xs, right: space.xs, width: 28, height: 28, borderRadius: 14, alignItems: "center", justifyContent: "center", backgroundColor: t.bgElevated, borderWidth: 1, borderColor: t.borderSubtle },
	viewer: { flex: 1, backgroundColor: t.bgBase },
	viewerHeader: { flexDirection: "row", alignItems: "center", gap: space.sm, paddingLeft: space.lg, paddingRight: space.sm, paddingVertical: space.xs },
	viewerTitle: { flex: 1, fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.subheadline.fontSize, fontWeight: "600" },
	viewerClose: { width: 36, height: 36, borderRadius: 18, alignItems: "center", justifyContent: "center", backgroundColor: t.bgElevated },
	// Edge to edge: the page carries the thread's inset itself (VIEWER_STYLE_SCRIPT).
	viewerBody: { flex: 1 },
	viewerSkeleton: { position: "absolute", top: 0, left: space.xl, right: space.xl, height: 360 },
	viewerError: { flex: 1, alignItems: "center", justifyContent: "center" },
	fallback: { alignSelf: "flex-start", marginVertical: space.xs, flexDirection: "row", alignItems: "center", gap: space.xs, borderRadius: 8, borderWidth: 1, borderColor: t.borderSubtle, paddingHorizontal: space.sm, paddingVertical: space.xs },
	fallbackTitle: { fontFamily: "Geist_400Regular", flexShrink: 1, color: t.textSecondary, fontSize: type.caption1.fontSize },
	fallbackAction: { fontFamily: "Geist_600SemiBold", color: t.accent, fontSize: type.caption2.fontSize, fontWeight: "600" },
});
