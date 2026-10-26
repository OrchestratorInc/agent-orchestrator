import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSession } from "./api";
import type { HostSnapshot } from "./otherHosts";

const fixtures = vi.hoisted(() => ({
	platform: "ios",
	keyboard: { height: 0, isVisible: false, progress: 0 },
	headerHeight: 155,
	hostStates: [] as HostSnapshot[],
	refreshAll: vi.fn(async () => {}),
	refreshHost: vi.fn(async (_hostId: string) => {}),
	showMenu: vi.fn(),
	push: vi.fn(),
	navigate: vi.fn(),
	configured: true,
	notificationsUnread: 0,
	theme: new Proxy({}, { get: () => "#000000" }),
}));

vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		useRouter: () => ({ push: fixtures.push, navigate: fixtures.navigate }),
		Stack: { Toolbar: Object.assign(host("StackToolbar"), { Button: host("StackToolbarButton"), Badge: host("StackToolbarBadge") }) },
	};
});
vi.mock("expo-router/react-navigation", () => ({ useHeaderHeight: () => fixtures.headerHeight }));
vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator", FlatList: "FlatList", ScrollView: "ScrollView", View: "View", Text: "Text", Pressable: "Pressable", Platform: { get OS() { return fixtures.platform; } },
	Keyboard: { dismiss: vi.fn() }, StyleSheet: { create: (styles: object) => styles },
}));
vi.mock("react-native-reanimated", () => ({ default: { View: "AnimatedView" }, useAnimatedStyle: (style: () => object) => style() }));
vi.mock("react-native-keyboard-controller", () => ({
	useKeyboardState: (select: (state: typeof fixtures.keyboard) => unknown) => select(fixtures.keyboard),
	useReanimatedKeyboardAnimation: () => ({ progress: { value: fixtures.keyboard.progress } }),
}));
vi.mock("react-native-safe-area-context", () => ({
	useSafeAreaInsets: () => ({ top: 0, bottom: 0 }),
	useSafeAreaFrame: () => ({ x: 0, y: 0, width: 412, height: 900 }),
}));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn() } }));
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
	allSessions: fixtures.hostStates.flatMap((host) => host.sessions.map((session) => ({ ...session, hostId: host.hostId }))), allProjects: [],
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
vi.mock("./worker-dock", () => ({ WorkerDock: "WorkerDock" }));
vi.mock("./worker-controls-sheet", () => ({ WorkerControlsSheet: "WorkerControlsSheet" }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));

import FleetScreen from "../app/(tabs)/(workers)/index";
import { WorkerBoardList } from "./worker-board-list";
import { workerListBottomInset } from "./worker-dock-layout";

let renderer: ReactTestRenderer;
function selectWindows() {
	act(() => { renderer = create(<FleetScreen />); });
	act(() => renderer.root.findByType("WorkerDock" as never).props.onSelectEnvironment("windows"));
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

beforeEach(() => {
	fixtures.platform = "ios";
	fixtures.keyboard = { height: 0, isVisible: false, progress: 0 };
	fixtures.configured = true;
	fixtures.notificationsUnread = 0;
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.refreshAll.mockClear();
	fixtures.refreshHost.mockClear();
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
		return renderer.root.findByType("StackToolbar" as never).findAllByType("StackToolbarButton" as never);
	}

	// The header is the native bar's: its title comes from the tab's stack, and
	// its actions are bar items the page declares, not a row drawn in the page.
	it("declares Settings and Notifications as trailing header items", () => {
		act(() => { renderer = create(<FleetScreen />); });
		expect(renderer.root.findByType("StackToolbar" as never).props.placement).toBe("right");
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

	// Settings is where pairing starts, so an unpaired board still offers it.
	it("keeps Settings in the header before a machine is paired", () => {
		fixtures.configured = false;
		fixtures.hostStates = [];
		act(() => { renderer = create(<FleetScreen />); });
		expect(renderer.root.findByType("UnpairedState" as never)).toBeTruthy();
		expect(headerButtons().map((button) => button.props.accessibilityLabel)).toEqual(["Settings"]);
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

// The keyboard's height runs from the bottom of the window. Android's page stops
// at the tab bar above that, so measuring the dock's lift from the page's own
// bottom edge floated it a whole tab bar above the keys.
describe("Workers dock over the keyboard", () => {
	it("sets the dock down just above the Android keyboard, clear of the tab bar", () => {
		fixtures.platform = "android";
		fixtures.keyboard = { height: 400, isVisible: true, progress: 1 };
		// A 900dp window whose page ends at 764dp, above a 136dp tab bar.
		act(() => {
			renderer = create(<FleetScreen />, {
				createNodeMock: () => ({ measureInWindow: (done: (x: number, y: number, width: number, height: number) => void) => done(0, 120, 412, 644) }),
			});
		});
		const page = renderer.root.findAllByType("View" as never)[0];
		act(() => page.props.onLayout());
		const [, resting, rise] = renderer.root.findByType("AnimatedView" as never).props.style;
		const fromWindowBottom = 136 + resting.bottom - rise.transform[0].translateY;
		expect(fromWindowBottom).toBe(400 + 8);
		expect(renderer.root.findByType(WorkerBoardList).props.contentBottomInset).toBe(workerListBottomInset(400 - 136 + 12));
	});

	// iOS lays the page out under the tab bar to the bottom of the window, so the
	// keyboard's own height is already the page's.
	it("sets the dock down just above the iOS keyboard", () => {
		fixtures.keyboard = { height: 336, isVisible: true, progress: 1 };
		act(() => { renderer = create(<FleetScreen />); });
		const [, resting, rise] = renderer.root.findByType("AnimatedView" as never).props.style;
		expect(resting.bottom - rise.transform[0].translateY).toBe(336 + 8);
	});
});

describe("Workers dock stability", () => {
	it("keeps the dock's environments across polls that change only machine state", () => {
		act(() => { renderer = create(<FleetScreen />); });
		const before = renderer.root.findByType("WorkerDock" as never).props.environments;
		fixtures.hostStates = fixtures.hostStates.map((host) => ({ ...host, lastSyncAt: host.lastSyncAt + 8_000, sessions: [...host.sessions] }));
		act(() => renderer.update(<FleetScreen />));
		expect(renderer.root.findByType("WorkerDock" as never).props.environments).toBe(before);

		fixtures.hostStates = fixtures.hostStates.map((host) => host.hostId === "windows" ? { ...host, name: "Gaming PC" } : host);
		act(() => renderer.update(<FleetScreen />));
		const renamed = renderer.root.findByType("WorkerDock" as never).props.environments;
		expect(renamed).not.toBe(before);
		// Machines are listed by name, so the rename also moves it first.
		expect(renamed.map((environment: { name: string }) => environment.name)).toEqual(["Gaming PC", "Mac"]);
	});
});

describe("Workers environment chips", () => {
	it("shows the machine chips only once two machines are paired", () => {
		act(() => { renderer = create(<FleetScreen />); });
		expect(renderer.root.findAllByProps({ testID: "worker-environment-strip" })).toHaveLength(1);

		fixtures.hostStates = fixtures.hostStates.slice(0, 1);
		act(() => renderer.update(<FleetScreen />));
		expect(renderer.root.findAllByProps({ testID: "worker-environment-strip" })).toHaveLength(0);
	});

	it("lists the machine chips by name", () => {
		fixtures.hostStates = [snapshot({ hostId: "z", name: "Zeta" }), snapshot({ hostId: "a", name: "alpha" })];
		act(() => { renderer = create(<FleetScreen />); });
		const chips = renderer.root.findAll((node) => (node.type as unknown) === "Pressable" && /^worker-environment-chip-(?!all$)/.test(node.props.testID ?? ""));
		expect(chips.map((chip) => chip.props.testID)).toEqual(["worker-environment-chip-a", "worker-environment-chip-z"]);
	});
});

describe("Workers selected-machine reachability", () => {
	it.each(["open", "closed"] as const)("targets the selected environment when creating work, even when %s", (connection) => {
		fixtures.hostStates[1].connection = connection;
		selectWindows();
		act(() => renderer.root.findByType("WorkerDock" as never).props.onSpawn());
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
	it("opens only the native environment choices without opening the full filters sheet", () => {
		selectWindows();
		expect(renderer.root.findByType("WorkerControlsSheet" as never).props.open).toBe(false);
		const menu = renderer.root.findByType("MenuView" as never);
		expect(menu.props.actions.map((action: { title: string }) => action.title)).toEqual(["All environments", "Mac", "Windows"]);
		expect(menu.props.actions[2].state).toBe("on");
		act(() => menu.props.onPressAction({ nativeEvent: { event: "mac" } }));
		expect(renderer.root.findByType("WorkerDock" as never).props.selectedEnvironmentId).toBe("mac");
	});
	it("opens an environment-only sheet on Android and applies a choice immediately", () => {
		fixtures.platform = "android";
		fixtures.hostStates[1].appearance = { symbol: "laptop" };
		selectWindows();
		act(() => renderer.root.findByProps({ accessibilityLabel: "Switch environment" }).props.onPress());
		const sheet = renderer.root.findByType("BottomSheet" as never);
		expect(sheet.props.index).toBe(0);
		expect(renderer.root.findByType("WorkerControlsSheet" as never).props.open).toBe(false);
		expect(renderer.root.findAllByType("MenuView" as never)).toHaveLength(0);
		const choices = sheet.findAllByType("Pressable" as never).filter(node => node.props.accessibilityRole === "radio");
		expect(choices.map(node => node.props.accessibilityLabel)).toEqual(["All environments", "Mac", "Windows"]);
		expect(choices[2].props.accessibilityState.checked).toBe(true);
		expect(choices[2].findAllByType("Feather" as never)[0].props.name).toBe("laptop");
		act(() => choices[1].props.onPress());
		expect(renderer.root.findByType("WorkerDock" as never).props.selectedEnvironmentId).toBe("mac");
		expect(renderer.root.findAllByType("BottomSheet" as never)).toHaveLength(0);
	});
	it("dismisses the Android sheet without changing the environment", () => {
		fixtures.platform = "android";
		selectWindows();
		act(() => renderer.root.findByProps({ accessibilityLabel: "Switch environment" }).props.onPress());
		act(() => renderer.root.findByType("BottomSheet" as never).props.onClose());
		expect(renderer.root.findByType("BottomSheet" as never).props.index).toBe(-1);
		expect(renderer.root.findByType("WorkerDock" as never).props.selectedEnvironmentId).toBe("windows");
	});
	it("keeps the small connecting indicator beside its status and away from the picker", () => {
		Object.assign(fixtures.hostStates[1], { connection: "connecting", loading: true, error: null, appearance: { symbol: "laptop" } });
		selectWindows();
		const status = renderer.root.findByProps({ testID: "worker-machine-status" });
		expect(status.findByType("ActivityIndicator" as never).props.size).toBe("small");
		expect(status.findByType("Text" as never).props.children).toBe("Connecting…");
		expect(renderer.root.findByType("MenuView" as never).findAllByType("ActivityIndicator" as never)).toHaveLength(0);
		expect(renderer.root.findAllByType("Feather" as never).some((node) => node.props.name === "laptop")).toBe(true);
		expect(renderer.root.findByType("MenuView" as never).props.actions[2].image).toBe("laptopcomputer");
	});
	it("uses the no-workers state only once the selected machine is connected", () => {
		Object.assign(fixtures.hostStates[1], { connection: "open", error: null });
		selectWindows();
		expect(emptyState().props.title).toBe("No workers on Windows");
	});
	it("does not hide unreachable-machine feedback behind an empty search", () => {
		selectWindows();
		act(() => renderer.root.findByType("WorkerDock" as never).props.onQueryChange("missing"));
		expect(emptyState().props.title).toBe("Can't reach Windows");
	});
	it("keeps already loaded workers visible when their machine becomes unreachable", () => {
		fixtures.hostStates[1].sessions = [session("cached")];
		selectWindows();
		expect(renderer.root.findByType("WorkerBoardList" as never).props.sessions).toHaveLength(1);
		expect(renderer.root.findAllByType("EmptyState" as never)).toHaveLength(0);
	});
});
