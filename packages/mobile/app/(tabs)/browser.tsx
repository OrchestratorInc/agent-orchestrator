import * as Clipboard from "expo-clipboard";
import { useRef, useState } from "react";
import { Linking, Pressable, Share, StyleSheet, Text, View } from "react-native";
import { WebView, type WebViewNavigation } from "react-native-webview";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { authHeaders } from "../../lib/config";
import { BrowserErrorBanner } from "../../lib/browser/BrowserErrorBanner";
import { BrowserToolbar } from "../../lib/browser/BrowserToolbar";
import { isHttpUrl, normalizeBrowserInput, shouldAttachPreviewAuth } from "../../lib/browser/browserUrl";
import { browserLoadEnd, browserLoadError, browserLoadStart, browserNavigationChanged, initialBrowserState } from "../../lib/browser/browserState";
import { Feather } from "../../lib/icons";
import { useApp } from "../../lib/store";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import type { Theme } from "../../lib/theme";
import { ScreenHeader } from "../../lib/ui";
import { iconSize, space, type } from "../../lib/tokens";
import { haptics } from "../../lib/haptics";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

/** A first-class browser surface, available even before a session has a preview. */
export default function BrowserScreen() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const { config } = useApp();
	const web = useRef<WebView>(null);
	const [sourceUrl, setSourceUrl] = useState("");
	const [state, setState] = useState(initialBrowserState);

	const navigate = (value: string) => {
		const result = normalizeBrowserInput(value, config?.host ?? "");
		if (!result.ok) {
			haptics.warning();
			setState((current) => browserLoadError(current, result.message));
			return;
		}
		const url = result.url.toString();
		setSourceUrl(url);
		setState((current) => browserLoadStart({ ...current, url }, url));
	};

	const currentUrl = state.url || sourceUrl;
	const source = sourceUrl
		? {
			uri: sourceUrl,
			...(config && shouldAttachPreviewAuth(sourceUrl, config) ? { headers: authHeaders(config) } : {}),
		}
		: undefined;

	return (
		<View style={styles.screen}>
			<View style={{ height: insets.top }} />
			<ScreenHeader title="Browser" />
			<View style={styles.content}>
				{source ? (
					<WebView
						ref={web}
						source={source}
						style={styles.web}
						onLoadStart={(event) => setState((current) => browserLoadStart(current, event.nativeEvent.url))}
						onLoadEnd={() => setState(browserLoadEnd)}
						onNavigationStateChange={(event: WebViewNavigation) => setState((current) => browserNavigationChanged(current, {
							url: event.url,
							title: event.title,
							canGoBack: event.canGoBack,
							canGoForward: event.canGoForward,
							loading: event.loading,
						}))}
						onShouldStartLoadWithRequest={(request) => {
							if (isHttpUrl(request.url)) return true;
							setState((current) => browserLoadError(current, "Only HTTP and HTTPS URLs can be opened."));
							return false;
						}}
						onHttpError={(event) => setState((current) => browserLoadError(current, `Page returned HTTP ${event.nativeEvent.statusCode}.`))}
						onError={(event) => setState((current) => browserLoadError(current, event.nativeEvent.description || "Couldn't load this page."))}
					/>
				) : (
					<View style={styles.empty}>
						<View style={styles.emptyIcon}><Feather name="globe" size={iconSize.xl} color={t.accent} /></View>
						<Text style={styles.emptyTitle}>Browse from AO</Text>
						<Text style={styles.emptyCopy}>Open a site, localhost preview, or an AO session preview without leaving the app.</Text>
						<Pressable accessibilityRole="button" onPress={() => navigate("https://example.com")} style={styles.exampleButton}>
							<Text style={styles.exampleButtonText}>Open example.com</Text>
						</Pressable>
					</View>
				)}
				{state.error ? <BrowserErrorBanner message={state.error} onDismiss={() => setState((current) => ({ ...current, error: undefined }))} onRetry={() => web.current?.reload()} /> : null}
			</View>
			<View style={{ paddingBottom: insets.bottom, backgroundColor: t.bgSurface }}>
				<BrowserToolbar
					url={currentUrl}
					title={state.title}
					loading={state.loading}
					canGoBack={state.canGoBack}
					canGoForward={state.canGoForward}
					canOpenExternal={isHttpUrl(currentUrl)}
					onBack={() => web.current?.["goBack"]()}
					onForward={() => web.current?.goForward()}
					onReload={() => web.current?.reload()}
					onStop={() => { web.current?.stopLoading(); setState((current) => ({ ...current, loading: false })); }}
					onSubmitUrl={navigate}
					onCopy={() => { if (currentUrl) void Clipboard.setStringAsync(currentUrl); }}
					onOpenExternal={() => { if (currentUrl) void Linking.openURL(currentUrl); }}
					onShare={() => { if (currentUrl) void Share.share({ url: currentUrl, message: currentUrl }); }}
				/>
			</View>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	content: { flex: 1 },
	web: { flex: 1, backgroundColor: "#ffffff" },
	empty: { flex: 1, alignItems: "center", justifyContent: "center", paddingHorizontal: space.xl },
	emptyIcon: { width: 72, height: 72, alignItems: "center", justifyContent: "center", borderRadius: 22, borderCurve: "continuous", backgroundColor: t.accentTint, marginBottom: space.lg },
	emptyTitle: { fontFamily: "Geist_600SemiBold", color: t.textPrimary, fontSize: type.title2.fontSize, lineHeight: type.title2.lineHeight, fontWeight: "600" },
	emptyCopy: { marginTop: space.sm, maxWidth: 320, textAlign: "center", fontFamily: "Geist_400Regular", color: t.textSecondary, fontSize: type.body.fontSize, lineHeight: type.body.lineHeight },
	exampleButton: { marginTop: space.xl, borderRadius: 12, borderCurve: "continuous", backgroundColor: t.bgElevated, paddingHorizontal: space.lg, paddingVertical: space.md },
	exampleButtonText: { fontFamily: "Geist_600SemiBold", color: t.accent, fontSize: type.subheadline.fontSize, fontWeight: "600" },
});
