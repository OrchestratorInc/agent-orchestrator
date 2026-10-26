import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "./config";
import type { Host } from "./hosts";

const fixtures = vi.hoisted(() => ({
	platform: { OS: "ios" },
	findHost: vi.fn<(id: string) => Promise<Host | null>>(async () => null),
	activeHost: vi.fn<() => Promise<Host | null>>(async () => null),
	save: vi.fn(async () => ({ routeChanged: false })),
	reloadConfig: vi.fn(async () => {}),
	reloadHostMetadata: vi.fn(async () => {}),
	alert: vi.fn(),
	back: vi.fn(),
	setSheetOptions: vi.fn(),
	dispatch: vi.fn(),
	announce: vi.fn(),
	preventRemove: { prevent: false, callback: undefined as undefined | ((event: { data: { action: unknown } }) => void) },
	hostStates: [] as { hostId: string; connection: "closed" | "connecting" | "open"; loading: boolean; config: ServerConfig | null }[],
	haptics: { success: vi.fn(), warning: vi.fn(), select: vi.fn() },
	theme: { accent: "accent", bgBase: "base", bgElevated: "card", green: "green", highlight: "caret", red: "red", textPrimary: "ink", textSecondary: "gray", textTertiary: "dim" },
}));

vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	const router = { back: fixtures.back, canGoBack: () => true, replace: vi.fn() };
	const navigation = { getParent: () => ({ setOptions: fixtures.setSheetOptions }), dispatch: fixtures.dispatch };
	return {
		Stack: { Toolbar: Object.assign(host("StackToolbar"), { Button: host("StackToolbarButton"), View: host("StackToolbarView") }) },
		useLocalSearchParams: () => ({ id: "a" }),
		useNavigation: () => navigation,
		usePreventRemove: (prevent: boolean, callback: (event: { data: { action: unknown } }) => void) => {
			fixtures.preventRemove.prevent = prevent;
			fixtures.preventRemove.callback = callback;
		},
		useRouter: () => router,
	};
});
vi.mock("react-native", () => ({
	AccessibilityInfo: { announceForAccessibilityWithOptions: fixtures.announce },
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
vi.mock("./environmentActions", () => ({ saveEnvironmentMachine: fixtures.save }));
vi.mock("./hosts", () => ({ activeHost: fixtures.activeHost, findHost: fixtures.findHost }));
vi.mock("./store", () => ({
	useApp: () => ({
		hostStates: fixtures.hostStates,
		configForHost: () => null,
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

import MachineEditScreen from "../app/settings/machine/[id]/edit";
import { MachineSaveError } from "./environmentEditor";

const a: Host = {
	id: "a",
	name: "Studio Mac",
	platform: "darwin",
	token: "secret",
	lastConnected: 2,
	appearance: { symbol: "laptop" },
	endpoints: [
		{ kind: "lan", host: "192.168.1.24", port: 3011, secure: false },
		{ kind: "tailscale", host: "studio.tail123.ts.net", port: 3011, secure: true },
	],
};
const onTailscale: ServerConfig = { host: "studio.tail123.ts.net", httpPort: "3011", muxPort: "3012", secure: true, password: "secret", hostId: "a", endpointKind: "tailscale" };

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.platform.OS = "ios";
	for (const mock of [fixtures.findHost, fixtures.activeHost, fixtures.save, fixtures.reloadConfig, fixtures.reloadHostMetadata, fixtures.alert, fixtures.back, fixtures.setSheetOptions, fixtures.dispatch, fixtures.announce]) mock.mockReset();
	fixtures.findHost.mockResolvedValue(a);
	fixtures.activeHost.mockResolvedValue(a);
	fixtures.save.mockResolvedValue({ routeChanged: false });
	fixtures.reloadHostMetadata.mockResolvedValue(undefined);
	fixtures.reloadConfig.mockResolvedValue(undefined);
	fixtures.hostStates = [{ hostId: "a", connection: "open", loading: false, config: onTailscale }];
	fixtures.preventRemove.prevent = false;
	fixtures.preventRemove.callback = undefined;
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
	await act(async () => { renderer = create(<MachineEditScreen />); });
}

type Modifier = { $type: string; args: unknown[] };
const modifiersOf = (node: ReactTestInstance) => (node.props.modifiers ?? []) as Modifier[];
const field = (testID: string) => renderer!.root.find((node) => typeof node.type === "string" && node.props.testID === testID);
async function type(testID: string, text: string) {
	const node = field(testID);
	await act(async () => node.props.onTextChange(text));
}
const save = () => renderer!.root.findByType("StackToolbarButton" as never);
const connectionSection = () => renderer!.root.findAllByType("Section" as never)[1];

describe("editing a machine", () => {
	it("starts from the machine as saved, on the route the phone is connected over", async () => {
		await open();
		expect(field("environment-name-a").props.text.value).toBe("Studio Mac");
		expect(field("environment-host-a").props.text.value).toBe("studio.tail123.ts.net");
		expect(field("environment-port-a").props.text.value).toBe("3011");
		expect(field("environment-password-a").type).toBe("SecureField");
		expect(field("environment-password-a").props.text.value).toBe("secret");
		expect(field("environment-tls-a").props).toMatchObject({ label: "Use TLS", isOn: true });
		expect(renderer!.root.findByType("Picker" as never).props.selection).toBe("laptop");
		for (const [testID, label] of [["environment-name-a", "Name"], ["environment-host-a", "Host"], ["environment-port-a", "API port"], ["environment-password-a", "Password"]]) {
			expect(modifiersOf(field(testID))).toContainEqual({ $type: "accessibilityLabel", args: [label] });
		}
		expect(modifiersOf(field("environment-password-a"))).toContainEqual({ $type: "textContentType", args: ["password"] });
	});

	it("saves from the bar once something changed, then goes back", async () => {
		await open();
		expect(save().props).toMatchObject({ children: "Save", accessibilityLabel: "Save machine changes", disabled: true });
		await type("environment-name-a", "Travel Mac");
		await type("environment-host-a", "travel.tail123.ts.net");
		act(() => renderer!.root.findByType("Picker" as never).props.onSelectionChange("cloud"));
		expect(save().props.disabled).toBe(false);
		await act(async () => save().props.onPress());
		expect(fixtures.save).toHaveBeenCalledWith(a, expect.objectContaining({
			machineName: "Travel Mac",
			appearance: { symbol: "cloud" },
			endpointIndex: 1,
			config: expect.objectContaining({ host: "travel.tail123.ts.net", httpPort: "3011", password: "secret", secure: true }),
		}), "ios");
		expect(fixtures.reloadHostMetadata).toHaveBeenCalledOnce();
		expect(fixtures.haptics.success).toHaveBeenCalledOnce();
		expect(fixtures.back).toHaveBeenCalledOnce();
		expect(fixtures.preventRemove.prevent).toBe(false);
	});

	it("reconnects the default machine over a changed route", async () => {
		fixtures.save.mockResolvedValue({ routeChanged: true });
		await open();
		await type("environment-port-a", "3012");
		await act(async () => save().props.onPress());
		expect(fixtures.reloadConfig).toHaveBeenCalledOnce();
	});

	it("keeps the editor and says what failed under the connection fields", async () => {
		fixtures.save.mockRejectedValue(new MachineSaveError("That password was rejected. Check it and try again.", "password"));
		await open();
		await type("environment-password-a", "wrong");
		await act(async () => save().props.onPress());
		expect(fixtures.back).not.toHaveBeenCalled();
		expect(fixtures.haptics.warning).toHaveBeenCalledOnce();
		expect(connectionSection().props.footer.props).toMatchObject({ error: true, children: "That password was rejected. Check it and try again." });
		// A footer isn't read out as it appears, so the screen reader is told.
		expect(fixtures.announce).toHaveBeenCalledWith("That password was rejected. Check it and try again.", { queue: true });
		await type("environment-password-a", "right");
		expect(connectionSection().props.footer).toBeUndefined();
	});

	it("explains an out-of-range port and keeps Save off", async () => {
		await open();
		await type("environment-port-a", "70000");
		expect(connectionSection().props.footer.props.children).toBe("Enter a port from 1 to 65535.");
		expect(save().props.disabled).toBe(true);
	});

	it("shows a spinner in Save's place while the save runs", async () => {
		let finish!: () => void;
		fixtures.save.mockReturnValue(new Promise((resolve) => { finish = () => resolve({ routeChanged: false }); }));
		await open();
		await type("environment-name-a", "Travel Mac");
		await act(async () => { void save().props.onPress(); });
		expect(renderer!.root.findByType("StackToolbarView" as never).findByType("Spinner" as never).props.accessibilityLabel).toBe("Saving changes");
		expect(field("environment-name-a").props.modifiers).toContainEqual({ $type: "disabled", args: [true] });
		await act(async () => finish());
	});
});

describe("leaving with unsaved changes", () => {
	// A swipe on the Settings sheet or a back would throw the edits away, so
	// the page holds the sheet and asks first, only while there is something
	// to lose.
	it("holds the Settings sheet only while there are unsaved changes", async () => {
		await open();
		expect(fixtures.setSheetOptions).not.toHaveBeenCalled();
		expect(fixtures.preventRemove.prevent).toBe(false);
		await type("environment-name-a", "Travel Mac");
		expect(fixtures.setSheetOptions).toHaveBeenLastCalledWith({ gestureEnabled: false });
		expect(fixtures.preventRemove.prevent).toBe(true);
		await type("environment-name-a", "Studio Mac");
		expect(fixtures.setSheetOptions).toHaveBeenLastCalledWith({ gestureEnabled: true });
		expect(fixtures.preventRemove.prevent).toBe(false);
	});

	it("asks before discarding, and leaves only on Discard", async () => {
		await open();
		await type("environment-name-a", "Travel Mac");
		const action = { type: "GO_BACK" };
		act(() => fixtures.preventRemove.callback?.({ data: { action } }));
		const [title, , buttons] = fixtures.alert.mock.calls.at(-1)!;
		expect(title).toBe("Discard changes?");
		buttons.find((button: { text: string }) => button.text === "Keep editing").onPress?.();
		expect(fixtures.dispatch).not.toHaveBeenCalled();
		buttons.find((button: { text: string; style: string }) => button.text === "Discard" && button.style === "destructive").onPress();
		expect(fixtures.dispatch).toHaveBeenCalledWith(action);
	});

	it("lets the sheet swipe away again when the page closes mid-edit", async () => {
		await open();
		await type("environment-name-a", "Travel Mac");
		await act(async () => renderer?.unmount());
		renderer = undefined;
		expect(fixtures.setSheetOptions).toHaveBeenLastCalledWith({ gestureEnabled: true });
	});
});

describe("an account machine", () => {
	it("edits only the name and icon, and says where the route comes from", async () => {
		fixtures.findHost.mockResolvedValue({ ...a, accountUserId: "user-a" });
		await open();
		expect(renderer!.root.findAll((node) => node.props.testID === "environment-host-a")).toHaveLength(0);
		expect(renderer!.root.findAll((node) => node.props.testID === "environment-password-a")).toHaveLength(0);
		expect(field("environment-account-managed-a").props.label).toBe("Address");
		expect(connectionSection().props.footer.props.children).toMatch(/Orchestrator Cloud account/);
		await type("environment-name-a", "Studio");
		await act(async () => save().props.onPress());
		expect(fixtures.save).toHaveBeenCalledWith(expect.objectContaining({ accountUserId: "user-a" }), expect.objectContaining({
			machineName: "Studio",
			config: expect.objectContaining({ host: "studio.tail123.ts.net", password: "secret" }),
		}), "ios");
	});
});

describe("saving on Android", () => {
	it("puts a check in the top app bar, whose items draw icons only", async () => {
		fixtures.platform.OS = "android";
		await open();
		expect(save().props).toMatchObject({ icon: "checkmark", accessibilityLabel: "Save machine changes", disabled: true });
		expect(save().props.children).toBeUndefined();
	});
});
