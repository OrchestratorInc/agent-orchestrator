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
vi.mock("@expo/ui/community/bottom-sheet", () => ({ default: "BottomSheet", BottomSheetView: "BottomSheetView", BottomSheetScrollView: "BottomSheetScrollView" }));
vi.mock("./store", () => ({ useApp: () => ({
	configured: fixtures.configured, connection: "open", loading: false, error: null,
	config: { hostId: "mac" },
	refreshAll: fixtures.refreshAll, refreshHost: fixtures.refreshHost,
	allSessions: fixtures.hostStates.flatMap((host) => host.sessions.map((session) => ({ ...session, hostId: host.hostId }))), allProjects: fixtures.projects,
	hostStates: fixtures.hostStates, notificationsUnread: fixtures.notificationsUnread,
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
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));

import FleetScreen from "../app/(tabs)/(workers)/index";
import { WorkerBoardList } from "./worker-board-list";
import { WorkerEnvironmentFilter } from "./worker-environment-filter";

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
function submenu(title: string | RegExp): ReactTestInstance {
	return filterMenu().findAllByType("StackToolbarMenu" as never).find((node) => typeof title === "string" ? node.props.title === title : title.test(node.props.title ?? ""))!;
}
/** Titled with its current choice, so it is found by its glyph. */
function environmentMenu(): ReactTestInstance {
	return filterMenu().findAllByType("StackToolbarMenu" as never).find((node) => node.props.icon === "tray.2")!;
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
	return choices(environmentMenu()).find((node) => node.props.isOn)?.props.children;
}
function search(text: string) {
	act(() => renderer.root.findByType("StackSearchBar" as never).props.onChangeText({ nativeEvent: { text } }));
}
function selectWindows() {
	act(() => { renderer = create(<FleetScreen />); });
	choose(environmentMenu(), "Windows");
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
	it("declares Settings and Notifications as trailing header items", () => {
		act(() => { renderer = create(<FleetScreen />); });
		const [settings, notifications] = headerButtons();
		expect(settings.props).toMatchObject({ icon: "gearshape", accessibilityLabel: "Settings" });
		expect(notifications.props).toMatchObject({ icon: "bell", accessibilityLabel: "Notifications" });
		expect(notifications.findAllByType("StackToolbarBadge" as never)).toHaveLength(0);
		act(() => settings.props.onPress());
		expect(fixtures.push).toHaveBeenCalledWith("/settings");
		act(() => notifications.props.onPress());
		expect(fixtures.navigate).toHaveBeenCalledWith("/notifications");
	});

	it("badges Notifications with the unread count across machines", () => {
		fixtures.hostStates = fixtures.hostStates.map((host) => ({ ...host, notificationsUnread: 2 }));
		act(() => { renderer = create(<FleetScreen />); });
		const notifications = headerButtons()[1];
		expect(notifications.props.accessibilityLabel).toBe("Notifications, 4 unread");
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

	// A large-title bar is translucent and the page is laid out under it, so the
	// board list has to be the page's first view and inset itself under the bar.
	it("makes the board list the page's first view, inset under the header", () => {
		act(() => { renderer = create(<FleetScreen />); });
		const screen = renderer.root.findAllByType("View" as never)[0];
		const first = screen.children[0] as { type: unknown; props: { contentInsetAdjustmentBehavior?: string } };
		expect(first.type).toBe(WorkerBoardList);
		expect(first.props.contentInsetAdjustmentBehavior).toBe("automatic");
	});

	// The list insets its rows under the bar itself; what floats over it does not,
	// so it is told how much of its top the bar covers.
	it("floats the board's overlays below the iOS bar the page runs under", () => {
		act(() => { renderer = create(<FleetScreen />); });
		expect(renderer.root.findByType(WorkerBoardList).props.overlayTopInset).toBe(155);
	});

	// Android's top app bar sits above the page and covers none of it.
	it("leaves the board's overlays at the top of the Android page", () => {
		fixtures.platform = "android";
		act(() => { renderer = create(<FleetScreen />); });
		expect(renderer.root.findByType(WorkerBoardList).props.overlayTopInset).toBe(0);
	});
});

// Search is the header's own: UISearchController under the large title on iOS,
// the top app bar's search action on Android.
describe("Workers search", () => {
	it("searches from the native header search bar", () => {
		act(() => { renderer = create(<FleetScreen />); });
		expect(renderer.root.findByType("StackSearchBar" as never).props).toMatchObject({
			placeholder: "Search workers",
			placement: "stacked",
			obscureBackground: false,
			autoCapitalize: "none",
			tintColor: "#highlight",
		});
		search("audit");
		expect(renderer.root.findByType(WorkerBoardList).props.query).toBe("audit");
	});

	it("says no workers were found when the search matches none", () => {
		fixtures.hostStates[0].sessions = [];
		act(() => { renderer = create(<FleetScreen />); });
		search("missing");
		expect(emptyState().props).toMatchObject({ title: "No workers found", message: "No workers match “missing”." });
	});

	// iOS cancels with its own button; Android closes the action view, by its X
	// or by Back. Either way the board returns to every worker.
	it.each(["onCancelButtonPress", "onClose"])("clears the search on %s", (event) => {
		act(() => { renderer = create(<FleetScreen />); });
		search("audit");
		act(() => renderer.root.findByType("StackSearchBar" as never).props[event]());
		expect(renderer.root.findByType(WorkerBoardList).props.query).toBe("");
	});

	// The bar registers its options with the header, so a handler that changed on
	// every poll would register them again each time.
	it("keeps the search bar's handlers across polls", () => {
		act(() => { renderer = create(<FleetScreen />); });
		const before = renderer.root.findByType("StackSearchBar" as never).props;
		fixtures.hostStates = fixtures.hostStates.map((host) => ({ ...host, lastSyncAt: host.lastSyncAt + 8_000 }));
		act(() => renderer.update(<FleetScreen />));
		const after = renderer.root.findByType("StackSearchBar" as never).props;
		expect(after.onChangeText).toBe(before.onChangeText);
		expect(after.onClose).toBe(before.onClose);
	});
});

describe("Workers filter menu", () => {
	// iOS leads the bar with the filter, across from the actions.
	it("leads the iOS bar with the environment and project choices, current ones checked", () => {
		act(() => { renderer = create(<FleetScreen />); });
		expect(filterMenu().props).toMatchObject({ icon: "line.3.horizontal.decrease.circle", accessibilityLabel: "Filter workers" });
		expect(environmentMenu().props.title).toBe("All environments");
		expect(choices(environmentMenu()).map((node) => [node.props.children, node.props.icon, node.props.isOn])).toEqual([
			["All environments", "server.rack", true],
			["Mac", "desktopcomputer", false],
			["Windows", "desktopcomputer", false],
		]);
		// Both are titled with their selection alone, which is also what it holds.
		expect(submenu("All projects").props.icon).toBe("folder");
		expect(choices(submenu("All projects")).map((node) => [node.props.children, node.props.isOn])).toEqual([["All projects", true]]);
	});

	// The state is in the glyph, not in colour alone.
	it("fills the iOS filter symbol once the board is filtered", () => {
		selectWindows();
		expect(fixtures.select).toHaveBeenCalledOnce();
		expect(selectedEnvironment()).toBe("Windows");
		expect(environmentMenu().props.title).toBe("Windows");
		expect(filterMenu().props).toMatchObject({ icon: "line.3.horizontal.decrease.circle.fill", accessibilityLabel: "Filter workers, filtered to Windows" });
		expect(filterMenu().findAllByType("StackToolbarBadge" as never)).toHaveLength(0);
	});

	// Android's top app bar has no leading actions, and badges a filtered board.
	it("heads the Android bar's trailing actions and badges a filtered board", () => {
		fixtures.platform = "android";
		act(() => { renderer = create(<FleetScreen />); });
		expect(toolbar("left")).toBeUndefined();
		expect(toolbar("right")!.props.children[0].props.accessibilityLabel).toBe("Filter workers");
		expect(filterMenu().findAllByType("StackToolbarBadge" as never)).toHaveLength(0);
		expect(choices(environmentMenu()).map((node) => node.props.icon)).toEqual([undefined, undefined, undefined]);
		choose(environmentMenu(), "Windows");
		expect(filterMenu().findByType("StackToolbarBadge" as never).props.style).toEqual({ backgroundColor: "#accent" });
		expect(filterMenu().props.accessibilityLabel).toBe("Filter workers, filtered to Windows");
	});

	// Compose's menu items draw the check with no selected state for TalkBack,
	// and one machine leaves no chip row to say it, so the titles carry it.
	it("names the chosen machine and project on Android, where the check is silent", () => {
		fixtures.platform = "android";
		fixtures.hostStates = fixtures.hostStates.slice(0, 1);
		fixtures.projects = [project("ao", "AO", "mac", "Mac")];
		act(() => { renderer = create(<FleetScreen />); });
		expect(renderer.root.findAllByType(WorkerEnvironmentFilter)).toHaveLength(0);
		choose(environmentMenu(), "Mac");
		expect(environmentMenu().props.title).toBe("Mac");
		expect(filterMenu().props.accessibilityLabel).toBe("Filter workers, filtered to Mac");
		choose(submenu("All projects"), "AO");
		expect(submenu("AO").props.icon).toBe("folder");
		expect(filterMenu().props.accessibilityLabel).toBe("Filter workers, filtered to Mac, AO");
	});

	it("scopes the project choices to the environment and drops one it does not have", () => {
		fixtures.projects = [project("ao", "AO", "mac", "Mac"), project("game", "Game", "windows", "Windows")];
		act(() => { renderer = create(<FleetScreen />); });
		choose(submenu("All projects"), "Game · Windows");
		expect(choices(submenu("Game · Windows")).find((node) => node.props.isOn)?.props.children).toBe("Game · Windows");
		choose(environmentMenu(), "Mac");
		expect(selectedEnvironment()).toBe("Mac");
		expect(choices(submenu("All projects")).map((node) => node.props.children)).toEqual(["All projects", "AO"]);
	});

	// Rebuilding the menu re-registers the header, which can close it under the
	// finger, so a poll that changes only machine state leaves it alone.
	it("keeps the header items across polls that change only machine state", () => {
		act(() => { renderer = create(<FleetScreen />); });
		const menu = toolbar("left")!.props.children;
		const trailing = toolbar("right")!.props;
		fixtures.hostStates = fixtures.hostStates.map((host) => ({ ...host, lastSyncAt: host.lastSyncAt + 8_000, sessions: [...host.sessions] }));
		act(() => renderer.update(<FleetScreen />));
		expect(toolbar("left")!.props.children).toBe(menu);
		expect(toolbar("right")!.props).toBe(trailing);

		fixtures.hostStates = fixtures.hostStates.map((host) => host.hostId === "windows" ? { ...host, name: "Gaming PC" } : host);
		act(() => renderer.update(<FleetScreen />));
		expect(toolbar("left")!.props.children).not.toBe(menu);
		// Machines are listed by name, so the rename also moves it first.
		expect(choices(environmentMenu()).map((node) => node.props.children)).toEqual(["All environments", "Gaming PC", "Mac"]);
	});
});

describe("Workers spawn action", () => {
	// iOS has no floating action button: the create action ends the header.
	it("ends the iOS header with a + that opens the spawn sheet", () => {
		act(() => { renderer = create(<FleetScreen />); });
		const add = toolbar("right")!.findAllByType("StackToolbarButton" as never).at(-1)!;
		expect(add.props).toMatchObject({ icon: "plus", accessibilityLabel: "Spawn worker" });
		act(() => add.props.onPress());
		expect(fixtures.push).toHaveBeenCalledWith({ pathname: "/spawn", params: { hostId: "mac" } });
	});

	// Android floats Material's FAB instead, at the page's bottom-end corner,
	// which already ends at the navigation bar.
	it("floats the Android spawn button instead of a header item", () => {
		fixtures.platform = "android";
		act(() => { renderer = create(<FleetScreen />); });
		expect(toolbar("right")!.findAllByType("StackToolbarButton" as never).map((node) => node.props.accessibilityLabel)).toEqual(["Settings", "Notifications"]);
		const fab = renderer.root.findByType("AddFab" as never);
		expect(fab.props).toMatchObject({ label: "Spawn worker", bottomInset: 0, testID: "spawn-worker" });
		expect(renderer.root.findByType(WorkerBoardList).props.contentBottomInset).toBe(72 + 24);
		act(() => fab.props.onPress());
		expect(fixtures.push).toHaveBeenCalledWith({ pathname: "/spawn", params: { hostId: "mac" } });
	});

	it("carries the project filter into the spawn sheet", () => {
		fixtures.projects = [project("game", "Game", "windows", "Windows")];
		act(() => { renderer = create(<FleetScreen />); });
		choose(submenu("All projects"), "Game");
		act(() => renderer.root.findByType("AddFab" as never).props.onPress());
		expect(fixtures.push).toHaveBeenCalledWith({ pathname: "/spawn", params: { hostId: "windows", projectId: "game" } });
	});
});

describe("Workers environment chips", () => {
	// Material's filter chips on Android (worker-environment-filter.android.test);
	// iOS draws nothing for them and keeps the choice in the header's menu.
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
	it("offers a retry for the selected machine only", async () => {
		selectWindows();
		await act(async () => renderer.root.findByProps({ title: "Retry" }).props.onPress());
		expect(fixtures.refreshHost).toHaveBeenCalledWith("windows");
		expect(fixtures.refreshAll).not.toHaveBeenCalled();
	});
	it("opens only the native environment choices from the unreachable machine's picker", () => {
		selectWindows();
		const menu = renderer.root.findByType("MenuView" as never);
		expect(menu.props.actions.map((action: { title: string }) => action.title)).toEqual(["All environments", "Mac", "Windows"]);
		expect(menu.props.actions[2].state).toBe("on");
		act(() => menu.props.onPressAction({ nativeEvent: { event: "mac" } }));
		expect(selectedEnvironment()).toBe("Mac");
	});
	it("opens an environment-only sheet on Android and applies a choice immediately", () => {
		fixtures.platform = "android";
		fixtures.hostStates[1].appearance = { symbol: "laptop" };
		selectWindows();
		act(() => renderer.root.findByProps({ accessibilityLabel: "Switch environment" }).props.onPress());
		const sheet = renderer.root.findByType("BottomSheet" as never);
		expect(sheet.props.index).toBe(0);
		expect(renderer.root.findAllByType("MenuView" as never)).toHaveLength(0);
		const options = sheet.findAllByType("Pressable" as never).filter(node => node.props.accessibilityRole === "radio");
		expect(options.map(node => node.props.accessibilityLabel)).toEqual(["All environments", "Mac", "Windows"]);
		expect(options[2].props.accessibilityState.checked).toBe(true);
		expect(options[2].findAllByType("SymbolView" as never)[0].props.name).toEqual({ ios: "laptopcomputer", android: "laptop" });
		act(() => options[1].props.onPress());
		expect(selectedEnvironment()).toBe("Mac");
		expect(renderer.root.findAllByType("BottomSheet" as never)).toHaveLength(0);
	});
	it("dismisses the Android sheet without changing the environment", () => {
		fixtures.platform = "android";
		selectWindows();
		act(() => renderer.root.findByProps({ accessibilityLabel: "Switch environment" }).props.onPress());
		act(() => renderer.root.findByType("BottomSheet" as never).props.onClose());
		expect(renderer.root.findByType("BottomSheet" as never).props.index).toBe(-1);
		expect(selectedEnvironment()).toBe("Windows");
	});
	it("keeps the small connecting indicator beside its status and away from the picker", () => {
		Object.assign(fixtures.hostStates[1], { connection: "connecting", loading: true, error: null, appearance: { symbol: "laptop" } });
		selectWindows();
		const status = renderer.root.findByProps({ testID: "worker-machine-status" });
		expect(status.findByType("ActivityIndicator" as never).props.size).toBe("small");
		expect(status.findByType("Text" as never).props.children).toBe("Connecting…");
		expect(renderer.root.findByType("MenuView" as never).findAllByType("ActivityIndicator" as never)).toHaveLength(0);
		expect(renderer.root.findAllByType("SymbolView" as never).some((node) => node.props.name?.android === "laptop")).toBe(true);
		expect(renderer.root.findByType("MenuView" as never).props.actions[2].image).toBe("laptopcomputer");
	});
	it("uses the no-workers state only once the selected machine is connected", () => {
		Object.assign(fixtures.hostStates[1], { connection: "open", error: null });
		selectWindows();
		expect(emptyState().props.title).toBe("No workers on Windows");
	});
	it("does not hide unreachable-machine feedback behind an empty search", () => {
		selectWindows();
		search("missing");
		expect(emptyState().props.title).toBe("Can't reach Windows");
	});
	it("keeps already loaded workers visible when their machine becomes unreachable", () => {
		fixtures.hostStates[1].sessions = [session("cached")];
		selectWindows();
		expect(renderer.root.findByType("WorkerBoardList" as never).props.sessions).toHaveLength(1);
		expect(renderer.root.findAllByType("EmptyState" as never)).toHaveLength(0);
	});
});
