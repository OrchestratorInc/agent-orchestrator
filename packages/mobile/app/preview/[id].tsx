import { Feather } from "../../lib/icons";
import { Stack, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from "react-native";
import { WebView } from "react-native-webview";
import { getPreview } from "../../lib/api";
import { authHeaders } from "../../lib/config";
import { haptics } from "../../lib/haptics";
import { hostRouteMatches, previewForConfig } from "../../lib/hostRoute";
import { BrowserLivePane } from "../../lib/preview/BrowserLivePane";
import { previewModeMenu, type PreviewMode } from "../../lib/preview/previewModeMenu";
import { HostScope, useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { iconSize, space, type } from "../../lib/tokens";
import { Button, EmptyState } from "../../lib/ui";
import { userFacingError } from "../../lib/connectionError";

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
	const styles = useThemedStyles(makeStyles);
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
	const onBrowserDisabled = useCallback(() => {
		setDisabledStream({ id: sessionID, hostId: routeHostId });
	}, [routeHostId, sessionID]);

	useLayoutEffect(() => {
		navigation.setOptions({ title: title || "Preview" });
	}, [navigation, title]);

	// The choice between the two views, as a native menu in the bar. Memoized:
	// each new element re-registers the header's items, and the page re-renders
	// on every preview poll.
	const mode: PreviewMode = responsive ? "app" : "browser";
	const showMenu = hostMatches && Boolean(config);
	const headerItems = useMemo(() => showMenu ? (
		<Stack.Toolbar placement="right">
			{previewModeMenu({ mode, appAvailable: canShowResponsive, onSelect: setActive })}
		</Stack.Toolbar>
	) : null, [canShowResponsive, mode, showMenu]);

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

	if (!hostMatches) return <View style={styles.center}><EmptyState icon="globe" title="Preview belongs to another machine" message="Open it from that machine's session." action={<Button title="Open board" icon="workers" onPress={() => router.navigate("/")} />} /></View>;
	if (!config && connection === "closed") return <View style={styles.center}><EmptyState icon="wifi-off" title="Machine offline" message="This preview loads once the app reconnects." /></View>;
	if (!config) return <View style={styles.center}><ActivityIndicator color={t.accent} /><Text style={styles.copy}>Connecting to this machine…</Text></View>;

	return <View style={styles.screen}>
		{headerItems}
		{refreshingResponsive ? <View style={styles.center}><ActivityIndicator color={t.accent} /><Text style={styles.copy}>Loading mobile preview…</Text></View> : !responsive || !preview ? <BrowserLivePane sessionID={sessionID} onDisabled={onBrowserDisabled} /> : <View style={styles.appPane}>
			<View style={styles.appBar}>
				<Feather name="globe" size={iconSize.sm} color={t.textTertiary} />
				<Text numberOfLines={1} style={styles.appPath}>{preview.entry}</Text>
				<Pressable accessibilityRole="button" accessibilityLabel="Reload app preview" hitSlop={10} onPress={() => { haptics.tap(); web.current?.reload(); }} style={styles.appAction}>
					<Feather name="refresh-cw" size={iconSize.sm} color={t.textSecondary} />
				</Pressable>
			</View>
			<WebView
				ref={web}
				source={{ uri: preview.url, headers: preview.authenticated && config ? authHeaders(config) : undefined }}
				style={styles.web}
				startInLoadingState
				renderLoading={() => <View style={styles.webLoading}><ActivityIndicator color={t.accent} /></View>}
				onLoadStart={() => setPreviewError(undefined)}
				onHttpError={(event) => setPreviewError(previewHttpErrorCopy(event.nativeEvent.statusCode))}
				onError={(event) => setPreviewError(event.nativeEvent.description || "Couldn't load this preview.")}
			/>
			{previewError ? <View accessibilityRole="alert" style={styles.webError}>
				<Feather name="alert-triangle" size={iconSize.sm} color={t.red} />
				<Text style={styles.webErrorText}>{previewError}</Text>
				<Pressable onPress={() => { haptics.tap(); setPreviewError(undefined); web.current?.reload(); }}><Text style={styles.retryText}>Retry</Text></Pressable>
			</View> : null}
		</View>}
	</View>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", gap: space.md, paddingHorizontal: space.xxxl, backgroundColor: t.bgBase },
	copy: { color: t.textSecondary, fontFamily: "Geist_400Regular", fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, textAlign: "center" },
	appPane: { flex: 1, backgroundColor: t.bgBase },
	appBar: { minHeight: 48, flexDirection: "row", alignItems: "center", gap: space.sm, paddingLeft: space.md, paddingRight: space.xs, borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: t.borderSubtle, backgroundColor: t.bgSurface },
	appPath: { flex: 1, color: t.textSecondary, fontFamily: "Geist_400Regular", fontSize: type.footnote.fontSize },
	appAction: { width: 42, height: 42, alignItems: "center", justifyContent: "center", borderRadius: 12 },
	web: { flex: 1, backgroundColor: t.bgBase },
	webLoading: { ...StyleSheet.absoluteFill, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	webError: { position: "absolute", left: 12, right: 12, bottom: 16, minHeight: 48, flexDirection: "row", alignItems: "center", gap: space.sm, borderRadius: 12, borderCurve: "continuous", borderWidth: 1, borderColor: t.tintRed, backgroundColor: t.bgElevated, paddingHorizontal: space.md, paddingVertical: space.sm },
	webErrorText: { flex: 1, color: t.textSecondary, fontFamily: "Geist_400Regular", fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
	retryText: { color: t.accent, fontFamily: "Geist_600SemiBold", fontSize: type.caption1.fontSize, fontWeight: "600" },
});

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

function previewHttpErrorCopy(status: number): string {
	if (status === 404 || status === 410) return "This page wasn't found. The agent may have moved or removed it.";
	if (status === 401 || status === 403) return "This page needs access this phone doesn't have.";
	if (status >= 500) return "The page's server hit an error. Check that the agent's dev server is running, then retry.";
	return "This page didn't load. Retry, or check it on that machine.";
}
