import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, FlatList, Pressable, RefreshControl, SectionList, StyleSheet, Text, TextInput, View } from "react-native";
import { userFacingError } from "../../../lib/connectionError";
import { haptics } from "../../../lib/haptics";
import { hostRouteMatches } from "../../../lib/hostRoute";
import { Feather } from "../../../lib/icons";
import { HostScope, useApp } from "../../../lib/store";
import type { Theme } from "../../../lib/theme";
import { textSelectionColors } from "../../../lib/textSelection";
import { useTheme, useThemedStyles } from "../../../lib/ThemeProvider";
import { font, fontScaleCap, iconSize, press, radius, space, touchTarget, type } from "../../../lib/tokens";
import { Button, EmptyState } from "../../../lib/ui";
import { getWorkspaceTree, searchWorkspaceFiles, type WorkspaceSearchResult, type WorkspaceTreeEntry } from "../../../lib/files/api";
import { ChangeRow, FileTypeIcon, FolderTypeIcon, LineCounts, ReviewTray, SegmentedTabs } from "../../../lib/files/components";
import { changeGroups, plural, splitPath, type ChangeEntry } from "../../../lib/files/model";
import { reviewKey } from "../../../lib/files/reviewStore";
import { useReviewSender } from "../../../lib/files/useReviewSender";
import { useWorkspaceManifest } from "../../../lib/files/useWorkspace";

export { RouteErrorBoundary as ErrorBoundary } from "../../../lib/RouteErrorBoundary";

type Tab = "changes" | "all";

/** The session's files: what the agent changed, and the whole worktree to browse. */
export default function SessionFilesScreen() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><SessionFilesContent /></HostScope> : <SessionFilesContent />;
}

function SessionFilesContent() {
	const { sessionId, hostId: routeHostId, tab: initialTab, title } = useLocalSearchParams<{ sessionId: string; hostId?: string; tab?: Tab; title?: string }>();
	const id = String(sessionId ?? "");
	const router = useRouter();
	const navigation = useNavigation();
	const { config, currentHostId, connection, loading: appLoading } = useApp();
	const hostMatches = hostRouteMatches(routeHostId, currentHostId);
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [tab, setTab] = useState<Tab>(initialTab === "all" ? "all" : "changes");
	const { manifest, loading, error, refreshing, refresh } = useWorkspaceManifest(hostMatches ? config : null, id, hostMatches);
	const key = reviewKey(currentHostId, id);
	const review = useReviewSender(config, key, id);
	const groups = useMemo(() => manifest ? changeGroups(manifest) : [], [manifest]);
	const commentsByPath = useMemo(() => {
		const counts = new Map<string, number>();
		for (const comment of review.comments) counts.set(comment.target.path, (counts.get(comment.target.path) ?? 0) + 1);
		return counts;
	}, [review.comments]);

	useLayoutEffect(() => {
		navigation.setOptions({ title: "Files", headerBackTitle: title || "Session" });
	}, [navigation, title]);

	const openFile = useCallback((path: string, mode: "diff" | "file", previousPath?: string) => {
		router.push({ pathname: "/files/[sessionId]/file", params: { sessionId: id, path, mode, previousPath, hostId: routeHostId } });
	}, [id, routeHostId, router]);
	const openChange = useCallback((file: ChangeEntry) => openFile(file.path, "diff", file.previousPath), [openFile]);

	if (!hostMatches) return <View style={styles.center}><EmptyState icon="monitor" title="These files belong to another machine" message="Open them from that machine's session." action={<Button title="Open board" icon="activity" onPress={() => router.navigate("/")} />} /></View>;
	// A cold start (a deep link, a notification) lands before the saved pairing
	// loads; that is not "offline" yet.
	if (!config && appLoading) return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	if (!config && connection === "closed") return <View style={styles.center}><EmptyState icon="wifi-off" title="Machine offline" message="Files load once the app reconnects." /></View>;

	const summary = manifest?.summary;
	const header = (
		<View style={styles.header}>
			<SegmentedTabs<Tab>
				value={tab}
				onChange={setTab}
				options={[
					{ key: "changes", label: "Changes", icon: "file-diff", badge: summary?.files || undefined },
					{ key: "all", label: "All files", icon: "list-tree" },
				]}
			/>
			{tab === "changes" && manifest ? <ChangesSummary manifest={manifest} /> : null}
		</View>
	);

	return (
		<View style={styles.screen}>
			{tab === "changes" ? (
				loading ? (
					<View style={styles.screen}>{header}<View style={styles.center}><ActivityIndicator color={t.accent} /><Text style={styles.copy}>Reading the worktree…</Text></View></View>
				) : (
					<SectionList
						sections={groups.map((group) => ({ key: group.key, title: group.title, data: group.files }))}
						keyExtractor={(file) => file.path}
						ListHeaderComponent={header}
						stickySectionHeadersEnabled={false}
						contentContainerStyle={{ paddingBottom: review.comments.length ? 96 : space.xxl }}
						refreshControl={<RefreshControl refreshing={refreshing} onRefresh={() => void refresh()} tintColor={t.textTertiary} />}
						renderSectionHeader={({ section }) => (
							<View style={styles.sectionHead}>
								<Text accessibilityRole="header" maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.sectionTitle}>{section.title}</Text>
								<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.sectionCount}>{section.data.length}</Text>
							</View>
						)}
						renderItem={({ item }) => <ChangeRow file={item} comments={commentsByPath.get(item.path) ?? 0} onPress={openChange} />}
						ItemSeparatorComponent={() => <View style={styles.divider} />}
						ListEmptyComponent={error ? (
							<EmptyState icon="alert-triangle" title="Couldn't load changes" message={error} action={<Button title="Try again" icon="refresh-cw" variant="ghost" onPress={() => void refresh()} />} />
						) : (
							<EmptyState icon="file-diff" title="No changes yet" message="When the agent edits files in this session, they show up here." action={<Button title="Browse all files" icon="list-tree" variant="ghost" onPress={() => setTab("all")} />} />
						)}
					/>
				)
			) : (
				<FileBrowser sessionId={id} header={header} bottomInset={review.comments.length ? 96 : space.xxl} onOpen={(path, changed) => openFile(path, changed ? "diff" : "file")} />
			)}
			<ReviewTray comments={review.comments} sending={review.sending} error={review.error} onSend={() => void review.send()} onRemove={review.remove} />
		</View>
	);
}

function ChangesSummary({ manifest }: { manifest: NonNullable<ReturnType<typeof useWorkspaceManifest>["manifest"]> }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { summary } = manifest;
	const base = manifest.compareMode === "head_fallback" ? "last commit" : manifest.compareBaseRef || "base branch";
	const notes = [
		manifest.compareMode === "head_fallback" ? "The base branch isn't available, so this compares against the last commit." : null,
		manifest.degraded ? "Git answered slowly; some changes may be missing. Pull to refresh." : null,
		manifest.truncated ? `Only the first ${plural(manifest.files.length, "file")} are listed.` : null,
	].filter((note): note is string => Boolean(note));
	return (
		<View style={styles.summary}>
			<View accessible accessibilityLabel={`${plural(summary.files, "file")} changed against ${base}, ${plural(summary.additions, "line")} added, ${plural(summary.deletions, "line")} removed`} style={styles.summaryRow}>
				<Feather name="git-branch" size={iconSize.sm} color={t.textTertiary} />
				<Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={1} style={styles.summaryText}>{`${plural(summary.files, "file")} vs ${base}`}</Text>
				<LineCounts additions={summary.additions} deletions={summary.deletions} />
			</View>
			{notes.map((note) => (
				<View key={note} style={styles.note}>
					<Feather name="info" size={iconSize.xs} color={t.amber} />
					<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.noteText}>{note}</Text>
				</View>
			))}
		</View>
	);
}

/** The worktree as folders, one level at a time, with a search across every path. */
function FileBrowser({ sessionId, header, bottomInset, onOpen }: { sessionId: string; header: React.ReactElement; bottomInset: number; onOpen: (path: string, changed: boolean) => void }) {
	const { config } = useApp();
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [dir, setDir] = useState("");
	const [tree, setTree] = useState<{ path: string; entries: WorkspaceTreeEntry[]; truncated: boolean } | null>(null);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<string>();
	const [query, setQuery] = useState("");
	const [results, setResults] = useState<WorkspaceSearchResult[] | null>(null);
	const [searching, setSearching] = useState(false);
	const request = useRef(0);

	const loadTree = useCallback(async (path: string) => {
		if (!config) return;
		const ticket = ++request.current;
		setLoading(true);
		setError(undefined);
		try {
			const next = await getWorkspaceTree(config, sessionId, path);
			if (ticket === request.current) setTree({ ...next, entries: [...next.entries].sort((a, b) => a.type === b.type ? a.name.localeCompare(b.name) : a.type === "dir" ? -1 : 1) });
		} catch (cause) {
			if (ticket === request.current) setError(userFacingError(cause, "Couldn't read this folder."));
		} finally {
			if (ticket === request.current) setLoading(false);
		}
	}, [config, sessionId]);

	useEffect(() => { void loadTree(dir); }, [dir, loadTree]);

	useEffect(() => {
		const trimmed = query.trim();
		if (!config || !trimmed) { setResults(null); setSearching(false); return; }
		setSearching(true);
		let cancelled = false;
		const timer = setTimeout(() => {
			searchWorkspaceFiles(config, sessionId, trimmed)
				.then((found) => { if (!cancelled) setResults(found.results); })
				.catch(() => { if (!cancelled) setResults([]); })
				.finally(() => { if (!cancelled) setSearching(false); });
		}, 250);
		return () => { cancelled = true; clearTimeout(timer); };
	}, [config, query, sessionId]);

	const crumbs = dir ? dir.split("/") : [];
	const searchBox = (
		<View style={styles.search}>
			<Feather name="search" size={iconSize.sm} color={t.textTertiary} />
			<TextInput
				value={query}
				onChangeText={setQuery}
				placeholder="Find a file"
				{...textSelectionColors(t)}
				placeholderTextColor={t.textTertiary}
				autoCapitalize="none"
				autoCorrect={false}
				clearButtonMode="while-editing"
				returnKeyType="search"
				accessibilityLabel="Find a file by name or path"
				style={styles.searchInput}
				maxFontSizeMultiplier={fontScaleCap.body}
			/>
			{searching ? <ActivityIndicator size="small" color={t.textTertiary} /> : null}
		</View>
	);
	const breadcrumb = !results ? (
		<View accessibilityRole="toolbar" style={styles.crumbs}>
			<Pressable accessibilityRole="button" accessibilityLabel="Worktree root" disabled={!dir} onPress={() => { haptics.tap(); setDir(""); }} style={styles.crumb}>
				<Feather name="folder" size={iconSize.sm} color={dir ? t.textSecondary : t.textPrimary} />
				<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.crumbText, !dir && styles.crumbTextOn]}>Worktree</Text>
			</Pressable>
			{crumbs.map((part, index) => {
				const target = crumbs.slice(0, index + 1).join("/");
				const last = index === crumbs.length - 1;
				return (
					<View key={target} style={styles.crumbPart}>
						<Feather name="chevron-right" size={iconSize.xs} color={t.textFaint} />
						<Pressable accessibilityRole="button" accessibilityLabel={`Folder ${part}`} disabled={last} onPress={() => { haptics.tap(); setDir(target); }} style={styles.crumb}>
							<Text maxFontSizeMultiplier={fontScaleCap.chrome} numberOfLines={1} style={[styles.crumbText, last && styles.crumbTextOn]}>{part}</Text>
						</Pressable>
					</View>
				);
			})}
			{tree && !loading ? <Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.crumbCount}>{tree.entries.length}</Text> : null}
		</View>
	) : null;

	if (results) {
		return (
			<FlatList
				data={results}
				keyExtractor={(item) => item.path}
				keyboardShouldPersistTaps="handled"
				ListHeaderComponent={<>{header}{searchBox}</>}
				contentContainerStyle={{ paddingBottom: bottomInset }}
				ItemSeparatorComponent={() => <View style={styles.divider} />}
				renderItem={({ item }) => <PathRow path={item.path} status={item.status} onPress={() => onOpen(item.path, item.status !== "unmodified")} />}
				ListEmptyComponent={searching ? null : <EmptyState icon="search" title="No matching files" message={`Nothing in this worktree matches “${query.trim()}”.`} />}
			/>
		);
	}

	return (
		<FlatList
			data={loading ? [] : tree?.entries ?? []}
			keyExtractor={(item) => item.path}
			keyboardShouldPersistTaps="handled"
			ListHeaderComponent={<>{header}{searchBox}{breadcrumb}</>}
			contentContainerStyle={{ paddingBottom: bottomInset }}
			ItemSeparatorComponent={() => <View style={styles.divider} />}
			renderItem={({ item }) => item.type === "dir" ? (
				<Pressable
					accessibilityRole="button"
					accessibilityLabel={`Folder ${item.name}${item.hasChanges ? ", has changes" : ""}`}
					onPress={() => { haptics.tap(); setDir(item.path); }}
					style={({ pressed }) => [styles.entry, pressed && { backgroundColor: t.bgElevatedHover }]}
				>
					<FolderTypeIcon name={item.name} />
					<Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={1} style={styles.entryName}>{item.name}</Text>
					{item.hasChanges ? <View style={styles.changeDot} /> : null}
					<Feather name="chevron-right" size={iconSize.sm} color={t.textFaint} />
				</Pressable>
			) : (
				<PathRow path={item.path} name={item.name} status={item.status} onPress={() => onOpen(item.path, Boolean(item.status && item.status !== "unmodified"))} />
			)}
			ListFooterComponent={tree?.truncated ? <Text style={styles.footNote}>This folder has more entries than the daemon lists. Use Find a file to reach the rest.</Text> : null}
			ListEmptyComponent={loading ? (
				<View style={styles.centerInline}><ActivityIndicator color={t.accent} /></View>
			) : error ? (
				<EmptyState icon="alert-triangle" title="Couldn't read this folder" message={error} action={<Button title="Try again" icon="refresh-cw" variant="ghost" onPress={() => void loadTree(dir)} />} />
			) : (
				<EmptyState icon="folder" title="Empty folder" />
			)}
		/>
	);
}

function PathRow({ path, name, status, onPress }: { path: string; name?: string; status?: WorkspaceTreeEntry["status"]; onPress: () => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const parts = splitPath(path);
	const changed = status && status !== "unmodified";
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`${name ?? parts.name}${!name && parts.dir ? ` in ${parts.dir}` : ""}${changed ? `, ${status}` : ""}`}
			accessibilityHint={changed ? "Opens the diff" : "Opens the file"}
			onPress={() => { haptics.tap(); onPress(); }}
			style={({ pressed }) => [styles.entry, pressed && { backgroundColor: t.bgElevatedHover }]}
		>
			<FileTypeIcon path={path} status={changed ? status : undefined} />
			<View style={{ flex: 1, minWidth: 0 }}>
				<Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={1} style={styles.pathName}>{name ?? parts.name}</Text>
				{!name && parts.dir ? <Text maxFontSizeMultiplier={fontScaleCap.body} numberOfLines={1} ellipsizeMode="head" style={styles.entryDir}>{parts.dir}</Text> : null}
			</View>
			<Feather name="chevron-right" size={iconSize.sm} color={t.textFaint} />
		</Pressable>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", gap: space.md, padding: space.xxl, backgroundColor: t.bgBase },
	centerInline: { paddingVertical: space.huge, alignItems: "center" },
	copy: { ...type.footnote, color: t.textTertiary, textAlign: "center" },
	header: { paddingHorizontal: space.lg, paddingTop: space.md, gap: space.md },
	summary: { gap: space.xs, paddingBottom: space.xs },
	summaryRow: { flexDirection: "row", alignItems: "center", gap: space.sm },
	summaryText: { flex: 1, ...type.footnote, color: t.textSecondary },
	note: { flexDirection: "row", alignItems: "flex-start", gap: space.xs },
	noteText: { flex: 1, ...type.footnote, color: t.textTertiary },
	sectionHead: { flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: space.lg, paddingTop: space.lg, paddingBottom: space.xs },
	sectionTitle: { fontFamily: font.semibold, fontSize: type.caption2.fontSize, letterSpacing: 0.9, textTransform: "uppercase", color: t.textTertiary },
	sectionCount: { fontFamily: font.medium, fontSize: type.caption2.fontSize, color: t.textFaint },
	divider: { height: StyleSheet.hairlineWidth, backgroundColor: t.borderSubtle, marginLeft: space.lg + iconSize.lg + space.xs + space.md },
	search: { flexDirection: "row", alignItems: "center", gap: space.sm, marginHorizontal: space.lg, marginTop: space.md, paddingHorizontal: space.md, minHeight: 40, borderRadius: radius.md, backgroundColor: t.bgElevated, borderWidth: StyleSheet.hairlineWidth, borderColor: t.borderSubtle },
	searchInput: { flex: 1, ...type.subheadline, color: t.textPrimary, paddingVertical: space.xs },
	crumbs: { flexDirection: "row", alignItems: "center", flexWrap: "wrap", paddingHorizontal: space.lg, paddingTop: space.md, paddingBottom: space.xs },
	crumbPart: { flexDirection: "row", alignItems: "center" },
	crumb: { minHeight: touchTarget - space.md, flexDirection: "row", alignItems: "center", gap: space.xs, paddingHorizontal: space.xxs },
	crumbText: { fontFamily: t.fontMono, fontSize: type.footnote.fontSize, color: t.textSecondary, maxWidth: 160 },
	crumbTextOn: { color: t.textPrimary },
	crumbCount: { marginLeft: "auto", fontFamily: font.medium, fontSize: type.caption2.fontSize, color: t.textFaint, backgroundColor: t.bgSubtle, borderRadius: radius.pill, paddingHorizontal: space.xs, overflow: "hidden" },
	entry: { minHeight: touchTarget + space.xxs, flexDirection: "row", alignItems: "center", gap: space.md, paddingHorizontal: space.lg },
	entryName: { flex: 1, ...type.subheadline, color: t.textPrimary },
	pathName: { ...type.subheadline, color: t.textPrimary },
	entryDir: { ...type.footnote, color: t.textTertiary },
	changeDot: { width: 7, height: 7, borderRadius: 4, backgroundColor: t.amber },
	footNote: { ...type.footnote, color: t.textTertiary, padding: space.lg },
	pressed: { opacity: press.opacity },
});
