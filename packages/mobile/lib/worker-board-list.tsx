import { Feather } from "./icons";
import { useFocusEffect } from "expo-router";
import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactElement, type RefObject } from "react";
import { Alert, AppState, FlatList, Platform, Pressable, RefreshControl, StyleSheet, Text, View } from "react-native";
import { LayoutAnimationConfig } from "react-native-reanimated";
import {
	absorbNewHosts,
	groupSessions,
	hasNewHost,
	shouldResnapshot,
	snapshotOrder,
	updatedSince,
	type BoardSection,
	type OrderSnapshot,
} from "./agentsView";
import type { DashboardSession } from "./api";
import type { BoardInteractionMode } from "./board-presentation";
import { resourceKey, sourceKey, type Scoped } from "./environment/scopedBoard";
import { haptics } from "./haptics";
import { machineAppearance } from "./machineAppearance";
import { useApp } from "./store";
import type { Theme } from "./theme";
import { statusVisual } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { ListSectionHeader } from "./ui";
import { archiveSessionMessage } from "./worker-action-model";
import { WorkerListRow } from "./worker-list-row";
import { filterWorkerSessions } from "./worker-search";
import { type, space } from "./tokens";
import { userFacingError } from "./connectionError";

// The archive rides along as one more section so it scrolls with the board
// rather than being pinned like desktop's strip — a phone has no room for a
// permanent footer above the tab bar.
type ListSection =
	| (Omit<BoardSection, "data"> & { data: Scoped<DashboardSession>[] })
	| { zone: "pinned" | "archive" | "search"; label: string; color: string; data: Scoped<DashboardSession>[] };

function groupScopedSessions(t: Theme, entries: readonly Scoped<DashboardSession>[]) {
	// Keep a distinct object identity even if two sources happen to share a value object.
	const values = entries.map((entry) => ({ ...entry.value }));
	const byValue = new Map(values.map((value, index) => [value, entries[index]]));
	const groups = groupSessions(t, values);
	const scoped = (values: DashboardSession[]) => values.map((value) => byValue.get(value)!);
	return {
		pinned: scoped(groups.pinned),
		sections: groups.sections.map((section) => ({ ...section, data: scoped(section.data) })),
		archived: scoped(groups.archived),
	};
}

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

const orderSessionKey = (session: DashboardSession): string => "orderKey" in session ? String(session.orderKey) : session.id;
function holdScopedOrder(snapshot: OrderSnapshot, entries: Scoped<DashboardSession>[]): Scoped<DashboardSession>[] {
	return [...entries].sort((a, b) => (snapshot.rank[resourceKey(a.source, a.value.id)] ?? -1) - (snapshot.rank[resourceKey(b.source, b.value.id)] ?? -1));
}


/**
 * One flat list, not a SectionList, and that is load-bearing.
 *
 * Flattening gives every host-qualified worker one stable key across sections,
 * including pin/unpin and status changes, without native row reflow animations.
 */
export type BoardRow =
	| { kind: "header"; key: string; label: string; open: boolean; collapsible: boolean }
	| { kind: "archive"; key: string }
	| { kind: "session"; key: string; entry: Scoped<DashboardSession> };

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
}: {
	/** Retained for the project-page adapter; row policy always comes from its own source. */
	interactionMode?: BoardInteractionMode;
	sessions: readonly Scoped<DashboardSession>[];
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
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { scopedBoard, hostStates, killOn, renameWorkerOn, setWorkerPinnedOn, restoreOn, resumeAgentOn } = useApp();
	const machines = useMemo(() => new Map(hostStates.map((host) => [host.hostId, { name: `${host.name}${host.connection === "closed" ? " (offline)" : ""}`, appearance: machineAppearance({ id: host.hostId, name: host.name, appearance: host.appearance }) }])), [hostStates]);
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
		() => new Map(scopedBoard.projects.map((entry) => [resourceKey(entry.source, entry.value.id), entry.value.name])),
		[scopedBoard.projects],
	);
	const projectNameFor = useCallback((entry: Scoped<DashboardSession>) => entry.value.projectId
		? projectNames.get(resourceKey(entry.source, entry.value.projectId)) ?? entry.value.projectId
		: "Standalone", [projectNames]);
	const filteredSessions = useMemo(
		() =>
			sessions.filter((entry) => filterWorkerSessions(
				[entry.value],
				query,
				() => projectNameFor(entry),
				(status) => statusVisual(t, status).label,
			).length > 0),
		[sessions, query, projectNameFor, t],
	);
	const fresh = useMemo(() => groupScopedSessions(t, sessions), [t, sessions]);
	const filteredGroups = useMemo(() => query.trim() ? groupScopedSessions(t, filteredSessions) : fresh, [t, filteredSessions, query, fresh]);
	const archived = fresh.archived;

	// The order is held still while the user looks (see OrderSnapshot): rows
	// update in place and news waits behind the "N updated" pill. Accepting news
	// installs the new snapshot atomically, without an intermediate empty one.
	const freshLiveEntries = useMemo(() => [...fresh.pinned, ...fresh.sections.flatMap((section) => section.data)], [fresh]);
	const freshLive = useMemo(() => freshLiveEntries.map((entry) => ({ ...entry.value, hostId: sourceKey(entry.source), orderKey: resourceKey(entry.source, entry.value.id) })), [freshLiveEntries]);
	const [snapshot, setSnapshot] = useState<OrderSnapshot>(() => snapshotOrder(freshLive, orderSessionKey));
	const freshLiveRef = useRef(freshLive);
	freshLiveRef.current = freshLive;
	const scrollAfterResort = useRef(false);
	const held = useMemo(() => {
		// Another machine's first load joins the held order rather than replacing
		// it, so news already waiting behind the pill neither jumps nor vanishes.
		const order = absorbNewHosts(snapshot, freshLive, orderSessionKey);
		const pinned = holdScopedOrder(order, fresh.pinned);
		const sections = fresh.sections.map((section) => ({ ...section, data: holdScopedOrder(order, section.data) }));
		const shown = [...pinned, ...sections.flatMap((section) => section.data)];
		const stale = shown.some((session, i) => session !== freshLiveEntries[i]);
		return { pinned, sections, stale, updated: stale ? updatedSince(order, freshLive, orderSessionKey) : 0 };
	}, [snapshot, fresh, freshLive, freshLiveEntries]);
	const { pinned, sections } = held;
	useEffect(() => {
		if (freshLive.length === 0) return;
		if (hasNewHost(snapshot, freshLive)) {
			setSnapshot((previous) => absorbNewHosts(previous, freshLive, orderSessionKey));
			return;
		}
		if (shouldResnapshot(snapshot, freshLive, held.stale, orderSessionKey)) {
			setSnapshot(previous => snapshotOrder(freshLive, orderSessionKey, previous));
		}
	}, [snapshot, freshLive, held.stale]);
	const resort = useCallback(() => setSnapshot(previous => snapshotOrder(freshLiveRef.current, orderSessionKey, previous)), []);
	useLayoutEffect(() => {
		if (!scrollAfterResort.current) return;
		scrollAfterResort.current = false;
		listRef?.current?.scrollToOffset({ offset: 0, animated: false });
	}, [snapshot, listRef]);
	// Opening the board, coming back to the app, or a different set of workers
	// is looking again, so each re-sorts.
	useFocusEffect(resort);
	useEffect(() => {
		const sub = AppState.addEventListener("change", (state) => {
			if (state === "active") resort();
		});
		return () => sub.remove();
	}, [resort]);
	useEffect(resort, [resort, identityKey]);
	// Pulling to refresh is asking for the latest order too.
	const refreshAndResort = useCallback(async () => {
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
		if (query.trim()) {
			const data = [...filteredGroups.pinned, ...filteredGroups.sections.flatMap((section) => section.data), ...filteredGroups.archived];
			return data.length === 0 ? [] : [{ zone: "search", label: "Search results", color: t.accent, data }];
		}
		const liveSections: ListSection[] = [
			...(pinned.length ? [{ zone: "pinned" as const, label: "Pinned", color: t.amber, data: pinned }] : []),
			...sections,
		];
		if (archived.length === 0) return liveSections;
		return [
			...liveSections,
			{ zone: "archive" as const, label: "Archive", color: t.textFaint, data: archiveOpen ? archived : [] },
		];
	}, [query, filteredGroups, pinned, sections, archived, archiveOpen, t]);

	// Headers and rows as one array of siblings, so a row changing section is a
	// reorder rather than an unmount. See BoardRow.
	const listData = useMemo<BoardRow[]>(
		() =>
			listSections.flatMap((section): BoardRow[] => {
				if (section.zone === "archive") {
					return [
						{ kind: "archive", key: "header:archive" } as const,
						...section.data.map((entry) => ({ kind: "session", key: resourceKey(entry.source, entry.value.id), entry }) as const),
					];
				}
				const collapsible = !ALWAYS_OPEN.has(section.zone);
				const open = !collapsible || !collapsedSections[section.zone];
				return [
					{ kind: "header", key: `header:${section.zone}`, label: section.label, open, collapsible } as const,
					...(open
						? section.data.map((entry) => ({ kind: "session", key: resourceKey(entry.source, entry.value.id), entry }) as const)
						: []),
				];
			}),
		[collapsedSections, listSections],
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

	const updateWorkerPin = useCallback(async (entry: Scoped<DashboardSession>, pinned: boolean) => {
		try {
			await setWorkerPinnedOn(entry.source, entry.value.id, pinned);
			haptics.success();
		} catch (cause) {
			haptics.error();
			Alert.alert("Couldn't update pin", userFacingError(cause, "Please try again."));
		}
	}, [setWorkerPinnedOn]);

	// Resume restarts a stopped agent; restore brings back a terminated session.
	// Both are recoveries rather than destructive, so neither asks first — the
	// failure path is an alert, not a confirmation.
	const runWorkerRecovery = useCallback(async (entry: Scoped<DashboardSession>, kind: "resume" | "restore") => {
		haptics.tap();
		try {
			await (kind === "resume" ? resumeAgentOn(entry.source, entry.value.id) : restoreOn(entry.source, entry.value.id));
			haptics.success();
		} catch (cause) {
			haptics.error();
			Alert.alert(
				kind === "resume" ? "Couldn't resume the agent" : "Couldn't restore the session",
				userFacingError(cause, "Please try again."),
			);
		}
	}, [restoreOn, resumeAgentOn]);

	const confirmDeleteSession = useCallback((entry: Scoped<DashboardSession>) => {
		haptics.warning();
		Alert.alert(
			"Archive session?",
			archiveSessionMessage(entry.value.displayName?.trim() || "this worker"),
			[
				{ text: "Cancel", style: "cancel" },
				{ text: "Archive session", style: "destructive", onPress: () => void killOn(entry.source, entry.value.id).catch(() => {}) },
			],
		);
	}, [killOn]);

	// Recycled list cells must never carry animated offsets across reordering.
	const toggleArchive = useCallback(() => setArchiveOpen((value) => !value), []);
	const actions = useMemo(() => ({
		renameStart: setRenamingWorkerId,
		rename: renameWorkerOn,
		pin: updateWorkerPin,
		archive: confirmDeleteSession,
		recover: runWorkerRecovery,
	}), [renameWorkerOn, updateWorkerPin, confirmDeleteSession, runWorkerRecovery]);
	const renderItem = useCallback(({ item }: { item: BoardRow }) => {
		if (item.kind === "archive") return <ArchiveHeader count={archived.length} open={archiveOpen} onToggle={toggleArchive} />;
		if (item.kind === "header") return <ListSectionHeader label={item.label} open={item.open}
			onToggle={item.collapsible ? () => toggleSection(item.key.replace("header:", "")) : undefined} />;
		return <BoundWorkerRow entry={item.entry} nowBucket={nowBucket}
			machine={item.entry.source.kind === "local" ? machines.get(item.entry.source.id) : undefined}
			projectName={showProject ? projectNameFor(item.entry) : item.entry.value.harness || "Agent"}
			isRenaming={renamingWorkerId === item.key} activeSwipeId={activeSwipeId}
			onSwipeOpen={openExclusiveSwipe} onSwipeClose={closeExclusiveSwipe} actions={actions} />;
	}, [archived.length, archiveOpen, toggleArchive, toggleSection, machines, nowBucket, showProject, projectNameFor,
		renamingWorkerId, activeSwipeId, openExclusiveSwipe, closeExclusiveSwipe, actions]);

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
					removeClippedSubviews={false}
					contentContainerStyle={{ paddingBottom: contentBottomInset }}
					keyboardDismissMode={Platform.OS === "ios" ? "interactive" : "on-drag"}
					keyboardShouldPersistTaps="handled"
					refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refreshAndResort} tintColor={t.accent} />}
					ListHeaderComponent={ListHeaderComponent}
					ListEmptyComponent={ListEmptyComponent}
					renderItem={renderItem}
				/>
			</LayoutAnimationConfig>
		{held.updated > 0 && !query.trim() ? <UpdatesPill count={held.updated} onPress={showLatest} /> : null}
		</View>
	);
}

type BoundWorkerRowProps = {
	entry: Scoped<DashboardSession>;
	machine?: { name: string; appearance: ReturnType<typeof machineAppearance> };
	projectName: string;
	nowBucket: number;
	isRenaming: boolean;
	activeSwipeId?: string;
	onSwipeOpen(id: string, close: () => void): void;
	onSwipeClose(id: string): void;
	actions: {
		renameStart(id: string | undefined): void;
		rename(source: Scoped<DashboardSession>["source"], id: string, title: string): Promise<void>;
		pin(entry: Scoped<DashboardSession>, pinned: boolean): Promise<void>;
		archive(entry: Scoped<DashboardSession>): void;
		recover(entry: Scoped<DashboardSession>, kind: "resume" | "restore"): Promise<void>;
	};
};

// Bind callbacks to the row's source, retaining their identity across board publications.
const BoundWorkerRow = memo(function BoundWorkerRow({ entry, actions, ...props }: BoundWorkerRowProps) {
	const session = entry.value;
	const rowKey = resourceKey(entry.source, entry.value.id);
	const handlers = useMemo(() => ({
		onRenameStart: () => actions.renameStart(rowKey),
		onRenameCancel: () => actions.renameStart(undefined),
		onRename: (title: string) => actions.rename(entry.source, session.id, title),
		onSetPinned: (pinned: boolean) => actions.pin(entry, pinned),
		onDelete: () => actions.archive(entry),
		onResume: () => { void actions.recover(entry, "resume"); },
		onRestore: () => { void actions.recover(entry, "restore"); },
	}), [actions, entry, session, rowKey]);
	return entry.source.kind === "local" ? <WorkerListRow {...props} interactionMode="full" session={session} source={entry.source} rowKey={rowKey} {...handlers} />
		: <WorkerListRow interactionMode="open-only" session={session} source={entry.source} projectName={props.projectName} nowBucket={props.nowBucket} rowKey={rowKey} />;
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
	});
