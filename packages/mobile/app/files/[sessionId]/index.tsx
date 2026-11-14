import { useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useState } from "react";
import { RefreshControl, SectionList, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { AppSymbol } from "../../../lib/AppSymbol";
import { hostRouteMatches } from "../../../lib/hostRoute";
import { refreshControlColors } from "../../../lib/refreshControlColors";
import { Spinner } from "../../../lib/Spinner";
import { HostScope, useApp } from "../../../lib/store";
import type { Theme } from "../../../lib/theme";
import { useTheme, useThemedStyles } from "../../../lib/ThemeProvider";
import { ToastHost } from "../../../lib/Toast";
import { font, fontScaleCap, iconSize, space, type } from "../../../lib/tokens";
import { Button, EmptyState } from "../../../lib/ui";
import { useToast } from "../../../lib/useToast";
import { CompareBar } from "../../../lib/files/CompareBar";
import { FileSearchBar, useFileSearch } from "../../../lib/files/FileSearch";
import { FolderList, SearchResults } from "../../../lib/files/FolderList";
import { plural, type ChangeEntry } from "../../../lib/files/model";
import { REVIEW_BUTTON_CLEARANCE, REVIEW_BUTTON_RISE } from "../../../lib/files/ReviewButton.types";
import { ReviewButton } from "../../../lib/files/ReviewButton";
import { reviewKey } from "../../../lib/files/reviewStore";
import { ChangeRow, FileRowDivider } from "../../../lib/files/rows";
import { SegmentedChoice } from "../../../lib/files/SegmentedChoice";
import type { FileRowAction } from "../../../lib/files/FileRowMenu.types";
import { useFileActions } from "../../../lib/files/useFileActions";
import { useReviewSender } from "../../../lib/files/useReviewSender";
import { useWorkspaceManifest } from "../../../lib/files/useWorkspace";
import { useFilesBackend } from "../../../lib/files/useFilesBackend";
import {
	annotationContext,
	branchLabel,
	changesLabel,
	compareBase,
	DEFAULT_SELECTION,
	inPick,
	normalizeSelection,
	selectionForBranch,
	selectionFromParams,
	type FilesSelection,
	type SelectionParams,
} from "../../../lib/files/selection";
import { useBranchBackend, useBranchList, useChangeList, useGitCapable } from "../../../lib/files/useChangeList";
import { filesBranchSheetRoute, filesChangesSheetRoute } from "../../../lib/sheetResult";

export { RouteErrorBoundary as ErrorBoundary } from "../../../lib/RouteErrorBoundary";

type Tab = "changes" | "all";

/** The session's files: what the agent changed, and the whole worktree to browse. */
export default function SessionFilesScreen() {
	const { hostId } = useLocalSearchParams<{ hostId?: string }>();
	return hostId ? <HostScope key={hostId} hostId={hostId}><SessionFilesContent /></HostScope> : <SessionFilesContent />;
}

function SessionFilesContent() {
	const params = useLocalSearchParams<{ sessionId: string; hostId?: string; tab?: Tab; title?: string } & SelectionParams>();
	const { sessionId, hostId: routeHostId, tab: initialTab, title } = params;
	const id = String(sessionId ?? "");
	const router = useRouter();
	const navigation = useNavigation();
	const { currentHostId, connection, loading: appLoading, sessions } = useApp();
	// The paired machine's daemon, or Orchestrator Cloud for a cloud session.
	const { backend, cloud, signedOut, unreachable } = useFilesBackend();
	const hostMatches = hostRouteMatches(routeHostId, currentHostId);
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [tab, setTab] = useState<Tab>(initialTab === "all" ? "all" : "changes");
	const { manifest, loading, error, refreshing, refresh: refreshManifest } = useWorkspaceManifest(hostMatches ? backend : null, id, hostMatches);
	// What the list compares: the worktree, or another branch or the session's
	// pull request, and which of its changes. The pickers need the daemon's
	// branches and its Uncommitted section; without them (Orchestrator Cloud, an
	// older daemon, a scratch session) the list stays the worktree's.
	// Settled once per machine from the daemon's answers, not from each poll: a
	// degraded poll must not drop the pickers or the pick.
	const capable = useGitCapable(hostMatches ? backend : null, manifest);
	const { list: branches, reload: reloadBranches } = useBranchList(hostMatches ? backend : null, id, capable);
	const pickers = Boolean(capable && branches?.supported);
	const [picked, setPicked] = useState<FilesSelection>(() => selectionFromParams(params));
	const selection = pickers ? picked : DEFAULT_SELECTION;
	const branchBackend = useBranchBackend(hostMatches ? backend : null, selection.branch);
	const list = useChangeList(branchBackend, id, selection, manifest, hostMatches);
	const refresh = useCallback(async () => {
		// A branch list that failed to load is read again with the rest.
		reloadBranches();
		await Promise.all([refreshManifest(), list.refresh()]);
	}, [list.refresh, refreshManifest, reloadBranches]);
	const key = reviewKey(currentHostId, id);
	const review = useReviewSender(backend, key, id);
	const toast = useToast();
	const { openFile, openFolder, openReview, act } = useFileActions({ sessionId: id, hostId: routeHostId, toast, selection });
	const insets = useSafeAreaInsets();
	// Search and the folders follow the picked branch.
	const search = useFileSearch(hostMatches ? branchBackend : null, id);
	const dismissSearch = search.dismiss;
	const searchActive = search.active;
	const groups = useMemo(() => list.groups ?? [], [list.groups]);
	const base = compareBase(manifest);
	const checkedOutName = branches?.branches.find((row) => row.checkedOut)?.name || sessions.find((session) => session.id === id)?.branch || undefined;
	const chooseBranch = useCallback(() => {
		router.push(filesBranchSheetRoute({ sessionId: id, hostId: routeHostId, selected: selection.branch, onSelect: (branch) => setPicked(selectionForBranch(branch)) }));
	}, [id, routeHostId, router, selection.branch]);
	const chooseChanges = useCallback(() => {
		router.push(filesChangesSheetRoute({ sessionId: id, hostId: routeHostId, selection, base, onSelect: (changes) => setPicked((current) => normalizeSelection({ branch: current.branch, changes })) }));
	}, [base, id, routeHostId, router, selection]);
	// A row counts the comments made in this pick; another pick's are about other code.
	const commentsByPath = useMemo(() => {
		const context = annotationContext(selection);
		const counts = new Map<string, number>();
		for (const comment of review.comments) if (inPick(comment.target, context)) counts.set(comment.target.path, (counts.get(comment.target.path) ?? 0) + 1);
		return counts;
	}, [review.comments, selection]);

	useLayoutEffect(() => {
		navigation.setOptions({ title: "Files", headerBackTitle: title || "Session" });
	}, [navigation, title]);

	const chooseTab = useCallback((next: Tab) => {
		// A search's results are All files'; going to Changes clears it.
		if (next !== "all") dismissSearch();
		setTab(next);
	}, [dismissSearch]);
	// The search finds files anywhere in the worktree, so typing on Changes
	// moves to All files, where its results show.
	useEffect(() => {
		if (searchActive) setTab("all");
	}, [searchActive]);
	const openChange = useCallback((file: ChangeEntry) => openFile(file.path, "diff", file.previousPath), [openFile]);
	const changeAction = useCallback((action: FileRowAction, file: ChangeEntry) => act(action, file), [act]);
	const openPath = useCallback((path: string, changed: boolean) => openFile(path, changed ? "diff" : "file"), [openFile]);
	const pathAction = useCallback((action: FileRowAction, path: string) => act(action, { path }), [act]);

	if (!hostMatches) return <View style={styles.center}><EmptyState icon="monitor" title="These files belong to another machine" message="Open them from that machine's session." action={<Button title="Open board" icon="workers" onPress={() => router.dismissTo("/")} />} /></View>;
	// A cold start (a deep link, a notification) lands before the saved pairing
	// loads; that is not "offline" yet.
	if (signedOut) return <View style={styles.center}><EmptyState icon="cloud" title="Signed out of Orchestrator Cloud" message="Sign in from Settings to see this session's files." /></View>;
	if (unreachable) return <View style={styles.center}><EmptyState icon="cloud" title="Can't reach Orchestrator Cloud" message={unreachable.message} action={<Button title="Retry" icon="refresh" onPress={unreachable.retry} />} /></View>;
	if (!backend && (appLoading || cloud)) return <View style={styles.center}><Spinner size="large" accessibilityLabel="Connecting" /></View>;
	if (!backend && connection === "closed") return <View style={styles.center}><EmptyState icon="wifi-off" title="Machine offline" message="Files load once the app reconnects." /></View>;

	const summary = list.summary;
	const viewing = selection.branch.kind !== "checked_out";
	const fromManifest = !viewing && !(selection.changes.kind === "commits" && selection.changes.sha);
	const shownBranch = branchLabel(selection.branch, checkedOutName);
	const changesText = changesLabel(selection.changes, base);
	const changesTrigger = {
		label: changesText,
		mono: selection.changes.kind === "commits" && Boolean(selection.changes.sha),
		accessibilityLabel: `Changes, ${changesText}${summary ? `, ${plural(summary.files, "file")}, ${plural(summary.additions, "line")} added, ${plural(summary.deletions, "line")} removed` : ""}`,
	};
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
			{manifest && (tab === "changes" || pickers) ? (
				<CompareBar
					branch={pickers ? { label: shownBranch, icon: selection.branch.kind === "pull_request" ? "git-pull-request" : "git-branch" } : undefined}
					onBranch={pickers ? chooseBranch : undefined}
					// All files lists a branch's tip, so only its branch applies there.
					changes={tab === "changes" ? changesTrigger : undefined}
					onChanges={pickers ? chooseChanges : undefined}
					summary={tab === "changes" ? summary : undefined}
				/>
			) : null}
			{/* About the worktree's manifest, so not about one commit or another branch. */}
			{tab === "changes" && manifest && fromManifest ? <ManifestNotes manifest={manifest} againstBase={selection.changes.kind === "all" || selection.changes.kind === "commits"} /> : null}
			{/* A refresh that failed under a list still shown: the list may be out of date. */}
			{tab === "changes" && list.error && list.groups ? <Note text={`Couldn't refresh: ${list.error}`} /> : null}
		</View>
	);

	let body: React.ReactNode;
	if (tab === "changes") {
		// The list stays mounted while loading: under the clear glass bar only a
		// scroll view insets itself below the header and its search field, and a
		// plain view there drew the tabs under the search field. A failed manifest
		// read leaves a worktree pick without groups; show its error, not the spinner.
		const busy = loading || (!list.groups && !list.error && !(error && fromManifest));
		body = (
			<SectionList
				sections={busy ? [] : groups.map((group) => ({ key: group.key, title: group.title, data: group.files }))}
				keyExtractor={(file) => file.path}
				ListHeaderComponent={header}
				contentInsetAdjustmentBehavior="automatic"
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
				ListEmptyComponent={busy ? (
					<EmptyState busy title={viewing ? `Reading ${shownBranch}…` : "Reading the worktree…"} />
				) : error || list.error ? (
					<EmptyState icon="alert-triangle" title="Couldn't load changes" message={error || list.error} action={<Button title="Try again" icon="refresh-cw" onPress={() => void refresh()} />} />
				) : selection === DEFAULT_SELECTION || (!viewing && selection.changes.kind === "all") ? (
					<EmptyState icon="file-diff" title="No changes yet" message="When the agent edits files in this session, they show up here." action={<Button title="Browse all files" icon="list-tree" onPress={() => chooseTab("all")} />} />
				) : (
					<EmptyState icon="file-diff" title={emptyTitle(selection, shownBranch)} />
				)}
			/>
		);
	} else if (search.active) {
		body = <SearchResults query={search.query} results={search.results} searching={search.searching} header={header} bottomInset={bottomInset} onOpenFile={openPath} onAction={pathAction} />;
	} else {
		body = <FolderList backend={branchBackend} sessionId={id} dir="" header={header} bottomInset={bottomInset} onOpenFile={openPath} onOpenFolder={openFolder} onAction={pathAction} />;
	}

	return (
		<View style={styles.screen}>
			{/* On both tabs: in the header only on All files, its coming and going
			    moved the tabs under it up and down on every switch. */}
			<FileSearchBar search={search} />
			{body}
			<ReviewButton count={review.comments.length} onPress={openReview} />
			<ToastHost toast={toast} bottomOffset={review.comments.length ? insets.bottom + REVIEW_BUTTON_RISE : undefined} />
		</View>
	);
}

/**
 * Why the worktree's list may be incomplete or compared with something else
 * than its base. `againstBase` is a list measured from the base (all changes,
 * the branch's commits), which the cap and the fallback are about; an
 * uncommitted scope is measured from HEAD.
 */
function ManifestNotes({ manifest, againstBase }: { manifest: NonNullable<ReturnType<typeof useWorkspaceManifest>["manifest"]>; againstBase: boolean }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const notes = [
		againstBase && manifest.compareMode === "head_fallback" ? "The base branch isn't available, so this compares against the last commit." : null,
		manifest.degraded ? "Git answered slowly; some changes may be missing. Pull to refresh." : null,
		againstBase && manifest.truncated ? `Only the first ${plural(manifest.files.length, "file")} are listed.` : null,
	].filter((note): note is string => Boolean(note));
	if (!notes.length) return null;
	return (
		<View style={styles.notes}>
			{notes.map((note) => (
				<View key={note} style={styles.note}>
					<AppSymbol name="info" size={iconSize.sm} color={t.amber} />
					<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.noteText}>{note}</Text>
				</View>
			))}
		</View>
	);
}

function Note({ text }: { text: string }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<View style={[styles.notes, styles.note]}>
			<AppSymbol name="info" size={iconSize.sm} color={t.amber} />
			<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.noteText}>{text}</Text>
		</View>
	);
}

/** What an empty pick says, other than the worktree's first "No changes yet". */
function emptyTitle(selection: FilesSelection, branch: string): string {
	if (selection.branch.kind !== "checked_out") return selection.changes.kind === "commits" && selection.changes.sha ? "This commit changed no files" : `No changes on ${branch}`;
	switch (selection.changes.kind) {
		case "uncommitted":
			return "No uncommitted changes";
		case "staged":
			return "Nothing staged";
		case "unstaged":
			return "Nothing unstaged";
		case "commits":
			return selection.changes.sha ? "This commit changed no files" : "No commits on this branch yet";
		default:
			return "No changes yet";
	}
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", gap: space.md, padding: space.xxl, backgroundColor: t.bgBase },
	header: { paddingHorizontal: space.lg, paddingTop: space.md, gap: space.md },
	notes: { gap: space.xs, paddingBottom: space.xs },
	note: { flexDirection: "row", alignItems: "flex-start", gap: space.xs },
	noteText: { flex: 1, ...type.footnote, color: t.textTertiary },
	sectionHead: { flexDirection: "row", alignItems: "center", gap: space.sm, paddingHorizontal: space.lg, paddingTop: space.lg, paddingBottom: space.xs },
	sectionTitle: { ...type.footnote, fontFamily: font.semibold, color: t.textSecondary },
	sectionCount: { ...type.footnote, color: t.textTertiary },
});
