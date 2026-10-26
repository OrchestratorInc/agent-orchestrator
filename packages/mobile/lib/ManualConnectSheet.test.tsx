import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Host } from "./hosts";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const fixtures = vi.hoisted(() => ({
	platform: { OS: "ios" },
	ping: vi.fn(async () => ({})),
	probeIdentity: vi.fn(async () => "host-new"),
	probeEndpoint: vi.fn(async () => ({ hostId: "host-1" })),
	saveConfig: vi.fn(async () => {}),
	saveHost: vi.fn(async () => {}),
	setActiveHost: vi.fn(async () => {}),
	renameHost: vi.fn(async () => {}),
	openSettings: vi.fn(async () => {}),
	haptics: { success: vi.fn(), warning: vi.fn(), tap: vi.fn(), select: vi.fn() },
	theme: { accent: "accent", bgBase: "base", bgElevated: "card", bgSurface: "surface", green: "green", highlight: "caret", red: "red", textPrimary: "ink", textSecondary: "gray", textTertiary: "dim" },
}));

vi.mock("react-native", () => ({
	Linking: { openSettings: fixtures.openSettings },
	Platform: fixtures.platform,
	ScrollView: "ScrollView",
	StyleSheet: { create: (styles: unknown) => styles },
	Text: "Text",
	View: "View",
}));
vi.mock("@expo/ui", () => ({ Host: "Host" }));
vi.mock("@expo/ui/swift-ui", async () => (await import("./nativeFormTestDoubles")).swiftUIDouble());
vi.mock("@expo/ui/swift-ui/modifiers", async () => {
	const doubles = await import("./nativeFormTestDoubles");
	return doubles.modifierDouble(doubles.swiftUIModifierNames);
});
vi.mock("@expo/ui/jetpack-compose", async () => (await import("./nativeFormTestDoubles")).composeDouble());
vi.mock("@expo/ui/jetpack-compose/modifiers", async () => (await import("./nativeFormTestDoubles")).composeModifierDouble());
vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Stack: { Toolbar: Object.assign(host("StackToolbar"), { Button: host("StackToolbarButton"), View: host("StackToolbarView") }) } };
});
vi.mock("./api", () => ({
	ApiError: class ApiError extends Error {
		constructor(readonly status: number) { super(`HTTP ${status}`); }
	},
	pingServer: fixtures.ping,
}));
vi.mock("./config", () => ({
	DEFAULT_CONFIG: { host: "", httpPort: "3011", muxPort: "14801", secure: false, password: "" },
	saveConfig: fixtures.saveConfig,
}));
vi.mock("./hosts", () => ({ saveHost: fixtures.saveHost, setActiveHost: fixtures.setActiveHost, renameHost: fixtures.renameHost }));
vi.mock("./connectRuntime", () => ({ probeEndpoint: fixtures.probeEndpoint, probeIdentity: fixtures.probeIdentity }));
vi.mock("./localNetworkStore", () => ({ setLocalNetworkConfirmed: vi.fn(async () => {}) }));
vi.mock("./telemetry/runtime", () => ({ mobileTelemetry: () => null }));
vi.mock("./haptics", () => ({ haptics: fixtures.haptics }));
vi.mock("./Spinner", () => ({ Spinner: "Spinner" }));
vi.mock("./ui", () => ({ Button: "RNButton", SHEET_SCROLL_CONTENT: {}, SheetHeader: "SheetHeader" }));
vi.mock("./formDrawables.android", () => ({ formDrawables: { visibility: "visibility.xml", visibilityOff: "visibility_off.xml", edit: "edit.xml", expandMore: "expand_more.xml" } }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemeState: () => ({ scheme: "dark", theme: fixtures.theme }),
	useThemedStyles: (factory: (theme: typeof fixtures.theme) => unknown) => factory(fixtures.theme),
}));

import { ApiError } from "./api";
import { ManualConnectSheet } from "./ManualConnectSheet";
import { PORT_ERROR } from "./useManualConnect";

const paired: Host = {
	id: "host-1",
	name: "Studio Mac",
	platform: "darwin",
	token: "secret",
	lastConnected: 1,
	endpoints: [{ kind: "lan", host: "192.168.1.24", port: 3011, secure: false }],
};

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	fixtures.platform.OS = "ios";
	for (const mock of [fixtures.ping, fixtures.probeIdentity, fixtures.probeEndpoint, fixtures.saveConfig, fixtures.saveHost, fixtures.setActiveHost, fixtures.renameHost]) mock.mockClear();
	fixtures.ping.mockImplementation(async () => ({}));
	fixtures.probeEndpoint.mockImplementation(async () => ({ hostId: "host-1" }));
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

type Modifier = { $type: string; args: unknown[] };
const modifiersOf = (node: ReactTestInstance) => (node.props.modifiers ?? []) as Modifier[];
const hasModifier = (node: ReactTestInstance, type: string, ...args: unknown[]) =>
	modifiersOf(node).some((modifier) => modifier.$type === type && args.every((arg, index) => modifier.args[index] === arg));

// SwiftUI views carry a testID prop; Compose ones a testID modifier.
function field(testID: string): ReactTestInstance {
	return renderer!.root.find((node) => typeof node.type === "string" && (node.props.testID === testID || hasModifier(node, "testID", testID)));
}

async function type(testID: string, text: string) {
	const node = field(testID);
	await act(async () => (node.props.onTextChange ?? node.props.onValueChange)(text));
}

function render(props: Partial<React.ComponentProps<typeof ManualConnectSheet>> = {}) {
	const onConnected = vi.fn();
	act(() => { renderer = create(<ManualConnectSheet onConnected={onConnected} {...props} />); });
	return { onConnected };
}

function confirm() {
	return renderer!.root.findByType("StackToolbarButton" as never);
}

describe("ManualConnectSheet on iOS", () => {
	it("lays the connection out as labelled rows of a native form", () => {
		render();
		const form = renderer!.root.findByType("Form" as never);
		expect(form.findAllByType("Section" as never)).toHaveLength(2);
		const labels = form.findAllByType("LabeledContent" as never).map((row) => row.props.label);
		expect(labels).toEqual(["Name", "Host", "API port", "Password"]);
		for (const [testID, label] of [["manual-connect-name", "Name"], ["manual-connect-host", "Host"], ["manual-connect-port", "API port"], ["manual-connect-password", "Password"]]) {
			expect(hasModifier(field(testID), "accessibilityLabel", label)).toBe(true);
		}
		expect(field("manual-connect-password").type).toBe("SecureField");
		expect(hasModifier(field("manual-connect-password"), "textContentType", "password")).toBe(true);
		expect(hasModifier(field("manual-connect-host"), "keyboardType", "url")).toBe(true);
		expect(hasModifier(field("manual-connect-port"), "keyboardType", "numeric")).toBe(true);
		const tls = field("manual-connect-tls");
		expect(tls.type).toBe("Toggle");
		expect(tls.props.label).toBe("Use TLS");
		expect(hasModifier(tls, "tint", "green")).toBe(true);
		// Nothing uppercase stands in for a label.
		expect(renderer!.root.findAll((node) => typeof node.props.children === "string" && /^[A-Z ]{4,}$/.test(node.props.children))).toHaveLength(0);
	});

	it("connects from the bar once the form is complete", async () => {
		const { onConnected } = render();
		expect(confirm().props.disabled).toBe(true);
		expect(confirm().props.children).toBe("Connect");
		await type("manual-connect-name", "Office PC");
		await type("manual-connect-host", "192.168.1.30");
		await type("manual-connect-password", "pw");
		expect(confirm().props.disabled).toBe(false);
		await act(async () => confirm().props.onPress());
		expect(fixtures.ping).toHaveBeenCalledWith(expect.objectContaining({ host: "192.168.1.30", httpPort: "3011", password: "pw" }));
		expect(fixtures.saveHost).toHaveBeenCalledWith(expect.objectContaining({ id: "host-new", name: "Office PC", token: "pw" }));
		expect(onConnected).toHaveBeenCalledOnce();
	});

	it("says what is wrong under the connection fields, in the error colour", async () => {
		fixtures.ping.mockRejectedValueOnce(new (ApiError as unknown as new (status: number) => Error)(401));
		render({ editingHost: paired });
		expect(confirm().props.children).toBe("Save");
		await type("manual-connect-password", "wrong");
		await act(async () => confirm().props.onPress());
		const connection = renderer!.root.findAllByType("Section" as never)[1];
		expect(connection.props.footer.props).toMatchObject({ error: true, children: "That password was rejected. Check it and try again." });
		expect(renderer!.root.findAll((node) => node.props.style?.backgroundColor === "red-tint")).toHaveLength(0);
		await type("manual-connect-password", "right");
		expect(renderer!.root.findAllByType("Section" as never)[1].props.footer).toBeUndefined();
	});

	it("explains an out-of-range port without enabling Connect", async () => {
		render({ editingHost: paired });
		await type("manual-connect-port", "70000");
		expect(renderer!.root.findAllByType("Section" as never)[1].props.footer.props.children).toBe(PORT_ERROR);
		expect(confirm().props.disabled).toBe(true);
	});

	it("reveals the password in place and hides it again", () => {
		render({ editingHost: paired });
		const reveal = renderer!.root.findByProps({ label: "Show password" });
		act(() => reveal.props.onPress());
		expect(field("manual-connect-password").type).toBe("TextField");
		act(() => renderer!.root.findByProps({ label: "Hide password" }).props.onPress());
		expect(field("manual-connect-password").type).toBe("SecureField");
	});

	it("offers Local Network settings when a LAN address does not answer", async () => {
		fixtures.ping.mockRejectedValueOnce(new Error("unreachable"));
		render();
		await type("manual-connect-name", "Office PC");
		await type("manual-connect-host", "192.168.1.30");
		await act(async () => confirm().props.onPress());
		const open = renderer!.root.findByProps({ label: "Open settings" });
		act(() => open.props.onPress());
		expect(fixtures.openSettings).toHaveBeenCalledOnce();
	});
});

describe("ManualConnectSheet on Android", () => {
	beforeEach(() => { fixtures.platform.OS = "android"; });

	async function renderAndroid(props: Partial<React.ComponentProps<typeof ManualConnectSheet>> = {}) {
		vi.resetModules();
		vi.doMock("./formFields", async () => await import("./formFields.android"));
		const { ManualConnectSheet: AndroidSheet } = await import("./ManualConnectSheet.android");
		const onConnected = vi.fn();
		act(() => { renderer = create(<AndroidSheet onConnected={onConnected} {...props} />); });
		return { onConnected };
	}

	it("keeps the title and Connect in the sheet, with outlined labelled fields", async () => {
		await renderAndroid();
		expect(renderer!.root.findByType("SheetHeader" as never).props.title).toBe("Connect manually");
		const fields = renderer!.root.findAllByType("OutlinedTextField" as never);
		expect(fields.map((node) => node.findByType("OutlinedTextField.Label" as never).findByType("Text" as never).props.children)).toEqual(["Name", "Host", "API port", "Password"]);
		const password = field("manual-connect-password");
		expect(password.props.visualTransformation).toBe("password");
		expect(modifiersOf(password).some((modifier) => modifier.$type === "semantics" && (modifier.args[0] as { contentType: string }).contentType === "password")).toBe(true);
		expect(password.findByType("Icon" as never).props.contentDescription).toBe("Show password");
		expect(renderer!.root.findByType("Switch" as never)).toBeTruthy();
		expect(renderer!.root.findByType("RNButton" as never).props).toMatchObject({ title: "Connect", disabled: true });
	});

	it("shows a rejected password as the password field's own error", async () => {
		await renderAndroid({ editingHost: paired });
		// resetModules gave the Android sheet its own copy of the api module.
		const api = await import("./api");
		fixtures.ping.mockRejectedValueOnce(new (api.ApiError as unknown as new (status: number) => Error)(401));
		await type("manual-connect-password", "wrong");
		await act(async () => renderer!.root.findByType("RNButton" as never).props.onPress());
		const password = field("manual-connect-password");
		expect(password.props.isError).toBe(true);
		expect(password.findByType("OutlinedTextField.SupportingText" as never).findByType("Text" as never).props.children).toBe("That password was rejected. Check it and try again.");
		expect(field("manual-connect-host").props.isError).toBe(false);
	});
});
