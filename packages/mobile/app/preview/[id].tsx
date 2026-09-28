import * as Clipboard from "expo-clipboard";
import { Feather } from "../../lib/icons";
import { useLocalSearchParams, useNavigation } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Linking, Pressable, Share, StyleSheet, Text, View } from "react-native";
import { WebView, type WebViewNavigation } from "react-native-webview";
import { getPreview } from "../../lib/api";
import { authHeaders } from "../../lib/config";
import { BrowserErrorBanner } from "../../lib/browser/BrowserErrorBanner";
import { BrowserToolbar } from "../../lib/browser/BrowserToolbar";
import { isHttpUrl, normalizeBrowserInput, shouldAttachPreviewAuth } from "../../lib/browser/browserUrl";
import { browserLoadEnd, browserLoadError, browserLoadStart, browserNavigationChanged, initialBrowserState, type MobileBrowserState } from "../../lib/browser/browserState";
import { headerActionStyle, headerGlyphStyle } from "../../lib/headerAction";
import { haptics } from "../../lib/haptics";
import { useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { iconSize, space, type } from "../../lib/tokens";

type BrowserSource = { url: string; entry: string };

/** Session-scoped counterpart of the desktop Browser inspector. */
export default function SessionPreviewScreen() {
	const { id, title, previewUrl } = useLocalSearchParams<{ id: string; title?: string; previewUrl?: string }>();
	const navigation = useNavigation();
	const { config } = useApp();
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const web = useRef<WebView>(null);
	const [preview, setPreview] = useState<{ entry: string; url: string; authenticated: boolean } | null>(null);
	const [browserSource, setBrowserSource] = useState<BrowserSource | null>(null);
	const [browserState, setBrowserState] = useState<MobileBrowserState>(initialBrowserState);
	const [loading, setLoading] = useState(true);
	const [discoveryError, setDiscoveryError] = useState<string>();
	const [toast, setToast] = useState<string>();
	const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

	const showToast = useCallback((message: string) => {
		setToast(message);
		if (toastTimer.current) clearTimeout(toastTimer.current);
		toastTimer.current = setTimeout(() => setToast(undefined), 1_500);
	}, []);

	useEffect(() => () => { if (toastTimer.current) clearTimeout(toastTimer.current); }, []);
	useEffect(() => {
		setPreview(null);
		setBrowserSource(null);
		setBrowserState(initialBrowserState);
		setLoading(true);
		setDiscoveryError(undefined);
	}, [id, previewUrl]);

	const refresh = useCallback(async () => {
		if (!config || !id) return;
		try {
			const next = await getPreview(config, id, previewUrl);
			setPreview(next);
			setDiscoveryError(undefined);
			if (next) {
				setBrowserSource((current) => current ?? { url: next.url, entry: next.entry });
				setBrowserState((current) => current.url ? current : { ...current, url: next.url });
			}
		}
		catch (cause) { setDiscoveryError(cause instanceof Error ? cause.message : String(cause)); }
		finally { setLoading(false); }
	}, [config, id, previewUrl]);

	useEffect(() => { void refresh(); const poll = setInterval(() => void refresh(), 5_000); return () => clearInterval(poll); }, [refresh]);

	const source = useMemo(() => {
		if (!config || !browserSource) return undefined;
		return {
			uri: browserSource.url,
			...(shouldAttachPreviewAuth(browserSource.url, config) ? { headers: authHeaders(config) } : {}),
		};
	}, [browserSource, config]);

	const pageTitle = browserState.title || title || preview?.entry || "Preview";
	useLayoutEffect(() => {
		navigation.setOptions({
			title: pageTitle,
			headerRight: () => <Pressable accessibilityRole="button" accessibilityLabel="Reload preview" hitSlop={10} onPress={() => { haptics.tap(); if (browserSource) web.current?.reload(); else void refresh(); }} style={headerActionStyle}><Feather name="refresh-cw" size={iconSize.md} color={t.textSecondary} style={headerGlyphStyle} /></Pressable>,
		});
	}, [browserSource, navigation, pageTitle, refresh, t.textSecondary]);

	const navigateTo = useCallback((value: string) => {
		if (!config) return;
		const result = normalizeBrowserInput(value, config.host);
		if (!result.ok) {
			haptics.error();
			setBrowserState((current) => browserLoadError(current, result.message));
			return;
		}
		haptics.tap();
		const url = result.url.href;
		setBrowserSource({ url, entry: result.url.hostname });
		setBrowserState((current) => browserLoadStart({ ...current, url }, url));
	}, [config]);

	const currentUrl = browserState.url || browserSource?.url || "";
	const retry = useCallback(() => {
		haptics.tap();
		setBrowserState((current) => ({ ...current, error: undefined }));
		if (browserSource) web.current?.reload();
		else void refresh();
	}, [browserSource, refresh]);
	const copyCurrentUrl = useCallback(() => {
		if (!currentUrl) return;
		void Clipboard.setStringAsync(currentUrl).then(() => { haptics.success(); showToast("URL copied"); }, () => { haptics.error(); showToast("Couldn't copy URL"); });
	}, [currentUrl, showToast]);
	const openCurrentUrl = useCallback(() => {
		if (!isHttpUrl(currentUrl)) return;
		void Linking.openURL(currentUrl).catch(() => { haptics.error(); showToast("Couldn't open URL"); });
	}, [currentUrl, showToast]);
	const shareCurrentUrl = useCallback(() => {
		if (!currentUrl) return;
		void Share.share({ message: currentUrl, url: currentUrl }).catch(() => undefined);
	}, [currentUrl]);

	if (!config || loading) return <View style={styles.center}><ActivityIndicator color={t.accent} /><Text style={styles.copy}>Looking for a session preview…</Text></View>;
	if (!browserSource) return <View style={styles.center}><Feather name={discoveryError ? "alert-triangle" : "globe"} size={iconSize.xl} color={discoveryError ? t.red : t.textTertiary} /><Text style={styles.title}>{discoveryError ? "Couldn't load the preview" : "No preview yet"}</Text><Text style={styles.copy}>{discoveryError || "Waiting for the agent to generate a page or document. This screen will keep checking."}</Text><Pressable onPress={() => { haptics.tap(); void refresh(); }} style={styles.retry}><Text style={styles.retryText}>Check again</Text></Pressable></View>;

	return <View style={styles.screen}>
		<WebView
			ref={web}
			source={source}
			style={styles.web}
			originWhitelist={["http://*", "https://*"]}
			startInLoadingState
			renderLoading={() => <View style={styles.webLoading}><ActivityIndicator color={t.accent} /></View>}
			onLoadStart={(event) => setBrowserState((current) => browserLoadStart(current, event.nativeEvent.url))}
			onLoadEnd={() => setBrowserState(browserLoadEnd)}
			onNavigationStateChange={(event: WebViewNavigation) => setBrowserState((current) => browserNavigationChanged(current, {
				url: event.url,
				title: event.title,
				canGoBack: event.canGoBack,
				canGoForward: event.canGoForward,
				loading: event.loading,
			}))}
			onShouldStartLoadWithRequest={(request) => {
				if (isHttpUrl(request.url)) return true;
				setBrowserState((current) => browserLoadError(current, "Only HTTP and HTTPS URLs can be opened."));
				return false;
			}}
			onHttpError={(event) => setBrowserState((current) => browserLoadError(current, `Preview returned HTTP ${event.nativeEvent.statusCode}.`))}
			onError={(event) => setBrowserState((current) => browserLoadError(current, event.nativeEvent.description || "Couldn't load this page."))}
		/>
		{browserState.error ? <BrowserErrorBanner message={browserState.error} onDismiss={() => setBrowserState((current) => ({ ...current, error: undefined }))} onRetry={retry} /> : null}
		{toast ? <View style={styles.toast}><Text style={styles.toastText}>{toast}</Text></View> : null}
		<BrowserToolbar
			url={currentUrl}
			title={browserState.title || preview?.entry}
			loading={browserState.loading}
			canGoBack={browserState.canGoBack}
			canGoForward={browserState.canGoForward}
			canOpenExternal={isHttpUrl(currentUrl)}
			onBack={() => { haptics.tap(); web.current?.["goBack"](); }}
			onForward={() => { haptics.tap(); web.current?.goForward(); }}
			onReload={() => { haptics.tap(); web.current?.reload(); }}
			onStop={() => { haptics.tap(); web.current?.stopLoading(); setBrowserState((current) => ({ ...current, loading: false })); }}
			onSubmitUrl={navigateTo}
			onCopy={copyCurrentUrl}
			onOpenExternal={openCurrentUrl}
			onShare={shareCurrentUrl}
		/>
	</View>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	web: { flex: 1, backgroundColor: t.bgBase },
	webLoading: { ...StyleSheet.absoluteFill, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", gap: space.md, paddingHorizontal: space.xxxl, backgroundColor: t.bgBase },
	title: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.body.fontSize, fontWeight: "600", textAlign: "center" },
	copy: { fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, textAlign: "center" },
	retry: { marginTop: space.xxs, minHeight: 40, justifyContent: "center", borderRadius: 8, borderCurve: "continuous", backgroundColor: t.accent, paddingHorizontal: space.md },
	retryText: { fontFamily: "Geist_600SemiBold", color: t.onAccent, fontSize: type.caption1.fontSize, fontWeight: "600" },
	toast: { position: "absolute", alignSelf: "center", bottom: 150, borderRadius: 999, borderCurve: "continuous", backgroundColor: t.bgElevated, borderWidth: 1, borderColor: t.borderDefault, paddingHorizontal: space.md, paddingVertical: space.sm },
	toastText: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.caption1.fontSize, fontWeight: "600" },
});

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
