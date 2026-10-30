import { Stack, useFocusEffect, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { BackHandler, Platform, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { WebView, type WebViewNavigation } from "react-native-webview";
import { getPreview } from "../../lib/api";
import { AppSymbol } from "../../lib/AppSymbol";
import { authHeaders } from "../../lib/config";
import { userFacingError } from "../../lib/connectionError";
import { rowDividerWidth } from "../../lib/divider";
import { hostRouteMatches, previewForConfig } from "../../lib/hostRoute";
import { BrowserLivePane } from "../../lib/preview/BrowserLivePane";
import { LoadingBar } from "../../lib/preview/LoadingBar";
import { previewModeMenu, type PreviewMode } from "../../lib/preview/previewModeMenu";
import { HostScope, useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles, useThemeState } from "../../lib/ThemeProvider";
import { toolbarIcon } from "../../lib/toolbarIcon";
import { font, gutter, iconSize, radius, space, type } from "../../lib/tokens";
import { Button, EmptyState } from "../../lib/ui";

/** One preview surface for the session browser and any generated app/document preview. */
export default function SessionPreviewScreen() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><SessionPreviewContent /></HostScope> : <SessionPreviewContent />;
}

function SessionPreviewContent() {
	const { id: rawID, title, hostId: routeHostId } = useLocalSearchParams<{ id: string; title?: string; hostId?: string }>();
	const sessionID = String(rawID ?? "");
	const navigation = useNavigation();
	const router = useRouter();
	const { config, currentHostId, connection } = useApp();
	const hostMatches = hostRouteMatches(routeHostId, currentHostId);
	const t = useTheme();
	const { scheme } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const web = useRef<WebView>(null);
	// An explicit choice wins over the initial disabled-sharing fallback.
	const [active, setActive] = useState<"browser" | "app" | null>(null);
	const [loaded, setLoaded] = useState<{ config: NonNullable<typeof config>; id: string; value: Awaited<ReturnType<typeof getPreview>> } | null>(null);
	const preview = loaded?.id === sessionID ? previewForConfig(loaded, config, routeHostId) : null;
	const currentConfig = useRef(config);
	currentConfig.current = config;
	const currentRoute = useRef(`${routeHostId ?? ""}|${sessionID}`);
	currentRoute.current = `${routeHostId ?? ""}|${sessionID}`;
	const [previewError, setPreviewError] = useState<string>();
	const canShowResponsive = Boolean(preview);
	const [disabledStream, setDisabledStream] = useState<{ id: string; hostId?: string } | null>(null);
	const sharingDisabled = disabledStream?.id === sessionID && disabledStream?.hostId === routeHostId;
	const wantsResponsive = active === "app" || (active === null && sharingDisabled);
	const refreshingResponsive = wantsResponsive && !previewError && loaded !== null &&
		loaded.id === sessionID && loaded.config.hostId === routeHostId && Boolean(loaded.value) && loaded.config !== config;
	const responsive = wantsResponsive && (canShowResponsive || refreshingResponsive);
	const showingApp = responsive && !refreshingResponsive && preview !== null;
	const onBrowserDisabled = useCallback(() => {
		setDisabledStream({ id: sessionID, hostId: routeHostId });
	}, [routeHostId, sessionID]);

	// The app preview's own page: its title for the bar, its history for Android's
	// back, and how far its load has come (1 once it is done).
	const [webTitle, setWebTitle] = useState<string>();
	const [webProgress, setWebProgress] = useState(1);
	const webHistory = useRef(false);
	const appURL = showingApp ? preview.url : undefined;
	useEffect(() => {
		setWebTitle(undefined);
		setWebProgress(1);
		webHistory.current = false;
	}, [appURL]);
	const onWebNavigation = useCallback((event: WebViewNavigation) => {
		webHistory.current = event.canGoBack;
		// Android reports the address as the title until the page names itself.
		setWebTitle(event.title && event.title !== event.url ? event.title : undefined);
	}, []);

	// The live browser names its page as it changes.
	const showingBrowser = !refreshingResponsive && !showingApp;
	const [browserTitle, setBrowserTitle] = useState<string>();
	const headerTitle = (showingApp && (webTitle || preview.entry)) || (showingBrowser && browserTitle) || title || "Preview";
	useLayoutEffect(() => {
		navigation.setOptions({ title: headerTitle });
	}, [headerTitle, navigation]);

	const reloadApp = useCallback(() => {
		setPreviewError(undefined);
		web.current?.reload();
	}, []);

	// Reload and the choice between the two views, as native items in the bar.
	// Memoized: each new element re-registers the header's items, and the page
	// re-renders on every preview poll. The live browser draws the bar's right
	// side itself, ending with the view menu handed to it.
	const mode: PreviewMode = responsive ? "app" : "browser";
	const showMenu = hostMatches && Boolean(config);
	const modeMenu = useMemo(() => showMenu ? previewModeMenu({ mode, appAvailable: canShowResponsive, onSelect: setActive }) : null, [canShowResponsive, mode, showMenu]);
	const headerItems = useMemo(() => modeMenu && !showingBrowser ? (
		<Stack.Toolbar placement="right">
			{showingApp ? <Stack.Toolbar.Button icon={toolbarIcon("refresh")} accessibilityLabel="Reload app preview" onPress={reloadApp} /> : null}
			{modeMenu}
		</Stack.Toolbar>
	) : null, [modeMenu, reloadApp, showingApp, showingBrowser]);

	// Android's back walks the page's own history first, as a browser's does,
	// and leaves Preview once the page is back where it started.
	const canGoBack = useCallback(() => webHistory.current, []);
	useFocusEffect(useCallback(() => {
		if (Platform.OS !== "android" || !showingApp) return;
		const subscription = BackHandler.addEventListener("hardwareBackPress", () => {
			if (!canGoBack()) return false;
			web.current?.goBack();
			return true;
		});
		return () => subscription.remove();
	}, [canGoBack, showingApp]));

	const refreshPreview = useCallback(async () => {
		if (!hostMatches || !config || !sessionID) return;
		const route = `${routeHostId ?? ""}|${sessionID}`;
		try {
			const value = await getPreview(config, sessionID);
			if (currentConfig.current === config && currentRoute.current === route) {
				setLoaded({ config, id: sessionID, value });
				setPreviewError(undefined);
			}
		} catch (cause) {
			if (currentConfig.current === config && currentRoute.current === route) setPreviewError(userFacingError(cause));
		}
	}, [config, hostMatches, routeHostId, sessionID]);

	useEffect(() => {
		setActive(null);
	}, [routeHostId, sessionID]);

	useEffect(() => {
		// Retain selection knowledge during endpoint changes; previewForConfig
		// still prevents rendering its old URL with the new endpoint credentials.
		setPreviewError(undefined);
		if (!hostMatches) return;
		void refreshPreview();
		const poll = setInterval(() => void refreshPreview(), 5_000);
		return () => clearInterval(poll);
	}, [hostMatches, refreshPreview]);

	// Preview is pushed over the board, so the way back to it pops rather than
	// stacking a second board on top.
	if (!hostMatches) return <View style={styles.center}><EmptyState icon="globe" title="Preview belongs to another machine" message="Open it from that machine's session." action={<Button title="Open board" icon="workers" onPress={() => router.dismissTo("/")} />} /></View>;
	if (!config && connection === "closed") return <View style={styles.center}><EmptyState icon="wifi-off" title="Machine offline" message="This preview loads once the app reconnects." /></View>;
	if (!config) return <View style={styles.center}><EmptyState busy title="Connecting to this machine…" /></View>;

	return <View style={styles.screen}>
		{headerItems}
		{refreshingResponsive ? <View style={styles.center}><EmptyState busy title="Loading mobile preview…" /></View> : !showingApp ? <BrowserLivePane sessionID={sessionID} onDisabled={onBrowserDisabled} onTitle={setBrowserTitle} headerItem={modeMenu} /> : <View style={styles.appPane}>
			<WebView
				ref={web}
				source={{ uri: preview.url, headers: preview.authenticated && config ? authHeaders(config) : undefined }}
				style={styles.web}
				// The bar shows a load in progress; a reload keeps the page readable
				// under it rather than covering it with a spinner.
				renderLoading={() => <View />}
				// iOS: the edge swipe walks the page's history and a pull reloads it,
				// as in Safari.
				allowsBackForwardNavigationGestures
				pullToRefreshEnabled
				refreshControlLightMode={scheme === "dark"}
				onLoadStart={() => {
					setPreviewError(undefined);
					setWebProgress(0);
				}}
				onLoadProgress={(event) => setWebProgress(event.nativeEvent.progress)}
				onLoadEnd={() => setWebProgress(1)}
				onNavigationStateChange={onWebNavigation}
				onHttpError={(event) => setPreviewError(previewHttpErrorCopy(event.nativeEvent.statusCode))}
				onError={(event) => setPreviewError(event.nativeEvent.description || "Couldn't load this preview.")}
			/>
			<LoadingBar loading={webProgress < 1} progress={webProgress} accessibilityLabel="Loading app preview" />
			{previewError ? <View accessibilityRole="alert" style={[styles.webError, { bottom: insets.bottom + space.lg }]}>
				<AppSymbol name="warning" size={iconSize.md} color={t.red} />
				<Text style={styles.webErrorText}>{previewError}</Text>
				<Button variant="ghost" title="Retry" onPress={reloadApp} />
			</View> : null}
		</View>}
	</View>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, backgroundColor: t.bgBase },
	appPane: { flex: 1, backgroundColor: t.bgBase },
	web: { flex: 1, backgroundColor: t.bgBase },
	webError: {
		position: "absolute",
		left: gutter,
		right: gutter,
		flexDirection: "row",
		alignItems: "center",
		gap: space.sm,
		borderRadius: radius.lg,
		borderCurve: "continuous",
		borderWidth: rowDividerWidth,
		borderColor: t.tintRed,
		backgroundColor: t.bgElevated,
		paddingLeft: space.md,
		paddingVertical: space.xxs,
	},
	webErrorText: { flex: 1, color: t.textSecondary, fontFamily: font.regular, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
});

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

function previewHttpErrorCopy(status: number): string {
	if (status === 404 || status === 410) return "This page wasn't found. The agent may have moved or removed it.";
	if (status === 401 || status === 403) return "This page needs access this phone doesn't have.";
	if (status >= 500) return "The page's server hit an error. Check that the agent's dev server is running, then retry.";
	return "This page didn't load. Retry, or check it on that machine.";
}
