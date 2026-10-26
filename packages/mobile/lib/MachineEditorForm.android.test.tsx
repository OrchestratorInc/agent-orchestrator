import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Host } from "./hosts";
import type { MachineEditor } from "./useMachineEditor";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const { theme } = vi.hoisted(() => ({
	theme: { accent: "accent", accentTint: "tint", bgBase: "base", red: "red", textPrimary: "ink", textSecondary: "gray", textTertiary: "dim", onAccent: "#000000", bgElevatedHover: "hover" },
}));

vi.mock("react-native", () => ({
	Platform: { OS: "android" },
	Pressable: "Pressable",
	ScrollView: "ScrollView",
	StyleSheet: { create: (styles: unknown) => styles },
	Text: "Text",
	View: "View",
}));
vi.mock("react-native-keyboard-controller", () => ({ KeyboardAvoidingView: "KeyboardAvoidingView" }));
vi.mock("@expo/ui/jetpack-compose", async () => (await import("./nativeFormTestDoubles")).composeDouble());
vi.mock("@expo/ui/jetpack-compose/modifiers", async () => (await import("./nativeFormTestDoubles")).composeModifierDouble());
vi.mock("./formDrawables.android", () => ({ formDrawables: { visibility: "visibility.xml", visibilityOff: "visibility_off.xml" } }));
vi.mock("./formFields", async () => await import("./formFields.android"));
vi.mock("./haptics", () => ({ haptics: { select: vi.fn() } }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => theme,
	useThemeState: () => ({ scheme: "dark", theme }),
	useThemedStyles: (factory: (value: typeof theme) => unknown) => factory(theme),
}));

import { MachineEditorForm } from "./MachineEditorForm.android";

const host: Host = {
	id: "a",
	name: "Studio Mac",
	platform: "darwin",
	token: "secret",
	lastConnected: 1,
	endpoints: [{ kind: "lan", host: "192.168.1.24", port: 3011, secure: false }],
};

function editor(patch: Partial<MachineEditor> = {}): MachineEditor {
	return {
		draft: { machineName: "Studio Mac", appearance: { symbol: "laptop" }, endpointIndex: 0, config: { host: "192.168.1.24", httpPort: "3011", muxPort: "14801", secure: false, password: "secret", hostId: "a" } },
		dirty: false,
		canSave: false,
		saving: false,
		failure: null,
		portError: undefined,
		accountManaged: false,
		setName: vi.fn(),
		setSymbol: vi.fn(),
		setHost: vi.fn(),
		setPort: vi.fn(),
		setPassword: vi.fn(),
		setSecure: vi.fn(),
		save: vi.fn(async () => true),
		...patch,
	};
}

let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

type Modifier = { $type: string; args: unknown[] };
const byTag = (testID: string) => renderer!.root.find((node) => typeof node.type === "string" && ((node.props.modifiers ?? []) as Modifier[]).some((m) => m.$type === "testID" && m.args[0] === testID));
const supporting = (node: ReactTestInstance) => node.findAllByType("OutlinedTextField.SupportingText" as never).map((slot) => slot.findByType("Text" as never).props.children);

describe("the Android machine editor", () => {
	it("uses outlined Material fields, the shape grid and a Material switch, in sentence case", () => {
		act(() => { renderer = create(<MachineEditorForm host={host} editor={editor()} />); });
		const labels = renderer!.root.findAllByType("OutlinedTextField.Label" as never).map((slot) => slot.findByType("Text" as never).props.children);
		expect(labels).toEqual(["Name", "Host", "API port", "Password"]);
		expect(renderer!.root.findByProps({ testID: "environment-symbol-a-laptop" }).props.accessibilityState.selected).toBe(true);
		expect(renderer!.root.findByType("Switch" as never).props.value).toBe(false);
		const headings = renderer!.root.findAll((node) => (node.type as unknown) === "Text" && node.props.accessibilityRole === "header").map((node) => node.props.children);
		expect(headings).toEqual(["Icon", "Connection"]);
	});

	it("puts each error on the field it is about", () => {
		act(() => {
			renderer = create(<MachineEditorForm host={host} editor={editor({ failure: { message: "That password was rejected. Check it and try again.", field: "password" }, portError: "Enter a port from 1 to 65535." })} />);
		});
		expect(byTag("environment-password-a").props.isError).toBe(true);
		expect(supporting(byTag("environment-password-a"))).toEqual(["That password was rejected. Check it and try again."]);
		expect(supporting(byTag("environment-port-a"))).toEqual(["Enter a port from 1 to 65535."]);
		expect(byTag("environment-host-a").props.isError).toBe(false);
	});

	// The keyboard controller stops the window resizing for the IME, so without
	// this the port, password and TLS rows sit under the keyboard, unscrollable.
	it("pads itself above the keyboard, so every field can scroll into sight", () => {
		act(() => { renderer = create(<MachineEditorForm host={host} editor={editor()} />); });
		const avoider = renderer!.root.findByType("KeyboardAvoidingView" as never);
		expect(avoider.props).toMatchObject({ behavior: "padding", automaticOffset: true });
		expect(avoider.findByType("ScrollView" as never).props.keyboardShouldPersistTaps).toBe("handled");
	});

	it("leaves an account machine's route to the account", () => {
		act(() => { renderer = create(<MachineEditorForm host={host} editor={editor({ accountManaged: true })} />); });
		expect(renderer!.root.findAllByType("OutlinedTextField" as never)).toHaveLength(1);
		expect(renderer!.root.findByProps({ testID: "environment-account-managed-a" }).props.children).toMatch(/Orchestrator Cloud account/);
	});
});
