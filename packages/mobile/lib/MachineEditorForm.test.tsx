import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Host } from "./hosts";
import type { MachineEditor } from "./useMachineEditor";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const { theme } = vi.hoisted(() => ({
	theme: { accent: "accent", bgBase: "base", bgElevated: "card", green: "green", highlight: "caret", red: "red", textPrimary: "ink", textSecondary: "gray", textTertiary: "dim" },
}));

vi.mock("react-native", () => ({ Platform: { OS: "ios" }, StyleSheet: { create: (styles: unknown) => styles }, Text: "Text", View: "View" }));
vi.mock("@expo/ui", () => ({ Host: "Host" }));
vi.mock("@expo/ui/swift-ui", async () => (await import("./nativeFormTestDoubles")).swiftUIDouble());
vi.mock("@expo/ui/swift-ui/modifiers", async () => {
	const doubles = await import("./nativeFormTestDoubles");
	return doubles.modifierDouble(doubles.swiftUIModifierNames);
});
vi.mock("./MachineSymbolMenu", () => ({ MachineSymbolMenu: "MachineSymbolMenu" }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => theme,
	useThemeState: () => ({ scheme: "dark", theme }),
	useThemedStyles: (factory: (value: typeof theme) => unknown) => factory(theme),
}));

import { MachineEditorForm } from "./MachineEditorForm";

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

const footers = () => renderer!.root.findAllByType("Section" as never).map((section) => section.props.footer);

// @expo/ui's Section is a different SwiftUI view with and without a footer, so
// adding or removing one rebuilds the section's fields and the focused field
// loses focus. A section with fields keeps its footer while errors come and go.
describe("the iOS machine editor", () => {
	it("keeps the connection footer mounted as the port error and a save failure come and go", () => {
		const states: Array<[string, Partial<MachineEditor>]> = [
			["no error", {}],
			["port error", { portError: "Enter a port from 1 to 65535." }],
			["no error again", {}],
			["save failed", { failure: { message: "That code or password was rejected. Get a new code and try again.", field: "password" } }],
			["failure cleared by an edit", {}],
		];
		act(() => { renderer = create(<MachineEditorForm host={host} editor={editor()} />); });
		for (const [state, patch] of states) {
			act(() => renderer!.update(<MachineEditorForm host={host} editor={editor(patch)} />));
			expect(footers()[1], state).toBeDefined();
		}
		expect(footers()[1].props.children).toBeUndefined();
	});

	it("keeps the name footer mounted for a machine that isn't account-managed too", () => {
		act(() => { renderer = create(<MachineEditorForm host={host} editor={editor()} />); });
		expect(footers()[0]).toBeDefined();
		expect(footers()[0].props.children).toBeUndefined();
	});

	it("keeps an account machine's name footer mounted as a save failure comes and goes", () => {
		const failure: MachineEditor["failure"] = { message: "Couldn't save the name. Try again.", field: "host" };
		act(() => { renderer = create(<MachineEditorForm host={host} editor={editor({ accountManaged: true })} />); });
		expect(footers()[0]).toBeDefined();
		act(() => renderer!.update(<MachineEditorForm host={host} editor={editor({ accountManaged: true, failure })} />));
		expect(footers()[0].props.children).toBe("Couldn't save the name. Try again.");
		act(() => renderer!.update(<MachineEditorForm host={host} editor={editor({ accountManaged: true })} />));
		expect(footers()[0]).toBeDefined();
	});
});
