import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, FlatList, RefreshControl, StyleSheet, Text, useWindowDimensions, View, type ViewToken } from "react-native";
import { ArtifactCard } from "../../lib/ArtifactCard";
import { useArtifactActions } from "../../lib/artifact-menu";
import { deliverableArtifacts } from "../../../shared/artifacts/deliverables";
import type { SessionArtifact } from "../../lib/artifacts";
import { haptics } from "../../lib/haptics";
import { hostRouteMatches, machineState } from "../../lib/hostRoute";
import { MachineRejectedState } from "../../lib/MachineRejectedState";
import type { ArtifactsBackend } from "../../lib/artifactsBackend";
import { useArtifactsBackend } from "../../lib/useArtifactsBackend";
import { HostScope, useApp } from "../../lib/store";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { space, type } from "../../lib/tokens";
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
	const app = useApp();
	const { currentHostId } = app;
	const hostMatches = hostRouteMatches(routeHostId, currentHostId);
	// The paired machine's daemon, or Orchestrator Cloud for a cloud session.
	const { backend, cloud, unavailable } = useArtifactsBackend(hostMatches ? sessionId : "");
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { width } = useWindowDimensions();
	const columns = width >= WIDE_LAYOUT ? 3 : 2;
	const { artifacts, loading, error, reload, markDeleted } = useSessionArtifacts(hostMatches ? sessionId : "", backend);
	// A listed file's modified time, so a markdown card's regenerated image is not served from cache.
	// The list shows deliverables only; `artifacts` stays whole for link lookups.
	const listedArtifacts = useMemo(() => (artifacts ? deliverableArtifacts(artifacts) : []), [artifacts]);
	const versionOf = useCallback((filePath: string) => artifacts?.find((file) => file.path === filePath)?.updatedAt, [artifacts]);
	const onDeleted = useCallback((artifact: SessionArtifact) => markDeleted(artifact.path), [markDeleted]);
	const { openMenu, menu } = useArtifactActions(sessionId, backend, onDeleted);
	const [refreshing, setRefreshing] = useState(false);
	// Each pull to refresh lets a card whose preview failed try it again.
	const [refreshes, setRefreshes] = useState(0);

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
			setRefreshes((count) => count + 1);
		}
	}, [reload]);

	if (!hostMatches) return <View style={styles.center}><EmptyState icon={{ ios: "doc.text", android: "description" }} title="These artifacts belong to another machine" message="Open them from that machine's session." action={<Button title="Open board" icon={{ ios: "square.grid.2x2", android: "dashboard" }} onPress={() => router.navigate("/")} />} /></View>;
	if (unavailable) return <View style={styles.center}><EmptyState icon="cloud" title="Artifacts unavailable" message={unavailable} /></View>;
	// Not for a connection an app switch closed: that one reconnects. A machine
	// that refused this phone says so rather than spinning (see machineState).
	const machine = !backend && !cloud ? machineState(app) : "ok";
	if (machine === "rejected") return <View style={styles.center}><MachineRejectedState /></View>;
	if (machine === "offline") return <View style={styles.center}><EmptyState icon="wifi-off" title="Machine offline" message="Artifacts load once the app reconnects." /></View>;
	if (!artifacts && (loading || !backend)) return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	if (!artifacts) return <View style={styles.center}><EmptyState icon="alert-triangle" title="Couldn't load artifacts" message={error} action={<Button title="Try again" icon="refresh-cw" onPress={() => void onRefresh()} />} /></View>;

	return (
		<>
			<FlatList
				key={columns}
				style={styles.screen}
				contentInsetAdjustmentBehavior="automatic"
				contentContainerStyle={[styles.content, listedArtifacts.length === 0 && styles.emptyContent]}
				data={listedArtifacts}
				numColumns={columns}
				columnWrapperStyle={listedArtifacts.length ? styles.row : undefined}
				keyExtractor={(artifact) => artifact.path}
				onViewableItemsChanged={onViewableItemsChanged}
				viewabilityConfig={viewabilityConfig}
				refreshControl={<RefreshControl refreshing={refreshing} onRefresh={() => void onRefresh()} tintColor={t.accent} />}
				ListHeaderComponent={title ? <Text numberOfLines={1} style={styles.caption}>{title}</Text> : null}
				ListEmptyComponent={<EmptyState icon={{ ios: "doc.text", android: "description" }} title="No artifacts yet" message="Pages and documents the agent writes for this session show up here." />}
				renderItem={({ item }) => (
					<View style={{ width: `${100 / columns}%`, paddingHorizontal: space.xs }}>
						<ArtifactCard artifact={item} backend={backend} sessionId={sessionId} visible={visiblePaths.has(item.path)} refreshes={refreshes} versionOf={versionOf} onOpen={() => open(item)} onMenu={item.bundle ? undefined : () => openMenu(item, { onOpen: () => open(item), onAsk: () => ask(item), onHistory: () => openHistory(item) })} />
					</View>
				)}
			/>
			{menu}
		</>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	content: { paddingHorizontal: space.lg - space.xs, paddingTop: space.md, paddingBottom: space.xxxl },
	emptyContent: { flexGrow: 1, justifyContent: "center" },
	row: { marginBottom: space.md },
	caption: { fontFamily: "Geist_500Medium", color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, paddingHorizontal: space.xs, marginBottom: space.md },
	center: { flex: 1, alignItems: "center", justifyContent: "center", paddingHorizontal: space.xxxl, backgroundColor: t.bgBase },
});

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
