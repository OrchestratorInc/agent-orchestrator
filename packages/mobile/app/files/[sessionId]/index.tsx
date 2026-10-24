import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, FlatList, Pressable, RefreshControl, SectionList, StyleSheet, Text, TextInput, View } from "react-native";
import { userFacingError } from "../../../lib/connectionError";
import { haptics } from "../../../lib/haptics";
import { Feather } from "../../../lib/icons";
import { PickerOptions } from "../../../lib/PickerOptions";
import type { Theme } from "../../../lib/theme";
import { textSelectionColors } from "../../../lib/textSelection";
import { useTheme, useThemedStyles } from "../../../lib/ThemeProvider";
import { font, fontScaleCap, iconSize, press, radius, space, touchTarget, type } from "../../../lib/tokens";
import { Button, EmptyState } from "../../../lib/ui";
import { type WorkspaceSearchResult, type WorkspaceTreeEntry } from "../../../lib/files/api";
import { FilesScope, useFilesScope } from "../../../lib/files/FilesScope";
import { ChangeRow, FileTypeIcon, FolderTypeIcon, LineCounts, ReviewTray, SegmentedTabs } from "../../../lib/files/components";
import { changeGroups, plural, splitPath, type ChangeEntry } from "../../../lib/files/model";
import { fileRoute } from "../../../lib/files/navigation";
import { useReviewSender } from "../../../lib/files/useReviewSender";
import { useWorkspaceManifest } from "../../../lib/files/useWorkspace";
import type { WorkspaceSource } from "../../../lib/files/workspaceSource";

export { RouteErrorBoundary as ErrorBoundary } from "../../../lib/RouteErrorBoundary";

type Tab = "changes" | "all";

/** The session's files: what the agent changed, and the whole worktree to browse. */
export default function SessionFilesScreen() {
	return <FilesScope><SessionFilesContent /></FilesScope>;
}

function SessionFilesContent() {
	const { tab: initialTab, title } = useLocalSearchParams<{ tab?: Tab; title?: string }>();
	const { sessionId: id, workspace, owner, reviewKey: key } = useFilesScope();
	const router = useRouter();
	const navigation = useNavigation();
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [tab, setTab] = useState<Tab>(initialTab === "all" ? "all" : "changes");
	const [choosingRepository, setChoosingRepository] = useState(false);
	const { manifest, loading, error, refreshing, refresh } = useWorkspaceManifest(workspace, id);
	const review = useReviewSender(workspace, key, id);
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
		if (!workspace.isCurrent()) return;
		const route = fileRoute(id, path, undefined, previousPath, owner);
		router.push({ ...route, params: { ...route.params, mode } });
	}, [id, owner, router, workspace]);
	const openChange = useCallback((file: ChangeEntry) => openFile(file.path, "diff", file.previousPath), [openFile]);

	const summary = manifest?.summary;
	const repositories = owner.source.kind === "cloud" ? manifest?.repositories?.filter((repository) => repository.present) ?? [] : [];
	const repositoryName = repositories.find((repository) => owner.repository ? repository.name === owner.repository : repository.main)?.label
		?? repositories.find((repository) => owner.repository ? repository.name === owner.repository : repository.main)?.name
		?? "Main repository";
	const header = (
		<View style={styles.header}>
			{repositories.length > 1 ? choosingRepository ? <PickerOptions title="Repository"
				options={repositories.map((repository) => ({ id: repository.main ? "" : repository.name, label: repository.label || repository.name, icon: "folder" }))}
				selectedValue={owner.repository ?? ""}
				onBack={() => setChoosingRepository(false)}
				onSelect={(repository) => {
					if (!workspace.isCurrent() || !repositories.some((item) => (item.main ? "" : item.name) === repository)) return;
					haptics.select(); setChoosingRepository(false); router.setParams({ repository, tab });
				}}
			/> : <Pressable accessibilityRole="button" accessibilityLabel="Choose repository" onPress={() => { haptics.tap(); setChoosingRepository(true); }} style={styles.repository}>
				<Feather name="folder" size={iconSize.sm} color={t.textSecondary} />
				<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.body} style={styles.repositoryName}>{repositoryName}</Text>
				<Feather name="chevron-down" size={iconSize.sm} color={t.textTertiary} />
			</Pressable> : null}
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
				<FileBrowser workspace={workspace} sessionId={id} header={header} bottomInset={review.comments.length ? 96 : space.xxl} onOpen={(path, changed) => openFile(path, changed ? "diff" : "file")} />
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
function FileBrowser({ workspace, sessionId, header, bottomInset, onOpen }: { workspace: WorkspaceSource; sessionId: string; header: React.ReactElement; bottomInset: number; onOpen: (path: string, changed: boolean) => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [dir, setDir] = useState("");
	const [tree, setTree] = useState<{ path: string; entries: WorkspaceTreeEntry[]; truncated: boolean } | null>(null);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<string>();
	const [query, setQuery] = useState("");
	const [results, setResults] = useState<WorkspaceSearchResult[] | null>(null);
	const [searching, setSearching] = useState(false);
	const [searchError, setSearchError] = useState<string>();
	const request = useRef(0);
	const treeController = useRef<AbortController | null>(null);
	const mounted = useRef(true);
	useEffect(() => {
		mounted.current = true;
		return () => { mounted.current = false; ++request.current; treeController.current?.abort(); };
	}, []);

	const loadTree = useCallback(async (path: string) => {
		if (!workspace.isCurrent()) return;
		treeController.current?.abort();
		const controller = new AbortController();
		treeController.current = controller;
		const ticket = ++request.current;
		const current = () => mounted.current && ticket === request.current && !controller.signal.aborted && workspace.isCurrent();
		setLoading(true);
		setError(undefined);
		try {
			const next = await workspace.getTree(sessionId, path, { signal: controller.signal });
			if (current()) setTree({ ...next, entries: [...next.entries].sort((a, b) => a.type === b.type ? a.name.localeCompare(b.name) : a.type === "dir" ? -1 : 1) });
		} catch (cause) {
			if (current()) setError(userFacingError(cause, "Couldn't read this folder."));
		} finally {
			if (current()) setLoading(false);
		}
	}, [workspace, sessionId]);

	useEffect(() => { void loadTree(dir); }, [dir, loadTree]);

	useEffect(() => {
		const trimmed = query.trim();
		if (!workspace.isCurrent() || !trimmed) { setResults(null); setSearching(false); setSearchError(undefined); return; }
		const controller = new AbortController();
		setResults([]);
		setSearchError(undefined);
		setSearching(true);
		let cancelled = false;
		const current = () => !cancelled && workspace.isCurrent();
		const timer = setTimeout(() => {
			workspace.search(sessionId, trimmed, { signal: controller.signal })
				.then((found) => { if (current()) setResults(found.results); })
				.catch((cause) => { if (current()) setSearchError(userFacingError(cause, "Couldn't search this workspace.")); })
				.finally(() => { if (current()) setSearching(false); });
		}, 250);
		return () => { cancelled = true; clearTimeout(timer); controller.abort(); };
	}, [workspace, query, sessionId]);

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
				ListEmptyComponent={searching ? null : searchError ? <EmptyState icon="alert-triangle" title="Couldn't search files" message={searchError} /> : <EmptyState icon="search" title="No matching files" message={`Nothing in this worktree matches “${query.trim()}”.`} />}
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
			ListFooterComponent={tree?.truncated ? <Text style={styles.footNote}>This folder has more entries than can be listed. Use Find a file to reach the rest.</Text> : null}
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
	repository: { minHeight: touchTarget, flexDirection: "row", alignItems: "center", gap: space.sm },
	repositoryName: { flex: 1, ...type.callout, color: t.textSecondary },
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
