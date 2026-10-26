import { Stack, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useLayoutEffect, useMemo, useState } from "react";
import { RefreshControl, SectionList, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { AppSymbol } from "../../../lib/AppSymbol";
import { hostRouteMatches } from "../../../lib/hostRoute";
import { Feather } from "../../../lib/icons";
import { refreshControlColors } from "../../../lib/refreshControlColors";
import { Spinner } from "../../../lib/Spinner";
import { HostScope, useApp } from "../../../lib/store";
import type { Theme } from "../../../lib/theme";
import { useTheme, useThemedStyles } from "../../../lib/ThemeProvider";
import { ToastHost } from "../../../lib/Toast";
import { font, fontScaleCap, iconSize, space, type } from "../../../lib/tokens";
import { Button, EmptyState } from "../../../lib/ui";
import { useToast } from "../../../lib/useToast";
import { LineCounts } from "../../../lib/files/components";
import { FolderList, SearchResults, useWorkspaceSearch } from "../../../lib/files/FolderList";
import { changeGroups, plural, type ChangeEntry } from "../../../lib/files/model";
import { REVIEW_BUTTON_CLEARANCE, REVIEW_BUTTON_RISE } from "../../../lib/files/ReviewButton.types";
import { ReviewButton } from "../../../lib/files/ReviewButton";
import { reviewKey } from "../../../lib/files/reviewStore";
import { ChangeRow, FileRowDivider } from "../../../lib/files/rows";
import { SegmentedChoice } from "../../../lib/files/SegmentedChoice";
import type { FileRowAction } from "../../../lib/files/FileRowMenu.types";
import { useFileActions } from "../../../lib/files/useFileActions";
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
	const [query, setQuery] = useState("");
	const { manifest, loading, error, refreshing, refresh } = useWorkspaceManifest(hostMatches ? config : null, id, hostMatches);
	const key = reviewKey(currentHostId, id);
	const review = useReviewSender(config, key, id);
	const toast = useToast();
	const { openFile, openFolder, openReview, act } = useFileActions({ sessionId: id, hostId: routeHostId, toast });
	const insets = useSafeAreaInsets();
	const search = useWorkspaceSearch(hostMatches && tab === "all" ? config : null, id, query);
	const groups = useMemo(() => manifest ? changeGroups(manifest) : [], [manifest]);
	const commentsByPath = useMemo(() => {
		const counts = new Map<string, number>();
		for (const comment of review.comments) counts.set(comment.target.path, (counts.get(comment.target.path) ?? 0) + 1);
		return counts;
	}, [review.comments]);

	useLayoutEffect(() => {
		navigation.setOptions({ title: "Files", headerBackTitle: title || "Session" });
	}, [navigation, title]);

	const chooseTab = useCallback((next: Tab) => {
		setTab(next);
		// The search belongs to All files and leaves with it.
		if (next !== "all") setQuery("");
	}, []);
	const searchChanged = useCallback((event: { nativeEvent: { text: string } }) => setQuery(event.nativeEvent.text), []);
	const searchClosed = useCallback(() => setQuery(""), []);
	const openChange = useCallback((file: ChangeEntry) => openFile(file.path, "diff", file.previousPath), [openFile]);
	const changeAction = useCallback((action: FileRowAction, file: ChangeEntry) => act(action, file), [act]);
	const openPath = useCallback((path: string, changed: boolean) => openFile(path, changed ? "diff" : "file"), [openFile]);
	const pathAction = useCallback((action: FileRowAction, path: string) => act(action, { path }), [act]);

	if (!hostMatches) return <View style={styles.center}><EmptyState icon="monitor" title="These files belong to another machine" message="Open them from that machine's session." action={<Button title="Open board" icon="workers" onPress={() => router.dismissTo("/")} />} /></View>;
	// A cold start (a deep link, a notification) lands before the saved pairing
	// loads; that is not "offline" yet.
	if (!config && appLoading) return <View style={styles.center}><Spinner size="large" accessibilityLabel="Connecting" /></View>;
	if (!config && connection === "closed") return <View style={styles.center}><EmptyState icon="wifi-off" title="Machine offline" message="Files load once the app reconnects." /></View>;

	const summary = manifest?.summary;
	const bottomInset = review.comments.length ? REVIEW_BUTTON_CLEARANCE : space.xxl;
	const header = (
		<View style={styles.header}>
			<SegmentedChoice<Tab>
				value={tab}
				onChange={chooseTab}
				accessibilityLabel="Files to show"
				options={[
					{ key: "changes", label: summary?.files ? `Changes ${summary.files}` : "Changes" },
					{ key: "all", label: "All files" },
				]}
			/>
			{tab === "changes" && manifest ? <ChangesSummary manifest={manifest} /> : null}
		</View>
	);

	let body: React.ReactNode;
	if (tab === "changes") {
		body = loading ? (
			<View style={styles.screen}>{header}<EmptyState busy title="Reading the worktree…" /></View>
		) : (
			<SectionList
				sections={groups.map((group) => ({ key: group.key, title: group.title, data: group.files }))}
				keyExtractor={(file) => file.path}
				ListHeaderComponent={header}
				stickySectionHeadersEnabled={false}
				contentContainerStyle={{ flexGrow: 1, paddingBottom: bottomInset }}
				refreshControl={<RefreshControl refreshing={refreshing} onRefresh={() => void refresh()} {...refreshControlColors(t)} />}
				renderSectionHeader={({ section }) => (
					<View style={styles.sectionHead}>
						<Text accessibilityRole="header" maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.sectionTitle}>{section.title}</Text>
						<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.sectionCount}>{section.data.length}</Text>
					</View>
				)}
				renderItem={({ item }) => <ChangeRow file={item} comments={commentsByPath.get(item.path) ?? 0} onPress={openChange} onAction={changeAction} />}
				ItemSeparatorComponent={FileRowDivider}
				ListEmptyComponent={error ? (
					<EmptyState icon="alert-triangle" title="Couldn't load changes" message={error} action={<Button title="Try again" icon="refresh-cw" onPress={() => void refresh()} />} />
				) : (
					<EmptyState icon="file-diff" title="No changes yet" message="When the agent edits files in this session, they show up here." action={<Button title="Browse all files" icon="list-tree" onPress={() => chooseTab("all")} />} />
				)}
			/>
		);
	} else if (search.active) {
		body = <SearchResults query={search.query} results={search.results} searching={search.searching} header={header} bottomInset={bottomInset} onOpenFile={openPath} onAction={pathAction} />;
	} else {
		body = <FolderList config={config} sessionId={id} dir="" header={header} bottomInset={bottomInset} onOpenFile={openPath} onOpenFolder={openFolder} onAction={pathAction} />;
	}

	return (
		<View style={styles.screen}>
			{/* Finding a file belongs to All files: the header's own search there,
			    a search field under the title on iOS and the top app bar's search
			    action on Android, gone again on Changes. It filters in place. */}
			{tab === "all" ? (
				<Stack.SearchBar
					placeholder="Find a file"
					placement="stacked"
					hideWhenScrolling={false}
					obscureBackground={false}
					autoCapitalize="none"
					tintColor={t.highlight}
					textColor={t.textPrimary}
					hintTextColor={t.textTertiary}
					headerIconColor={t.accent}
					onChangeText={searchChanged}
					onCancelButtonPress={searchClosed}
					onClose={searchClosed}
				/>
			) : null}
			{body}
			<ReviewButton count={review.comments.length} onPress={openReview} />
			<ToastHost toast={toast} bottomOffset={review.comments.length ? insets.bottom + REVIEW_BUTTON_RISE : undefined} />
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
					<AppSymbol name="info" size={iconSize.sm} color={t.amber} />
					<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.noteText}>{note}</Text>
				</View>
			))}
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", gap: space.md, padding: space.xxl, backgroundColor: t.bgBase },
	header: { paddingHorizontal: space.lg, paddingTop: space.md, gap: space.md },
	summary: { gap: space.xs, paddingBottom: space.xs },
	summaryRow: { flexDirection: "row", alignItems: "center", gap: space.sm },
	summaryText: { flex: 1, ...type.footnote, color: t.textSecondary },
	note: { flexDirection: "row", alignItems: "flex-start", gap: space.xs },
	noteText: { flex: 1, ...type.footnote, color: t.textTertiary },
	sectionHead: { flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: space.lg, paddingTop: space.lg, paddingBottom: space.xs },
	sectionTitle: { ...type.footnote, fontFamily: font.semibold, color: t.textSecondary },
	sectionCount: { ...type.footnote, color: t.textTertiary },
});
