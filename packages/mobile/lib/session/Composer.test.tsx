import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }),
	haptics: { tap: vi.fn(), select: vi.fn() },
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Pressable: host("Pressable"), StyleSheet: { create: (styles: object) => styles }, TextInput: host("TextInput"), View: host("View") };
});
vi.mock("../ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemeState: () => ({ scheme: "dark" }),
	useThemedStyles: (factory: (t: unknown) => unknown) => factory(fixtures.theme),
}));
vi.mock("../haptics", () => ({ haptics: fixtures.haptics }));
vi.mock("../textSelection", () => ({ textSelectionColors: () => ({}) }));
vi.mock("../pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: undefined, pressed: { opacity: 0.7 } }) }));
vi.mock("../chat/composer-glass", async () => {
	const React = await import("react");
	return { composerGlassSupported: false, ComposerGlass: (props: object) => React.createElement("ComposerGlass", props) };
});
vi.mock("../chat/composerStyles", () => ({ COMPOSER_RADIUS: 28 }));
vi.mock("../voice/MicKey", async () => {
	const React = await import("react");
	return { MicKey: (props: object) => React.createElement("MicKey", props) };
});
vi.mock("../voice/DictationField", async () => {
	const React = await import("react");
	return { DictationField: (props: object) => React.createElement("DictationField", props) };
});
vi.mock("./RouteToggle", async () => {
	const React = await import("react");
	return { RouteToggle: (props: object) => React.createElement("RouteToggle", props) };
});

import { Composer } from "./Composer";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.haptics.tap.mockReset();
	fixtures.haptics.select.mockReset();
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

const voice = { state: "idle" as const, partial: "", toggleLatched: vi.fn(), subscribeLevel: vi.fn(() => () => {}) };

async function render(props: Partial<Parameters<typeof Composer>[0]> = {}) {
	const all = {
		value: "",
		onChangeText: vi.fn(),
		onSend: vi.fn(),
		sending: false,
		target: "agent" as const,
		onTargetChange: vi.fn(),
		voice,
		keyboardVisible: false,
		onDismissKeyboard: vi.fn(),
		...props,
	};
	await act(async () => { renderer = create(<Composer {...all} />); });
	return all;
}

describe("the terminal composer", () => {
	it("is the chat composer's capsule, with its glass behind it", async () => {
		await render();
		const pill = renderer!.root.findAll((node) => node.type === ("View" as never) && node.props.style?.borderRadius === 28);
		expect(pill).toHaveLength(1);
		expect(pill[0].props.style).toMatchObject({ backgroundColor: "#bgElevated", borderCurve: "continuous" });
		expect(pill[0].findByType("ComposerGlass" as never).props.radius).toBe(28);
	});

	it("names its field for screen readers by the route it sends on", async () => {
		await render();
		expect(renderer!.root.findByType("TextInput" as never).props.accessibilityLabel).toBe("Message the agent");
		await render({ target: "terminal" });
		expect(renderer!.root.findByType("TextInput" as never).props.accessibilityLabel).toBe("Text for the terminal");
	});

	it("sends from a touch-target arrow on the accent, leaving the haptic to the outcome", async () => {
		const props = await render({ value: "hello" });
		const send = renderer!.root.findByProps({ accessibilityLabel: "Send" });
		expect(send.props.style({ pressed: false })).toEqual(expect.arrayContaining([expect.objectContaining({ width: 44, height: 44, borderRadius: 22 }), { backgroundColor: "#accent" }]));
		expect(send.findByType("SymbolView" as never).props).toMatchObject({ name: { ios: "arrow.up", android: "arrow_upward" }, tintColor: "#onAccent" });
		act(() => send.props.onPress());
		expect(props.onSend).toHaveBeenCalledOnce();
		expect(fixtures.haptics.tap).not.toHaveBeenCalled();
	});

	it("cannot send an empty field or while a send is running", async () => {
		await render({ value: "  " });
		expect(renderer!.root.findByProps({ accessibilityLabel: "Send" }).props.disabled).toBe(true);
		await render({ value: "x", sending: true });
		expect(renderer!.root.findByProps({ accessibilityLabel: "Send" }).props.accessibilityState).toEqual({ disabled: true });
	});

	it("switches the route through the native toggle, which always means 'to the terminal'", async () => {
		const props = await render({ target: "terminal" });
		const toggle = renderer!.root.findByType("RouteToggle" as never);
		expect(toggle.props.on).toBe(true);
		act(() => toggle.props.onChange(false));
		expect(props.onTargetChange).toHaveBeenCalledWith("agent");
		expect(fixtures.haptics.select).toHaveBeenCalledOnce();
	});

	it("hides the toggle for a plain shell", async () => {
		await render({ targetLocked: true, target: "terminal" });
		expect(renderer!.root.findAllByType("RouteToggle" as never)).toHaveLength(0);
	});

	it("offers to hide the keyboard only while there is one", async () => {
		await render();
		expect(renderer!.root.findAllByProps({ accessibilityLabel: "Hide keyboard" })).toHaveLength(0);
		const props = await render({ keyboardVisible: true });
		const hide = renderer!.root.findByProps({ accessibilityLabel: "Hide keyboard" });
		expect(hide.findByType("SymbolView" as never).props.name).toEqual({ ios: "keyboard.chevron.compact.down", android: "keyboard_hide" });
		act(() => hide.props.onPress());
		expect(props.onDismissKeyboard).toHaveBeenCalledOnce();
	});

	it("sizes the mic like the other controls", async () => {
		await render();
		expect(renderer!.root.findByType("MicKey" as never).props).toMatchObject({ variant: "plain", size: 44 });
	});

	// Tap to start, tap to finish.
	it("hands the mic the one-step toggle", async () => {
		await render();
		expect(renderer!.root.findByType("MicKey" as never).props.onToggleLatched).toBe(voice.toggleLatched);
	});

	// The dictation takes the field's place in the pill, instead of a strip
	// over the terminal that flashed "Keep holding…" on every press.
	it("shows dictation in the field's place while the mic is open", async () => {
		await render();
		expect(renderer!.root.findAllByType("DictationField" as never)).toHaveLength(0);
		await render({ voice: { ...voice, state: "recording", partial: "run the tests" } });
		expect(renderer!.root.findByType("DictationField" as never).props).toMatchObject({ state: "recording", partial: "run the tests", subscribeLevel: voice.subscribeLevel });
		const field = renderer!.root.findByProps({ accessibilityLabel: "Message the agent" });
		expect(field.props.style).toContainEqual({ display: "none" });
	});
});
