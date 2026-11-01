import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "./config";
import type { Host } from "./hosts";

const fixtures = vi.hoisted(() => ({
	loadHosts: vi.fn<() => Promise<Host[]>>(async () => []),
	activeHost: vi.fn<() => Promise<Host | null>>(async () => null),
	switchHost: vi.fn(async () => {}),
	reloadConfig: vi.fn(async () => {}),
	reloadHostMetadata: vi.fn(async () => {}),
	forget: vi.fn(),
	alert: vi.fn(),
	push: vi.fn(),
	dismissTo: vi.fn(),
	setSheetOptions: vi.fn(),
	setPageOptions: vi.fn(),
	focused: { current: true },
	copy: vi.fn(async () => true),
	platform: { OS: "android", Version: 34 },
	hostStates: [] as { hostId: string; connection: "closed" | "connecting" | "open"; loading: boolean; config: ServerConfig | null }[],
	haptics: { select: vi.fn(), error: vi.fn(), warning: vi.fn(), success: vi.fn() },
	theme: { accent: "accent", bgBase: "base", bgElevated: "card", green: "green", red: "red", textPrimary: "ink", textSecondary: "gray", textTertiary: "dim" },
}));

vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	const router = { push: fixtures.push, dismissTo: fixtures.dismissTo };
	const navigation = { setOptions: fixtures.setPageOptions, getParent: () => ({ setOptions: fixtures.setSheetOptions }), isFocused: () => fixtures.focused.current };
	return {
		Stack: {
			Screen: host("StackScreen"),
			Toolbar: Object.assign(host("StackToolbar"), { Button: host("StackToolbarButton") }),
		},
		useFocusEffect: (effect: () => void) => React.useEffect(effect, [effect]),
		useLocalSearchParams: () => ({ id: "a" }),
		useNavigation: () => navigation,
		useRouter: () => router,
	};
});
vi.mock("react-native", () => ({
	Alert: { alert: fixtures.alert },
	Platform: fixtures.platform,
	StyleSheet: { create: (styles: object) => styles },
	View: "View",
}));
vi.mock("@expo/ui", () => ({ Host: "Host" }));
vi.mock("@expo/ui/swift-ui", async () => (await import("./nativeFormTestDoubles")).swiftUIDouble());
vi.mock("@expo/ui/swift-ui/modifiers", async () => {
	const doubles = await import("./nativeFormTestDoubles");
	return doubles.modifierDouble(doubles.swiftUIModifierNames);
});
vi.mock("@expo/ui/jetpack-compose", async () => (await import("./nativeFormTestDoubles")).composeDouble());
vi.mock("@expo/ui/jetpack-compose/modifiers", async () => ({
	...(await import("./nativeFormTestDoubles")).composeModifierDouble(),
	alpha: (value: number) => ({ $type: "alpha", args: [value] }),
	graphicsLayer: (value: unknown) => ({ $type: "graphicsLayer", args: [value] }),
}));
vi.mock("./toolbarIcon.android", () => ({ materialDrawable: (name: string) => `${name}.xml` }));
vi.mock("expo-clipboard", () => ({ setStringAsync: fixtures.copy }));
vi.mock("./Toast", () => ({ ToastHost: "ToastHost" }));
vi.mock("./environmentActions", () => ({ forgetEnvironmentMachine: fixtures.forget }));
vi.mock("./hosts", () => ({ activeHost: fixtures.activeHost, loadHosts: fixtures.loadHosts }));
vi.mock("./store", () => ({
	useApp: () => ({
		hostStates: fixtures.hostStates,
		configForHost: () => null,
		switchHost: fixtures.switchHost,
		reloadConfig: fixtures.reloadConfig,
		reloadHostMetadata: fixtures.reloadHostMetadata,
	}),
}));
vi.mock("./haptics", () => ({ haptics: fixtures.haptics }));
vi.mock("./Spinner", () => ({ Spinner: "Spinner" }));
vi.mock("./ui", () => ({ EmptyState: "EmptyState" }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemeState: () => ({ scheme: "dark", theme: fixtures.theme }),
}));

import MachineDetailScreen from "../app/settings/machine/[id]/index";
import { takeEnvironmentNotice } from "./environmentNotice";

const a: Host = {
	id: "a",
	name: "Studio Mac",
	platform: "darwin",
	token: "secret",
	lastConnected: 2,
	endpoints: [
		{ kind: "lan", host: "192.168.1.24", port: 3011, secure: false },
		{ kind: "tailscale", host: "studio.tail123.ts.net", port: 3011, secure: true },
	],
};
const b: Host = { ...a, id: "b", name: "Dev", endpoints: [{ kind: "lan", host: "192.168.1.25", port: 3011, secure: false }] };
const onTailscale: ServerConfig = { host: "studio.tail123.ts.net", httpPort: "3011", muxPort: "3012", secure: true, password: "secret", hostId: "a", endpointKind: "tailscale" };

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	for (const mock of [fixtures.loadHosts, fixtures.activeHost, fixtures.switchHost, fixtures.reloadConfig, fixtures.reloadHostMetadata, fixtures.forget, fixtures.alert, fixtures.push, fixtures.dismissTo, fixtures.setSheetOptions, fixtures.setPageOptions, fixtures.copy, ...Object.values(fixtures.haptics)]) mock.mockReset();
	fixtures.focused.current = true;
	fixtures.platform.Version = 34;
	fixtures.loadHosts.mockResolvedValue([a, b]);
	fixtures.activeHost.mockResolvedValue(b);
	fixtures.switchHost.mockResolvedValue(undefined);
	fixtures.reloadConfig.mockResolvedValue(undefined);
	fixtures.reloadHostMetadata.mockResolvedValue(undefined);
	fixtures.hostStates = [{ hostId: "a", connection: "open", loading: false, config: onTailscale }];
	takeEnvironmentNotice();
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
	await act(async () => { renderer = create(<MachineDetailScreen />); });
}

type Modifier = { $type: string; args: unknown[] };
const modifiersOf = (node: ReactTestInstance) => (node.props.modifiers ?? []) as Modifier[];
const byTestID = (testID: string) => renderer!.root.find((node) => typeof node.type === "string" && (node.props.testID === testID || modifiersOf(node).some((m) => m.$type === "testID" && m.args[0] === testID)));
const queryTestID = (testID: string) => renderer!.root.findAll((node) => typeof node.type === "string" && node.props.testID === testID);

describe("a machine's page on iOS", () => {
	it("is titled for the machine and edits it from the bar", async () => {
		await open();
		expect(renderer!.root.findByType("StackScreen" as never).props.options).toEqual({ title: "Studio Mac" });
		const edit = renderer!.root.findByType("StackToolbarButton" as never);
		expect(edit.props).toMatchObject({ accessibilityLabel: "Edit Studio Mac", children: "Edit" });
		act(() => edit.props.onPress());
		expect(fixtures.push).toHaveBeenCalledWith({ pathname: "/settings/machine/[id]/edit", params: { id: "a" } });
	});

	// Each new options object sets the header again, and the page re-renders on
	// every fleet poll.
	it("keeps the title's options across re-renders", async () => {
		await open();
		const options = renderer!.root.findByType("StackScreen" as never).props.options;
		await act(async () => byTestID("environment-make-default-a").props.onPress());
		expect(renderer!.root.findByType("StackScreen" as never).props.options).toBe(options);
	});

	it("shows the status in words, the active route and the other routes in a disclosure group", async () => {
		await open();
		const form = renderer!.root.findByType("Form" as never);
		const labels = form.findAllByType("LabeledContent" as never).map((row) => row.props.label);
		expect(labels).toEqual(["Status", "Platform", "Active route", "LAN"]);
		const status = byTestID("machine-status");
		expect(status.props.children).toBe("Connected");
		expect(modifiersOf(status)).toContainEqual({ $type: "foregroundStyle", args: ["green"] });
		expect(byTestID("environment-active-address-a").props.children).toBe("studio.tail123.ts.net:3011");
		const others = renderer!.root.findByType("DisclosureGroup" as never);
		expect(others.props).toMatchObject({ isExpanded: false, testID: "environment-other-routes-a" });
		act(() => others.props.onIsExpandedChange(true));
		expect(renderer!.root.findByType("DisclosureGroup" as never).props.isExpanded).toBe(true);
	});

	it("makes the machine the default, then stops offering to", async () => {
		await open();
		const makeDefault = byTestID("environment-make-default-a");
		expect(makeDefault.props.label).toBe("Make default");
		await act(async () => makeDefault.props.onPress());
		expect(fixtures.switchHost).toHaveBeenCalledWith("a");
		expect(fixtures.haptics.select).toHaveBeenCalledOnce();
		expect(queryTestID("environment-make-default-a")).toHaveLength(0);
	});

	it("explains a default that couldn't change", async () => {
		fixtures.switchHost.mockRejectedValue(new Error("offline"));
		await open();
		await act(async () => byTestID("environment-make-default-a").props.onPress());
		expect(fixtures.alert).toHaveBeenCalledWith("Could not change default machine", "Try again from Machines.");
		expect(byTestID("environment-make-default-a")).toBeTruthy();
	});

	it("reconnects by scanning a new code for this machine", async () => {
		await open();
		act(() => byTestID("environment-reconnect-a").props.onPress());
		expect(fixtures.push).toHaveBeenCalledWith({ pathname: "/pair", params: { repairHostId: "a" } });
	});

	it("asks before forgetting, then returns to the list, which says what happened", async () => {
		fixtures.activeHost.mockResolvedValue(a);
		fixtures.forget.mockResolvedValue({ remaining: [b], nextDefault: b, wasDefault: true, notice: "Studio Mac forgotten. Dev is now the default." });
		await open();
		const forget = byTestID("environment-forget-a");
		expect(forget.props).toMatchObject({ role: "destructive", label: "Forget machine" });
		act(() => forget.props.onPress());
		expect(fixtures.forget).not.toHaveBeenCalled();
		const [title, message, buttons] = fixtures.alert.mock.calls.at(-1)!;
		expect(title).toBe("Forget Studio Mac?");
		expect(message).toMatch(/saved connection from this phone[\s\S]*workers and files[\s\S]*pair[\s\S]*reachable/i);
		expect(buttons.find((button: { text: string }) => button.text === "Forget").style).toBe("destructive");
		await act(async () => buttons.find((button: { text: string }) => button.text === "Forget").onPress());
		expect(fixtures.forget).toHaveBeenCalledWith(a, "a");
		expect(fixtures.reloadConfig).toHaveBeenCalledOnce();
		expect(fixtures.dismissTo).toHaveBeenCalledWith("/settings/environments");
		expect(takeEnvironmentNotice()).toBe("Studio Mac forgotten. Dev is now the default.");
	});

	// A forget can't be called back. Returning to the list after Settings had
	// closed under it would put Settings in the board's place.
	it("holds the page and Settings while a forget runs, and returns only while still on top", async () => {
		let finish!: () => void;
		fixtures.forget.mockReturnValue(new Promise((resolve) => {
			finish = () => resolve({ remaining: [b], nextDefault: b, wasDefault: false, notice: "Studio Mac forgotten." });
		}));
		await open();
		act(() => byTestID("environment-forget-a").props.onPress());
		const buttons = fixtures.alert.mock.calls.at(-1)![2];
		await act(async () => { buttons.find((button: { text: string }) => button.text === "Forget").onPress(); });
		expect(fixtures.setSheetOptions).toHaveBeenLastCalledWith({ gestureEnabled: false });
		expect(fixtures.setPageOptions).toHaveBeenLastCalledWith({ gestureEnabled: false });
		fixtures.focused.current = false;
		await act(async () => finish());
		expect(fixtures.setSheetOptions).toHaveBeenLastCalledWith({ gestureEnabled: true });
		expect(fixtures.setPageOptions).toHaveBeenLastCalledWith({ gestureEnabled: true });
		expect(fixtures.dismissTo).not.toHaveBeenCalled();
		expect(takeEnvironmentNotice()).toBe("Studio Mac forgotten.");
	});

	it("keeps the page and says why when forgetting fails", async () => {
		fixtures.forget.mockRejectedValue(new Error("This machine's saved connection couldn't be removed. Try again."));
		await open();
		act(() => byTestID("environment-forget-a").props.onPress());
		const buttons = fixtures.alert.mock.calls.at(-1)![2];
		await act(async () => buttons.find((button: { text: string }) => button.text === "Forget").onPress());
		expect(fixtures.alert).toHaveBeenLastCalledWith("Couldn't forget machine", "This machine's saved connection couldn't be removed. Try again.");
		expect(fixtures.dismissTo).not.toHaveBeenCalled();
		expect(takeEnvironmentNotice()).toBeUndefined();
	});

	it("says so when the machine is no longer paired", async () => {
		fixtures.loadHosts.mockResolvedValue([b]);
		await open();
		expect(renderer!.root.findByType("EmptyState" as never).props.title).toBe("Machine not found");
	});
});

describe("a machine's page on Android", () => {
	async function render(props: { isDefault?: boolean; forgetting?: boolean } = {}) {
		const { MachineDetail } = await import("./MachineDetail.android");
		const handlers = { onMakeDefault: vi.fn(), onReconnect: vi.fn(), onForget: vi.fn(), onEdit: vi.fn() };
		await act(async () => {
			renderer = create(
				<MachineDetail
					host={a}
					activeConfig={onTailscale}
					isDefault={props.isDefault ?? false}
					status={{ kind: "offline", label: "Offline" }}
					switching={false}
					forgetting={props.forgetting ?? false}
					{...handlers}
				/>,
			);
		});
		return handlers;
	}
	const click = (node: ReactTestInstance) => (modifiersOf(node).find((m) => m.$type === "clickable")?.args[0] as (() => void) | undefined);

	it("edits from a top app bar icon and lists the machine as Material rows", async () => {
		const handlers = await render();
		const edit = renderer!.root.findByType("StackToolbarButton" as never);
		expect(edit.props).toMatchObject({ icon: "edit.xml", accessibilityLabel: "Edit Studio Mac" });
		act(() => edit.props.onPress());
		expect(handlers.onEdit).toHaveBeenCalledOnce();
		const status = byTestID("machine-status").findByType("ListItem.TrailingContent" as never).findByType("Text" as never);
		expect(status.props).toMatchObject({ color: "red", children: "Offline" });
		const active = byTestID("environment-active-address-a");
		expect(active.findByType("ListItem.SupportingContent" as never).findByType("Text" as never).props.children).toBe("studio.tail123.ts.net:3011");
	});

	it("expands the other routes in place", async () => {
		await render();
		expect(queryTestID("environment-other-route-lan://192.168.1.24:3011")).toHaveLength(0);
		const toggle = byTestID("environment-other-routes-a");
		expect(toggle.findByType("Icon" as never).props.contentDescription).toBe("Show other routes");
		act(() => click(toggle)!());
		expect(byTestID("environment-other-route-lan://192.168.1.24:3011")).toBeTruthy();
		expect(byTestID("environment-other-routes-a").findByType("Icon" as never).props.contentDescription).toBe("Hide other routes");
	});

	// Compose text can't be selected, so a route row copies its address, on a
	// tap or the long press a settings row answers.
	it("copies a route's address from its row", async () => {
		await render();
		const active = byTestID("environment-active-address-a");
		const press = modifiersOf(active).find((m) => m.$type === "combinedClickable")!.args[0] as { onClick: () => void; onLongClick: () => void };
		act(() => press.onClick());
		expect(fixtures.copy).toHaveBeenLastCalledWith("studio.tail123.ts.net:3011");
		act(() => click(byTestID("environment-other-routes-a"))!());
		const other = byTestID("environment-other-route-lan://192.168.1.24:3011");
		act(() => (modifiersOf(other).find((m) => m.$type === "combinedClickable")!.args[0] as { onLongClick: () => void }).onLongClick());
		expect(fixtures.copy).toHaveBeenLastCalledWith("192.168.1.24:3011");
		// Android 13 and later confirm the copy themselves.
		expect(renderer!.root.findByType("ToastHost" as never).props.toast.current).toBeUndefined();
	});

	it("confirms a copy itself before Android 13", async () => {
		fixtures.platform.Version = 32;
		await render();
		const active = byTestID("environment-active-address-a");
		act(() => (modifiersOf(active).find((m) => m.$type === "combinedClickable")!.args[0] as { onLongClick: () => void }).onLongClick());
		expect(renderer!.root.findByType("ToastHost" as never).props.toast.current.text).toBe("Address copied.");
	});

	it("draws Forget in the error colour and holds it while a forget runs", async () => {
		const handlers = await render();
		const forget = byTestID("environment-forget-a");
		expect(forget.props.colors.contentColor).toBe("red");
		act(() => click(forget)!());
		expect(handlers.onForget).toHaveBeenCalledOnce();
		act(() => renderer!.unmount());
		renderer = undefined;
		await render({ forgetting: true });
		const busy = byTestID("environment-forget-a");
		expect(click(busy)).toBeUndefined();
		expect(modifiersOf(busy)).toContainEqual({ $type: "alpha", args: [0.4] });
	});

	it("offers Make default only for another machine", async () => {
		await render({ isDefault: true });
		expect(queryTestID("environment-make-default-a")).toHaveLength(0);
		expect(renderer!.root.findAll((node) => node.props.children === "New workers and reconnects use this machine.").length).toBeGreaterThan(0);
	});
});
