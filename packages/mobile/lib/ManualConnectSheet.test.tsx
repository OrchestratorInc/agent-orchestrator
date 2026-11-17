import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Host } from "./hosts";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const fixtures = vi.hoisted(() => ({
	platform: { OS: "ios" },
	ping: vi.fn(async () => ({})),
	probeIdentity: vi.fn(async () => "host-new"),
	probeEndpoint: vi.fn(async () => ({ hostId: "host-1" })),
	pair: vi.fn(async (): Promise<import("./deviceToken").PairDeviceResult> => ({ kind: "unsupported" })),
	saveConfig: vi.fn(async () => {}),
	saveHost: vi.fn(async () => {}),
	setActiveHost: vi.fn(async () => {}),
	renameHost: vi.fn(async () => {}),
	openSettings: vi.fn(async () => {}),
	announce: vi.fn(),
	haptics: { success: vi.fn(), warning: vi.fn(), tap: vi.fn(), select: vi.fn() },
	theme: { accent: "accent", bgBase: "base", bgElevated: "card", bgSurface: "surface", green: "green", highlight: "caret", red: "red", textPrimary: "ink", textSecondary: "gray", textTertiary: "dim" },
}));

vi.mock("react-native", () => ({
	AccessibilityInfo: { announceForAccessibilityWithOptions: fixtures.announce },
	Linking: { openSettings: fixtures.openSettings },
	Platform: fixtures.platform,
	ScrollView: "ScrollView",
	StyleSheet: { create: (styles: unknown) => styles },
	Text: "Text",
	View: "View",
}));
vi.mock("react-native-keyboard-controller", () => ({ KeyboardAvoidingView: "KeyboardAvoidingView" }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, right: 0, bottom: 24, left: 0 }) }));
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
vi.mock("./deviceTokenRuntime", () => ({ pairThisDevice: fixtures.pair }));
vi.mock("./localNetworkStore", () => ({ setLocalNetworkConfirmed: vi.fn(async () => {}) }));
vi.mock("./telemetry/runtime", () => ({ mobileTelemetry: () => null }));
vi.mock("./haptics", () => ({ haptics: fixtures.haptics }));
vi.mock("./Spinner", () => ({ Spinner: "Spinner" }));
vi.mock("./ui", () => ({ Button: "RNButton", SHEET_SCROLL_CONTENT: {}, SheetHeader: "SheetHeader" }));
vi.mock("./toolbarIcon.android", () => ({ materialDrawable: (name: string) => `${name}.xml` }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemeState: () => ({ scheme: "dark", theme: fixtures.theme }),
	useThemedStyles: (factory: (theme: typeof fixtures.theme) => unknown) => factory(fixtures.theme),
}));

import { ApiError } from "./api";
import { ManualConnectSheet } from "./ManualConnectSheet";

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
	for (const mock of [fixtures.ping, fixtures.probeIdentity, fixtures.probeEndpoint, fixtures.saveConfig, fixtures.saveHost, fixtures.setActiveHost, fixtures.renameHost, fixtures.announce]) mock.mockClear();
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

/** The Connection section's footer as it renders: whether it draws anything, and its text's modifiers. */
function connectionFooter(): { drawn: boolean; modifiers: Modifier[] } {
	let footer: ReactTestRenderer | undefined;
	act(() => { footer = create(renderer!.root.findAllByType("Section" as never)[1].props.footer); });
	const text = footer!.root.findAllByType("Text" as never)[0];
	const result = { drawn: footer!.toJSON() !== null, modifiers: text ? (text.props.modifiers as Modifier[]) : [] };
	act(() => footer!.unmount());
	return result;
}

describe("ManualConnectSheet on iOS", () => {
	it("labels every field for VoiceOver and hides the password", () => {
		render();
		for (const [testID, label] of [["manual-connect-name", "Name"], ["manual-connect-host", "Host"], ["manual-connect-port", "Port"], ["manual-connect-password", "Password"], ["manual-connect-tls", "Use TLS"]]) {
			expect(hasModifier(field(testID), "accessibilityLabel", label)).toBe(true);
		}
		expect(field("manual-connect-password").type).toBe("SecureField");
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

	it.each([
		["a one-time code", "pairing-code" as const, undefined],
		["the pairing password", "password" as const, "typed"],
	])("swaps %s for this phone's own token before saving", async (_name, credential, fallbackToken) => {
		const token = "aod_" + "a".repeat(64);
		fixtures.pair.mockResolvedValueOnce({ kind: "paired", token, credential });
		render();
		await type("manual-connect-name", "Office PC");
		await type("manual-connect-host", "192.168.1.30");
		await type("manual-connect-password", "typed");
		await act(async () => confirm().props.onPress());
		expect(fixtures.pair).toHaveBeenCalledWith(expect.objectContaining({ host: "192.168.1.30", password: "typed", hostId: "host-new" }), { fresh: true });
		expect(fixtures.ping).toHaveBeenCalledWith(expect.objectContaining({ password: token }));
		expect(fixtures.saveConfig).toHaveBeenCalledWith(expect.objectContaining({ password: token }));
		const saved = (fixtures.saveHost.mock.calls as unknown as [Host][]).at(-1)![0];
		expect(saved.token).toBe(token);
		expect(saved.fallbackToken).toBe(fallbackToken);
	});

	// The route returns from onConnected early when its sheet has lost focus.
	// The form still lets go once the connect settles, or the sheet would come
	// back locked against Cancel and the swipe.
	it("lets go of the sheet once a connect settles, even one whose sheet was left", async () => {
		let answer!: (value: object) => void;
		fixtures.ping.mockImplementationOnce(() => new Promise((resolve) => { answer = resolve; }));
		const onBusyChange = vi.fn();
		const { onConnected } = render({ onBusyChange });
		await type("manual-connect-name", "Office PC");
		await type("manual-connect-host", "192.168.1.30");
		await type("manual-connect-password", "pw");
		await act(async () => { void confirm().props.onPress(); });
		expect(onBusyChange).toHaveBeenLastCalledWith(true);
		await act(async () => answer({}));
		expect(onConnected).toHaveBeenCalledOnce();
		expect(onBusyChange).toHaveBeenLastCalledWith(false);
	});

	it("says what is wrong under the connection fields, in the error colour", async () => {
		fixtures.ping.mockRejectedValueOnce(new (ApiError as unknown as new (status: number) => Error)(401));
		render({ editingHost: paired });
		expect(confirm().props.children).toBe("Save");
		await type("manual-connect-password", "wrong");
		await act(async () => confirm().props.onPress());
		const connection = renderer!.root.findAllByType("Section" as never)[1];
		expect(connection.props.footer.props).toMatchObject({ error: true, children: "That code or password was rejected. Get a new code and try again." });
		// A footer isn't read out as it appears, so VoiceOver is told.
		expect(fixtures.announce).toHaveBeenCalledWith("That code or password was rejected. Get a new code and try again.", { queue: true });
		await type("manual-connect-password", "right");
		// Cleared but still mounted (see the footer test below).
		expect(renderer!.root.findAllByType("Section" as never)[1].props.footer.props).toMatchObject({ error: false, children: undefined });
	});

	it("enables Connect once the form has a name and a host, saying what it waits for", async () => {
		render();
		const hint = () => renderer!.root.findAllByType("Section" as never)[1].props.footer?.props;
		expect(confirm().props.disabled).toBe(true);
		expect(hint()).toMatchObject({ children: "Add a name and a host to connect." });
		expect(hint().error).toBeFalsy();
		await type("manual-connect-host", "192.168.1.30");
		expect(confirm().props.disabled).toBe(true);
		expect(hint()).toMatchObject({ children: "Add a name to connect." });
		await type("manual-connect-name", "Office PC");
		expect(confirm().props.disabled).toBe(false);
		expect(hint().children).toBeUndefined();
	});

	// FormSection keeps the footer slot mounted (formSection.source.test.ts guards
	// that no Section's footer comes and goes). What the footer draws must not
	// change shape either: whenever it shows text, the same single modifier.
	it("draws the connection footer with the same modifiers while the hint, the port error and a failure come and go", async () => {
		fixtures.ping.mockRejectedValueOnce(new (ApiError as unknown as new (status: number) => Error)(401));
		render();
		const steps: Array<[string, () => Promise<void>]> = [
			["name", () => type("manual-connect-name", "Office PC")],
			["first host character", () => type("manual-connect-host", "1")],
			["host cleared", () => type("manual-connect-host", "")],
			["host", () => type("manual-connect-host", "192.168.1.30")],
			["port invalid", () => type("manual-connect-port", "0")],
			["port valid", () => type("manual-connect-port", "3011")],
			["password", () => type("manual-connect-password", "wrong")],
			["failed connect", () => act(async () => confirm().props.onPress())],
			["password fixed", () => type("manual-connect-password", "right")],
		];
		const kinds = () => connectionFooter().modifiers.map((modifier) => modifier.$type);
		expect(kinds()).toEqual(["foregroundStyle"]);
		for (const [step, run] of steps) {
			await run();
			if (connectionFooter().drawn) expect(kinds(), step).toEqual(["foregroundStyle"]);
		}
	});

	// A hint and an error draw with the same modifier, only its style changes;
	// with nothing to say the footer draws nothing (no empty row, nothing for VoiceOver).
	it("styles the hint and an error through one modifier and draws nothing when empty", async () => {
		render();
		const styles = () => connectionFooter().modifiers.filter((modifier) => modifier.$type === "foregroundStyle").map((modifier) => modifier.args[0]);
		expect(styles()).toEqual([{ type: "hierarchical", style: "secondary" }]);
		await type("manual-connect-port", "0");
		expect(styles()).toEqual(["red"]);
		await type("manual-connect-port", "3011");
		await type("manual-connect-name", "Office PC");
		await type("manual-connect-host", "192.168.1.30");
		expect(connectionFooter().drawn).toBe(false);
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

	it("shows a rejected password as the password field's own error", async () => {
		await renderAndroid({ editingHost: paired });
		// resetModules gave the Android sheet its own copy of the api module.
		const api = await import("./api");
		fixtures.ping.mockRejectedValueOnce(new (api.ApiError as unknown as new (status: number) => Error)(401));
		await type("manual-connect-password", "wrong");
		await act(async () => renderer!.root.findByType("RNButton" as never).props.onPress());
		const password = field("manual-connect-password");
		expect(password.props.isError).toBe(true);
		expect(password.findByType("OutlinedTextField.SupportingText" as never).findByType("Text" as never).props.children).toBe("That code or password was rejected. Get a new code and try again.");
		expect(field("manual-connect-host").props.isError).toBe(false);
		// Supporting text is read only with its field focused, so TalkBack is told.
		expect(fixtures.announce).toHaveBeenCalledWith("That code or password was rejected. Get a new code and try again.", { queue: true });
	});

	// Connect follows the last field.
	it("puts Connect right after the fields", async () => {
		await renderAndroid();
		const connect = renderer!.root.findByType("RNButton" as never);
		expect(connect.props.title).toBe("Connect");
		expect(connect.props.disabled).toBe(true);
		const page = renderer!.root.findByType("KeyboardAvoidingView" as never);
		expect(page.props.behavior).toBe("padding");
		const scroll = renderer!.root.findByType("ScrollView" as never);
		expect(scroll.findAll((node) => node === connect)).toHaveLength(1);
		// The last field clears the gesture bar when scrolled to the end.
		expect(Object.assign({}, ...[scroll.props.contentContainerStyle].flat()).paddingBottom).toBeGreaterThanOrEqual(24);
	});

	// A disabled Connect alone does not say a name is required.
	it("says under Connect what it waits for", async () => {
		await renderAndroid();
		const hint = () => renderer!.root.findAll((node) => node.type === ("Text" as never) && typeof node.props.children === "string" && node.props.children.startsWith("Add ")).map((node) => node.props.children);
		expect(hint()).toEqual(["Add a name and a host to connect."]);
		await type("manual-connect-name", "Office PC");
		expect(hint()).toEqual(["Add a host to connect."]);
		await type("manual-connect-host", "192.168.1.30");
		expect(hint()).toEqual([]);
	});

	// No back button on this page: the way back names where it goes.
	it("goes back to the scanner from Connect with QR code", async () => {
		const onScanInstead = vi.fn();
		await renderAndroid({ onScanInstead });
		const scan = renderer!.root.findAllByType("RNButton" as never).find((button) => button.props.title === "Connect with QR code")!;
		act(() => scan.props.onPress());
		expect(onScanInstead).toHaveBeenCalledOnce();
	});

	it("connects from the password's Go key once the form is complete", async () => {
		await renderAndroid();
		const go = () => field("manual-connect-password").props.keyboardActions.onGo();
		expect(field("manual-connect-password").props.keyboardOptions.imeAction).toBe("go");
		await act(async () => go());
		expect(fixtures.ping).not.toHaveBeenCalled();
		await type("manual-connect-name", "Office PC");
		await type("manual-connect-host", "192.168.1.30");
		await act(async () => go());
		expect(fixtures.ping).toHaveBeenCalledOnce();
	});
});
