import AsyncStorage from "@react-native-async-storage/async-storage";
import { useGlobalSearchParams, usePathname, useRouter } from "expo-router";
import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Alert, Animated, Easing, FlatList, Pressable, RefreshControl, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { SFSymbol } from "sf-symbols-typescript";
import { formatTimeTerse } from "../../../product-ui/src/formatting";
import { loadAccount, onAccountChange, signInToAccount, type Account } from "../account";
import { AccountAvatar, accountName } from "../AccountAvatar";
import { syncAccountHosts } from "../accountHosts";
import { useCloud } from "../cloud/CloudProvider";
import { isCloudHost } from "../cloud/mapping";
import { userFacingError } from "../connectionError";
import { haptics } from "../haptics";
import { hostedProjectSections, hostedRowKey, type HostedProjectRow } from "../hostedRows";
import { Feather, type FeatherIconName } from "../icons";
import { LongPressMenu, type LongPressMenuAction } from "../long-press-menu";
import { OrchestratorIcon } from "../orchestrator-icon";
import { useOpenPage } from "../pageNavigation";
import { STANDALONE_PROJECT } from "../projectFilter";
import { reviewRouteForSession } from "../reviewView";
import { useApp } from "../store";
import type { MachineFreshness } from "../machineFreshness";
import { useMachineFreshness } from "../useMachineFreshness";
import { sortWorkerEnvironments } from "../worker-controls";
import { refreshControlColors } from "../refreshControlColors";
import { UnpairedState } from "../UnpairedState";
import { useRefreshActions } from "../useRefreshActions";
import { useTheme, useThemeState } from "../ThemeProvider";
import { THEMES } from "../../../shared/theme/palettes";
import type { Theme } from "../theme";
import { textSelectionColors } from "../textSelection";
import { font } from "../tokens";
import { useOrchestratorLauncher } from "../useOrchestratorLauncher";
import { useReducedMotion } from "../useReducedMotion";
import { ARCHIVE_SESSION_CONFIRM, workerContextActions, type WorkerActionId } from "../worker-action-model";
import { changedAt, isUnread, loadReadState, markRead, useReadState } from "./readState";
import { useSidebarMaybe } from "./sidebarContext";
import {
	buildSidebarTree,
	capRows,
	projectLetter,
	projectTileColor,
	SIDEBAR_INITIAL_SECTION_LIMIT,
	SIDEBAR_PROJECT_SESSION_LIMIT,
	SIDEBAR_SESSIONS_LIMIT,
	sidebarProjectKey,
	sidebarSessionKey,
	type SidebarIndicator,
	type SidebarProjectNode,
	type SidebarSessionRow,
} from "./sidebarModel";

// Desktop sizing (frontend/src/renderer/components/Sidebar.tsx), raised to a
// 36pt row where desktop uses 32px: DESIGN.md §8 asks for 36 when the
// interaction is imprecise, and a finger is. Type, icon sizes, insets and
// colours are the desktop's.
const ROW_HEIGHT = 36;
const ROW_RADIUS = 8;
const ROW_INSET = 10;
const ICON = 16;
const LABEL_SIZE = 14;
const ACTION_SIZE = 36;
const SESSION_INDENT = 14;

type Palette = {
	bg: string;
	text: string;
	muted: string;
	passive: string;
	hover: string;
	active: string;
	border: string;
	working: string;
	attention: string;
	ready: string;
	review: string;
	merged: string;
};

/** The desktop sidebar's colours, from the shared palette where it names them. */
export function sidebarPalette(t: Theme, scheme: "light" | "dark", style: string): Palette {
	const theme = THEMES.find((item) => item.id === style) ?? THEMES[0];
	const roles = scheme === "dark" ? theme.dark : theme.light;
	return {
		bg: roles.sidebar,
		text: t.textPrimary,
		muted: t.textSecondary,
		passive: t.textTertiary,
		// color-mix(foreground 4% / 7%) on desktop.
		hover: withAlpha(t.textPrimary, 0.04),
		active: withAlpha(t.textPrimary, 0.07),
		border: t.borderSubtle,
		working: t.blue,
		attention: t.red,
		ready: t.green,
		review: t.amber,
		merged: t.purple,
	};
}

function withAlpha(hex: string, alpha: number): string {
	const match = /^#([0-9a-f]{6})$/i.exec(hex);
	if (!match) return hex;
	const value = parseInt(match[1], 16);
	return `rgba(${(value >> 16) & 255},${(value >> 8) & 255},${value & 255},${alpha})`;
}

// ── Persisted disclosure state ────────────────────────────────────────────────

const PREFS_KEY = "ao.sidebar.prefs.v2";
/** `collapsed`: projects the user folded. Home shows every project's dropdown until then. */
type Prefs = { collapsed: string[]; pinnedProjects: string[]; closedSections: string[]; device: string };
/** `device`: the devices bar's choice, a host id; empty shows every device. */
const EMPTY_PREFS: Prefs = { collapsed: [], pinnedProjects: [], closedSections: [], device: "" };

function useSidebarPrefs() {
	const [prefs, setPrefs] = useState<Prefs>(EMPTY_PREFS);
	const loaded = useRef(false);
	useEffect(() => {
		void AsyncStorage.getItem(PREFS_KEY).then((raw) => {
			loaded.current = true;
			if (raw) setPrefs({ ...EMPTY_PREFS, ...JSON.parse(raw) as Partial<Prefs> });
		}).catch(() => { loaded.current = true; });
	}, []);
	const update = useCallback((next: (prev: Prefs) => Prefs) => {
		setPrefs((prev) => {
			const value = next(prev);
			void AsyncStorage.setItem(PREFS_KEY, JSON.stringify(value)).catch(() => {});
			return value;
		});
	}, []);
	const setDevice = useCallback((device: string) => update((prev) => ({ ...prev, device })), [update]);
	const toggleIn = useCallback((field: "collapsed" | "pinnedProjects" | "closedSections", key: string) => update((prev) => ({
		...prev,
		[field]: prev[field].includes(key) ? prev[field].filter((item) => item !== key) : [...prev[field], key],
	})), [update]);
	return { prefs, toggleIn, setDevice };
}

// ── The sidebar ───────────────────────────────────────────────────────────────

/**
 * The desktop's left sidebar, split the way a phone wants it.
 *
 * `home` is the app's main view: Sessions (agents with no project) and Projects
 * with their sessions, a header with the sidebar button and Search, and New
 * chat floating at the bottom right. `drawer` is the side bar that slides in
 * from the left with the rest: Pull requests, All workers, Notifications,
 * Pinned, and the footer with the account, machines and Settings. Both read
 * the same rows and run the same actions.
 */
export function Sidebar({ part, close }: { part: "home" | "drawer"; close?: () => void }) {
	const { theme: t, scheme, style } = useThemeState();
	const colors = useMemo(() => sidebarPalette(t, scheme, style), [t, scheme, style]);
	const styles = useMemo(() => makeStyles(colors), [colors]);
	const insets = useSafeAreaInsets();
	const router = useRouter();
	const pathname = usePathname();
	const params = useGlobalSearchParams<{ id?: string; hostId?: string }>();
	const app = useApp();
	// The store is a new object on every update; callbacks the rows hold read it
	// through a ref so they stay the same function, and a row's memo holds.
	const appRef = useRef(app);
	appRef.current = app;
	const cloudState = useCloud();
	const read = useReadState();
	const { prefs, toggleIn, setDevice } = useSidebarPrefs();
	const freshness = useMachineFreshness();
	const { busyProjects, openOrchestrator } = useOrchestratorLauncher();
	const openPage = useOpenPage();
	const [query, setQuery] = useState<string | null>(null);
	const [renamingKey, setRenamingKey] = useState<string>();
	const [showAll, setShowAll] = useState<Record<string, boolean>>({});

	useEffect(() => { void loadReadState(); }, []);

	// Home is under every page it opens, so the row of the page on top stays lit.
	const onSession = pathname.startsWith("/session/");
	const activeKey = onSession && params.id ? sidebarSessionKey({ id: String(params.id), hostId: params.hostId ? String(params.hostId) : undefined }) : undefined;
	const { refreshing, onRefresh } = useRefreshActions(app.refreshAll);
	const environments = app.hostStates.length + (app.cloud ? 1 : 0);
	// The devices bar: every paired machine and Orchestrator Cloud, in name order.
	const devices = useMemo(() => sortWorkerEnvironments([...app.hostStates, ...(app.cloud ? [app.cloud] : [])]
		.map((host) => ({ id: host.hostId, name: host.name, appearance: host.appearance }))), [app.cloud, app.hostStates]);
	// A device that has gone (forgotten, signed out) shows everything again.
	const device = devices.some((item) => item.id === prefs.device) ? prefs.device : "";

	const tree = useMemo(() => buildSidebarTree({
		sessions: app.allSessions,
		projects: app.allProjects,
		showHosts: !device && environments > 1,
		hostId: device || undefined,
		activeKey,
		isUnread: (session) => isUnread(read, sidebarSessionKey(session), changedAt(session)),
		isCloudHost,
		query: query ?? "",
	}), [activeKey, app.allProjects, app.allSessions, device, environments, query, read]);

	// Each project's orchestrator, as the Projects page found it.
	const projectRows = useMemo(() => {
		const map = new Map<string, HostedProjectRow>();
		for (const section of hostedProjectSections(app.cloud ? [...app.hostStates, app.cloud] : app.hostStates)) {
			for (const row of section.data) map.set(sidebarProjectKey(row.project), row);
		}
		return map;
	}, [app.cloud, app.hostStates]);

	const projectRowsRef = useRef(projectRows);
	projectRowsRef.current = projectRows;

	// Pinned projects first, in the order they were pinned (desktop's pinnedFirst).
	const projects = useMemo(() => {
		const pinned = prefs.pinnedProjects;
		if (pinned.length === 0) return tree.projects;
		const rank = (node: SidebarProjectNode) => pinned.indexOf(node.key);
		return [...tree.projects.filter((node) => rank(node) >= 0).sort((a, b) => rank(a) - rank(b)), ...tree.projects.filter((node) => rank(node) < 0)];
	}, [prefs.pinnedProjects, tree.projects]);

	const go = useCallback((href: Parameters<typeof router.navigate>[0]) => {
		close?.();
		router.navigate(href);
	}, [close, router]);

	// A session opens over home, and back returns there.
	const openSession = useCallback((row: SidebarSessionRow) => {
		const session = row.session;
		markRead(row.key, changedAt(session));
		close?.();
		router.push({ pathname: "/session/[id]", params: { id: session.id, projectId: session.projectId, hostId: session.hostId ?? appRef.current.currentHostId } });
	}, [close, router]);

	const newTask = useCallback((projectId?: string, hostId?: string) => {
		go(projectId ? { pathname: "/spawn", params: { projectId, ...(hostId ? { hostId } : {}) } } : "/spawn");
	}, [go]);

	const launchOrchestrator = useCallback((node: SidebarProjectNode) => {
		const row = projectRowsRef.current.get(node.key);
		if (!row) return;
		// Opens a running orchestrator, or starts or resumes one, as the Projects page did.
		close?.();
		openOrchestrator(row);
	}, [close, openOrchestrator]);
	const toggleProject = useCallback((node: SidebarProjectNode) => toggleIn("collapsed", node.key), [toggleIn]);
	const pinProject = useCallback((node: SidebarProjectNode) => toggleIn("pinnedProjects", node.key), [toggleIn]);
	const newProjectTask = useCallback((node: SidebarProjectNode) => newTask(node.project.id, node.project.hostId), [newTask]);

	const sessionAction = useCallback((row: SidebarSessionRow, id: WorkerActionId) => {
		const app = appRef.current;
		const session = row.session;
		const hostId = session.hostId;
		const fail = (title: string) => (cause: unknown) => {
			haptics.error();
			Alert.alert(title, userFacingError(cause, "Please try again."));
		};
		switch (id) {
			case "open":
				return openSession(row);
			case "pin":
			case "unpin":
				return void app.setWorkerPinned(session.id, id === "pin", hostId).then(() => haptics.success(), fail("Couldn't update pin"));
			case "rename":
				return setRenamingKey(row.key);
			case "resume":
				return void app.resumeAgent(session.id, hostId).then(() => haptics.success(), fail("Couldn't resume the agent"));
			case "restore":
				return void app.restore(session.id, hostId).then(() => haptics.success(), fail("Couldn't restore the session"));
			case "openPr": {
				const route = reviewRouteForSession(session, hostId ?? app.currentHostId);
				if (route) { close?.(); openPage(route); }
				return;
			}
			default:
				haptics.warning();
				Alert.alert(ARCHIVE_SESSION_CONFIRM.title, ARCHIVE_SESSION_CONFIRM.message, [
					{ text: ARCHIVE_SESSION_CONFIRM.cancel, style: "cancel" },
					{ text: ARCHIVE_SESSION_CONFIRM.confirm, style: "destructive", onPress: () => void app.kill(session.id, hostId).catch(fail("Couldn't archive the session")) },
				]);
		}
	}, [close, openPage, openSession]);

	const commitRename = useCallback((row: SidebarSessionRow, name: string) => {
		const app = appRef.current;
		setRenamingKey(undefined);
		const next = name.trim();
		if (!next || next === row.title) return;
		void app.renameWorker(row.session.id, next, row.session.hostId).then(() => haptics.success(), (cause: unknown) => {
			haptics.error();
			Alert.alert("Couldn't rename the session", userFacingError(cause, "Please try again."));
		});
	}, []);
	const cancelRename = useCallback(() => setRenamingKey(undefined), []);

	const sessionRow = (row: SidebarSessionRow, indented: boolean) => (
		<SessionRow
			key={row.key}
			row={row}
			indented={indented}
			active={row.key === activeKey}
			renaming={renamingKey === row.key}
			width={rowWidth}
			colors={colors}
			styles={styles}
			onOpen={openSession}
			onAction={sessionAction}
			onRename={commitRename}
			onCancelRename={cancelRename}
		/>
	);

	const unread = (app.hostStates.length > 1
		? app.hostStates.reduce((count, host) => count + host.notificationsUnread, 0)
		: app.notificationsUnread) + (app.cloud ? cloudState.notificationsUnread : 0);
	const searching = query !== null;
	const sectionOpen = (key: string) => searching || !prefs.closedSections.includes(key);
	const standalone = capRows(tree.standalone, SIDEBAR_SESSIONS_LIMIT, searching || showAll.standalone === true, activeKey);
	const listedProjects = capRows(projects, SIDEBAR_INITIAL_SECTION_LIMIT, searching || showAll.projects === true);
	const paired = app.hostStates.length > 0 || Boolean(app.cloud);
	const sidebar = useSidebarMaybe();

	// Home is one virtualized list, as the board before it was: only the rows
	// on screen are mounted, each with its native menu, instead of every
	// project and session at once. The list (home's, or the drawer's) is
	// measured once and gives its session rows their width, which iOS's menu
	// host needs to truncate a long title rather than grow past the edge.
	const [listWidth, setListWidth] = useState<number>();
	const onListLayout = useCallback((event: { nativeEvent: { layout: { width: number } } }) => {
		const next = Math.round(event.nativeEvent.layout.width);
		setListWidth((prev) => (prev === next ? prev : next));
	}, []);
	// The rows' width inside the list's side padding.
	const rowWidth = listWidth ? listWidth - LIST_PADDING * 2 : undefined;
	const homeItems = useMemo((): HomeItem[] => {
		if (part !== "home") return [];
		const items: HomeItem[] = [{ kind: "section", id: "projects" }];
		if (sectionOpen("projects")) {
			if (!paired) items.push({ kind: "unpaired" });
			else if (projects.length === 0) items.push({ kind: "empty", text: searching ? "No matches." : "No projects yet." });
			for (const node of listedProjects.listed) {
				const expanded = searching || !prefs.collapsed.includes(node.key);
				items.push({ kind: "project", node, expanded });
				if (!expanded) continue;
				const row = projectRows.get(node.key);
				if (row) items.push({ kind: "orchestrator", node, row });
				const capped = capRows(node.sessions, SIDEBAR_PROJECT_SESSION_LIMIT, searching || showAll[node.key] === true, activeKey);
				for (const session of capped.listed) items.push({ kind: "session", row: session, indented: true });
				if (capped.hiddenCount > 0) {
					items.push({ kind: "more", id: node.key, indented: true, label: showAll[node.key] ? "Show fewer agents" : `Show ${capped.hiddenCount} more agent${capped.hiddenCount === 1 ? "" : "s"}` });
				}
			}
			if (listedProjects.hiddenCount > 0) {
				items.push({ kind: "more", id: "projects", indented: false, label: showAll.projects ? "Show fewer projects" : `Show ${listedProjects.hiddenCount} more project${listedProjects.hiddenCount === 1 ? "" : "s"}` });
			}
		}
		if (tree.standalone.length > 0 || !searching) {
			items.push({ kind: "section", id: "standalone" });
			if (sectionOpen("standalone")) {
				for (const session of standalone.listed) items.push({ kind: "session", row: session, indented: false });
				if (standalone.hiddenCount > 0) items.push({ kind: "more", id: "standalone", indented: false, label: showAll.standalone ? "Show less" : `Show ${standalone.hiddenCount} more` });
			}
		}
		return items;
		// sectionOpen, listedProjects and standalone are derived from the values listed.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [part, prefs, paired, projects, searching, showAll, projectRows, activeKey, tree.standalone]);
	const toggleShowAll = useCallback((id: string) => setShowAll((prev) => ({ ...prev, [id]: !prev[id] })), []);
	const homeExtra = useMemo(() => ({ activeKey, renamingKey, rowWidth, busyProjects, pathname, projectId: params.id, colors, styles }), [activeKey, renamingKey, rowWidth, busyProjects, pathname, params.id, colors, styles]);
	const renderHomeItem = ({ item }: { item: HomeItem }) => {
		switch (item.kind) {
			case "section": {
				const projectsSection = item.id === "projects";
				return (
					<View style={projectsSection ? null : styles.sectionGap}>
					<SectionHeader
						label={projectsSection ? "Projects" : "Sessions"}
						open={sectionOpen(item.id)}
						onToggle={() => toggleIn("closedSections", item.id)}
						trailing={projectsSection
							? <SectionAction icon="plus" label="New project" onPress={() => go("/cloud/new-project")} colors={colors} />
							: paired ? <SectionAction icon="plus" label="New standalone agent" onPress={() => newTask(STANDALONE_PROJECT)} colors={colors} /> : null}
						styles={styles}
						colors={colors}
					/>
					</View>
				);
			}
			case "unpaired":
				return <UnpairedState />;
			case "empty":
				return <Text style={styles.empty}>{item.text}</Text>;
			case "project": {
				const node = item.node;
				const row = projectRows.get(node.key);
				return (
					<ProjectRow
						node={node}
						expanded={item.expanded}
						pinned={prefs.pinnedProjects.includes(node.key)}
						orchestratorAction={row?.action}
						orchestratorBusy={busyProjects.has(hostedRowKey(node.project.hostId ?? "", node.project.id))}
						colors={colors}
						styles={styles}
						onToggle={toggleProject}
						onOrchestrator={launchOrchestrator}
						onNewTask={newProjectTask}
						onTogglePin={pinProject}
					/>
				);
			}
			case "orchestrator": {
				const { node, row } = item;
				return (
					<OrchestratorRow
						action={row.action}
						busy={busyProjects.has(hostedRowKey(node.project.hostId ?? "", node.project.id))}
						active={Boolean(row.link?.id) && activeKey === sidebarSessionKey({ id: row.link!.id, hostId: node.project.hostId })}
						colors={colors}
						styles={styles}
						onPress={() => launchOrchestrator(node)}
						testID={`sidebar-orchestrator-row-${node.project.id}`}
					/>
				);
			}
			case "session":
				return sessionRow(item.row, item.indented);
			case "more":
				return (
					<View style={item.indented ? { paddingLeft: SESSION_INDENT + 2 } : null}>
						<ShowMore expanded={showAll[item.id] === true} label={item.label} onPress={() => toggleShowAll(item.id)} styles={styles} colors={colors} />
					</View>
				);
		}
	};

	if (part === "drawer") {
		return (
			<View style={[styles.root, styles.drawerRoot, { paddingTop: insets.top + 8, paddingBottom: Math.max(insets.bottom, 8) }]} testID="sidebar-drawer">
				<View style={styles.brand}>
					<Text accessibilityRole="header" style={styles.brandLabel} numberOfLines={1}>Orchestrator</Text>
				</View>
				<View style={styles.fixed}>
					<NavRow icon="git-pull-request" label="Pull requests" onPress={() => go("/prs")} styles={styles} colors={colors} active={pathname.startsWith("/prs")} />
					<NavRow icon="layout-grid" label="All workers" onPress={() => go("/board")} styles={styles} colors={colors} disabled={!paired} />
					<NavRow icon="bell" label="Notifications" onPress={() => go("/notifications")} styles={styles} colors={colors} active={pathname.startsWith("/notifications")} trailing={unread > 0 ? String(unread) : undefined} />
				</View>
				<ScrollView style={styles.scroll} contentContainerStyle={styles.scrollContent} showsVerticalScrollIndicator={false} onLayout={onListLayout}>
					{tree.pinned.length > 0 ? (
						<Section label="Pinned" open={sectionOpen("pinned")} onToggle={() => toggleIn("closedSections", "pinned")} styles={styles} colors={colors}>
							{tree.pinned.map((row) => sessionRow(row, false))}
						</Section>
					) : null}
				</ScrollView>
				<Footer
					styles={styles}
					colors={colors}
					onSettings={() => go("/settings")}
					onMachines={() => go("/pair")}
				/>
			</View>
		);
	}

	return (
		<View style={[styles.root, { paddingTop: insets.top }]} testID="sidebar-home">
			<View style={styles.homeHeader}>
				<HeaderButton icon="panel-left" label={unread > 0 ? `Open sidebar, ${unread} unread notification${unread === 1 ? "" : "s"}` : "Open sidebar"} onPress={() => sidebar?.open()} colors={colors} dot={unread > 0} testID="sidebar-open" />
				<Text accessibilityRole="header" style={[styles.brandLabel, styles.homeTitle]} numberOfLines={1}>Orchestrator</Text>
				<HeaderButton icon={searching ? "x" : "search"} label={searching ? "Close search" : "Search"} onPress={() => setQuery(searching ? null : "")} colors={colors} />
			</View>
			{devices.length > 0 ? (
				<DevicesBar devices={devices} selected={device} freshness={freshness} onSelect={setDevice} colors={colors} styles={styles} />
			) : null}
			{searching ? (
				<View style={styles.fixed}>
					<View style={[styles.navRow, styles.searchRow]}>
						<Feather name="search" size={ICON} color={colors.muted} />
						<TextInput
							autoFocus
							value={query ?? ""}
							onChangeText={setQuery}
							placeholder="Search sessions and projects"
							placeholderTextColor={colors.passive}
							style={styles.searchInput}
							returnKeyType="search"
							{...textSelectionColors(t)}
							accessibilityLabel="Search sessions and projects"
						/>
					</View>
				</View>
			) : null}

			<FlatList
				data={homeItems}
				keyExtractor={homeItemKey}
				renderItem={renderHomeItem}
				extraData={homeExtra}
				onLayout={onListLayout}
				style={styles.scroll}
				contentContainerStyle={[styles.scrollContent, { paddingBottom: FAB_HEIGHT + 32 + insets.bottom }]}
				keyboardShouldPersistTaps="handled"
				showsVerticalScrollIndicator={false}
				initialNumToRender={18}
				maxToRenderPerBatch={12}
				windowSize={9}
				refreshControl={<RefreshControl refreshing={refreshing} onRefresh={onRefresh} {...refreshControlColors(t)} />}
				testID="sidebar-home-list"
			/>

			{/* New chat, floating at the bottom right over the list. */}
			{paired ? (
				<Pressable
					accessibilityRole="button"
					accessibilityLabel="New chat"
					onPress={() => newTask()}
					style={({ pressed }) => [styles.fab, { bottom: Math.max(insets.bottom, 16) + 8, backgroundColor: t.accent }, pressed && styles.fabPressed]}
					testID="new-chat"
				>
					<Feather name="edit" size={18} color={t.onAccent} />
					<Text style={[styles.fabLabel, { color: t.onAccent }]}>New chat</Text>
				</Pressable>
			) : null}
		</View>
	);
}

/**
 * The project's orchestrator, leading its dropdown above the workers it
 * coordinates: open when it runs, start or resume when it does not.
 */
function OrchestratorRow({ action, busy, active, colors, styles, onPress, testID }: {
	action: "open" | "start" | "resume";
	busy: boolean;
	active: boolean;
	colors: Palette;
	styles: Styles;
	onPress(): void;
	testID?: string;
}) {
	// Its state only: the workers are listed right under it.
	const detail = action === "open" ? "Running" : action === "resume" ? "Stopped" : "Not started";
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={`Orchestrator, ${detail}`}
			accessibilityHint={action === "open" ? "Opens the orchestrator" : action === "resume" ? "Resumes the orchestrator" : "Starts the orchestrator"}
			disabled={busy}
			onPress={onPress}
			style={({ pressed }) => [styles.sessionRow, styles.indented, active && { backgroundColor: colors.active }, pressed && !active && { backgroundColor: colors.hover }]}
			testID={testID}
		>
			<View style={glyphSlot}>
				{busy ? <Spinner color={colors.working} /> : <OrchestratorIcon size={12} color={action === "open" ? colors.text : colors.muted} />}
			</View>
			<Text style={[styles.sessionTitle, (active || action === "open") && styles.sessionTitleBright]} numberOfLines={1}>Orchestrator</Text>
			<Text style={styles.age}>{detail}</Text>
		</Pressable>
	);
}

/**
 * The devices bar: a thin row of chips under home's header, one per paired
 * machine and Orchestrator Cloud, each with its connection dot. Choosing one
 * shows only that device's projects and sessions; All shows every device.
 */
function DevicesBar({ devices, selected, freshness, onSelect, colors, styles }: {
	devices: readonly { id: string; name: string }[];
	selected: string;
	freshness: ReadonlyMap<string, MachineFreshness>;
	onSelect(id: string): void;
	colors: Palette;
	styles: Styles;
}) {
	const chip = (id: string, label: string, dot?: string) => {
		const on = selected === id;
		return (
			<Pressable
				key={id || "all"}
				accessibilityRole="button"
				accessibilityState={{ selected: on }}
				accessibilityLabel={id ? `${label}, ${freshnessLabel(freshness.get(id))}` : "All devices"}
				onPress={() => { haptics.select(); onSelect(on && id ? "" : id); }}
				hitSlop={{ top: 8, bottom: 8 }}
				style={[styles.deviceChip, on && { backgroundColor: colors.active, borderColor: colors.active }]}
				testID={`device-${id || "all"}`}
			>
				{dot ? <View style={[styles.deviceDot, { backgroundColor: dot }]} /> : null}
				<Text style={[styles.deviceLabel, on && styles.navLabelActive]} numberOfLines={1}>{label}</Text>
			</Pressable>
		);
	};
	return (
		<ScrollView horizontal showsHorizontalScrollIndicator={false} style={styles.devicesBar} contentContainerStyle={styles.devicesContent} testID="devices-bar">
			{chip("", "All")}
			{devices.map((item) => chip(item.id, item.name, freshnessColor(freshness.get(item.id), colors)))}
		</ScrollView>
	);
}

// The freshness map lists only machines that are not live; a missing entry is live.
function freshnessColor(state: MachineFreshness | undefined, colors: Palette): string {
	return state === "unreachable" ? colors.passive : state === "lagging" ? colors.review : colors.ready;
}

function freshnessLabel(state: MachineFreshness | undefined): string {
	return state === "unreachable" ? "can't be reached" : state === "lagging" ? "slow to answer" : "connected";
}

/** A 36pt square header control, the desktop sidebar's icon-button size. */
function HeaderButton({ icon, label, onPress, colors, dot = false, testID }: { icon: FeatherIconName; label: string; onPress(): void; colors: Palette; dot?: boolean; testID?: string }) {
	return (
		<Pressable accessibilityRole="button" accessibilityLabel={label} onPress={onPress} hitSlop={4} style={({ pressed }) => [actionStyle, pressed && { backgroundColor: colors.hover }]} testID={testID}>
			<Feather name={icon} size={18} color={colors.text} />
			{dot ? <View style={[headerDot, { backgroundColor: colors.attention, borderColor: colors.bg }]} /> : null}
		</Pressable>
	);
}

const headerDot = { position: "absolute", top: 7, right: 7, width: 8, height: 8, borderRadius: 4, borderWidth: 1.5 } as const;
const FAB_HEIGHT = 48;
/** The home list's side padding (styles.scrollContent). */
const LIST_PADDING = 8;

/** One row of home's list, flattened from its sections so the list can be virtualized. */
type HomeItem =
	| { kind: "section"; id: "projects" | "standalone" }
	| { kind: "unpaired" }
	| { kind: "empty"; text: string }
	| { kind: "project"; node: SidebarProjectNode; expanded: boolean }
	| { kind: "orchestrator"; node: SidebarProjectNode; row: HostedProjectRow }
	| { kind: "session"; row: SidebarSessionRow; indented: boolean }
	| { kind: "more"; id: string; indented: boolean; label: string };

function homeItemKey(item: HomeItem): string {
	switch (item.kind) {
		case "section": return `section:${item.id}`;
		case "unpaired": return "unpaired";
		case "empty": return "empty";
		case "project": return `project:${item.node.key}`;
		case "orchestrator": return `orchestrator:${item.node.key}`;
		case "session": return `session:${item.row.key}`;
		case "more": return `more:${item.id}`;
	}
}

/**
 * The width a row's slot gives it. iOS's native menu host sizes what it wraps
 * to its content rather than stretching it, so a row inside a long-press menu
 * is given its slot's width explicitly.
 */
function useSlotWidth() {
	const [width, setWidth] = useState<number>();
	const onLayout = useCallback((event: { nativeEvent: { layout: { width: number } } }) => {
		const next = Math.round(event.nativeEvent.layout.width);
		setWidth((prev) => (prev === next ? prev : next));
	}, []);
	return { width, onLayout };
}

// ── Rows ──────────────────────────────────────────────────────────────────────

type Styles = ReturnType<typeof makeStyles>;

function NavRow({ icon, label, onPress, active = false, disabled = false, trailing, styles, colors }: {
	icon: FeatherIconName;
	label: string;
	onPress(): void;
	active?: boolean;
	disabled?: boolean;
	trailing?: string;
	styles: Styles;
	colors: Palette;
}) {
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={trailing ? `${label}, ${trailing}` : label}
			accessibilityState={{ selected: active, disabled }}
			disabled={disabled}
			onPress={onPress}
			style={({ pressed }) => [styles.navRow, (active || pressed) && { backgroundColor: active ? colors.active : colors.hover }, disabled && styles.disabled]}
		>
			<Feather name={icon} size={ICON} color={active ? colors.text : colors.muted} />
			<Text style={[styles.navLabel, active && styles.navLabelActive]} numberOfLines={1}>{label}</Text>
			{trailing ? <Text style={styles.navTrailing}>{trailing}</Text> : null}
		</Pressable>
	);
}

function Section({ label, open, onToggle, trailing, children, styles, colors }: {
	label: string;
	open: boolean;
	onToggle(): void;
	trailing?: ReactNode;
	children: ReactNode;
	styles: Styles;
	colors: Palette;
}) {
	return (
		<View style={styles.section}>
			<SectionHeader label={label} open={open} onToggle={onToggle} trailing={trailing} styles={styles} colors={colors} />
			{open ? children : null}
		</View>
	);
}

function SectionHeader({ label, open, onToggle, trailing, styles, colors }: {
	label: string;
	open: boolean;
	onToggle(): void;
	trailing?: ReactNode;
	styles: Styles;
	colors: Palette;
}) {
	return (
		<View style={styles.sectionHeader}>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={label}
				accessibilityState={{ expanded: open }}
				onPress={onToggle}
				style={styles.sectionToggle}
			>
				<Text style={styles.sectionLabel}>{label}</Text>
				<View style={{ transform: [{ rotate: open ? "90deg" : "0deg" }] }}>
					<Feather name="chevron-right" size={12} color={colors.passive} />
				</View>
			</Pressable>
			{trailing}
		</View>
	);
}

function SectionAction({ icon, label, onPress, colors }: { icon: FeatherIconName; label: string; onPress(): void; colors: Palette }) {
	return (
		<Pressable
			accessibilityRole="button"
			accessibilityLabel={label}
			onPress={onPress}
			style={({ pressed }) => [actionStyle, pressed && { backgroundColor: colors.hover }]}
			testID={`sidebar-${label.toLowerCase().replace(/\s+/g, "-")}`}
		>
			<Feather name={icon} size={ICON} color={colors.muted} />
		</Pressable>
	);
}

const actionStyle = { width: ACTION_SIZE, height: ACTION_SIZE, borderRadius: ROW_RADIUS, alignItems: "center", justifyContent: "center" } as const;

function ShowMore({ label, onPress, styles }: { label: string; expanded: boolean; onPress(): void; styles: Styles; colors: Palette }) {
	return (
		<Pressable accessibilityRole="button" accessibilityLabel={label} onPress={onPress} style={styles.showMore}>
			<Text style={styles.showMoreLabel}>{label}</Text>
		</Pressable>
	);
}

const PROJECT_MENU_SYMBOLS: Record<string, { icon: FeatherIconName; systemImage: SFSymbol }> = {
	"new-task": { icon: "plus", systemImage: "plus" },
	orchestrator: { icon: "layers", systemImage: "point.3.connected.trianglepath.dotted" },
	pin: { icon: "pin", systemImage: "pin" },
	unpin: { icon: "pin-off", systemImage: "pin.slash" },
};

type ProjectMenuId = "new-task" | "orchestrator" | "pin" | "unpin";

type ProjectRowProps = {
	node: SidebarProjectNode;
	expanded: boolean;
	pinned: boolean;
	/** The project's orchestrator, when it has one: what pressing it does, and whether it is on its way. */
	orchestratorAction?: "open" | "start" | "resume";
	orchestratorBusy: boolean;
	colors: Palette;
	styles: Styles;
	onToggle(node: SidebarProjectNode): void;
	onOrchestrator(node: SidebarProjectNode): void;
	onNewTask(node: SidebarProjectNode): void;
	onTogglePin(node: SidebarProjectNode): void;
};

/** A project row redraws only when something it shows changes, not on every store update. */
function sameProjectRow(a: ProjectRowProps, b: ProjectRowProps): boolean {
	return a.node.key === b.node.key
		&& a.node.project.name === b.node.project.name
		&& a.node.project.id === b.node.project.id
		&& a.node.project.hostId === b.node.project.hostId
		&& a.node.hostLabel === b.node.hostLabel
		&& a.node.cloud === b.node.cloud
		&& a.node.activity === b.node.activity
		&& a.expanded === b.expanded
		&& a.pinned === b.pinned
		&& a.orchestratorAction === b.orchestratorAction
		&& a.orchestratorBusy === b.orchestratorBusy
		&& a.colors === b.colors
		&& a.styles === b.styles
		&& a.onToggle === b.onToggle
		&& a.onOrchestrator === b.onOrchestrator
		&& a.onNewTask === b.onNewTask
		&& a.onTogglePin === b.onTogglePin;
}

const ProjectRow = memo(function ProjectRow({ node, expanded, pinned, orchestratorAction, orchestratorBusy, colors, styles, onToggle, onOrchestrator, onNewTask, onTogglePin }: ProjectRowProps) {
	const orchestrator = orchestratorAction ? { action: orchestratorAction, busy: orchestratorBusy } : undefined;
	const name = node.project.name;
	const orchestratorLabel = orchestrator?.action === "open" ? "Open orchestrator" : "Start orchestrator";
	const hasHost = Boolean(node.project.hostId);
	const actions = useMemo((): LongPressMenuAction<ProjectMenuId>[] => [
		{ id: "new-task", title: "New task", ...PROJECT_MENU_SYMBOLS["new-task"] },
		...(orchestratorAction ? [{ id: "orchestrator" as const, title: orchestratorLabel, ...PROJECT_MENU_SYMBOLS.orchestrator }] : []),
		pinned ? { id: "unpin", title: "Unpin project", ...PROJECT_MENU_SYMBOLS.unpin } : { id: "pin", title: "Pin project", ...PROJECT_MENU_SYMBOLS.pin },
	], [hasHost, orchestratorAction, orchestratorLabel, pinned]);
	const onAction = (id: ProjectMenuId) => {
		if (id === "new-task") onNewTask(node);
		else if (id === "orchestrator") onOrchestrator(node);
		else onTogglePin(node);
	};
	const collapsedMark = !expanded ? node.activity : null;
	const slot = useSlotWidth();
	return (
		<View style={styles.projectRow} testID={`sidebar-project-${node.project.id}`}>
			<View style={styles.fill} onLayout={slot.onLayout}>
			<LongPressMenu
				actions={actions}
				onAction={onAction}
				onPress={() => onToggle(node)}
				accessibilityLabel={`${name}${node.hostLabel ? `, ${node.hostLabel}` : ""}`}
				accessibilityHint={expanded ? "Collapses the project's sessions" : "Shows the project's sessions"}
				style={[styles.projectButton, slot.width ? { width: slot.width } : null]}
				pressedStyle={{ backgroundColor: colors.hover }}
			>
				<View style={styles.projectIcon}>
					{collapsedMark === "attention" ? (
						<Feather name="alert-circle" size={14} color={colors.attention} />
					) : (
						<View style={[styles.tile, { backgroundColor: projectTileColor(node.project.id) }]}>
							<Text style={styles.tileLetter}>{projectLetter(name)}</Text>
						</View>
					)}
					{collapsedMark === "working" || collapsedMark === "unread" ? (
						<View style={[styles.cornerDot, { backgroundColor: collapsedMark === "working" ? colors.working : colors.muted, borderColor: colors.bg }]} />
					) : null}
				</View>
				<Text style={styles.projectLabel} numberOfLines={1}>{name}</Text>
				{node.hostLabel ? <Text style={styles.hostBadge} numberOfLines={1}>{node.hostLabel}</Text> : null}
				{node.cloud ? <Feather name="cloud" size={12} color={colors.passive} /> : null}
			</LongPressMenu>
			</View>
			<View style={styles.rowActions}>
				{pinned ? (
					<Pressable accessibilityRole="button" accessibilityLabel="Unpin project" onPress={() => onTogglePin(node)} style={actionStyle}>
						<Feather name="pin" size={14} color={colors.muted} />
					</Pressable>
				) : null}
				{orchestrator ? (
					<Pressable
						accessibilityRole="button"
						accessibilityLabel={`${orchestratorLabel} for ${name}`}
						disabled={orchestrator.busy}
						onPress={() => onOrchestrator(node)}
						style={({ pressed }) => [actionStyle, pressed && { backgroundColor: colors.hover }, orchestrator.busy && styles.disabled]}
						testID={`sidebar-orchestrator-${node.project.id}`}
					>
						<OrchestratorIcon size={ICON} color={orchestrator.action === "open" ? colors.text : colors.muted} />
					</Pressable>
				) : null}
				<LongPressMenu
					openOn="press"
					actions={actions}
					onAction={onAction}
					accessibilityLabel={`${name} actions`}
					accessibilityHint="Shows the project's actions"
					style={actionStyle}
					testID={`sidebar-project-actions-${node.project.id}`}
				>
					<View style={actionStyle}>
						<Feather name="more-vertical" size={ICON} color={colors.muted} />
					</View>
				</LongPressMenu>
			</View>
		</View>
	);
}, sameProjectRow);

const SESSION_MENU_SYMBOLS: Record<WorkerActionId, { icon: FeatherIconName; systemImage: SFSymbol }> = {
	open: { icon: "arrow-right", systemImage: "arrow.forward" },
	pin: { icon: "pin", systemImage: "pin" },
	unpin: { icon: "pin-off", systemImage: "pin.slash" },
	rename: { icon: "edit-3", systemImage: "pencil" },
	resume: { icon: "refresh-cw", systemImage: "arrow.clockwise" },
	restore: { icon: "rotate-ccw", systemImage: "arrow.uturn.backward" },
	openPr: { icon: "git-pull-request", systemImage: "arrow.up.forward.square" },
	delete: { icon: "archive", systemImage: "archivebox" },
};

type SessionRowProps = {
	row: SidebarSessionRow;
	indented: boolean;
	active: boolean;
	renaming: boolean;
	/** The row's width: iOS's menu host sizes what it wraps to its content, so it is given explicitly. */
	width?: number;
	colors: Palette;
	styles: Styles;
	onOpen(row: SidebarSessionRow): void;
	onAction(row: SidebarSessionRow, id: WorkerActionId): void;
	onRename(row: SidebarSessionRow, name: string): void;
	onCancelRename(): void;
};

/** What a session row's menu offers, which only a few of the session's fields decide. */
function sessionMenuFlags(row: SidebarSessionRow) {
	const session = row.session;
	const terminated = session.isTerminated === true || session.status === "terminated";
	return {
		pinned: Boolean(session.isPinned),
		terminated,
		stopped: !terminated && (session.status === "exited" || session.status === "errored"),
		hasPr: Boolean(reviewRouteForSession(session, session.hostId)),
		cloud: row.cloud,
	};
}

/**
 * A session row redraws only when something it shows changes. The rows are
 * rebuilt on every store update, which ticks constantly while agents work;
 * redrawing every row and its native menu each time is what made home's
 * scroll stutter.
 */
function sameSessionRow(a: SessionRowProps, b: SessionRowProps): boolean {
	if (a.row !== b.row) {
		const x = a.row;
		const y = b.row;
		if (x.key !== y.key || x.title !== y.title || x.indicator !== y.indicator || x.unread !== y.unread || x.at !== y.at || x.hostLabel !== y.hostLabel || x.cloud !== y.cloud || x.session.id !== y.session.id || x.session.hostId !== y.session.hostId) return false;
		const fx = sessionMenuFlags(x);
		const fy = sessionMenuFlags(y);
		if (fx.pinned !== fy.pinned || fx.terminated !== fy.terminated || fx.stopped !== fy.stopped || fx.hasPr !== fy.hasPr) return false;
	}
	return a.indented === b.indented
		&& a.active === b.active
		&& a.renaming === b.renaming
		&& a.width === b.width
		&& a.colors === b.colors
		&& a.styles === b.styles
		&& a.onOpen === b.onOpen
		&& a.onAction === b.onAction
		&& a.onRename === b.onRename
		&& a.onCancelRename === b.onCancelRename;
}

const SessionRow = memo(function SessionRow({ row, indented, active, renaming, width, colors, styles, onOpen, onAction, onRename, onCancelRename }: SessionRowProps) {
	const session = row.session;
	const flags = sessionMenuFlags(row);
	const actions = useMemo(
		() => workerContextActions(flags).map((action) => ({ id: action.id, title: action.title, ...SESSION_MENU_SYMBOLS[action.id] })),
		// eslint-disable-next-line react-hooks/exhaustive-deps -- the flags' values, not the object rebuilt each render
		[flags.pinned, flags.terminated, flags.stopped, flags.hasPr, flags.cloud],
	);
	const theme = useTheme();
	const [draft, setDraft] = useState(row.title);
	useEffect(() => { if (renaming) setDraft(row.title); }, [renaming, row.title]);
	// Only a session that needs you, or the open one, reads at full brightness.
	const bright = active || row.indicator === "attention";
	const age = row.at ? formatTimeTerse(row.at) : undefined;
	const content = (
		<>
			<StatusGlyph indicator={row.indicator} unread={row.unread} colors={colors} />
			{renaming ? (
				<TextInput
					autoFocus
					value={draft}
					onChangeText={setDraft}
					selectTextOnFocus
					maxLength={100}
					returnKeyType="done"
					{...textSelectionColors(theme)}
					onSubmitEditing={() => onRename(row, draft)}
					onBlur={() => onCancelRename()}
					style={[styles.sessionTitle, styles.renameInput]}
					accessibilityLabel={`Rename ${row.title}`}
				/>
			) : (
				<Text style={[styles.sessionTitle, bright && styles.sessionTitleBright]} numberOfLines={1}>{row.title}</Text>
			)}
			{row.hostLabel ? <Text style={styles.hostBadge} numberOfLines={1}>{row.hostLabel}</Text> : null}
			{row.cloud ? <Feather name="cloud" size={12} color={colors.passive} /> : null}
			{age && !renaming ? <Text style={styles.age}>{age}</Text> : null}
		</>
	);
	if (renaming) {
		return <View style={[styles.sessionRow, indented && styles.indented, { backgroundColor: colors.active }]}>{content}</View>;
	}
	return (
		<View style={indented ? styles.indented : null}>
		<LongPressMenu
			actions={actions.filter((action) => action.id !== "open")}
			onAction={(id) => onAction(row, id)}
			onPress={() => onOpen(row)}
			accessibilityLabel={`${row.title}${indicatorLabel(row.indicator, row.unread)}${row.hostLabel ? `, ${row.hostLabel}` : ""}`}
			accessibilityHint="Opens the session. Touch and hold for actions."
			style={[styles.sessionRow, width ? { width: indented ? width - SESSION_INDENT : width } : null, active && { backgroundColor: colors.active }]}
			pressedStyle={{ backgroundColor: colors.hover }}
			testID={`sidebar-session-${session.id}`}
		>
			{content}
		</LongPressMenu>
		</View>
	);
}, sameSessionRow);

function indicatorLabel(indicator: SidebarIndicator, unread: boolean): string {
	switch (indicator) {
		case "working": return ", working";
		case "attention": return ", needs you";
		case "ready": return ", ready to merge";
		case "review": return ", in review";
		case "merged": return ", merged";
		default: return unread ? ", unread" : "";
	}
}

/**
 * Desktop's SessionStatusDot: a blue spinner while the agent works, a red alert
 * when it needs you, the PR's state when it has one, and otherwise a quiet dot,
 * filled until opened and hollow after. Each state has its own shape, so colour
 * is never the only difference.
 */
function StatusGlyph({ indicator, unread, colors }: { indicator: SidebarIndicator; unread: boolean; colors: Palette }) {
	return (
		<View style={glyphSlot}>
			{indicator === "working" ? <Spinner color={colors.working} />
				: indicator === "attention" ? <Feather name="alert-circle" size={12} color={colors.attention} />
				: indicator === "review" ? <Feather name="alert-circle" size={12} color={colors.review} />
				: indicator === "ready" ? <Feather name="git-pull-request" size={12} color={colors.ready} />
				: indicator === "merged" ? <View style={{ opacity: 0.7 }}><Feather name="git-merge" size={12} color={colors.merged} /></View>
				: <View style={[dotStyle, unread ? { backgroundColor: withAlpha(colors.muted, 0.6) } : { borderWidth: 1, borderColor: withAlpha(colors.muted, 0.6) }]} />}
		</View>
	);
}

const glyphSlot = { width: 24, height: 12, alignItems: "center", justifyContent: "center" } as const;
const dotStyle = { width: 8, height: 8, borderRadius: 4 } as const;

function Spinner({ color }: { color: string }) {
	const reduceMotion = useReducedMotion();
	const spin = useRef(new Animated.Value(0)).current;
	useEffect(() => {
		if (reduceMotion) return;
		const loop = Animated.loop(Animated.timing(spin, { toValue: 1, duration: 900, easing: Easing.linear, useNativeDriver: true }));
		loop.start();
		return () => loop.stop();
	}, [reduceMotion, spin]);
	const rotate = spin.interpolate({ inputRange: [0, 1], outputRange: ["0deg", "360deg"] });
	return (
		<Animated.View style={{ transform: [{ rotate }] }}>
			<Feather name="loader" size={12} color={color} />
		</Animated.View>
	);
}

// ── Footer ────────────────────────────────────────────────────────────────────

/**
 * The desktop footer's one row: the account on the left (or Sign in), then the
 * machines button where the desktop has Connect mobile, and Settings.
 */
function Footer({ styles, colors, onSettings, onMachines }: { styles: Styles; colors: Palette; onSettings(): void; onMachines(): void }) {
	const [account, setAccount] = useState<Account | null>(null);
	const [busy, setBusy] = useState(false);
	useEffect(() => {
		let current = true;
		void loadAccount().then((value) => { if (current) setAccount(value); }).catch(() => {});
		const off = onAccountChange((value) => { if (current) setAccount(value); });
		return () => { current = false; off(); };
	}, []);
	const signIn = () => {
		setBusy(true);
		void signInToAccount().then(async (value) => {
			setAccount(value);
			if (value) await syncAccountHosts(value);
		}).catch((cause: unknown) => Alert.alert("Couldn't sign in", userFacingError(cause))).finally(() => setBusy(false));
	};
	return (
		<View style={styles.footer}>
			{account ? (
				<Pressable accessibilityRole="button" accessibilityLabel={`Account, ${accountName(account)}`} onPress={onSettings} style={({ pressed }) => [styles.navRow, styles.footerAccount, pressed && { backgroundColor: colors.hover }]}>
					<AccountAvatar account={account} size={20} />
					<Text style={styles.navLabel} numberOfLines={1}>{accountName(account)}</Text>
				</Pressable>
			) : (
				<Pressable accessibilityRole="button" accessibilityLabel="Sign in" disabled={busy} onPress={signIn} style={({ pressed }) => [styles.navRow, styles.footerAccount, pressed && { backgroundColor: colors.hover }, busy && styles.disabled]}>
					<Feather name="log-in" size={ICON} color={colors.muted} />
					<Text style={styles.navLabel} numberOfLines={1}>Sign in</Text>
				</Pressable>
			)}
			<Pressable accessibilityRole="button" accessibilityLabel="Pair a machine" onPress={onMachines} style={({ pressed }) => [actionStyle, pressed && { backgroundColor: colors.hover }]}>
				<Feather name="monitor-smartphone" size={ICON} color={colors.muted} />
			</Pressable>
			<Pressable accessibilityRole="button" accessibilityLabel="Settings" onPress={onSettings} style={({ pressed }) => [actionStyle, pressed && { backgroundColor: colors.hover }]} testID="sidebar-settings">
				<Feather name="settings" size={ICON} color={colors.muted} />
			</Pressable>
		</View>
	);
}

// ── Styles ────────────────────────────────────────────────────────────────────

const makeStyles = (c: Palette) => StyleSheet.create({
	root: { flex: 1, backgroundColor: c.bg },
	brand: { paddingHorizontal: 14, paddingBottom: 12, alignSelf: "flex-start" },
	brandLabel: { fontFamily: font.semibold, fontSize: 16, lineHeight: 20, letterSpacing: -0.16, color: c.text },
	fixed: { paddingHorizontal: 8, gap: 2, marginBottom: 12 },
	navRow: { height: ROW_HEIGHT, flexDirection: "row", alignItems: "center", gap: 10, borderRadius: ROW_RADIUS, paddingHorizontal: ROW_INSET },
	navLabel: { flex: 1, fontFamily: font.medium, fontSize: LABEL_SIZE, color: c.muted },
	navLabelActive: { color: c.text },
	navTrailing: { fontFamily: font.medium, fontSize: 12, color: c.passive, fontVariant: ["tabular-nums"] },
	searchRow: { backgroundColor: c.hover },
	searchInput: { flex: 1, fontFamily: font.regular, fontSize: LABEL_SIZE, color: c.text, paddingVertical: 0 },
	disabled: { opacity: 0.45 },
	scroll: { flex: 1 },
	scrollContent: { paddingHorizontal: LIST_PADDING, paddingBottom: 12 },
	section: { marginBottom: 8 },
	sectionGap: { marginTop: 8 },
	sectionHeader: { height: 32, flexDirection: "row", alignItems: "center" },
	sectionToggle: { flex: 1, height: 32, flexDirection: "row", alignItems: "center", gap: 4, paddingHorizontal: ROW_INSET },
	sectionLabel: { fontFamily: font.medium, fontSize: LABEL_SIZE, color: c.muted },
	empty: { paddingHorizontal: ROW_INSET, paddingVertical: 8, fontFamily: font.regular, fontSize: 13, color: c.passive },
	projectRow: { height: ROW_HEIGHT, flexDirection: "row", alignItems: "center" },
	projectButton: { height: ROW_HEIGHT, flexDirection: "row", alignItems: "center", gap: 8, borderRadius: ROW_RADIUS, paddingLeft: ROW_INSET, paddingRight: 4, minWidth: 0 },
	projectIcon: { width: ICON, height: ICON, alignItems: "center", justifyContent: "center" },
	tile: { width: ICON, height: ICON, borderRadius: 4, alignItems: "center", justifyContent: "center" },
	tileLetter: { fontFamily: font.semibold, fontSize: 9, lineHeight: 11, color: c.text, opacity: 0.8 },
	cornerDot: { position: "absolute", top: -2, right: -2, width: 7, height: 7, borderRadius: 3.5, borderWidth: 1.5 },
	projectLabel: { flex: 1, flexShrink: 1, minWidth: 0, fontFamily: font.medium, fontSize: LABEL_SIZE, color: c.muted },
	hostBadge: { maxWidth: "45%", fontFamily: font.medium, fontSize: 10, color: c.muted, borderWidth: StyleSheet.hairlineWidth, borderColor: c.border, borderRadius: 4, paddingHorizontal: 5, overflow: "hidden" },
	rowActions: { flexDirection: "row", alignItems: "center" },
	sessionRow: { height: ROW_HEIGHT, flexDirection: "row", alignItems: "center", gap: 2, borderRadius: ROW_RADIUS, paddingLeft: 4, paddingRight: 10 },
	indented: { marginLeft: SESSION_INDENT },
	sessionTitle: { flex: 1, flexShrink: 1, minWidth: 0, fontFamily: font.regular, fontSize: LABEL_SIZE, color: c.muted },
	sessionTitleBright: { color: c.text },
	renameInput: { color: c.text, paddingVertical: 0 },
	age: { marginLeft: 6, fontFamily: font.regular, fontSize: 11, color: c.passive, fontVariant: ["tabular-nums"] },
	showMore: { height: 32, justifyContent: "center", paddingHorizontal: ROW_INSET },
	showMoreLabel: { fontFamily: font.regular, fontSize: 13, color: c.passive },
	footer: { flexDirection: "row", alignItems: "center", gap: 2, paddingHorizontal: 8, paddingTop: 8, borderTopWidth: StyleSheet.hairlineWidth, borderTopColor: c.border },
	footerAccount: { flex: 1 },
	drawerRoot: { borderRightWidth: StyleSheet.hairlineWidth, borderRightColor: c.border },
	homeHeader: { height: 52, flexDirection: "row", alignItems: "center", gap: 4, paddingHorizontal: 8 },
	homeTitle: { flex: 1, paddingHorizontal: 6 },
	devicesBar: { flexGrow: 0, flexShrink: 0 },
	devicesContent: { paddingHorizontal: 12, paddingBottom: 6, gap: 5, alignItems: "center" },
	deviceChip: { height: 22, flexDirection: "row", alignItems: "center", gap: 4, paddingHorizontal: 8, borderRadius: 11, borderWidth: StyleSheet.hairlineWidth, borderColor: c.border },
	deviceDot: { width: 6, height: 6, borderRadius: 3 },
	deviceLabel: { fontFamily: font.medium, fontSize: 11, color: c.muted, maxWidth: 140 },
	fab: { position: "absolute", right: 16, height: FAB_HEIGHT, borderRadius: FAB_HEIGHT / 2, paddingHorizontal: 18, flexDirection: "row", alignItems: "center", gap: 8, shadowColor: "#000", shadowOpacity: 0.18, shadowRadius: 12, shadowOffset: { width: 0, height: 4 }, elevation: 6 },
	fabLabel: { fontFamily: font.medium, fontSize: 15 },
	fabPressed: { opacity: 0.85 },
	fill: { flex: 1, minWidth: 0 },
});
