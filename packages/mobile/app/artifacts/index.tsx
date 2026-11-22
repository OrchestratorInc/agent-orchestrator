import { Stack, useRouter } from "expo-router";
import { useCallback, useMemo, useState } from "react";
import { FlatList, Pressable, RefreshControl, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { formatTimeTerse } from "../../../product-ui/src/formatting";
import { artifactIndexRows, filterArtifactRows, type ArtifactIndexRow } from "../../lib/artifactsIndex";
import { haptics } from "../../lib/haptics";
import { Feather, type FeatherIconName } from "../../lib/icons";
import { refreshControlColors } from "../../lib/refreshControlColors";
import { sessionTitle } from "../../lib/sessionStatus";
import { useApp } from "../../lib/store";
import { textSelectionColors } from "../../lib/textSelection";
import type { Theme } from "../../lib/theme";
import { useTheme, useThemedStyles } from "../../lib/ThemeProvider";
import { font } from "../../lib/tokens";
import { useRefreshActions } from "../../lib/useRefreshActions";

export { RouteErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";

const KIND_ICONS: Record<ArtifactIndexRow["kind"], FeatherIconName> = { html: "globe", markdown: "file-text", file: "file" };

/**
 * Every session's artifacts in one list, as the desktop sidebar's Artifacts
 * page: newest change first, each with the session and project it came from,
 * and a search over all three. A row opens the file in the artifact viewer.
 */
export default function ArtifactsPage() {
	const router = useRouter();
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const insets = useSafeAreaInsets();
	const app = useApp();
	const [query, setQuery] = useState("");
	const { refreshing, onRefresh } = useRefreshActions(app.refreshAll);

	const rows = useMemo(() => {
		const projects = new Map(app.allProjects.map((project) => [`${project.hostId}:${project.id}`, project.name]));
		return artifactIndexRows(
			app.allSessions.map((session) => ({ id: session.id, hostId: session.hostId, projectId: session.projectId, artifacts: session.artifacts, title: sessionTitle(session) })),
			(hostId, projectId) => projects.get(`${hostId}:${projectId}`),
		);
	}, [app.allProjects, app.allSessions]);
	const shown = useMemo(() => filterArtifactRows(rows, query), [query, rows]);

	const open = useCallback((row: ArtifactIndexRow) => {
		haptics.tap();
		router.push({ pathname: "/artifact/[sessionId]", params: { sessionId: row.sessionId, path: row.path, hostId: row.hostId } });
	}, [router]);

	return (
		<>
			<Stack.Screen options={{ title: "Artifacts" }} />
			<FlatList
				style={styles.page}
				contentContainerStyle={[styles.content, { paddingBottom: insets.bottom + 24 }]}
				contentInsetAdjustmentBehavior="automatic"
				keyboardShouldPersistTaps="handled"
				data={shown}
				keyExtractor={(row) => row.key}
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
					<Pressable
						accessibilityRole="button"
						accessibilityLabel={`${item.name}, from ${item.sessionTitle}${item.projectName ? ` in ${item.projectName}` : ""}`}
						onPress={() => open(item)}
						style={({ pressed }) => [styles.row, pressed && styles.pressed]}
						testID={`artifact-${item.sessionId}-${item.path}`}
					>
						<View style={styles.icon}><Feather name={KIND_ICONS[item.kind]} size={16} color={t.textSecondary} /></View>
						<View style={styles.text}>
							<Text style={styles.name} numberOfLines={1}>{item.name}</Text>
							<Text style={styles.meta} numberOfLines={1}>{[item.sessionTitle, item.projectName].filter(Boolean).join(" · ")}</Text>
						</View>
						<Text style={styles.age}>{formatTimeTerse(item.updatedAt)}</Text>
					</Pressable>
				)}
				ItemSeparatorComponent={() => <View style={styles.separator} />}
				refreshControl={<RefreshControl refreshing={refreshing} onRefresh={onRefresh} {...refreshControlColors(t)} />}
			/>
		</>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	page: { flex: 1, backgroundColor: t.bgBase },
	content: { paddingHorizontal: 16, paddingTop: 8 },
	search: { height: 38, flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12, marginBottom: 12, borderRadius: 10, backgroundColor: t.bgElevated },
	searchInput: { flex: 1, fontFamily: font.regular, fontSize: 15, color: t.textPrimary },
	row: { minHeight: 56, flexDirection: "row", alignItems: "center", gap: 12, paddingHorizontal: 8, paddingVertical: 8, borderRadius: 10 },
	pressed: { backgroundColor: t.bgSubtle },
	icon: { width: 32, height: 32, borderRadius: 8, alignItems: "center", justifyContent: "center", backgroundColor: t.bgElevated },
	text: { flex: 1, minWidth: 0, gap: 2 },
	name: { fontFamily: font.medium, fontSize: 15, color: t.textPrimary },
	meta: { fontFamily: font.regular, fontSize: 13, color: t.textTertiary },
	age: { fontFamily: font.regular, fontSize: 12, color: t.textTertiary },
	separator: { height: StyleSheet.hairlineWidth, marginLeft: 52, backgroundColor: t.borderDefault },
	empty: { alignItems: "center", gap: 8, paddingTop: 80, paddingHorizontal: 32 },
	emptyTitle: { fontFamily: font.medium, fontSize: 16, color: t.textPrimary },
	emptyMessage: { fontFamily: font.regular, fontSize: 14, color: t.textTertiary, textAlign: "center" },
});
