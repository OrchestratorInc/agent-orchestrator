import AsyncStorage from "@react-native-async-storage/async-storage";
import { useGlobalSearchParams, usePathname, useRouter } from "expo-router";
import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Alert, Animated, Easing, Pressable, ScrollView, StyleSheet, Text, TextInput, View } from "react-native";
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
import { useTheme, useThemeState } from "../ThemeProvider";
import { THEMES } from "../../../shared/theme/palettes";
import type { Theme } from "../theme";
import { textSelectionColors } from "../textSelection";
import { font } from "../tokens";
import { useOrchestratorLauncher } from "../useOrchestratorLauncher";
import { useReducedMotion } from "../useReducedMotion";
import { ARCHIVE_SESSION_CONFIRM, workerContextActions, type WorkerActionId } from "../worker-action-model";
import { changedAt, isUnread, loadReadState, markRead, useReadState } from "./readState";
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

const PREFS_KEY = "ao.sidebar.prefs.v1";
type Prefs = { expanded: string[]; pinnedProjects: string[]; closedSections: string[] };
const EMPTY_PREFS: Prefs = { expanded: [], pinnedProjects: [], closedSections: [] };

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
	const toggleIn = useCallback((field: keyof Prefs, key: string) => update((prev) => ({
		...prev,
		[field]: prev[field].includes(key) ? prev[field].filter((item) => item !== key) : [...prev[field], key],
	})), [update]);
	return { prefs, toggleIn };
}

// ── The sidebar ───────────────────────────────────────────────────────────────

/**
 * The desktop's left sidebar, top to bottom: the Orchestrator brand (home),
 * Search, the top rows, Pinned, Sessions (agents with no project), Projects with
 * their sessions, and the footer with the account, machines and Settings.
 */
export function Sidebar({ close }: { close: () => void }) {
	const { theme: t, scheme, style } = useThemeState();
	const colors = useMemo(() => sidebarPalette(t, scheme, style), [t, scheme, style]);
	const styles = useMemo(() => makeStyles(colors), [colors]);
	const insets = useSafeAreaInsets();
	const router = useRouter();
	const pathname = usePathname();
	const params = useGlobalSearchParams<{ id?: string; hostId?: string }>();
	const app = useApp();
	const cloudState = useCloud();
	const read = useReadState();
	const { prefs, toggleIn } = useSidebarPrefs();
	const { busyProjects, openOrchestrator } = useOrchestratorLauncher();
	const openPage = useOpenPage();
	const [query, setQuery] = useState<string | null>(null);
	const [renamingKey, setRenamingKey] = useState<string>();
	const [showAll, setShowAll] = useState<Record<string, boolean>>({});
	const [dismissed, setDismissed] = useState<ReadonlySet<string>>(() => new Set());

	useEffect(() => { void loadReadState(); }, []);

	const onSession = pathname.startsWith("/session/");
	const activeKey = onSession && params.id ? sidebarSessionKey({ id: String(params.id), hostId: params.hostId ? String(params.hostId) : undefined }) : undefined;
	const environments = app.hostStates.length + (app.cloud ? 1 : 0);

	const tree = useMemo(() => buildSidebarTree({
		sessions: app.allSessions,
		projects: app.allProjects,
		showHosts: environments > 1,
		activeKey,
		isUnread: (session) => isUnread(read, sidebarSessionKey(session), changedAt(session)),
		isCloudHost,
		query: query ?? "",
	}), [activeKey, app.allProjects, app.allSessions, environments, query, read]);

	// Each project's orchestrator, as the Projects page found it.
	const projectRows = useMemo(() => {
		const map = new Map<string, HostedProjectRow>();
		for (const section of hostedProjectSections(app.cloud ? [...app.hostStates, app.cloud] : app.hostStates)) {
			for (const row of section.data) map.set(sidebarProjectKey(row.project), row);
		}
		return map;
	}, [app.cloud, app.hostStates]);

	// Pinned projects first, in the order they were pinned (desktop's pinnedFirst).
	const projects = useMemo(() => {
		const pinned = prefs.pinnedProjects;
		if (pinned.length === 0) return tree.projects;
		const rank = (node: SidebarProjectNode) => pinned.indexOf(node.key);
		return [...tree.projects.filter((node) => rank(node) >= 0).sort((a, b) => rank(a) - rank(b)), ...tree.projects.filter((node) => rank(node) < 0)];
	}, [prefs.pinnedProjects, tree.projects]);

	const go = useCallback((href: Parameters<typeof router.navigate>[0]) => {
		close();
		router.navigate(href);
	}, [close, router]);

	// Switching sessions replaces the one on screen, as the desktop's main pane
	// does; from anywhere else the session opens over the board.
	const openSession = useCallback((row: SidebarSessionRow) => {
		const session = row.session;
		markRead(row.key, changedAt(session));
		close();
		const target = { pathname: "/session/[id]" as const, params: { id: session.id, projectId: session.projectId, hostId: session.hostId ?? app.currentHostId } };
		if (onSession) router.replace(target);
		else router.push(target);
	}, [app.currentHostId, close, onSession, router]);

	const newTask = useCallback((projectId?: string, hostId?: string) => {
		go(projectId ? { pathname: "/spawn", params: { projectId, ...(hostId ? { hostId } : {}) } } : "/spawn");
	}, [go]);

	const openProject = useCallback((node: SidebarProjectNode) => {
		if (!node.project.hostId) return;
		go({ pathname: "/project/[id]", params: { id: node.project.id, hostId: node.project.hostId } });
	}, [go]);

	const launchOrchestrator = useCallback((node: SidebarProjectNode) => {
		const row = projectRows.get(node.key);
		if (!row) return;
		if (row.action === "open" && row.link?.id) {
			haptics.select();
			close();
			const target = { pathname: "/session/[id]" as const, params: { id: row.link.id, projectId: row.project.id, hostId: node.project.hostId ?? app.currentHostId } };
			if (onSession) router.replace(target);
			else router.push(target);
			return;
		}
		close();
		openOrchestrator(row);
	}, [app.currentHostId, close, onSession, openOrchestrator, projectRows, router]);

	const sessionAction = useCallback((row: SidebarSessionRow, id: WorkerActionId) => {
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
				if (route) { close(); openPage(route); }
				return;
			}
			default:
				haptics.warning();
				Alert.alert(ARCHIVE_SESSION_CONFIRM.title, ARCHIVE_SESSION_CONFIRM.message, [
					{ text: ARCHIVE_SESSION_CONFIRM.cancel, style: "cancel" },
					{ text: ARCHIVE_SESSION_CONFIRM.confirm, style: "destructive", onPress: () => void app.kill(session.id, hostId).catch(fail("Couldn't archive the session")) },
				]);
		}
	}, [app, close, openPage, openSession]);

	const commitRename = useCallback((row: SidebarSessionRow, name: string) => {
		setRenamingKey(undefined);
		const next = name.trim();
		if (!next || next === row.title) return;
		void app.renameWorker(row.session.id, next, row.session.hostId).then(() => haptics.success(), (cause: unknown) => {
			haptics.error();
			Alert.alert("Couldn't rename the session", userFacingError(cause, "Please try again."));
		});
	}, [app]);

	const sessionRow = (row: SidebarSessionRow, indented: boolean) => (
		<SessionRow
			key={row.key}
			row={row}
			indented={indented}
			active={row.key === activeKey}
			renaming={renamingKey === row.key}
			colors={colors}
			styles={styles}
			onOpen={openSession}
			onAction={sessionAction}
			onRename={commitRename}
			onCancelRename={() => setRenamingKey(undefined)}
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

	return (
		<View style={[styles.root, { paddingTop: insets.top + 8, paddingBottom: Math.max(insets.bottom, 8) }]} testID="sidebar">
			{/* Brand → home. No fill behind it (DESIGN.md "Brand → home"). */}
			<Pressable accessibilityRole="button" accessibilityLabel="Go home" onPress={() => go("/")} style={styles.brand} testID="sidebar-brand">
				<Text style={styles.brandLabel} numberOfLines={1}>Orchestrator</Text>
			</Pressable>

			<View style={styles.fixed}>
				{searching ? (
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
						<Pressable accessibilityRole="button" accessibilityLabel="Close search" hitSlop={8} onPress={() => setQuery(null)}>
							<Feather name="x" size={ICON} color={colors.muted} />
						</Pressable>
					</View>
				) : (
					<NavRow icon="search" label="Search" onPress={() => setQuery("")} styles={styles} colors={colors} />
				)}
				<NavRow icon="edit" label="New task" onPress={() => newTask()} styles={styles} colors={colors} disabled={!paired} />
				<NavRow icon="git-pull-request" label="Pull requests" onPress={() => go("/prs")} styles={styles} colors={colors} active={pathname.startsWith("/prs")} />
				<NavRow icon="bell" label="Notifications" onPress={() => go("/notifications")} styles={styles} colors={colors} active={pathname.startsWith("/notifications")} trailing={unread > 0 ? String(unread) : undefined} />
			</View>

			<ScrollView style={styles.scroll} contentContainerStyle={styles.scrollContent} keyboardShouldPersistTaps="handled" showsVerticalScrollIndicator={false}>
				{tree.pinned.length > 0 ? (
					<Section label="Pinned" open={sectionOpen("pinned")} onToggle={() => toggleIn("closedSections", "pinned")} styles={styles} colors={colors}>
						{tree.pinned.map((row) => sessionRow(row, false))}
					</Section>
				) : null}

				{tree.standalone.length > 0 || !searching ? (
					<Section
						label="Sessions"
						open={sectionOpen("standalone")}
						onToggle={() => toggleIn("closedSections", "standalone")}
						trailing={paired ? <SectionAction icon="plus" label="New standalone agent" onPress={() => newTask(STANDALONE_PROJECT)} colors={colors} /> : null}
						styles={styles}
						colors={colors}
					>
						{standalone.listed.map((row) => sessionRow(row, false))}
						{standalone.hiddenCount > 0 ? (
							<ShowMore
								expanded={showAll.standalone === true}
								label={showAll.standalone ? "Show less" : `Show ${standalone.hiddenCount} more`}
								onPress={() => setShowAll((prev) => ({ ...prev, standalone: !prev.standalone }))}
								styles={styles}
								colors={colors}
							/>
						) : null}
					</Section>
				) : null}

				<Section
					label="Projects"
					open={sectionOpen("projects")}
					onToggle={() => toggleIn("closedSections", "projects")}
					trailing={<SectionAction icon="plus" label="New project" onPress={() => go("/cloud/new-project")} colors={colors} />}
					styles={styles}
					colors={colors}
				>
					{!paired ? (
						<Text style={styles.empty}>Pair a machine or sign in to see your projects.</Text>
					) : projects.length === 0 ? (
						<Text style={styles.empty}>{searching ? "No matches." : "No projects yet."}</Text>
					) : null}
					{listedProjects.listed.map((node) => {
						// The open session's project shows its list until the user folds it (desktop's initial expand).
						const expanded = searching || prefs.expanded.includes(node.key) || (node.sessions.some((row) => row.key === activeKey) && !dismissed.has(node.key));
						const capped = capRows(node.sessions, SIDEBAR_PROJECT_SESSION_LIMIT, searching || showAll[node.key] === true, activeKey);
						const row = projectRows.get(node.key);
						return (
							<View key={node.key}>
								<ProjectRow
									node={node}
									expanded={expanded}
									pinned={prefs.pinnedProjects.includes(node.key)}
									active={pathname.startsWith("/project/") && params.id === node.project.id}
									orchestrator={row ? { action: row.action, busy: busyProjects.has(hostedRowKey(node.project.hostId ?? "", node.project.id)) } : undefined}
									colors={colors}
									styles={styles}
									onToggle={() => {
										if (expanded && !prefs.expanded.includes(node.key)) setDismissed((prev) => new Set(prev).add(node.key));
										else toggleIn("expanded", node.key);
									}}
									onOrchestrator={() => launchOrchestrator(node)}
									onNewTask={() => newTask(node.project.id, node.project.hostId)}
									onOpenProject={() => openProject(node)}
									onTogglePin={() => toggleIn("pinnedProjects", node.key)}
								/>
								{expanded ? (
									<View>
										{capped.listed.map((item) => sessionRow(item, true))}
										{capped.hiddenCount > 0 ? (
											<View style={{ paddingLeft: SESSION_INDENT + 2 }}>
												<ShowMore
													expanded={showAll[node.key] === true}
													label={showAll[node.key] ? "Show fewer agents" : `Show ${capped.hiddenCount} more agent${capped.hiddenCount === 1 ? "" : "s"}`}
													onPress={() => setShowAll((prev) => ({ ...prev, [node.key]: !prev[node.key] }))}
													styles={styles}
													colors={colors}
												/>
											</View>
										) : null}
									</View>
								) : null}
							</View>
						);
					})}
					{listedProjects.hiddenCount > 0 ? (
						<ShowMore
							expanded={showAll.projects === true}
							label={showAll.projects ? "Show fewer projects" : `Show ${listedProjects.hiddenCount} more project${listedProjects.hiddenCount === 1 ? "" : "s"}`}
							onPress={() => setShowAll((prev) => ({ ...prev, projects: !prev.projects }))}
							styles={styles}
							colors={colors}
						/>
					) : null}
				</Section>
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
			{open ? children : null}
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
	board: { icon: "layout-grid", systemImage: "square.grid.2x2" },
	pin: { icon: "pin", systemImage: "pin" },
	unpin: { icon: "pin-off", systemImage: "pin.slash" },
};

type ProjectMenuId = "new-task" | "orchestrator" | "board" | "pin" | "unpin";

const ProjectRow = memo(function ProjectRow({ node, expanded, pinned, active, orchestrator, colors, styles, onToggle, onOrchestrator, onNewTask, onOpenProject, onTogglePin }: {
	node: SidebarProjectNode;
	expanded: boolean;
	pinned: boolean;
	active: boolean;
	orchestrator?: { action: "open" | "start" | "resume"; busy: boolean };
	colors: Palette;
	styles: Styles;
	onToggle(): void;
	onOrchestrator(): void;
	onNewTask(): void;
	onOpenProject(): void;
	onTogglePin(): void;
}) {
	const name = node.project.name;
	const orchestratorLabel = orchestrator?.action === "open" ? "Open orchestrator" : "Start orchestrator";
	const actions: LongPressMenuAction<ProjectMenuId>[] = [
		{ id: "new-task", title: "New task", ...PROJECT_MENU_SYMBOLS["new-task"] },
		...(orchestrator ? [{ id: "orchestrator" as const, title: orchestratorLabel, ...PROJECT_MENU_SYMBOLS.orchestrator }] : []),
		...(node.project.hostId ? [{ id: "board" as const, title: "Open dashboard", ...PROJECT_MENU_SYMBOLS.board }] : []),
		pinned ? { id: "unpin", title: "Unpin project", ...PROJECT_MENU_SYMBOLS.unpin } : { id: "pin", title: "Pin project", ...PROJECT_MENU_SYMBOLS.pin },
	];
	const onAction = (id: ProjectMenuId) => {
		if (id === "new-task") onNewTask();
		else if (id === "orchestrator") onOrchestrator();
		else if (id === "board") onOpenProject();
		else onTogglePin();
	};
	const collapsedMark = !expanded ? node.activity : null;
	const slot = useSlotWidth();
	return (
		<View style={styles.projectRow} testID={`sidebar-project-${node.project.id}`}>
			<View style={styles.fill} onLayout={slot.onLayout}>
			<LongPressMenu
				actions={actions}
				onAction={onAction}
				onPress={onToggle}
				accessibilityLabel={`${name}${node.hostLabel ? `, ${node.hostLabel}` : ""}`}
				accessibilityHint={expanded ? "Collapses the project's sessions" : "Shows the project's sessions"}
				style={[styles.projectButton, slot.width ? { width: slot.width } : null, active && { backgroundColor: colors.active }]}
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
				<Text style={[styles.projectLabel, active && styles.navLabelActive]} numberOfLines={1}>{name}</Text>
				{node.hostLabel ? <Text style={styles.hostBadge} numberOfLines={1}>{node.hostLabel}</Text> : null}
				{node.cloud ? <Feather name="cloud" size={12} color={colors.passive} /> : null}
			</LongPressMenu>
			</View>
			<View style={styles.rowActions}>
				{pinned ? (
					<Pressable accessibilityRole="button" accessibilityLabel="Unpin project" onPress={onTogglePin} style={actionStyle}>
						<Feather name="pin" size={14} color={colors.muted} />
					</Pressable>
				) : null}
				{orchestrator ? (
					<Pressable
						accessibilityRole="button"
						accessibilityLabel={`${orchestratorLabel} for ${name}`}
						disabled={orchestrator.busy}
						onPress={onOrchestrator}
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
});

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

const SessionRow = memo(function SessionRow({ row, indented, active, renaming, colors, styles, onOpen, onAction, onRename, onCancelRename }: {
	row: SidebarSessionRow;
	indented: boolean;
	active: boolean;
	renaming: boolean;
	colors: Palette;
	styles: Styles;
	onOpen(row: SidebarSessionRow): void;
	onAction(row: SidebarSessionRow, id: WorkerActionId): void;
	onRename(row: SidebarSessionRow, name: string): void;
	onCancelRename(): void;
}) {
	const session = row.session;
	const terminated = session.isTerminated === true || session.status === "terminated";
	const actions = workerContextActions({
		pinned: Boolean(session.isPinned),
		terminated,
		stopped: !terminated && (session.status === "exited" || session.status === "errored"),
		hasPr: Boolean(reviewRouteForSession(session, session.hostId)),
		cloud: row.cloud,
	}).map((action) => ({ id: action.id, title: action.title, ...SESSION_MENU_SYMBOLS[action.id] }));
	const theme = useTheme();
	const [draft, setDraft] = useState(row.title);
	const slot = useSlotWidth();
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
		<View style={indented ? styles.indented : null} onLayout={slot.onLayout}>
		<LongPressMenu
			actions={actions.filter((action) => action.id !== "open")}
			onAction={(id) => onAction(row, id)}
			onPress={() => onOpen(row)}
			accessibilityLabel={`${row.title}${indicatorLabel(row.indicator, row.unread)}${row.hostLabel ? `, ${row.hostLabel}` : ""}`}
			accessibilityHint="Opens the session. Touch and hold for actions."
			style={[styles.sessionRow, slot.width ? { width: slot.width } : null, active && { backgroundColor: colors.active }]}
			pressedStyle={{ backgroundColor: colors.hover }}
			testID={`sidebar-session-${session.id}`}
		>
			{content}
		</LongPressMenu>
		</View>
	);
});

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
	root: { flex: 1, backgroundColor: c.bg, borderRightWidth: StyleSheet.hairlineWidth, borderRightColor: c.border },
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
	scrollContent: { paddingHorizontal: 8, paddingBottom: 12 },
	section: { marginBottom: 8 },
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
	fill: { flex: 1, minWidth: 0 },
});
