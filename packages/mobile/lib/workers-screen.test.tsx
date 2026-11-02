import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSession } from "./api";
import type { HostSnapshot } from "./otherHosts";

const fixtures = vi.hoisted(() => ({
	platform: "ios",
	hostStates: [] as HostSnapshot[],
	refreshAll: vi.fn(async () => {}),
	refreshHost: vi.fn(async (_hostId: string) => {}),
	showMenu: vi.fn(),
	push: vi.fn(),
	theme: new Proxy({}, { get: () => "#000000" }),
}));

vi.mock("expo-router", () => ({ useRouter: () => ({ push: fixtures.push, navigate: vi.fn() }) }));
vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator", FlatList: "FlatList", ScrollView: "ScrollView", View: "View", Text: "Text", Pressable: "Pressable", Platform: { get OS() { return fixtures.platform; } },
	Keyboard: { dismiss: vi.fn() }, StyleSheet: { create: (styles: object) => styles },
}));
vi.mock("react-native-reanimated", () => ({ default: { View: "AnimatedView" }, useAnimatedStyle: () => ({}) }));
vi.mock("react-native-keyboard-controller", () => ({
	useKeyboardState: () => 0, useReanimatedKeyboardAnimation: () => ({ progress: { value: 0 } }),
}));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0 }) }));
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
	configured: true, connection: "open", loading: false, error: null,
	config: { hostId: "mac" },
	refreshAll: fixtures.refreshAll, refreshHost: fixtures.refreshHost,
	allSessions: fixtures.hostStates.flatMap((host) => host.sessions.map((session) => ({ ...session, hostId: host.hostId }))), allProjects: [],
	hostStates: fixtures.hostStates, notificationsUnread: 0,
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
vi.mock("./useTabScrollToTop", () => ({ useTabScrollToTop: () => ({ current: null }) }));
vi.mock("./ui", async () => {
	const React = await import("react");
	return {
		Button: "Button", HeaderIconButton: "HeaderIconButton", ScreenHeader: "ScreenHeader",
		EmptyState: (props: { action?: React.ReactNode }) => React.createElement("EmptyState", props, props.action),
	};
});
vi.mock("./worker-board-list", async () => {
	const React = await import("react");
	return { WorkerBoardList: ({ ListEmptyComponent, ...props }: { ListEmptyComponent: React.ReactNode; sessions: unknown[] }) =>
		React.createElement("WorkerBoardList", props, props.sessions.length === 0 ? ListEmptyComponent : null) };
});
vi.mock("./worker-dock", () => ({ WorkerDock: "WorkerDock" }));
vi.mock("./worker-controls-sheet", () => ({ WorkerControlsSheet: "WorkerControlsSheet" }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));

import FleetScreen from "../app/(tabs)/index";

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
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.refreshAll.mockClear();
	fixtures.refreshHost.mockClear();
	fixtures.showMenu.mockClear();
	fixtures.push.mockClear();
	fixtures.hostStates = [
		snapshot({ hostId: "mac", name: "Mac", connection: "open", error: null, sessions: [session("worker-1")] }),
		snapshot({}),
	];
});
afterEach(() => { act(() => renderer?.unmount()); vi.unstubAllGlobals(); });

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
