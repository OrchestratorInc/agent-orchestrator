import { Feather } from "./icons";
import { useFocusEffect } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactElement, type RefObject } from "react";
import { Alert, AppState, FlatList, Platform, Pressable, RefreshControl, StyleSheet, Text, View } from "react-native";
import { LayoutAnimationConfig } from "react-native-reanimated";
import {
	groupSessions,
	holdOrder,
	shouldResnapshot,
	snapshotOrder,
	updatedSince,
	type BoardSection,
	type OrderSnapshot,
} from "./agentsView";
import type { DashboardSession } from "./api";
import { BoardRowTransition } from "./BoardRowTransition";
import { haptics } from "./haptics";
import { hostedProjectKey, hostedRowKey, hostedSessionKey, sessionHostId } from "./hostedRows";
import { useApp } from "./store";
import type { Theme } from "./theme";
import { statusVisual } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { ListSectionHeader } from "./ui";
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
 * is a way to lose it. `needs_you` is the reason the app was opened at all, and
 * `pinned` is what someone deliberately put at the top; a board that can hide
 * either of those behind a chevron can hide the two things that matter most.
 * Everything else folds and stays folded, because the shape of a long board is
 * the user's call.
 */
const ALWAYS_OPEN = new Set<ListSection["zone"]>(["search", "needs_you", "pinned"]);

/**
 * One flat list, not a SectionList, and that is load-bearing.
 *
 * A row moving between sections has to stay mounted for its layout animation to
 * run. In a SectionList it changes parent, which unmounts and remounts it — so a
 * pinned row vanished from one section and reappeared in the other instead of
 * travelling there. Flattened, the same move is a reorder within one array,
 * which is exactly what LinearTransition animates.
 */
export type BoardRow =
	| { kind: "header"; key: string; label: string; open: boolean; collapsible: boolean }
	| { kind: "archive"; key: string }
	| { kind: "session"; key: string; session: DashboardSession };

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
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const { projects, allProjects, hostStates, kill, renameWorker, setWorkerPinned, restore, resumeAgent } = useApp();
	const [renamingWorkerId, setRenamingWorkerId] = useState<string>();
	const [activeSwipeId, setActiveSwipeId] = useState<string>();
	const activeSwipeRef = useRef<{ id: string; close(): void } | undefined>(undefined);
	// Collapsed by default, like desktop's archive strip: it is history, and on a
	// long-running project it is most of the sessions.
	const [archiveOpen, setArchiveOpen] = useState(initialArchiveOpen);
	// Every group can be folded away as well. Open by default — the board is a
	// working view, and a section nobody opened is a section nobody saw — but a
	// collapsed group keeps its header, so the shape of the board stays readable.
	const [collapsedSections, setCollapsedSections] = useState<Record<string, boolean>>({});
	// Minute-granular, so a memoised row still updates its relative timestamp even
	// when nothing about the session itself has changed.
	const nowBucket = Math.floor(Date.now() / 60_000);

	const projectNames = useMemo(
		() => new Map([...projects, ...allProjects].map((project) => [hostedProjectKey(project), project.name])),
		[projects, allProjects],
	);
	const multipleHosts = hostStates.length > 1;
	const projectNameFor = useCallback((session: DashboardSession) => {
		const hostId = sessionHostId(session);
		const name = session.projectId
			? projectNames.get(hostId ? hostedRowKey(hostId, session.projectId) : session.projectId) ?? session.projectId
			: "Standalone";
		const hostName = "hostName" in session && typeof session.hostName === "string" ? session.hostName : undefined;
		const offline = hostStates.find((host) => host.hostId === hostId)?.connection === "closed";
		return multipleHosts && hostName ? `${name ? `${name} · ` : ""}${hostName}${offline ? " (offline)" : ""}` : name;
	}, [hostStates, multipleHosts, projectNames]);
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
	const filteredGroups = useMemo(() => groupSessions(t, filteredSessions), [t, filteredSessions]);
	const archived = fresh.archived;

	// The order is held still while the user looks (see OrderSnapshot): rows
	// update in place and news waits behind the "N updated" pill. Clearing the
	// snapshot re-sorts; the effect below takes a new one from the fresh order.
	const [snapshot, setSnapshot] = useState<OrderSnapshot | null>(null);
	const freshLive = useMemo(() => [...fresh.pinned, ...fresh.sections.flatMap((section) => section.data)], [fresh]);
	const held = useMemo(() => {
		if (!snapshot) return { pinned: fresh.pinned, sections: fresh.sections, stale: false, updated: 0 };
		const pinned = holdOrder(snapshot, fresh.pinned, hostedSessionKey);
		const sections = fresh.sections.map((section) => ({ ...section, data: holdOrder(snapshot, section.data, hostedSessionKey) }));
		const shown = [...pinned, ...sections.flatMap((section) => section.data)];
		const stale = shown.some((session, i) => session !== freshLive[i]);
		return { pinned, sections, stale, updated: stale ? updatedSince(snapshot, freshLive, hostedSessionKey) : 0 };
	}, [snapshot, fresh, freshLive]);
	const { pinned, sections } = held;
	useEffect(() => {
		if (freshLive.length === 0) return;
		if (shouldResnapshot(snapshot, freshLive, held.stale, hostedSessionKey)) {
			setSnapshot(snapshotOrder(freshLive, hostedSessionKey));
		}
	}, [snapshot, freshLive, held.stale]);
	const resort = useCallback(() => setSnapshot(null), []);
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
		resort();
		listRef?.current?.scrollToOffset({ offset: 0, animated: true });
	}, [resort, listRef]);

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
						...section.data.map((session) => ({ kind: "session", key: hostedSessionKey(session), session }) as const),
					];
				}
				const collapsible = !ALWAYS_OPEN.has(section.zone);
				const open = !collapsible || !collapsedSections[section.zone];
				return [
					{ kind: "header", key: `header:${section.zone}`, label: section.label, open, collapsible } as const,
					...(open
						? section.data.map((session) => ({ kind: "session", key: hostedSessionKey(session), session }) as const)
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
			"Delete session?",
			`This terminates ${session.displayName?.trim() || "this worker"}. Its conversation and worktree are preserved.`,
			[
				{ text: "Cancel", style: "cancel" },
				{ text: "Delete session", style: "destructive", onPress: () => void kill(session.id, sessionHostId(session)).catch(() => {}) },
			],
		);
	}, [kill]);

	return (
		<View style={styles.listWrap}>
			{/* skipEntering so the first render and every poll-driven rebuild do not
			    cascade one animation per row. Only rows that arrive after the list is
			    already on screen animate in — which is the only case worth seeing. */}
			<LayoutAnimationConfig skipEntering>
				<FlatList
					ref={listRef}
					key={identityKey}
					data={listData}
					keyExtractor={(item) => item.key}
					contentContainerStyle={{ paddingBottom: contentBottomInset }}
					keyboardDismissMode={Platform.OS === "ios" ? "interactive" : "on-drag"}
					keyboardShouldPersistTaps="handled"
					refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refreshAndResort} tintColor={t.accent} />}
					ListHeaderComponent={ListHeaderComponent}
					ListEmptyComponent={ListEmptyComponent}
					renderItem={({ item }) => {
						// Headers animate too, so a section appearing or emptying reflows
						// with the rows rather than snapping around them.
						if (item.kind === "archive") {
							return (
								<BoardRowTransition>
									<ArchiveHeader count={archived.length} open={archiveOpen} onToggle={() => setArchiveOpen((v) => !v)} />
								</BoardRowTransition>
							);
						}
						if (item.kind === "header") {
							return (
								<BoardRowTransition>
									<ListSectionHeader
										label={item.label}
										open={item.open}
										onToggle={item.collapsible ? () => toggleSection(item.key.replace("header:", "")) : undefined}
									/>
								</BoardRowTransition>
							);
						}
					const session = item.session;
					const rowKey = hostedSessionKey(session);
					return (
						<BoardRowTransition>
							<WorkerListRow
								nowBucket={nowBucket}
								session={session}
								rowKey={rowKey}
								projectName={showProject ? projectNameFor(session) : session.harness || "Agent"}
								isRenaming={renamingWorkerId === rowKey}
								activeSwipeId={activeSwipeId}
								onSwipeOpen={openExclusiveSwipe}
								onSwipeClose={closeExclusiveSwipe}
								onRenameStart={() => setRenamingWorkerId(rowKey)}
								onRenameCancel={() => setRenamingWorkerId(undefined)}
								onRename={(title) => renameWorker(session.id, title, sessionHostId(session))}
								onSetPinned={(next) => updateWorkerPin(session, next)}
								onDelete={() => confirmDeleteSession(session)}
								onResume={() => runWorkerRecovery(session, "resume")}
								onRestore={() => runWorkerRecovery(session, "restore")}
							/>
						</BoardRowTransition>
					);
					}}
				/>
			</LayoutAnimationConfig>
		{held.updated > 0 && !query.trim() ? <UpdatesPill count={held.updated} onPress={showLatest} /> : null}
		</View>
	);
}

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
