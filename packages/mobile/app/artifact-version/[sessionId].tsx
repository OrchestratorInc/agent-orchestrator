import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, ScrollView, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { WebView } from "react-native-webview";
import { getArtifactVersion, restoreArtifactVersion } from "../../lib/api";
import { artifactLanguage, artifactViewKind, shownText } from "../../lib/artifacts";
import { inlineArtifactPage } from "../../lib/artifactPageInliner";
import { formatVersionTime, versionPageHtml, type ArtifactVersion } from "../../lib/artifactVersions";
import { backOr } from "../../lib/backNavigation";
import { ChatMarkdown } from "../../lib/chat/ChatMarkdown";
import { HighlightedCodeText } from "../../lib/chat/HighlightedCodeText";
import { useRenderTheme } from "../../lib/chat/RenderFrame";
import type { ServerConfig } from "../../lib/config";
import { userFacingError } from "../../lib/connectionError";
import { haptics } from "../../lib/haptics";
import { hostRouteMatches } from "../../lib/hostRoute";
import { HostScope, useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { space, type } from "../../lib/tokens";
import { Button, EmptyState } from "../../lib/ui";
import { artifactAssetLoader } from "../../lib/useInlinedArtifactPage";

// Past this, a version shows as plain text: a full grammar pass would stall the JS thread.
const MAX_HIGHLIGHTED_CHARS = 100 * 1024;

/**
 * One saved version of an artifact, read-only, with the two ways out: restore
 * it, or go back to the current file. Markdown renders and an HTML page shows
 * as the page, as the viewer shows the current file, so a version can be told
 * apart at a glance. A truncated page, and anything else, shows as source.
 */
export default function ArtifactVersionScreen() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><ArtifactVersionContent /></HostScope> : <ArtifactVersionContent />;
}

function ArtifactVersionContent() {
	const { sessionId: rawId, path: rawPath, version: rawVersion, hostId: routeHostId } = useLocalSearchParams<{ sessionId: string; path?: string; version?: string; hostId?: string }>();
	const sessionId = String(rawId ?? "");
	const path = String(rawPath ?? "");
	const version = Number(rawVersion);
	const navigation = useNavigation();
	const router = useRouter();
	const insets = useSafeAreaInsets();
	const { config, currentHostId, refresh } = useApp();
	const hostMatches = hostRouteMatches(routeHostId, currentHostId);
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [loaded, setLoaded] = useState<ArtifactVersion>();
	const [loadError, setLoadError] = useState<string>();
	const [attempt, setAttempt] = useState(0);
	const [restoring, setRestoring] = useState(false);
	const [restoreError, setRestoreError] = useState<string>();
	const now = useRef(new Date()).current;
	const valid = Boolean(sessionId && path && Number.isInteger(version) && version > 0);

	useLayoutEffect(() => {
		navigation.setOptions({ title: path.split("/").pop() || "Version" });
	}, [navigation, path]);

	useEffect(() => {
		if (!config || !hostMatches || !valid) return;
		const controller = new AbortController();
		setLoadError(undefined);
		getArtifactVersion(config, sessionId, path, version, { signal: controller.signal })
			.then(setLoaded)
			.catch((cause) => { if (!controller.signal.aborted) setLoadError(userFacingError(cause, "Couldn't load this version.")); });
		return () => controller.abort();
	}, [attempt, config, hostMatches, path, sessionId, valid, version]);

	const restore = useCallback(async () => {
		if (!config) return;
		setRestoring(true);
		setRestoreError(undefined);
		try {
			await restoreArtifactVersion(config, sessionId, path, version);
			haptics.success();
			// The file's new modified time reloads the viewer behind this screen.
			void refresh().catch(() => {});
			backOr(router);
		} catch (cause) {
			haptics.error();
			setRestoreError(userFacingError(cause, "Couldn't restore this version."));
		} finally {
			setRestoring(false);
		}
	}, [config, path, refresh, router, sessionId, version]);

	if (!hostMatches) return <View style={styles.center}><EmptyState icon={{ ios: "doc.text", android: "description" }} title="This artifact belongs to another machine" message="Open it from that machine's session." /></View>;
	if (!valid) return <View style={styles.center}><EmptyState icon="history" title="This version can't be opened" message="Open it again from the artifact's history." /></View>;
	if (!loaded && loadError) return <View style={styles.center}><EmptyState icon="alert-triangle" title="Couldn't load this version" message={loadError} action={<Button title="Try again" icon="refresh-cw" onPress={() => setAttempt((n) => n + 1)} />} /></View>;
	if (!loaded) return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;

	const markdown = artifactViewKind({ path, kind: "file" }) === "markdown";
	// A long version shows its start, as the viewer does: a text view is laid out whole.
	const shown = shownText(loaded.content);
	// Only the path is known here; the daemon calls .html and .htm files pages.
	const page = /\.html?$/i.test(path) && !loaded.truncated;
	return (
		<View style={styles.screen}>
			<View style={styles.banner}>
				<Text style={styles.bannerText}>Viewing the version from {formatVersionTime(loaded.createdAt, now) || `version ${loaded.version}`}</Text>
			</View>
			{loaded.truncated || shown.truncated ? (
				<View style={styles.truncated}><Text style={styles.truncatedText}>Showing the start of this version. Restoring it writes the whole file.</Text></View>
			) : null}
			{page && config ? (
				<VersionPage config={config} sessionId={sessionId} path={path} version={loaded} />
			) : (
				<ScrollView style={styles.screen} contentContainerStyle={markdown ? styles.document : styles.code}>
					{markdown ? (
						<ChatMarkdown text={shown.text} />
					) : (
						<HighlightedCodeText code={shown.text} language={artifactLanguage(path)} streaming={shown.text.length > MAX_HIGHLIGHTED_CHARS} style={styles.codeText} />
					)}
				</ScrollView>
			)}
			<View style={[styles.bar, { paddingBottom: Math.max(insets.bottom, space.md) }]}>
				{restoreError ? <Text accessibilityRole="alert" style={styles.error}>{restoreError}</Text> : null}
				<View style={styles.actions}>
					<Button title="Back to current" variant="ghost" onPress={() => backOr(router)} style={styles.action} />
					<Button title={restoring ? "Restoring" : "Restore this version"} icon={{ ios: "arrow.counterclockwise", android: "restore" }} loading={restoring} disabled={restoring} onPress={() => void restore()} style={styles.action} />
				</View>
			</View>
		</View>
	);
}

/**
 * A saved HTML version drawn as a page. The version is stored raw, so AO's
 * theme is written in here, and the files it links are inlined from the
 * artifact folder as for the live page (they are today's files, not the
 * version's). If inlining fails, the page shows without them.
 */
function VersionPage({ config, sessionId, path, version }: { config: ServerConfig; sessionId: string; path: string; version: ArtifactVersion }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const theme = useRenderTheme();
	const themed = useMemo(() => versionPageHtml(version.content, theme), [theme, version.content]);
	const [inlined, setInlined] = useState<{ from: string; html: string }>();
	useEffect(() => {
		let live = true;
		inlineArtifactPage(themed, path, artifactAssetLoader(config, sessionId), sessionId)
			.then((result) => { if (live) setInlined({ from: themed, html: result.html }); })
			.catch(() => { if (live) setInlined({ from: themed, html: themed }); });
		return () => { live = false; };
	}, [config, path, sessionId, themed]);
	const html = inlined?.from === themed ? inlined.html : undefined;
	if (!html) return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	return (
		<WebView
			// Loaded from memory with no origin: it cannot reach the daemon.
			source={{ html }}
			style={styles.web}
			setSupportMultipleWindows={false}
			javaScriptCanOpenWindowsAutomatically={false}
			// A version is read-only: links inside it do not navigate.
			onShouldStartLoadWithRequest={(request) => request.isTopFrame === false || /^(about:|data:)/.test(request.url)}
		/>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", paddingHorizontal: space.xxxl, backgroundColor: t.bgBase },
	banner: { paddingHorizontal: space.lg, paddingVertical: space.sm, borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: t.borderSubtle, backgroundColor: t.bgSurface },
	bannerText: { fontFamily: "Geist_500Medium", color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight, fontWeight: "500" },
	truncated: { paddingHorizontal: space.lg, paddingVertical: space.sm, backgroundColor: t.tintAmber },
	truncatedText: { fontFamily: "Geist_400Regular", color: t.amber, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
	web: { flex: 1, backgroundColor: t.bgBase },
	document: { paddingHorizontal: space.lg, paddingTop: space.lg, paddingBottom: space.xxxl },
	code: { padding: space.lg, paddingBottom: space.xxxl },
	codeText: { fontFamily: t.fontMono, color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	bar: { gap: space.sm, paddingHorizontal: space.lg, paddingTop: space.md, borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: t.borderSubtle, backgroundColor: t.bgSurface },
	actions: { flexDirection: "row", gap: space.sm },
	action: { flex: 1 },
	error: { fontFamily: "Geist_400Regular", color: t.red, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
});

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
