import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Host } from "./hosts";

const fixtures = vi.hoisted(() => ({
	loadHosts: vi.fn<() => Promise<Host[]>>(async () => []),
	activeHost: vi.fn<() => Promise<Host | null>>(async () => null),
	refreshAll: vi.fn(async () => {}),
	push: vi.fn(),
	hostStates: [] as { hostId: string; connection: "closed" | "connecting" | "open"; loading: boolean; config: unknown }[],
	theme: { accent: "accent", bgBase: "base", bgElevated: "card", red: "red" },
}));

vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	const router = { push: fixtures.push };
	return {
		Stack: { Toolbar: Object.assign(host("StackToolbar"), { Button: host("StackToolbarButton") }) },
		useFocusEffect: (effect: () => void) => React.useEffect(effect, [effect]),
		useRouter: () => router,
	};
});
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		RefreshControl: host("RefreshControl"),
		ScrollView: host("ScrollView"),
		StyleSheet: { create: (styles: object) => styles },
		View: host("View"),
	};
});
vi.mock("../lib/EnvironmentMachineRow", async () => {
	const React = await import("react");
	return { EnvironmentMachineList: (props: object) => React.createElement("EnvironmentMachineList", props) };
});
vi.mock("../lib/hosts", () => ({ activeHost: fixtures.activeHost, loadHosts: fixtures.loadHosts }));
vi.mock("../lib/store", () => ({
	useApp: () => ({ hostStates: fixtures.hostStates, configForHost: () => null, refreshAll: fixtures.refreshAll }),
}));
vi.mock("../lib/ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme),
}));
vi.mock("../lib/Toast", async () => {
	const React = await import("react");
	return { ToastHost: (props: object) => React.createElement("ToastHost", props) };
});
vi.mock("../lib/Spinner", () => ({ Spinner: "Spinner" }));
vi.mock("../lib/ui", async () => {
	const React = await import("react");
	return {
		Button: (props: object) => React.createElement("Button", props),
		EmptyState: ({ action, ...props }: { action?: React.ReactNode }) => React.createElement("EmptyState", props, action),
	};
});
vi.mock("../lib/RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));

import EnvironmentsScreen from "../app/settings/environments";
import { postEnvironmentNotice } from "./environmentNotice";

const a: Host = { id: "a", name: "A", platform: "darwin", endpoints: [{ kind: "lan", host: "10.0.0.2", port: 3011, secure: false }], token: "token", lastConnected: 2 };
const b: Host = { ...a, id: "b", name: "B", lastConnected: 1 };

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.loadHosts.mockReset().mockResolvedValue([]);
	fixtures.activeHost.mockReset().mockResolvedValue(null);
	fixtures.refreshAll.mockClear();
	fixtures.push.mockClear();
	fixtures.hostStates = [];
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

async function open() {
	await act(async () => { renderer = create(<EnvironmentsScreen />); });
}

describe("Environments header", () => {
	// The title is the native header's; Add machine is its trailing item.
	it("adds a machine from the native header", async () => {
		await open();
		expect(renderer!.root.findAll((node) => (node.type as unknown) === "Text" && node.props.children === "Environments")).toHaveLength(0);
		expect(renderer!.root.findByType("StackToolbar" as never).props.placement).toBe("right");
		const add = renderer!.root.findByType("StackToolbarButton" as never);
		expect(add.props).toMatchObject({ icon: "plus", accessibilityLabel: "Add machine" });
		act(() => add.props.onPress());
		expect(fixtures.push).toHaveBeenCalledWith("/pair");
	});
});

describe("Environments list", () => {
	it("lists every paired machine with its default and connection, and opens its page", async () => {
		fixtures.loadHosts.mockResolvedValue([a, b]);
		fixtures.activeHost.mockResolvedValue(b);
		fixtures.hostStates = [{ hostId: "a", connection: "open", loading: false, config: null }];
		await open();
		const list = renderer!.root.findByType("EnvironmentMachineList" as never);
		expect(list.props.machines.map((machine: { host: Host; isDefault: boolean; status: { label: string } }) => [machine.host.id, machine.isDefault, machine.status.label]))
			.toEqual([["a", false, "Connected"], ["b", true, "Offline"]]);
		expect(list.props.footer).toBe("Machines available for new workers and reconnects.");
		act(() => list.props.onOpen("b"));
		expect(fixtures.push).toHaveBeenCalledWith({ pathname: "/settings/machine/[id]", params: { id: "b" } });
	});

	it("refreshes the fleet and the saved machines together", async () => {
		fixtures.loadHosts.mockResolvedValue([a]);
		await open();
		fixtures.loadHosts.mockResolvedValue([a, b]);
		await act(async () => renderer!.root.findByType("EnvironmentMachineList" as never).props.onRefresh());
		expect(fixtures.refreshAll).toHaveBeenCalledOnce();
		expect(renderer!.root.findByType("EnvironmentMachineList" as never).props.machines).toHaveLength(2);
	});

	// A machine is forgotten from its own page, which closes; the list says so.
	it("announces a machine forgotten on its page when the list comes back", async () => {
		fixtures.loadHosts.mockResolvedValue([b]);
		postEnvironmentNotice("A forgotten. B is now the default.");
		await open();
		expect(renderer!.root.findByType("ToastHost" as never).props.toast.current.text).toBe("A forgotten. B is now the default.");
		expect(renderer!.root.findAllByProps({ testID: "environment-forgotten-notice" })).toHaveLength(0);
	});
});

describe("Environments empty and failed states", () => {
	it("offers to add a machine when none is saved, with a themed pull to refresh", async () => {
		await open();
		const empty = renderer!.root.findByType("EmptyState" as never);
		expect(empty.props).toMatchObject({ icon: "devices", title: "No saved machines", message: "Connect a machine to view its workers." });
		const add = empty.findByType("Button" as never);
		expect(add.props.title).toBe("Add a machine");
		act(() => add.props.onPress());
		expect(fixtures.push).toHaveBeenCalledWith("/pair");
		const refresh = renderer!.root.findByType("ScrollView" as never).props.refreshControl;
		expect(refresh.props).toMatchObject({ tintColor: "accent", colors: ["accent"], progressBackgroundColor: "card" });
		// The shared empty state, not a card of its own.
		expect(renderer!.root.findAll((node) => node.props.style?.borderRadius === 24)).toHaveLength(0);
	});

	it("says the machines couldn't load, as an alert with a retry", async () => {
		fixtures.loadHosts.mockRejectedValue(new Error("storage"));
		await open();
		const alert = renderer!.root.findByProps({ accessibilityRole: "alert" });
		const empty = alert.findByType("EmptyState" as never);
		expect(empty.props).toMatchObject({ icon: "warning", iconColor: "red", message: "The saved environments couldn't be loaded." });
		fixtures.loadHosts.mockResolvedValue([a]);
		await act(async () => empty.findByType("Button" as never).props.onPress());
		expect(renderer!.root.findByType("EnvironmentMachineList" as never).props.error).toBeUndefined();
	});
});
