import { Stack, useRouter } from "expo-router";
import { memo, useCallback, useMemo, useRef, useState } from "react";
import { FlatList, RefreshControl, StyleSheet, Text, TextInput, useWindowDimensions, View, type ViewToken } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { ArtifactCard, ARTIFACT_GRID_WINDOW, ARTIFACT_VIEWABILITY } from "../../lib/ArtifactCard";
import { artifactIndexRows, filterArtifactRows, type ArtifactIndexRow } from "../../lib/artifactsIndex";
import { daemonArtifactsBackend, type ArtifactsBackend } from "../../lib/artifactsBackend";
import { haptics } from "../../lib/haptics";
import { Feather } from "../../lib/icons";
import { relativeTime } from "../../lib/notificationView";
import { refreshControlColors } from "../../lib/refreshControlColors";
import { sessionTitle } from "../../lib/sessionStatus";
import { useApp } from "../../lib/store";
import { textSelectionColors } from "../../lib/textSelection";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { font, space } from "../../lib/tokens";
import { useRefreshActions } from "../../lib/useRefreshActions";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

// Cards reflow to three across once a column would be wider than a phone's half.
const WIDE_LAYOUT = 600;

/**
 * Every session's artifacts in one grid, as the desktop sidebar's Artifacts
 * page: a card per file with a preview of it, its name, and where it is and
 * how long ago it changed; newest first, with a search over the file, its
 * session and its project. A card opens the file in the artifact viewer.
 */
export default function ArtifactsPage() {
	const router = useRouter();
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const { width } = useWindowDimensions();
	const columns = width >= WIDE_LAYOUT ? 3 : 2;
	const app = useApp();
	const [query, setQuery] = useState("");
	const { refreshing, onRefresh } = useRefreshActions(app.refreshAll);
	// Each pull to refresh lets a card whose preview failed try it again.
	const [refreshes, setRefreshes] = useState(0);

	const rows = useMemo(() => {
		const projects = new Map(app.allProjects.map((project) => [`${project.hostId}:${project.id}`, project.name]));
		return artifactIndexRows(
			app.allSessions.map((session) => ({ id: session.id, hostId: session.hostId, hostName: session.hostName, projectId: session.projectId, artifacts: session.artifacts, outputType: session.outputType, title: sessionTitle(session) })),
			(hostId, projectId) => projects.get(`${hostId}:${projectId}`),
		);
	}, [app.allProjects, app.allSessions]);
	const shown = useMemo(() => filterArtifactRows(rows, query), [query, rows]);

	// One backend per paired machine: a card reads its file from its own machine.
	const backends = useMemo(() => {
		const map = new Map<string, ArtifactsBackend>();
		for (const host of app.hostStates) if (host.config) map.set(host.hostId, daemonArtifactsBackend(host.config));
		return map;
	}, [app.hostStates]);

	// Previews load only for cards on screen; FlatList wants these callbacks stable.
	const [visibleKeys, setVisibleKeys] = useState<ReadonlySet<string>>(() => new Set());
	const onViewableItemsChanged = useRef(({ viewableItems }: { viewableItems: ViewToken<ArtifactIndexRow>[] }) => {
		setVisibleKeys(new Set(viewableItems.flatMap((token) => token.item ? [token.item.key] : [])));
	}).current;
	const viewabilityConfig = useRef(ARTIFACT_VIEWABILITY).current;

	const open = useCallback((row: ArtifactIndexRow) => {
		haptics.tap();
		router.push({ pathname: "/artifact/[sessionId]", params: { sessionId: row.sessionId, path: row.path, hostId: row.hostId } });
	}, [router]);

	const refresh = useCallback(() => {
		setRefreshes((count) => count + 1);
		onRefresh();
	}, [onRefresh]);

	return (
		<>
			<Stack.Screen options={{ title: "Artifacts" }} />
			<FlatList
				key={columns}
				style={styles.page}
				contentContainerStyle={[styles.content, { paddingBottom: insets.bottom + 24 }]}
				contentInsetAdjustmentBehavior="automatic"
				keyboardShouldPersistTaps="handled"
				data={shown}
				numColumns={columns}
				columnWrapperStyle={shown.length ? styles.row : undefined}
				keyExtractor={(row) => row.key}
				onViewableItemsChanged={onViewableItemsChanged}
				viewabilityConfig={viewabilityConfig}
				// Lazy: a few screens of cards are mounted at a time, not the whole list.
				{...ARTIFACT_GRID_WINDOW}
				ListHeaderComponent={rows.length ? (
					<View style={styles.search}>
						<Feather name="search" size={15} color={t.textTertiary} />
						<TextInput
							value={query}
							onChangeText={setQuery}
							placeholder="Search artifacts"
							placeholderTextColor={t.textTertiary}
							returnKeyType="search"
							style={styles.searchInput}
							accessibilityLabel="Search artifacts"
							{...textSelectionColors(t)}
						/>
					</View>
				) : null}
				ListEmptyComponent={(
					<View style={styles.empty}>
						<Feather name="package" size={28} color={t.textTertiary} />
						<Text style={styles.emptyTitle}>{rows.length ? "No matches" : "No artifacts yet"}</Text>
						<Text style={styles.emptyMessage}>{rows.length ? "Try another name, session or project." : "Files your agents make for you show up here."}</Text>
					</View>
				)}
				renderItem={({ item }) => (
					<GridCell row={item} columns={columns} backend={backends.get(item.hostId) ?? null} visible={visibleKeys.has(item.key)} refreshes={refreshes} onOpen={open} />
				)}
				refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} {...refreshControlColors(t)} />}
			/>
		</>
	);
}

/**
 * One card in the grid, redrawn only when its own visibility, file or retry
 * count changes: scrolling changes which cards are on screen, and redrawing
 * every mounted card each time is what made the page feel like it loaded
 * everything at once.
 */
const GridCell = memo(function GridCell({ row, columns, backend, visible, refreshes, onOpen }: {
	row: ArtifactIndexRow;
	columns: number;
	backend: ArtifactsBackend | null;
	visible: boolean;
	refreshes: number;
	onOpen(row: ArtifactIndexRow): void;
}) {
	const versionOf = useCallback((path: string) => row.sessionArtifacts.find((file) => file.path === path)?.updatedAt, [row.sessionArtifacts]);
	return (
		<View style={{ width: `${100 / columns}%`, paddingHorizontal: space.xs }}>
			<ArtifactCard
				artifact={row.artifact}
				backend={backend}
				sessionId={row.sessionId}
				visible={visible}
				refreshes={refreshes}
				versionOf={versionOf}
				meta={[row.hostName, relativeTime(row.updatedAt)].filter(Boolean).join(" · ")}
				onOpen={() => onOpen(row)}
			/>
		</View>
	);
});

const makeStyles = (t: Theme) => StyleSheet.create({
	page: { flex: 1, backgroundColor: t.bgBase },
	content: { paddingHorizontal: space.lg - space.xs, paddingTop: space.sm },
	row: { marginBottom: space.md },
	search: { height: 38, flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12, marginHorizontal: space.xs, marginBottom: space.md, borderRadius: 10, backgroundColor: t.bgElevated },
	searchInput: { flex: 1, fontFamily: font.regular, fontSize: 15, color: t.textPrimary },
	empty: { alignItems: "center", gap: 8, paddingTop: 80, paddingHorizontal: 32 },
	emptyTitle: { fontFamily: font.medium, fontSize: 16, color: t.textPrimary },
	emptyMessage: { fontFamily: font.regular, fontSize: 14, color: t.textTertiary, textAlign: "center" },
});
