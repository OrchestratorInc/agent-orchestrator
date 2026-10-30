import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useLayoutEffect, useRef, useState } from "react";
import { ActivityIndicator, FlatList, Image, Pressable, RefreshControl, StyleSheet, Text, useWindowDimensions, View, type ViewToken } from "react-native";
import { ArtifactThumbnail, hasContentThumbnail } from "../../lib/ArtifactThumbnail";
import { artifactImageSource } from "../../lib/api";
import { useArtifactActions } from "../../lib/artifact-menu";
import { artifactExtensionLabel, artifactViewKind, formatArtifactSize, type SessionArtifact } from "../../lib/artifacts";
import { haptics } from "../../lib/haptics";
import { hostRouteMatches } from "../../lib/hostRoute";
import { Feather, type FeatherIconName } from "../../lib/icons";
import { relativeTime } from "../../lib/notificationView";
import { imageSourceForPlatform } from "../../lib/imageSource";
import type { ServerConfig } from "../../lib/config";
import { HostScope, useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { fontScaleCap, press, radius, space, type } from "../../lib/tokens";
import { Button, EmptyState } from "../../lib/ui";
import { useSessionArtifacts } from "../../lib/useSessionArtifacts";

/** Session-scoped counterpart of the desktop's Artifacts inspector tab. */
export default function SessionArtifactsScreen() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><SessionArtifactsContent /></HostScope> : <SessionArtifactsContent />;
}

// Cards reflow to three across once a column would be wider than a phone's half.
const WIDE_LAYOUT = 600;

function SessionArtifactsContent() {
	const { sessionId: rawId, title, hostId: routeHostId } = useLocalSearchParams<{ sessionId: string; title?: string; hostId?: string }>();
	const sessionId = String(rawId ?? "");
	const navigation = useNavigation();
	const router = useRouter();
	const { config, currentHostId, connection } = useApp();
	const hostMatches = hostRouteMatches(routeHostId, currentHostId);
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { width } = useWindowDimensions();
	const columns = width >= WIDE_LAYOUT ? 3 : 2;
	const { artifacts, loading, error, reload, markDeleted } = useSessionArtifacts(hostMatches ? sessionId : "");
	const onDeleted = useCallback((artifact: SessionArtifact) => markDeleted(artifact.path), [markDeleted]);
	const { openMenu, menu } = useArtifactActions(sessionId, onDeleted);
	const [refreshing, setRefreshing] = useState(false);

	useLayoutEffect(() => {
		navigation.setOptions({ title: "Artifacts" });
	}, [navigation]);

	const open = useCallback((artifact: SessionArtifact) => {
		haptics.tap();
		router.push({ pathname: "/artifact/[sessionId]", params: { sessionId, path: artifact.path, hostId: routeHostId } });
	}, [routeHostId, router, sessionId]);

	// Ask the agent opens the file with a comment on all of it already open.
	const ask = useCallback((artifact: SessionArtifact) => {
		router.push({ pathname: "/artifact/[sessionId]", params: { sessionId, path: artifact.path, hostId: routeHostId, ask: "1" } });
	}, [routeHostId, router, sessionId]);

	const openHistory = useCallback((artifact: SessionArtifact) => {
		router.push({ pathname: "/sheets/artifact-history", params: { sessionId, path: artifact.path, name: artifact.name, hostId: routeHostId, origin: "gallery" } });
	}, [routeHostId, router, sessionId]);

	// Previews load only for cards on screen; FlatList wants these callbacks stable.
	const [visiblePaths, setVisiblePaths] = useState<ReadonlySet<string>>(() => new Set());
	const onViewableItemsChanged = useRef(({ viewableItems }: { viewableItems: ViewToken<SessionArtifact>[] }) => {
		setVisiblePaths(new Set(viewableItems.flatMap((token) => token.item ? [token.item.path] : [])));
	}).current;
	const viewabilityConfig = useRef({ itemVisiblePercentThreshold: 1, minimumViewTime: 120 }).current;

	const onRefresh = useCallback(async () => {
		haptics.tap();
		setRefreshing(true);
		try {
			await reload();
		} finally {
			setRefreshing(false);
		}
	}, [reload]);

	if (!hostMatches) return <View style={styles.center}><EmptyState icon="layers" title="These artifacts belong to another machine" message="Open them from that machine's session." action={<Button title="Open board" icon="activity" onPress={() => router.navigate("/")} />} /></View>;
	if (!config && connection === "closed") return <View style={styles.center}><EmptyState icon="wifi-off" title="Machine offline" message="Artifacts load once the app reconnects." /></View>;
	if (!artifacts && (loading || !config)) return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	if (!artifacts) return <View style={styles.center}><EmptyState icon="alert-triangle" title="Couldn't load artifacts" message={error} action={<Button title="Try again" icon="refresh-cw" onPress={() => void onRefresh()} />} /></View>;

	return (
		<>
			<FlatList
				key={columns}
				style={styles.screen}
				contentContainerStyle={[styles.content, artifacts.length === 0 && styles.emptyContent]}
				data={artifacts}
				numColumns={columns}
				columnWrapperStyle={artifacts.length ? styles.row : undefined}
				keyExtractor={(artifact) => artifact.path}
				onViewableItemsChanged={onViewableItemsChanged}
				viewabilityConfig={viewabilityConfig}
				refreshControl={<RefreshControl refreshing={refreshing} onRefresh={() => void onRefresh()} tintColor={t.accent} />}
				ListHeaderComponent={title ? <Text numberOfLines={1} style={styles.caption}>{title}</Text> : null}
				ListEmptyComponent={<EmptyState icon="layers" title="No artifacts yet" message="Pages and documents the agent writes for this session show up here." />}
				renderItem={({ item }) => (
					<View style={{ width: `${100 / columns}%`, paddingHorizontal: space.xs }}>
						<ArtifactCard artifact={item} config={config} sessionId={sessionId} visible={visiblePaths.has(item.path)} thumbnail={config && artifactViewKind(item) === "image" ? artifactImageSource(config, sessionId, item.path, item.updatedAt) : undefined} onOpen={() => open(item)} onMenu={() => openMenu(item, { onOpen: () => open(item), onAsk: () => ask(item), onHistory: () => openHistory(item) })} />
					</View>
				)}
			/>
			{menu}
		</>
	);
}

function ArtifactCard({ artifact, config, sessionId, visible, thumbnail, onOpen, onMenu }: {
	artifact: SessionArtifact;
	config: ServerConfig | null;
	sessionId: string;
	/** On screen: the card's preview of its contents may load. */
	visible: boolean;
	/** An image artifact's own picture, drawn over its icon tile. */
	thumbnail?: { uri: string; headers: Record<string, string> };
	onOpen(): void;
	onMenu(): void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	// The URI carries the file's modified time, so a rewrite loads afresh.
	const [failedUri, setFailedUri] = useState<string>();
	const picture = thumbnail && failedUri !== thumbnail.uri ? thumbnail : undefined;
	// A card that previews its contents shows a plain tile while the preview
	// loads, and its icon only if the preview cannot load.
	const [previewFailedAt, setPreviewFailedAt] = useState<string>();
	const preview = Boolean(config && hasContentThumbnail(artifact)) && previewFailedAt !== artifact.updatedAt;
	const onPreviewFailed = useCallback(() => setPreviewFailedAt(artifact.updatedAt), [artifact.updatedAt]);
	const visual = cardVisual(artifact, t);
	const age = relativeTime(artifact.updatedAt);
	const meta = [age, formatArtifactSize(artifact.size)].filter(Boolean).join(" · ");
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`${artifact.name}, ${meta}`}
			accessibilityHint="Opens the artifact. Long-press for more actions."
			accessibilityActions={[{ name: "longpress", label: "More actions" }]}
			onAccessibilityAction={(event) => { if (event.nativeEvent.actionName === "longpress") onMenu(); }}
			onPress={onOpen}
			onLongPress={onMenu}
			delayLongPress={350}
			style={styles.card}
		>
			{({ pressed }) => (<>
				<View style={[styles.poster, { backgroundColor: preview ? t.bgSubtle : visual.tint }]}>
					{preview ? null : <>
						<Feather name={visual.icon} size={28} color={visual.color} />
						<Text maxFontSizeMultiplier={fontScaleCap.body} style={[styles.extension, { color: visual.color }]}>{artifactExtensionLabel(artifact.path)}</Text>
					</>}
					{preview && config ? <ArtifactThumbnail config={config} sessionId={sessionId} artifact={artifact} visible={visible} onFailed={onPreviewFailed} /> : null}
					{picture ? (
						<Image
							accessibilityIgnoresInvertColors
							source={imageSourceForPlatform(picture)}
							resizeMode="cover"
							onError={() => setFailedUri(picture.uri)}
							style={StyleSheet.absoluteFill}
						/>
					) : null}
				</View>
				<View style={styles.label}>
					<Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={1} style={styles.name}>{artifact.name}</Text>
					{meta ? <Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={1} style={styles.meta}>{meta}</Text> : null}
				</View>
				{/* Pressed, the card dims under a veil: an Android WebView does not fade
				    with a parent's opacity, and fading one can blank it for a frame. */}
				{pressed ? <View pointerEvents="none" style={styles.pressed} /> : null}
			</>)}
		</Pressable>
	);
}

/** Each kind of file gets its own glyph and hue, so a grid of them scans at a glance. */
function cardVisual(artifact: SessionArtifact, t: Theme): { icon: FeatherIconName; color: string; tint: string } {
	switch (artifactViewKind(artifact)) {
		case "html": return { icon: "globe", color: t.blue, tint: t.tintBlue };
		case "markdown": return { icon: "file-text", color: t.purple, tint: t.tintPurple };
		case "image": return { icon: "image", color: t.green, tint: t.tintGreen };
		case "web-media": return { icon: /\.svg$/i.test(artifact.path) ? "image" : "play", color: t.amber, tint: t.tintAmber };
		case "text": return { icon: "code", color: t.orange, tint: t.tintOrange };
	}
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	content: { paddingHorizontal: space.lg - space.xs, paddingTop: space.md, paddingBottom: space.xxxl },
	emptyContent: { flexGrow: 1, justifyContent: "center" },
	row: { marginBottom: space.md },
	caption: { fontFamily: "Geist_500Medium", color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, paddingHorizontal: space.xs, marginBottom: space.md },
	center: { flex: 1, alignItems: "center", justifyContent: "center", paddingHorizontal: space.xxxl, backgroundColor: t.bgBase },
	card: { overflow: "hidden", borderRadius: radius.lg, borderCurve: "continuous", backgroundColor: t.bgElevated, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle },
	poster: { aspectRatio: 4 / 3, alignItems: "center", justifyContent: "center", gap: space.xs, overflow: "hidden" },
	pressed: { ...StyleSheet.absoluteFill, backgroundColor: t.bgBase, opacity: 1 - press.opacity },
	extension: { fontFamily: t.fontMono, fontSize: type.caption2.fontSize, fontWeight: "600", letterSpacing: 0.4 },
	label: { paddingHorizontal: space.md, paddingVertical: space.sm, gap: space.hair },
	name: { fontFamily: "Geist_500Medium", color: t.textPrimary, fontSize: type.subheadline.fontSize, fontWeight: "500" },
	meta: { fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.caption2.fontSize, lineHeight: type.caption2.lineHeight },
});

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
