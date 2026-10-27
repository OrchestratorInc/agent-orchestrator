import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { ActivityIndicator, Image, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { WebView } from "react-native-webview";
import { artifactFileUrl, getArtifactText } from "../../lib/api";
import { useArtifactActions } from "../../lib/artifact-menu";
import { artifactLanguage, artifactViewKind, formatArtifactSize, looksBinary, MAX_ARTIFACT_TEXT_BYTES, type SessionArtifact } from "../../lib/artifacts";
import { backOr } from "../../lib/backNavigation";
import { HighlightedCodeText } from "../../lib/chat/HighlightedCodeText";
import { ChatMarkdown } from "../../lib/chat/ChatMarkdown";
import { authHeaders, type ServerConfig } from "../../lib/config";
import { userFacingError } from "../../lib/connectionError";
import { headerActionStyle, headerGlyphStyle } from "../../lib/headerAction";
import { hostRouteMatches } from "../../lib/hostRoute";
import { Feather } from "../../lib/icons";
import { HostScope, useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { iconSize, space, type } from "../../lib/tokens";
import { Button, EmptyState } from "../../lib/ui";
import { useSessionArtifacts } from "../../lib/useSessionArtifacts";

// Above this, a text file shows as plain text: a full grammar pass over it
// would stall the JS thread.
const MAX_HIGHLIGHTED_BYTES = 100 * 1024;

/**
 * One artifact, full screen: the phone's counterpart of the desktop's artifact
 * viewer. Opened from the gallery, or from a worker report's file link. A file
 * the session does not list (the list is capped) still opens, as the page the
 * daemon renders, without the actions menu.
 */
export default function ArtifactViewerScreen() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><ArtifactViewerContent /></HostScope> : <ArtifactViewerContent />;
}

function ArtifactViewerContent() {
	const { sessionId: rawId, path: rawPath, hostId: routeHostId } = useLocalSearchParams<{ sessionId: string; path?: string; hostId?: string }>();
	const sessionId = String(rawId ?? "");
	const path = String(rawPath ?? "");
	const navigation = useNavigation();
	const router = useRouter();
	const { config, currentHostId, connection } = useApp();
	const hostMatches = hostRouteMatches(routeHostId, currentHostId);
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { artifacts, loading } = useSessionArtifacts(hostMatches ? sessionId : "");
	const artifact = artifacts?.find((candidate) => candidate.path === path);
	const leave = useCallback(() => backOr(router), [router]);
	const { openMenu, menu } = useArtifactActions(sessionId, leave);

	useLayoutEffect(() => {
		navigation.setOptions({
			title: artifact?.name ?? path.split("/").pop() ?? "Artifact",
			headerRight: artifact
				? () => (
					<Pressable accessibilityRole="button" accessibilityLabel="Artifact actions" hitSlop={10} onPress={() => openMenu(artifact, { inViewer: true })} style={headerActionStyle}>
						<Feather name="more-horizontal" size={iconSize.md} color={t.textSecondary} style={headerGlyphStyle} />
					</Pressable>
				)
				: undefined,
		});
	}, [artifact, navigation, openMenu, path, t.textSecondary]);

	if (!hostMatches) return <View style={styles.center}><EmptyState icon="layers" title="This artifact belongs to another machine" message="Open it from that machine's session." action={<Button title="Open board" icon="activity" onPress={() => router.navigate("/")} />} /></View>;
	if (!config && connection === "closed") return <View style={styles.center}><EmptyState icon="wifi-off" title="Machine offline" message="The artifact loads once the app reconnects." /></View>;
	if (!config || (!artifacts && loading)) return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	if (!artifact) {
		const url = artifactFileUrl(config, sessionId, path);
		return url ? <ArtifactWeb config={config} url={url} version="" /> : <Unavailable title="This artifact can't be opened" message="Its path isn't one the phone can request." />;
	}
	return <><ArtifactBody config={config} sessionId={sessionId} artifact={artifact} />{menu}</>;
}

function ArtifactBody({ config, sessionId, artifact }: { config: ServerConfig; sessionId: string; artifact: SessionArtifact }) {
	const kind = artifactViewKind(artifact);
	const url = artifactFileUrl(config, sessionId, artifact.path);
	if (!url) return <Unavailable title="This artifact can't be opened" message="Its path isn't one the phone can request." />;
	// A text file too large to load into the app still shows, as the page the
	// daemon serves.
	const tooLargeForText = artifact.size > MAX_ARTIFACT_TEXT_BYTES;
	if (kind === "html" || kind === "web-media" || ((kind === "markdown" || kind === "text") && tooLargeForText)) {
		return <ArtifactWeb config={config} url={url} version={artifact.updatedAt} />;
	}
	if (kind === "image") return <ArtifactImage config={config} url={url} version={artifact.updatedAt} name={artifact.name} />;
	return <ArtifactText config={config} sessionId={sessionId} artifact={artifact} markdown={kind === "markdown"} />;
}

function ArtifactWeb({ config, url, version }: { config: ServerConfig; url: string; version: string }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const web = useRef<WebView>(null);
	const [error, setError] = useState<string>();
	return (
		<View style={styles.screen}>
			<WebView
				// A new version of the file reloads the page.
				key={version}
				ref={web}
				source={{ uri: url, headers: authHeaders(config) }}
				style={styles.web}
				startInLoadingState
				renderLoading={() => <View style={styles.webLoading}><ActivityIndicator color={t.accent} /></View>}
				onLoadStart={() => setError(undefined)}
				onHttpError={(event) => setError(event.nativeEvent.statusCode === 404 ? "The worker no longer has this file." : "This artifact didn't load. Retry, or check it on that machine.")}
				onError={(event) => setError(event.nativeEvent.description || "This artifact didn't load.")}
			/>
			{error ? (
				<View accessibilityRole="alert" style={styles.webError}>
					<Feather name="alert-triangle" size={iconSize.sm} color={t.red} />
					<Text style={styles.webErrorText}>{error}</Text>
					<Pressable onPress={() => { setError(undefined); web.current?.reload(); }}><Text style={styles.retryText}>Retry</Text></Pressable>
				</View>
			) : null}
		</View>
	);
}

function ArtifactImage({ config, url, version, name }: { config: ServerConfig; url: string; version: string; name: string }) {
	const styles = useThemedStyles(makeStyles);
	const [failed, setFailed] = useState(false);
	const [ratio, setRatio] = useState(1);
	// The version busts the image cache when the agent rewrites the file.
	const uri = `${url}?v=${encodeURIComponent(version)}`;
	useEffect(() => {
		let live = true;
		Image.getSizeWithHeaders(uri, authHeaders(config)).then(({ width, height }) => { if (live && width > 0 && height > 0) setRatio(width / height); }).catch(() => {});
		return () => { live = false; };
	}, [config, uri]);
	if (failed) return <Unavailable title="This image didn't load" message="The phone may not support its format. Open it on the machine instead." />;
	return (
		<ScrollView style={styles.screen} contentContainerStyle={styles.imageContent} maximumZoomScale={4} minimumZoomScale={1} centerContent>
			<Image
				source={{ uri, headers: authHeaders(config) }}
				accessibilityLabel={name}
				accessibilityIgnoresInvertColors
				resizeMode="contain"
				onError={() => setFailed(true)}
				style={[styles.image, { aspectRatio: ratio }]}
			/>
		</ScrollView>
	);
}

type TextState = { key: string; text?: string; error?: string };

function ArtifactText({ config, sessionId, artifact, markdown }: { config: ServerConfig; sessionId: string; artifact: SessionArtifact; markdown: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	// Re-read whenever the poll reports a newer version of the file.
	const key = `${sessionId}|${artifact.path}|${artifact.updatedAt}`;
	const [state, setState] = useState<TextState | null>(null);
	const [attempt, setAttempt] = useState(0);
	useEffect(() => {
		const controller = new AbortController();
		getArtifactText(config, sessionId, artifact.path, { signal: controller.signal })
			.then((text) => setState({ key, text }))
			.catch((cause) => { if (!controller.signal.aborted) setState({ key, error: userFacingError(cause) }); });
		return () => controller.abort();
	}, [artifact.path, attempt, config, key, sessionId]);
	// The previous version stays up while the next one loads.
	const text = state?.text;
	if (text === undefined) {
		if (state?.key === key && state.error) return <Unavailable title="Couldn't load the artifact" message={state.error} action={<Button title="Try again" icon="refresh-cw" onPress={() => setAttempt((n) => n + 1)} />} />;
		return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	}
	if (looksBinary(text)) return <Unavailable title="This file can't be shown on the phone" message={`It isn't text (${formatArtifactSize(artifact.size)}). Open it on the machine instead.`} />;
	if (markdown) {
		return (
			<ScrollView style={styles.screen} contentContainerStyle={styles.document}>
				<ChatMarkdown text={text} />
			</ScrollView>
		);
	}
	return (
		<ScrollView style={styles.screen} contentContainerStyle={styles.code}>
			<HighlightedCodeText
				code={text}
				language={artifactLanguage(artifact.path)}
				// Plain text past the cap: highlighting is skipped the same way it is for streaming code.
				streaming={artifact.size > MAX_HIGHLIGHTED_BYTES}
				style={styles.codeText}
			/>
		</ScrollView>
	);
}

function Unavailable({ title, message, action }: { title: string; message?: string; action?: ReactNode }) {
	const styles = useThemedStyles(makeStyles);
	return <View style={styles.center}><EmptyState icon="file" title={title} message={message} action={action} /></View>;
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", paddingHorizontal: space.xxxl, backgroundColor: t.bgBase },
	web: { flex: 1, backgroundColor: t.bgBase },
	webLoading: { ...StyleSheet.absoluteFill, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	webError: { position: "absolute", left: 12, right: 12, bottom: 16, minHeight: 44, flexDirection: "row", alignItems: "center", gap: space.sm, borderRadius: 12, borderCurve: "continuous", borderWidth: 1, borderColor: t.tintRed, backgroundColor: t.bgElevated, paddingHorizontal: space.md, paddingVertical: space.sm },
	webErrorText: { fontFamily: "Geist_400Regular", flex: 1, color: t.textSecondary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
	retryText: { fontFamily: "Geist_600SemiBold", color: t.accent, fontSize: type.caption1.fontSize, fontWeight: "600" },
	imageContent: { flexGrow: 1, alignItems: "center", justifyContent: "center", padding: space.lg },
	image: { width: "100%" },
	document: { paddingHorizontal: space.lg, paddingTop: space.lg, paddingBottom: space.xxxl },
	code: { padding: space.lg, paddingBottom: space.xxxl },
	codeText: { fontFamily: t.fontMono, color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
});

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
