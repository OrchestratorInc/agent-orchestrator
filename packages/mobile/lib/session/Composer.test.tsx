import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }),
	haptics: { tap: vi.fn(), select: vi.fn() },
	dismissKeyboard: vi.fn(),
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { ActivityIndicator: host("ActivityIndicator"), Keyboard: { dismiss: fixtures.dismissKeyboard }, Pressable: host("Pressable"), StyleSheet: { create: (styles: object) => styles }, TextInput: host("TextInput"), View: host("View") };
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
vi.mock("../chat/composerStyles", () => ({
	COMPOSER_RADIUS: 28,
	COMPOSER_DISC: 34,
	COMPOSER_FONT_SCALE_CAP: 1.3,
	composerStyleRules: (t: Record<string, string>) => ({ composer: { borderRadius: 28, borderCurve: "continuous", backgroundColor: t.bgElevated }, composerRow: {}, input: {} }),
	useComposerFieldSize: () => ({ fontScale: 1, field: {}, row: {} }),
}));
vi.mock("./RouteToggle", async () => {
	const React = await import("react");
	return { RouteToggle: (props: object) => React.createElement("RouteToggle", props) };
});
vi.mock("../voice/DictationField", async () => {
	const React = await import("react");
	return { DictationField: (props: object) => React.createElement("DictationField", props) };
});

import { Composer } from "./Composer";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.haptics.tap.mockReset();
	fixtures.haptics.select.mockReset();
	fixtures.dismissKeyboard.mockReset();
	voice.toggleLatched.mockReset();
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
	it("names its field for screen readers by the route it sends on", async () => {
		await render();
		expect(renderer!.root.findByType("TextInput" as never).props.accessibilityLabel).toBe("Message the agent");
		await render({ target: "terminal" });
		expect(renderer!.root.findByType("TextInput" as never).props.accessibilityLabel).toBe("Text for the terminal");
	});

	it("sends from the round button, leaving the haptic to the outcome", async () => {
		const props = await render({ value: "hello" });
		act(() => renderer!.root.findByProps({ accessibilityLabel: "Send" }).props.onPress());
		expect(props.onSend).toHaveBeenCalledOnce();
		expect(fixtures.haptics.tap).not.toHaveBeenCalled();
	});

	it("cannot send an empty field or while a send is running", async () => {
		await render({ value: "  ", voice: { ...voice, state: "unavailable" } });
		expect(renderer!.root.findByProps({ accessibilityLabel: "Send" }).props.disabled).toBe(true);
		await render({ value: "x", sending: true });
		expect(renderer!.root.findByProps({ accessibilityLabel: "Send" }).props.accessibilityState).toEqual({ disabled: true });
	});

	it("switches the route through the toggle, which always means 'to the terminal'", async () => {
		const props = await render({ target: "terminal" });
		const toggle = renderer!.root.findByType("RouteToggle" as never);
		expect(toggle.props.on).toBe(true);
		act(() => toggle.props.onChange(false));
		expect(props.onTargetChange).toHaveBeenCalledWith("agent");
		expect(fixtures.haptics.select).toHaveBeenCalledOnce();
	});

	// Where the chat composer keeps its approval control.
	it("keeps the toggle right beside the round button", async () => {
		await render({ keyboardVisible: true });
		// The mock's host element, then the mock, then the row holding both.
		const toggle = renderer!.root.findByType("RouteToggle" as never).parent!;
		const children = toggle.parent!.children as typeof toggle[];
		expect(children[children.indexOf(toggle) + 1].props.accessibilityLabel).toBe("Dictate a message");
	});

	it("hides the toggle for a plain shell", async () => {
		await render({ targetLocked: true, target: "terminal" });
		expect(renderer!.root.findAllByType("RouteToggle" as never)).toHaveLength(0);
	});

	it("offers to hide the keyboard only while there is one", async () => {
		await render();
		expect(renderer!.root.findAllByProps({ accessibilityLabel: "Hide keyboard" })).toHaveLength(0);
		const props = await render({ keyboardVisible: true });
		act(() => renderer!.root.findByProps({ accessibilityLabel: "Hide keyboard" }).props.onPress());
		expect(props.onDismissKeyboard).toHaveBeenCalledOnce();
	});

	// Tap to start, tap to finish: the round button dictates while the field is
	// empty, as the chat's does, and drops the keyboard so the trace has the room.
	it("dictates from the round button while the field is empty", async () => {
		await render();
		expect(renderer!.root.findAllByProps({ accessibilityLabel: "Send" })).toHaveLength(0);
		act(() => renderer!.root.findByProps({ accessibilityLabel: "Dictate a message" }).props.onPress());
		expect(fixtures.dismissKeyboard).toHaveBeenCalledOnce();
		expect(voice.toggleLatched).toHaveBeenCalledOnce();
	});

	it("stops the mic from the same button", async () => {
		await render({ value: "draft", voice: { ...voice, state: "recording" } });
		act(() => renderer!.root.findByProps({ accessibilityLabel: "Stop dictating" }).props.onPress());
		expect(voice.toggleLatched).toHaveBeenCalledOnce();
		expect(fixtures.dismissKeyboard).not.toHaveBeenCalled();
	});

	it("shows dictation in the field's place while the mic is open", async () => {
		await render();
		expect(renderer!.root.findAllByType("DictationField" as never)).toHaveLength(0);
		await render({ voice: { ...voice, state: "recording", partial: "run the tests" } });
		expect(renderer!.root.findByType("DictationField" as never).props).toMatchObject({ state: "recording", partial: "run the tests", subscribeLevel: voice.subscribeLevel });
		const field = renderer!.root.findByProps({ accessibilityLabel: "Message the agent" });
		expect(field.props.style).toContainEqual({ display: "none" });
	});
});
