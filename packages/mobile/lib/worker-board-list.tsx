import { Feather } from "./icons";
import { useFocusEffect, useRouter } from "expo-router";
import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ComponentProps, type ReactElement, type RefObject } from "react";
import { ActivityIndicator, Alert, AppState, FlatList, Platform, Pressable, RefreshControl, StyleSheet, Text, View } from "react-native";
import { LayoutAnimationConfig } from "react-native-reanimated";
import {
	absorbNewHosts,
	groupSessions,
	hasNewHost,
	holdOrder,
	isArchived,
	shouldResnapshot,
	snapshotOrder,
	updatedSince,
	type BoardSection,
	type OrderSnapshot,
} from "./agentsView";
import type { DashboardSession } from "./api";
import { ArchivedWorkerRow } from "./archived-worker-row";
import { useArchivedSessions, type ArchiveSource } from "./archive/useArchivedSessions";
import { haptics } from "./haptics";
import { hostedProjectKey, hostedRowKey, hostedSessionKey, sessionHostId, sessionMachineIdentity } from "./hostedRows";
import { useApp } from "./store";
import type { Theme } from "./theme";
import { statusVisual } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { ListSectionHeader } from "./ui";
import { ARCHIVE_SESSION_CONFIRM } from "./worker-action-model";
import { WorkerListRow } from "./worker-list-row";
import { filterWorkerSessions } from "./worker-search";
import { type, space } from "./tokens";
import { userFacingError } from "./connectionError";

// The archive rides along as one more section so it scrolls with the board
// rather than being pinned like desktop's strip — a phone has no room for a
// permanent footer above the tab bar.
type ListSection =
	| BoardSection
	| { zone: "pinned"; label: string; color: string; data: DashboardSession[] }
	| { zone: "archive"; label: string; color: string; data: DashboardSession[] }
	| { zone: "search"; label: string; color: string; data: DashboardSession[] };

/**
 * Sections that never fold, whatever the user has toggled.
 *
 * Search results are a transient answer — folding the thing you just asked for
 * is a way to lose it. `needs_you` is the reason the app was opened at all,
 * `pinned` is what someone deliberately put at the top, and `recent` is the
 * ordinary list beneath a plain text separator. Archive has its own disclosure
 * control below the live board.
 */
const ALWAYS_OPEN = new Set<ListSection["zone"]>(["search", "needs_you", "pinned", "recent"]);
const boardRowKey = (item: BoardRow) => item.key;

/**
 * One flat list, not a SectionList, and that is load-bearing.
 *
 * Flattening gives every host-qualified worker one stable key across sections,
 * including pin/unpin and status changes, without native row reflow animations.
 */
export type BoardRow =
	| { kind: "header"; key: string; label: string; open: boolean; collapsible: boolean }
	| { kind: "archive"; key: string }
	| { kind: "session"; key: string; session: DashboardSession }
	// Read-only with Restore, as on desktop. See ArchivedWorkerRow.
	| { kind: "archived"; key: string; session: DashboardSession }
	// Loading, failed, or more to come, below an archive that pages.
	| { kind: "archive-status"; key: string };

const NO_ARCHIVE_SOURCES: readonly ArchiveSource[] = [];

const sessionRow = (session: DashboardSession): BoardRow =>
	isArchived(session)
		? { kind: "archived", key: hostedSessionKey(session), session }
		: { kind: "session", key: hostedSessionKey(session), session };

/**
 * A screenful of rows either side of the viewport, rather than FlatList's ten.
 *
 * The default (21 viewports in total) kept roughly 200 rows mounted, which on a
 * board with a long archive meant building and laying out every one of them the
 * moment Archive opened. Seven still leaves three screens of runway each way for
 * a fling, and the rows are cheap enough to fill it as the list moves.
 */
export const BOARD_LIST_WINDOW = { initialNumToRender: 12, maxToRenderPerBatch: 8, windowSize: 7 } as const;

/**
 * The Workers board's grouped list: Pinned, Needs you, Recent, and a
 * collapsible Archive, with every row action wired.
 *
 * Extracted so a project's own page shows its workers exactly as the Workers tab
 * does — same grouping, same ordering, same swipe and long-press actions —
 * rather than a second list that would drift from this one. Callers scope the
 * sessions; this owns how they are presented and acted on.
 */
export function WorkerBoardList({
	sessions,
	query = "",
	listRef,
	contentBottomInset,
	refreshing,
	onRefresh,
	ListHeaderComponent,
	ListEmptyComponent,
	initialArchiveOpen = false,
	showProject = true,
	identityKey,
	archiveSources = NO_ARCHIVE_SOURCES,
}: {
	sessions: DashboardSession[];
	/** Non-empty switches the board to a single flat "Search results" section. */
	query?: string;
	listRef?: RefObject<FlatList<BoardRow> | null>;
	contentBottomInset: number;
	refreshing: boolean;
	onRefresh(): void | Promise<void>;
	ListHeaderComponent?: ReactElement | null;
	ListEmptyComponent?: ReactElement | null;
	initialArchiveOpen?: boolean;
	/** Off on a project's own page, where every row would repeat its name; the agent shows instead. */
	showProject?: boolean;
	/**
	 * Changes when the *set* of workers changes wholesale — a different project
	 * filter, or a search. The list is remounted on a new value.
	 *
	 * Swapping the filter in place left the board showing two lists at once:
	 * recycled cells of the old grouping drawn under the new data, the empty state
	 * on top of rows that were still on screen. A remount has no old cells to
	 * recycle, and no row-to-row layout animation to run for a change that is not
	 * a row moving.
	 */
	identityKey?: string;
	/**
	 * Machines whose daemon pages its archive. Their archived workers are not in
	 * `sessions`; they are fetched when Archive opens, or a search runs.
	 */
	archiveSources?: readonly ArchiveSource[];
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const { projects, allProjects, currentHostId, connection, hostStates, kill, renameWorker, setWorkerPinned, restore, resumeAgent } = useApp();
	const [renamingWorkerId, setRenamingWorkerId] = useState<string>();
	const [activeSwipeId, setActiveSwipeId] = useState<string>();
	const activeSwipeRef = useRef<{ id: string; close(): void } | undefined>(undefined);
	// Collapsed by default, like desktop's archive strip: it is history, and on a
	// long-running project it is most of the sessions.
	const [archiveOpen, setArchiveOpen] = useState(initialArchiveOpen);
	// Any future optional group may be folded away. The current live groups stay
	// open; Archive has its own disclosure row below them.
	const [collapsedSections, setCollapsedSections] = useState<Record<string, boolean>>({});
	// Minute-granular, so a memoised row still updates its relative timestamp even
	// when nothing about the session itself has changed.
	const nowBucket = Math.floor(Date.now() / 60_000);

	const projectNames = useMemo(
		() => new Map([...projects, ...allProjects].map((project) => [hostedProjectKey(project), project.name])),
		[projects, allProjects],
	);
	const projectNameFor = useCallback((session: DashboardSession) => {
		const hostId = sessionHostId(session);
		return session.projectId
			? projectNames.get(hostId ? hostedRowKey(hostId, session.projectId) : session.projectId) ?? session.projectId
			: "Standalone";
	}, [projectNames]);
	const filteredSessions = useMemo(
		() =>
			filterWorkerSessions(
				sessions,
				query,
				(_projectId, session) => projectNameFor(session),
				(status) => statusVisual(t, status).label,
			),
		[sessions, query, projectNameFor, t],
	);
	const fresh = useMemo(() => groupSessions(t, sessions), [t, sessions]);
	const filteredGroups = useMemo(() => query.trim() ? groupSessions(t, filteredSessions) : fresh, [t, filteredSessions, query, fresh]);

	// The order is held still while the user looks (see OrderSnapshot): rows
	// update in place and news waits behind the "N updated" pill. Accepting news
	// installs the new snapshot atomically, without an intermediate empty one.
	const freshLive = useMemo(() => [...fresh.pinned, ...fresh.sections.flatMap((section) => section.data)], [fresh]);

	// The archive: inline from a machine that still sends it with the poll, paged
	// from one that does not, and merged into one list in archive order. It is
	// only fetched while someone is looking at it (Archive open, or a search).
	const searching = query.trim().length > 0;
	const [archiveRefresh, setArchiveRefresh] = useState(0);
	const pagedArchive = useArchivedSessions(archiveSources, {
		active: archiveOpen || searching,
		exhaustive: searching,
		inline: fresh.archived,
		refreshToken: archiveRefresh,
	});
	// A restored session is live by the next poll, before the archive refreshes.
	const liveKeys = useMemo(() => new Set(freshLive.map(hostedSessionKey)), [freshLive]);
	const archived = useMemo(
		() => pagedArchive.rows.filter((session) => !liveKeys.has(hostedSessionKey(session))),
		[pagedArchive.rows, liveKeys],
	);
	const archiveCount = pagedArchive.total;
	const searchedArchive = useMemo(
		() => searching
			? filterWorkerSessions(archived, query, (_projectId, session) => projectNameFor(session), (status) => statusVisual(t, status).label)
			: [],
		[searching, archived, query, projectNameFor, t],
	);
	const archiveStatusShown = pagedArchive.loading || pagedArchive.error !== null || pagedArchive.hasMore;
	const [snapshot, setSnapshot] = useState<OrderSnapshot>(() => snapshotOrder(freshLive, hostedSessionKey));
	const freshLiveRef = useRef(freshLive);
	freshLiveRef.current = freshLive;
	const scrollAfterResort = useRef(false);
	const held = useMemo(() => {
		// Another machine's first load joins the held order rather than replacing
		// it, so news already waiting behind the pill neither jumps nor vanishes.
		const order = absorbNewHosts(snapshot, freshLive, hostedSessionKey);
		const pinned = holdOrder(order, fresh.pinned, hostedSessionKey);
		const sections = fresh.sections.map((section) => ({ ...section, data: holdOrder(order, section.data, hostedSessionKey) }));
		const shown = [...pinned, ...sections.flatMap((section) => section.data)];
		const stale = shown.some((session, i) => session !== freshLive[i]);
		return { pinned, sections, stale, updated: stale ? updatedSince(order, freshLive, hostedSessionKey) : 0 };
	}, [snapshot, fresh, freshLive]);
	const { pinned, sections } = held;
	useEffect(() => {
		if (freshLive.length === 0) return;
		if (hasNewHost(snapshot, freshLive)) {
			setSnapshot((previous) => absorbNewHosts(previous, freshLive, hostedSessionKey));
			return;
		}
		if (shouldResnapshot(snapshot, freshLive, held.stale, hostedSessionKey)) {
			setSnapshot(previous => snapshotOrder(freshLive, hostedSessionKey, previous));
		}
	}, [snapshot, freshLive, held.stale]);
	const resort = useCallback(() => setSnapshot(previous => snapshotOrder(freshLiveRef.current, hostedSessionKey, previous)), []);
	useLayoutEffect(() => {
		if (!scrollAfterResort.current) return;
		scrollAfterResort.current = false;
		listRef?.current?.scrollToOffset({ offset: 0, animated: false });
	}, [snapshot, listRef]);
	// Opening the board, coming back to the app, or a different set of workers
	// is looking again, so each re-sorts. Leaving it counts too: tabs keep
	// visited screens mounted, so unmounting alone does not say the user moved on.
	const focusEpoch = useRef(0);
	useFocusEffect(useCallback(() => {
		resort();
		return () => { focusEpoch.current += 1; };
	}, [resort]));
	useEffect(() => {
		const sub = AppState.addEventListener("change", (state) => {
			if (state === "active") resort();
		});
		return () => sub.remove();
	}, [resort]);
	useEffect(resort, [resort, identityKey]);
	// Pulling to refresh is asking for the latest order too.
	const refreshAndResort = useCallback(async () => {
		setArchiveRefresh((value) => value + 1);
		try {
			await onRefresh();
		} finally {
			resort();
		}
	}, [onRefresh, resort]);
	const showLatest = useCallback(() => {
		haptics.select();
		scrollAfterResort.current = true;
		resort();
	}, [resort]);

	// The archive is the last section, rendered only when expanded so a collapsed
	// strip costs nothing to scroll past.
	const listSections = useMemo<ListSection[]>(() => {
		if (searching) {
			const data = [...filteredGroups.pinned, ...filteredGroups.sections.flatMap((section) => section.data), ...searchedArchive];
			// Kept while the archive is still being read, or could not be, so its
			// status row (progress, or the error and Retry) shows instead of "no
			// results" for a match that has not arrived yet.
			return data.length === 0 && !pagedArchive.loading && !pagedArchive.error
				? []
				: [{ zone: "search", label: "Search results", color: t.accent, data }];
		}
		const liveSections: ListSection[] = [
			...(pinned.length ? [{ zone: "pinned" as const, label: "Pinned", color: t.amber, data: pinned }] : []),
			...sections,
		];
		if (archiveCount === 0 && archived.length === 0) return liveSections;
		return [
			...liveSections,
			{ zone: "archive" as const, label: "Archive", color: t.textFaint, data: archiveOpen ? archived : [] },
		];
	}, [searching, filteredGroups, searchedArchive, pagedArchive.loading, pagedArchive.error, pinned, sections, archiveCount, archived, archiveOpen, t]);

	// Headers and rows as one array of siblings, so a row changing section is a
	// reorder rather than an unmount. See BoardRow.
	const listData = useMemo<BoardRow[]>(
		() =>
			listSections.flatMap((section): BoardRow[] => {
				const status: BoardRow[] = archiveStatusShown && (section.zone === "search" || (section.zone === "archive" && archiveOpen))
					? [{ kind: "archive-status", key: "archive:status" }]
					: [];
				if (section.zone === "archive") {
					return [{ kind: "archive", key: "header:archive" } as const, ...section.data.map(sessionRow), ...status];
				}
				const collapsible = !ALWAYS_OPEN.has(section.zone);
				const open = !collapsible || !collapsedSections[section.zone];
				return [
					{ kind: "header", key: `header:${section.zone}`, label: section.label, open, collapsible } as const,
					// Search mixes live and archived workers; each keeps its own row.
					...(open ? section.data.map(sessionRow) : []),
					...status,
				];
			}),
		[collapsedSections, listSections, archiveStatusShown, archiveOpen],
	);

	const toggleSection = useCallback((zone: string) => {
		haptics.tap();
		setCollapsedSections((current) => ({ ...current, [zone]: !current[zone] }));
	}, []);

	// Swipeable's Android callbacks arrive after the UI thread has already begun
	// opening the next rail. Close the previous native row synchronously so two
	// action rails cannot be visible while React propagates the active id.
	const openExclusiveSwipe = useCallback((id: string, close: () => void) => {
		const previous = activeSwipeRef.current;
		if (previous?.id !== id) previous?.close();
		activeSwipeRef.current = { id, close };
		setActiveSwipeId(id);
	}, []);
	const closeExclusiveSwipe = useCallback((id: string) => {
		if (activeSwipeRef.current?.id === id) activeSwipeRef.current = undefined;
		setActiveSwipeId((activeId) => (activeId === id ? undefined : activeId));
	}, []);

	const updateWorkerPin = useCallback(async (session: DashboardSession, pinned: boolean) => {
		try {
			await setWorkerPinned(session.id, pinned, sessionHostId(session));
			haptics.success();
		} catch (cause) {
			haptics.error();
			Alert.alert("Couldn't update pin", userFacingError(cause, "Please try again."));
		}
	}, [setWorkerPinned]);

	// Resume restarts a stopped agent; restore brings back a terminated session.
	// Both are recoveries rather than destructive, so neither asks first — the
	// failure path is an alert, not a confirmation.
	const runWorkerRecovery = useCallback(async (session: DashboardSession, kind: "resume" | "restore") => {
		haptics.tap();
		try {
			await (kind === "resume" ? resumeAgent(session.id, sessionHostId(session)) : restore(session.id, sessionHostId(session)));
			haptics.success();
		} catch (cause) {
			haptics.error();
			Alert.alert(
				kind === "resume" ? "Couldn't resume the agent" : "Couldn't restore the session",
				userFacingError(cause, "Please try again."),
			);
		}
	}, [restore, resumeAgent]);

	const confirmDeleteSession = useCallback((session: DashboardSession) => {
		haptics.warning();
		Alert.alert(
			ARCHIVE_SESSION_CONFIRM.title,
			ARCHIVE_SESSION_CONFIRM.message,
			[
				{ text: ARCHIVE_SESSION_CONFIRM.cancel, style: "cancel" },
				{ text: ARCHIVE_SESSION_CONFIRM.confirm, style: "destructive", onPress: () => void kill(session.id, sessionHostId(session)).catch(() => {}) },
			],
		);
	}, [kill]);

	// Desktop's archive: one restore at a time, the error under the card it
	// belongs to, and a successful restore opens the session. A restore that
	// finishes after the user has moved to another board must not navigate, nor
	// one that finishes after they left this one for another tab or session:
	// the board stays mounted then, so only the focus epoch says they went.
	const [restoringKey, setRestoringKey] = useState<string>();
	const [restoreErrors, setRestoreErrors] = useState<Record<string, string>>({});
	const restoringRef = useRef<string | undefined>(undefined);
	const restoreGeneration = useRef(0);
	useEffect(() => {
		restoreGeneration.current += 1;
		restoringRef.current = undefined;
		setRestoringKey(undefined);
		setRestoreErrors({});
	}, [identityKey]);
	useEffect(() => () => { restoreGeneration.current += 1; }, []);
	// Restore is a request to the session's own machine, so like desktop it is
	// unavailable while that machine is unreachable. Keyed by the offline ids
	// rather than hostStates, which is a new array on every poll.
	const offlineKey = hostStates.filter((host) => host.connection !== "open").map((host) => host.hostId).join("\n");
	const offlineHosts = useMemo(() => new Set(offlineKey ? offlineKey.split("\n") : []), [offlineKey]);
	const machineOffline = useCallback((session: DashboardSession) => {
		const hostId = sessionHostId(session);
		// A row without a host id belongs to this board's own connection (HostScope
		// already scopes that to the right machine).
		return hostId ? offlineHosts.has(hostId) : connection !== "open";
	}, [offlineHosts, connection]);
	const restoreArchived = useCallback(async (session: DashboardSession) => {
		if (restoringRef.current || machineOffline(session)) return;
		const key = hostedSessionKey(session);
		const generation = restoreGeneration.current;
		const current = () => generation === restoreGeneration.current;
		const focus = focusEpoch.current;
		restoringRef.current = key;
		setRestoringKey(key);
		setRestoreErrors(({ [key]: _cleared, ...rest }) => rest);
		haptics.tap();
		try {
			await restore(session.id, sessionHostId(session));
			if (!current()) return;
			haptics.success();
			// The busy state below still clears; only the navigation is dropped.
			if (focus !== focusEpoch.current) return;
			router.push({
				pathname: "/session/[id]",
				params: { id: session.id, projectId: session.projectId, hostId: sessionHostId(session) ?? currentHostId },
			});
		} catch (cause) {
			if (!current()) return;
			haptics.error();
			setRestoreErrors((errors) => ({ ...errors, [key]: userFacingError(cause, "Couldn't restore this session.") }));
		} finally {
			if (current()) {
				restoringRef.current = undefined;
				setRestoringKey(undefined);
			}
		}
	}, [restore, router, currentHostId, machineOffline]);

	// Recycled list cells must never carry animated offsets across reordering.
	const toggleArchive = useCallback(() => setArchiveOpen((value) => !value), []);
	const actions = useMemo(() => ({
		renameStart: setRenamingWorkerId,
		rename: renameWorker,
		pin: updateWorkerPin,
		archive: confirmDeleteSession,
		recover: runWorkerRecovery,
	}), [renameWorker, updateWorkerPin, confirmDeleteSession, runWorkerRecovery]);
	const renderItem = useCallback(({ item }: { item: BoardRow }) => {
		if (item.kind === "archive") return <ArchiveHeader count={archiveCount} open={archiveOpen} onToggle={toggleArchive} />;
		if (item.kind === "archive-status") return <ArchiveStatusRow loading={pagedArchive.loading} error={pagedArchive.error}
			hasMore={pagedArchive.hasMore} onRetry={pagedArchive.retry} onLoadMore={pagedArchive.loadMore} />;
		if (item.kind === "header") return <ListSectionHeader label={item.label} open={item.open}
			onToggle={item.collapsible ? () => toggleSection(item.key.replace("header:", "")) : undefined} />;
		if (item.kind === "archived") return <ArchivedWorkerRow session={item.session} nowBucket={nowBucket}
			projectName={showProject ? projectNameFor(item.session) : item.session.harness || "Agent"}
			restoring={restoringKey === item.key} restoreDisabled={restoringKey !== undefined}
			offline={machineOffline(item.session)} restoreError={restoreErrors[item.key]} onRestore={restoreArchived} />;
		return <BoundWorkerRow session={item.session} hostId={currentHostId} nowBucket={nowBucket}
			projectName={showProject ? projectNameFor(item.session) : item.session.harness || "Agent"}
			isRenaming={renamingWorkerId === item.key} activeSwipeId={activeSwipeId}
			onSwipeOpen={openExclusiveSwipe} onSwipeClose={closeExclusiveSwipe} actions={actions} />;
	}, [archiveCount, archiveOpen, pagedArchive.loading, pagedArchive.error, pagedArchive.hasMore, pagedArchive.retry, pagedArchive.loadMore, toggleArchive, toggleSection, currentHostId, nowBucket, showProject, projectNameFor,
		renamingWorkerId, activeSwipeId, openExclusiveSwipe, closeExclusiveSwipe, actions, restoringKey, restoreErrors, restoreArchived, machineOffline]);

	return (
		<View style={styles.listWrap}>
			{/* A filter/search scope is a wholesale replacement, not rows moving
			    inside one board. Key this boundary and suppress both sides of that
			    replacement so outgoing native cells cannot overlap the new list or
			    retain invisible height. Same-board row changes still animate below. */}
			<LayoutAnimationConfig key={identityKey} skipEntering skipExiting>
				<FlatList
					ref={listRef}
					data={listData}
					keyExtractor={boardRowKey}
					{...BOARD_LIST_WINDOW}
					removeClippedSubviews={false}
					contentContainerStyle={{ paddingBottom: contentBottomInset }}
					keyboardDismissMode={Platform.OS === "ios" ? "interactive" : "on-drag"}
					keyboardShouldPersistTaps="handled"
					refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refreshAndResort} tintColor={t.accent} />}
					ListHeaderComponent={ListHeaderComponent}
					ListEmptyComponent={ListEmptyComponent}
					renderItem={renderItem}
					// The next archive page loads as its end comes into view.
					onEndReached={archiveOpen || searching ? pagedArchive.loadMore : undefined}
					onEndReachedThreshold={1.5}
				/>
			</LayoutAnimationConfig>
		{held.updated > 0 && !query.trim() ? <UpdatesPill count={held.updated} onPress={showLatest} /> : null}
		</View>
	);
}

type BoundWorkerRowProps = Pick<ComponentProps<typeof WorkerListRow>,
	"session" | "hostId" | "projectName" | "nowBucket" | "isRenaming" | "activeSwipeId" | "onSwipeOpen" | "onSwipeClose"> & {
	actions: {
		renameStart(id: string | undefined): void;
		rename(id: string, title: string, hostId?: string): Promise<void>;
		pin(session: DashboardSession, pinned: boolean): Promise<void>;
		archive(session: DashboardSession): void;
		recover(session: DashboardSession, kind: "resume" | "restore"): Promise<void>;
	};
};

// Bind once per changed session, not once per whole-board context publication.
const BoundWorkerRow = memo(function BoundWorkerRow({ actions, ...props }: BoundWorkerRowProps) {
	const session = props.session;
	const rowKey = hostedSessionKey(session);
	const machine = useMemo(() => sessionMachineIdentity(session), [session]);
	const handlers = useMemo(() => ({
		onRenameStart: () => actions.renameStart(rowKey),
		onRenameCancel: () => actions.renameStart(undefined),
		onRename: (title: string) => actions.rename(session.id, title, sessionHostId(session)),
		onSetPinned: (pinned: boolean) => actions.pin(session, pinned),
		onDelete: () => actions.archive(session),
		onResume: () => { void actions.recover(session, "resume"); },
		onRestore: () => { void actions.recover(session, "restore"); },
	}), [actions, session, rowKey]);
	return <WorkerListRow {...props} rowKey={rowKey} machine={machine} {...handlers} />;
});

// News that arrived while the board was held still. Tapping re-sorts and goes
// to the top, where the news now is.
function UpdatesPill({ count, onPress }: { count: number; onPress: () => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const label = `${count} updated`;
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`${label}. Show latest.`}
			onPress={onPress}
			style={({ pressed }) => [styles.updatesPill, pressed && { opacity: 0.85 }]}
		>
			<Feather name="arrow-up" size={14} color={t.onAccent} />
			<Text style={styles.updatesLabel}>{label}</Text>
		</Pressable>
	);
}

// Below a paged archive: the next page loading, a failure to retry, or a way to
// ask for more when the end of the list was reached before a page arrived.
function ArchiveStatusRow({ loading, error, hasMore, onRetry, onLoadMore }: {
	loading: boolean;
	error: string | null;
	hasMore: boolean;
	onRetry(): void;
	onLoadMore(): void;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	if (error) {
		return (
			<View style={styles.archiveStatus}>
				<Text accessibilityRole="alert" style={[styles.archiveStatusText, { color: t.red }]} numberOfLines={2}>{error}</Text>
				<Pressable accessibilityRole="button" accessibilityLabel="Retry loading archived sessions" hitSlop={8} onPress={onRetry}
					style={({ pressed }) => pressed && { opacity: 0.6 }}>
					<Text style={styles.archiveStatusAction}>Retry</Text>
				</Pressable>
			</View>
		);
	}
	if (loading) {
		return (
			<View style={styles.archiveStatus} accessible accessibilityLabel="Loading archived sessions">
				<ActivityIndicator size="small" color={t.textTertiary} />
				<Text style={styles.archiveStatusText}>Loading archived sessions…</Text>
			</View>
		);
	}
	if (!hasMore) return null;
	return (
		<Pressable accessibilityRole="button" onPress={onLoadMore} style={({ pressed }) => [styles.archiveStatus, pressed && { opacity: 0.6 }]}>
			<Text style={styles.archiveStatusAction}>Show more</Text>
		</Pressable>
	);
}

function ArchiveHeader({ count, open, onToggle }: { count: number; open: boolean; onToggle: () => void }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityState={{ expanded: open }}
			accessibilityLabel={`Archive, ${count} session${count === 1 ? "" : "s"}`}
			onPress={() => {
				haptics.tap();
				onToggle();
			}}
			style={({ pressed }) => [styles.archiveHeader, pressed && { opacity: 0.6 }]}
		>
			<Feather name={open ? "chevron-down" : "chevron-right"} size={15} color={t.textTertiary} />
			<Text style={styles.archiveLabel}>Archive</Text>
			<Text style={styles.archiveCount}>{count}</Text>
		</Pressable>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
		listWrap: { flex: 1 },
		updatesPill: {
			position: "absolute",
			top: space.sm,
			alignSelf: "center",
			flexDirection: "row",
			alignItems: "center",
			gap: space.xs,
			paddingHorizontal: space.md,
			paddingVertical: space.xs + 2,
			borderRadius: 20,
			backgroundColor: t.accent,
			shadowColor: "#000",
			shadowOpacity: 0.3,
			shadowRadius: 8,
			shadowOffset: { width: 0, height: 2 },
			elevation: 6,
		},
		updatesLabel: { fontFamily: "Geist_600SemiBold", color: t.onAccent, fontSize: type.footnote.fontSize, fontWeight: "600" },
		archiveHeader: {
			flexDirection: "row",
			alignItems: "center",
			gap: space.sm,
			paddingHorizontal: space.lg,
			paddingTop: space.xl,
			paddingBottom: space.sm,
		},
		archiveLabel: { fontFamily: "Geist_500Medium", color: t.textTertiary, fontSize: type.caption1.fontSize, lineHeight: type.caption1.lineHeight, fontWeight: "500", flex: 1 },
		archiveCount: { color: t.textFaint, fontSize: type.caption1.fontSize, fontWeight: "600", fontFamily: t.fontMono },
		archiveStatus: { flexDirection: "row", alignItems: "center", justifyContent: "center", gap: space.sm, minHeight: 56, paddingHorizontal: space.lg },
		archiveStatusText: { flexShrink: 1, fontFamily: "Geist_400Regular", color: t.textTertiary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
		archiveStatusAction: { fontFamily: "Geist_600SemiBold", color: t.accent, fontSize: type.footnote.fontSize, fontWeight: "600" },
	});
