import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSession } from "./api";
import type { HostedProject } from "./hostedRows";
import type { HostSnapshot } from "./otherHosts";

const fixtures = vi.hoisted(() => {
	const push = vi.fn(), navigate = vi.fn();
	return {
		platform: "ios",
		headerHeight: 155,
		hostStates: [] as HostSnapshot[],
		projects: [] as HostedProject[],
		refreshAll: vi.fn(async () => {}),
		refreshHost: vi.fn(async (_hostId: string) => {}),
		select: vi.fn(),
		showMenu: vi.fn(),
		push,
		navigate,
		// expo-router's router is one object for the app's lifetime.
		router: { push, navigate },
		configured: true,
		notificationsUnread: 0,
		theme: new Proxy({}, { get: (_, key) => `#${String(key)}` }),
	};
});

vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		useRouter: () => fixtures.router,
		// Focused for the whole of a test: the effect runs once, as on mount.
		useFocusEffect: (effect: () => void | (() => void)) => React.useEffect(effect, [effect]),
		Stack: {
			SearchBar: host("StackSearchBar"),
			Toolbar: Object.assign(host("StackToolbar"), {
				Button: host("StackToolbarButton"),
				Badge: host("StackToolbarBadge"),
				Menu: host("StackToolbarMenu"),
				MenuAction: host("StackToolbarMenuAction"),
			}),
		},
	};
});
vi.mock("expo-router/react-navigation", () => ({ useHeaderHeight: () => fixtures.headerHeight }));
vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator", FlatList: "FlatList", ScrollView: "ScrollView", View: "View", Text: "Text", Pressable: "Pressable", Platform: { get OS() { return fixtures.platform; } },
	Keyboard: { dismiss: vi.fn() }, StyleSheet: { create: (styles: object) => styles },
}));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0 }) }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn(), select: fixtures.select } }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("react-native-svg", () => ({ default: "Svg", Path: "Path", Rect: "Rect" }));
vi.mock("@expo/ui/community/menu", async () => {
	const React = await import("react");
	return { MenuView: React.forwardRef(function MenuView({ children, ...props }: { children?: React.ReactNode }, ref) {
		React.useImperativeHandle(ref, () => ({ show: fixtures.showMenu }));
		return React.createElement("MenuView", props, children);
	}) };
});
// vitest resolves the iOS helper; Metro hands Android the drawable variant.
vi.mock("./machine-menu-image", async (importOriginal) => {
	const real = await importOriginal<typeof import("./machine-menu-image")>();
	return { machineMenuImage: (symbol: Parameters<typeof real.machineMenuImage>[0]) => fixtures.platform === "android" ? `drawable:${symbol}` : real.machineMenuImage(symbol) };
});
// The machine chips are tested on their own (worker-environment-filter.test and
// its Android twin); here they are a host whose props are the board's wiring.
vi.mock("./worker-environment-filter", () => ({ WorkerEnvironmentFilter: "WorkerEnvironmentFilter" }));
// The picker is a SwiftUI menu or a Compose dropdown, tested in environment-picker.test.
vi.mock("./environment-picker", () => ({ EnvironmentPicker: "EnvironmentPicker" }));
vi.mock("./store", () => ({ useApp: () => ({
	configured: fixtures.configured, configResolved: true, connection: "open", loading: false, error: null,
	config: { hostId: "mac" },
	refreshAll: fixtures.refreshAll, refreshHost: fixtures.refreshHost,
	allSessions: fixtures.hostStates.flatMap((host) => host.sessions.map((session) => ({ ...session, hostId: host.hostId }))), allProjects: fixtures.projects,
	hostStates: fixtures.hostStates, notificationsUnread: fixtures.notificationsUnread,
	phoneNetwork: { connected: true, internet: true },
}) }));
vi.mock("./StaleBanner", () => ({ StaleBanner: "StaleBanner" }));
vi.mock("./useBoardFailure", () => ({ useBoardFailure: () => ({ icon: "wifi-off", title: "Can't connect", hint: "Try again" }) }));
vi.mock("./UnpairedState", () => ({ UnpairedState: "UnpairedState" }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemeState: () => ({ scheme: "dark" }),
	useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme),
}));
vi.mock("./ui", async () => {
	const React = await import("react");
	return {
		Button: "Button",
		EmptyState: (props: { action?: React.ReactNode }) => React.createElement("EmptyState", props, props.action),
	};
});
vi.mock("./worker-board-list", async () => {
	const React = await import("react");
	return { WorkerBoardList: ({ ListHeaderComponent, ListEmptyComponent, ...props }: { ListHeaderComponent: React.ReactNode; ListEmptyComponent: React.ReactNode; sessions: unknown[] }) =>
		React.createElement("WorkerBoardList", props, ListHeaderComponent, props.sessions.length === 0 ? ListEmptyComponent : null) };
});
// The real one draws nothing on iOS; this stands in for Android's FAB on both.
vi.mock("./AddFab", () => ({ AddFab: "AddFab", addFabClearance: 72 }));
vi.mock("./frost", () => ({ useTabBarFrost: () => ({ inset: 0, frost: null }) }));
vi.mock("./largeTitle", () => ({
	useLargeTitle: (_title: string, onScroll?: unknown) => ({ header: null, edge: null, onScroll, topInset: 0 }),
}));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));

import { startNewTask } from "./newTaskAction";
import FleetScreen from "../app/(tabs)/(workers)/index";
import { WorkerBoardList } from "./worker-board-list";
import { WorkerEnvironmentFilter } from "./worker-environment-filter";
import { ALL_WORKER_ENVIRONMENTS } from "./worker-controls";

let renderer: ReactTestRenderer;

function toolbar(placement: "left" | "right"): ReactTestInstance | undefined {
	return renderer.root.findAllByType("StackToolbar" as never).find((node) => node.props.placement === placement);
}
/** The filter menu: leading on iOS, the first trailing item on Android. */
function filterMenu(): ReactTestInstance {
	const bar = fixtures.platform === "ios" ? toolbar("left")! : toolbar("right")!;
	// Depth first, so the root menu comes before the submenus inside it.
	return bar.findAllByType("StackToolbarMenu" as never)[0];
}
/** The machine chips over the board: the one place to choose a machine. */
function chips(): ReactTestInstance {
	return renderer.root.findByType(WorkerEnvironmentFilter);
}
function chooseMachine(name: string) {
	const strip = chips();
	const id = name === "All machines" ? ALL_WORKER_ENVIRONMENTS : strip.props.environments.find((environment: { name: string }) => environment.name === name)?.id;
	if (!id) throw new Error(`No "${name}" chip`);
	act(() => strip.props.onSelectEnvironment(id));
}
function choices(menu: ReactTestInstance): ReactTestInstance[] {
	return menu.findAllByType("StackToolbarMenuAction" as never);
}
function choose(menu: ReactTestInstance, label: string) {
	const action = choices(menu).find((node) => node.props.children === label);
	if (!action) throw new Error(`No "${label}" in ${choices(menu).map((node) => node.props.children).join(", ")}`);
	act(() => action.props.onPress());
}
function selectedEnvironment(): string | undefined {
	const strip = chips();
	const id = strip.props.selectedEnvironmentId;
	return id === ALL_WORKER_ENVIRONMENTS ? "All machines" : strip.props.environments.find((environment: { id: string }) => environment.id === id)?.name;
}
function selectWindows() {
	act(() => { renderer = create(<FleetScreen />); });
	chooseMachine("Windows");
}
function emptyState() {
	const status = renderer.root.findAllByProps({ testID: "worker-machine-status" })[0];
	return status ? { props: { title: status.props.accessibilityLabel, message: renderer.root.findByProps({ testID: "worker-machine-hint" }).props.children } } : renderer.root.findByType("EmptyState" as never);
}

function session(id: string): DashboardSession {
	return {
		id, projectId: "project-1", status: "idle", mode: "chat", branch: null,
		issueId: null, issueTitle: null, userPrompt: null, displayName: null, summary: null,
		createdAt: "2026-10-09T00:00:00Z", lastActivityAt: "2026-10-09T00:00:00Z",
	};
}
function snapshot(patch: Partial<HostSnapshot>): HostSnapshot {
	return {
		hostId: "windows", name: "Windows", appearance: { symbol: "desktop" },
		endpoints: [], config: null, connection: "closed", loading: false, error: "Host is unreachable", errorStatus: null,
		projects: [], projectsKnown: false, sessions: [], orchestrators: [], orchestratorId: null,
		stats: {}, notificationsUnread: 0, lastSyncAt: 0, ...patch,
	};
}
function project(id: string, name: string, hostId: string, hostName: string): HostedProject {
	return { id, name, hostId, hostName } as HostedProject;
}

beforeEach(() => {
	fixtures.platform = "ios";
	fixtures.configured = true;
	fixtures.notificationsUnread = 0;
	fixtures.projects = [];
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.refreshAll.mockClear();
	fixtures.refreshHost.mockClear();
	fixtures.select.mockClear();
	fixtures.showMenu.mockClear();
	fixtures.push.mockClear();
	fixtures.navigate.mockClear();
	fixtures.hostStates = [
		snapshot({ hostId: "mac", name: "Mac", connection: "open", error: null, sessions: [session("worker-1")] }),
		snapshot({}),
	];
});
afterEach(() => { act(() => renderer?.unmount()); vi.unstubAllGlobals(); });

describe("Workers header", () => {
	function headerButtons() {
		return toolbar("right")!.findAllByType("StackToolbarButton" as never);
	}

	// The header is the native bar's: its title comes from the tab's stack, and
	// its actions are bar items the page declares, not a row drawn in the page.
	it("declares Settings, Pull Requests and Notifications as trailing header items", () => {
		act(() => { renderer = create(<FleetScreen />); });
		const [settings, prs, notifications] = headerButtons();
		expect([settings, prs, notifications].map((button) => button.props.accessibilityLabel)).toEqual(["Settings", "Pull Requests", "Notifications"]);
		act(() => prs.props.onPress());
		expect(fixtures.navigate).toHaveBeenCalledWith("/prs");
		expect(notifications.findAllByType("StackToolbarBadge" as never)).toHaveLength(0);
		act(() => settings.props.onPress());
		expect(fixtures.navigate).toHaveBeenCalledWith("/settings");
		act(() => notifications.props.onPress());
		expect(fixtures.navigate).toHaveBeenCalledWith("/notifications");
	});

	it("badges Notifications with the unread count across machines", () => {
		fixtures.hostStates = fixtures.hostStates.map((host) => ({ ...host, notificationsUnread: 2 }));
		act(() => { renderer = create(<FleetScreen />); });
		const notifications = headerButtons()[2];
		expect(notifications.props.accessibilityLabel).toBe("Notifications, 4 unread");
		expect(notifications.findByType("StackToolbarBadge" as never).props.children).toBe("4");
	});

	// Android's toolbar reads the badge after the label, so the count is said once.
	it("leaves the count to the badge on Android", () => {
		fixtures.platform = "android";
		fixtures.hostStates = fixtures.hostStates.map((host) => ({ ...host, notificationsUnread: 2 }));
		act(() => { renderer = create(<FleetScreen />); });
		const notifications = renderer.root.findAllByType("StackToolbarButton" as never).find((node) => node.props.icon === "bell")!;
		expect(notifications.props.accessibilityLabel).toBe("Unread notifications");
		expect(notifications.findByType("StackToolbarBadge" as never).props.children).toBe("4");
	});

	// Settings is where pairing starts, so an unpaired board still offers it, and
	// there is nothing yet to search, filter or spawn.
	it("keeps Settings alone in the header before a machine is paired", () => {
		fixtures.configured = false;
		fixtures.hostStates = [];
		act(() => { renderer = create(<FleetScreen />); });
		expect(renderer.root.findByType("UnpairedState" as never)).toBeTruthy();
		expect(headerButtons().map((button) => button.props.accessibilityLabel)).toEqual(["Settings"]);
		expect(toolbar("left")).toBeUndefined();
		expect(renderer.root.findAllByType("StackSearchBar" as never)).toHaveLength(0);
		expect(renderer.root.findAllByType("AddFab" as never)).toHaveLength(0);
	});

});

describe("Workers filter menu", () => {
	// iOS leads the bar with the filter, across from the actions. It holds the
	// projects alone: machines are the chip row's.
	it("leads the iOS bar with the project choices, the current one checked, and no machines", () => {
		fixtures.projects = [project("ao", "AO", "mac", "Mac"), project("game", "Game", "windows", "Windows")];
		act(() => { renderer = create(<FleetScreen />); });
		expect(filterMenu().props.accessibilityLabel).toBe("Filter workers");
		expect(filterMenu().findAllByType("StackToolbarMenu" as never).flatMap((node) => node.props.title ? [node.props.title] : [])).toEqual(["Project"]);
		expect(choices(filterMenu()).map((node) => [node.props.children, node.props.isOn])).toEqual([
			["All projects", true],
			["AO · Mac", false],
			["Game · Windows", false],
		]);
		expect(JSON.stringify(choices(filterMenu()).map((node) => node.props.children))).not.toMatch(/All machines/);
	});

	// The state is in the glyph, not in colour alone; a chosen machine is the
	// chips' to show, not the filter's.
	it("fills the iOS filter symbol once a project filters the board", () => {
		fixtures.projects = [project("ao", "AO", "mac", "Mac"), project("game", "Game", "windows", "Windows")];
		act(() => { renderer = create(<FleetScreen />); });
		chooseMachine("Windows");
		expect(filterMenu().props.icon).toBe("line.3.horizontal.decrease.circle");
		choose(filterMenu(), "Game");
		expect(filterMenu().props).toMatchObject({ icon: "line.3.horizontal.decrease.circle.fill", accessibilityLabel: "Filter workers, filtered to Game" });
		expect(filterMenu().findAllByType("StackToolbarBadge" as never)).toHaveLength(0);
	});

	// Android's top app bar has no leading actions, and badges a filtered board.
	it("heads the Android bar's trailing actions and badges a project-filtered board", () => {
		fixtures.platform = "android";
		fixtures.projects = [project("ao", "AO", "mac", "Mac"), project("web", "Web", "mac", "Mac")];
		act(() => { renderer = create(<FleetScreen />); });
		expect(toolbar("left")).toBeUndefined();
		expect(toolbar("right")!.props.children[0].props.accessibilityLabel).toBe("Filter workers");
		expect(filterMenu().findAllByType("StackToolbarBadge" as never)).toHaveLength(0);
		choose(filterMenu(), "AO");
		expect(filterMenu().findAllByType("StackToolbarBadge" as never)).toHaveLength(1);
		expect(filterMenu().props.accessibilityLabel).toBe("Filter workers, filtered to AO");
	});

	// Machines are filtered by the chips alone; with one project there is
	// nothing left for the filter to narrow.
	it("leaves the filter out with one project, however many machines", () => {
		fixtures.projects = [project("ao", "AO", "mac", "Mac")];
		act(() => { renderer = create(<FleetScreen />); });
		expect(renderer.root.findAllByType("StackToolbarMenu" as never)).toHaveLength(0);
		expect(renderer.root.findAllByType(WorkerEnvironmentFilter)).toHaveLength(1);
	});

	it("scopes the project choices to the chosen machine and drops one it does not have", () => {
		fixtures.projects = [project("ao", "AO", "mac", "Mac"), project("game", "Game", "windows", "Windows")];
		act(() => { renderer = create(<FleetScreen />); });
		choose(filterMenu(), "Game · Windows");
		expect(choices(filterMenu()).find((node) => node.props.isOn)?.props.children).toBe("Game · Windows");
		chooseMachine("Mac");
		expect(selectedEnvironment()).toBe("Mac");
		expect(choices(filterMenu()).map((node) => node.props.children)).toEqual(["All projects", "AO"]);
	});

	// Rebuilding the menu re-registers the header, which can close it under the
	// finger, so a poll that changes only machine state leaves it alone.
	it("keeps the header items across polls that change only machine state", () => {
		fixtures.projects = [project("ao", "AO", "mac", "Mac"), project("game", "Game", "windows", "Windows")];
		act(() => { renderer = create(<FleetScreen />); });
		const menu = toolbar("left")!.props.children;
		const trailing = toolbar("right")!.props;
		fixtures.hostStates = fixtures.hostStates.map((host) => ({ ...host, lastSyncAt: host.lastSyncAt + 8_000, sessions: [...host.sessions] }));
		act(() => renderer.update(<FleetScreen />));
		expect(toolbar("left")!.props.children).toBe(menu);
		expect(toolbar("right")!.props).toBe(trailing);
	});
});

describe("Workers spawn action", () => {
	// iOS has no floating action button: the create action ends the header.
	// New task is the tab bar's own round button on iOS; the board keeps
	// settings and alerts in its header, and hands the button its own filters.
	it("starts a task from the tab bar's button on the board's own filters", () => {
		act(() => { renderer = create(<FleetScreen />); });
		expect(toolbar("right")!.findAllByType("StackToolbarButton" as never).map((node) => node.props.accessibilityLabel)).toEqual(["Settings", "Pull Requests", "Notifications"]);
		act(() => startNewTask(() => fixtures.push("/spawn")));
		expect(fixtures.push).toHaveBeenCalledWith({ pathname: "/spawn", params: { hostId: "mac" } });
	});

	// Android floats Material's FAB instead, at the page's bottom-end corner,
	// which already ends at the navigation bar.
	// Settings and Pull Requests are tabs on Android, so the bar keeps the bell.
	it("floats the Android spawn button instead of a header item", () => {
		fixtures.platform = "android";
		act(() => { renderer = create(<FleetScreen />); });
		expect(toolbar("right")!.findAllByType("StackToolbarButton" as never).map((node) => node.props.accessibilityLabel)).toEqual(["Notifications"]);
		const fab = renderer.root.findByType("AddFab" as never);
		expect(fab.props.label).toBe("New task");
		act(() => fab.props.onPress());
		expect(fixtures.push).toHaveBeenCalledWith({ pathname: "/spawn", params: { hostId: "mac" } });
	});

	it("carries the project filter into the spawn sheet", () => {
		fixtures.projects = [project("ao", "AO", "mac", "Mac"), project("game", "Game", "windows", "Windows")];
		act(() => { renderer = create(<FleetScreen />); });
		choose(filterMenu(), "Game · Windows");
		act(() => renderer.root.findByType("AddFab" as never).props.onPress());
		expect(fixtures.push).toHaveBeenCalledWith({ pathname: "/spawn", params: { hostId: "windows", projectId: "game" } });
	});
});

describe("Workers environment chips", () => {
	// The one place to choose a machine, on both platforms: Material's filter
	// chips on Android, the app's own pills on iOS.
	it("offers the machine chips only once two machines are paired", () => {
		act(() => { renderer = create(<FleetScreen />); });
		expect(renderer.root.findAllByType(WorkerEnvironmentFilter)).toHaveLength(1);
		expect(renderer.root.findAllByProps({ testID: "worker-environment-strip" })).toHaveLength(0);

		fixtures.hostStates = fixtures.hostStates.slice(0, 1);
		act(() => renderer.update(<FleetScreen />));
		expect(renderer.root.findAllByType(WorkerEnvironmentFilter)).toHaveLength(0);
	});

	it("lists the machine chips by name", () => {
		fixtures.hostStates = [snapshot({ hostId: "z", name: "Zeta" }), snapshot({ hostId: "a", name: "alpha" })];
		act(() => { renderer = create(<FleetScreen />); });
		const chips = renderer.root.findByType(WorkerEnvironmentFilter);
		expect(chips.props.environments.map((environment: { id: string }) => environment.id)).toEqual(["a", "z"]);
		act(() => chips.props.onSelectEnvironment("z"));
		expect(selectedEnvironment()).toBe("Zeta");
		expect(renderer.root.findByType(WorkerEnvironmentFilter).props.selectedEnvironmentId).toBe("z");
	});
});

describe("Workers machine label", () => {
	// No heading names the machine: one paired, or one chosen in the filter,
	// and the rows leave its name off, since it would be the same on every row.
	it("names no machine over the rows, which leave it off when all share one", () => {
		fixtures.hostStates[1] = snapshot({ connection: "open", error: null, sessions: [session("worker-2")] });
		act(() => { renderer = create(<FleetScreen />); });
		expect(renderer.root.findAllByProps({ testID: "worker-machine-heading" })).toHaveLength(0);
		expect(renderer.root.findByType(WorkerBoardList).props.showMachine).toBe(true);
		chooseMachine("Windows");
		expect(renderer.root.findAllByProps({ testID: "worker-machine-heading" })).toHaveLength(0);
		expect(renderer.root.findByType(WorkerBoardList).props.showMachine).toBe(false);

		fixtures.hostStates = fixtures.hostStates.slice(0, 1);
		act(() => { renderer = create(<FleetScreen />); });
		expect(renderer.root.findAllByProps({ testID: "worker-machine-heading" })).toHaveLength(0);
		expect(renderer.root.findByType(WorkerBoardList).props.showMachine).toBe(false);
	});
});

describe("Workers selected-machine reachability", () => {
	it.each(["open", "closed"] as const)("targets the selected environment when creating work, even when %s", (connection) => {
		fixtures.hostStates[1].connection = connection;
		selectWindows();
		act(() => renderer.root.findByType("AddFab" as never).props.onPress());
		expect(fixtures.push).toHaveBeenCalledWith({ pathname: "/spawn", params: { hostId: "windows" } });
	});
	it("does not claim an unreachable machine has no workers while another machine is online", () => {
		selectWindows();
		expect(emptyState().props.title).toBe("Can't reach Windows");
		expect(emptyState().props.message).toContain("workers");
	});
	it("shows connecting instead of an empty board during the selected machine's initial connection", () => {
		Object.assign(fixtures.hostStates[1], { connection: "connecting", loading: true, error: null });
		selectWindows();
		expect(emptyState().props.title).toBe("Connecting to Windows");
	});
	it("updates the selected-machine message as connection attempts finish", () => {
		fixtures.hostStates[0].sessions = [];
		Object.assign(fixtures.hostStates[1], { connection: "connecting", loading: true, error: null });
		selectWindows();
		expect(emptyState().props.title).toBe("Connecting to Windows");
		Object.assign(fixtures.hostStates[1], { connection: "closed", loading: false, error: "Host is unreachable" });
		act(() => renderer.update(<FleetScreen />));
		expect(emptyState().props.title).toBe("Can't reach Windows");
		Object.assign(fixtures.hostStates[1], { connection: "open", error: null });
		act(() => renderer.update(<FleetScreen />));
		expect(emptyState().props.title).toBe("No workers on Windows");
	});
	// The refresh control gives each platform's own feedback, and the Retry
	// actions that share the handler buzz once rather than twice.
	it("refreshes from a pull without a haptic of its own", async () => {
		const { haptics } = await import("./haptics");
		vi.mocked(haptics.tap).mockClear();
		act(() => { renderer = create(<FleetScreen />); });
		await act(async () => renderer.root.findByType(WorkerBoardList).props.onRefresh());
		expect(fixtures.refreshAll).toHaveBeenCalled();
		expect(haptics.tap).not.toHaveBeenCalled();
	});
	it("offers a retry for the selected machine only", async () => {
		selectWindows();
		await act(async () => renderer.root.findByProps({ title: "Retry" }).props.onPress());
		expect(fixtures.refreshHost).toHaveBeenCalledWith("windows");
		expect(fixtures.refreshAll).not.toHaveBeenCalled();
	});
	// The same picker on both platforms; its native menus are tested on their own.
	it("offers every environment from the unreachable machine's picker and applies a choice immediately", () => {
		selectWindows();
		const picker = renderer.root.findByType("EnvironmentPicker" as never);
		expect(picker.props.environments.map((environment: { name: string }) => environment.name)).toEqual(["Mac", "Windows"]);
		expect(picker.props.selectedEnvironmentId).toBe("windows");
		act(() => picker.props.onSelectEnvironment("mac"));
		expect(selectedEnvironment()).toBe("Mac");
	});
	it("keeps the small connecting indicator beside its status and away from the picker", () => {
		Object.assign(fixtures.hostStates[1], { connection: "connecting", loading: true, error: null, appearance: { symbol: "laptop" } });
		selectWindows();
		const status = renderer.root.findByProps({ testID: "worker-machine-status" });
		expect(status.findByType("ActivityIndicator" as never).props.size).toBe("small");
		expect(status.findByType("Text" as never).props.children).toBe("Connecting…");
		expect(status.findAllByType("EnvironmentPicker" as never)).toHaveLength(0);
		expect(renderer.root.findAllByType("SymbolView" as never).some((node) => node.props.name?.android === "laptop")).toBe(true);
		expect(renderer.root.findByType("EnvironmentPicker" as never).props.environments[1].appearance).toEqual({ symbol: "laptop" });
	});
	// One unanswered poll on the selected machine is a blip, not a machine that is down.
	it("says reconnecting, not can't reach, after a single unanswered poll", async () => {
		const { EMPTY_LINK_TRACKER, recordPoll } = await import("./linkHealth");
		Object.assign(fixtures.hostStates[1], { connection: "closed", loading: false, linkTracker: recordPoll(EMPTY_LINK_TRACKER, "no-answer", Date.now()) });
		selectWindows();
		const status = renderer.root.findByProps({ testID: "worker-machine-status" });
		expect(status.findByType("Text" as never).props.children).toBe("Reconnecting…");
		expect(status.props.accessibilityLabel).toBe("Reconnecting to Windows");
		expect(renderer.root.findByProps({ testID: "worker-machine-hint" }).props.children).toMatch(/^Still trying to reach/);
	});
	it("uses the no-workers state only once the selected machine is connected", () => {
		Object.assign(fixtures.hostStates[1], { connection: "open", error: null });
		selectWindows();
		expect(emptyState().props.title).toBe("No workers on Windows");
	});
	it("keeps already loaded workers visible when their machine becomes unreachable", () => {
		fixtures.hostStates[1].sessions = [session("cached")];
		selectWindows();
		expect(renderer.root.findByType("WorkerBoardList" as never).props.sessions).toHaveLength(1);
		expect(renderer.root.findAllByType("EmptyState" as never)).toHaveLength(0);
	});
});
