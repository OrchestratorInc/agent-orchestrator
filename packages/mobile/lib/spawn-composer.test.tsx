import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }),
	dismiss: vi.fn(),
}));

vi.mock("react-native", () => ({
	Platform: { OS: "ios" },
	ActivityIndicator: "ActivityIndicator", Keyboard: { dismiss: fixtures.dismiss }, Pressable: "Pressable", TextInput: "TextInput", View: "View",
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1, absoluteFill: {} },
}));
vi.mock("react-native-reanimated", () => ({
	default: { View: "AnimatedView" },
	interpolate: () => 0, useAnimatedStyle: () => ({}), useSharedValue: (value: unknown) => ({ value }), withSpring: (value: unknown) => value, withTiming: (value: unknown) => value,
}));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => false }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (make: (theme: object) => unknown) => make(fixtures.theme) }));
vi.mock("./pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: undefined, pressed: {} }) }));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./chat/ChatAttachmentMenu", () => ({ ChatAttachmentMenu: "ChatAttachmentMenu" }));
vi.mock("./chat/composer-glass", () => ({ ComposerGlass: "ComposerGlass", composerGlassSupported: false }));

import { SpawnComposer, type SpawnComposerProps } from "./spawn-composer";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
	fixtures.dismiss.mockClear();
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

const baseProps = (overrides: Partial<SpawnComposerProps> = {}): SpawnComposerProps => ({
	prompt: "Fix the flaky test",
	onChangePrompt: vi.fn(),
	placeholder: "Ask Codex",
	settings: "Settings",
	hasAttachments: false,
	onChoosePhoto: vi.fn(),
	onChooseFile: vi.fn(),
	voice: { state: "idle", mode: "push", onPressIn: vi.fn(), onPressOut: vi.fn(), onToggleLatched: vi.fn() },
	onSpawn: vi.fn(),
	busy: false,
	disabled: false,
	...overrides,
});

async function render(props: SpawnComposerProps) {
	if (renderer) await act(async () => renderer?.unmount());
	await act(async () => { renderer = create(<SpawnComposer {...props} />); });
	return renderer!.root;
}

const hostType = (node: ReactTestInstance): string => (typeof node.type === "string" ? node.type : "");
const primary = (root: ReactTestInstance) => root.findAll((node) => hostType(node) === "Pressable" && ["spawn-submit", "spawn-dictate"].includes(node.props.testID))[0];
const glyph = (button: ReactTestInstance) => button.findAll((node) => hostType(node) === "AppSymbol")[0]?.props.name;
const disc = (button: ReactTestInstance) => button.findAll((node) => hostType(node) === "View")[0];

describe("SpawnComposer", () => {
	// The chat's pill: the glass, the field, the controls row and the two
	// corner controls, attach being the chat's own menu.
	it("draws the chat composer's pill around the prompt and the settings", async () => {
		const props = baseProps();
		const root = await render(props);
		expect(root.findAllByType("ComposerGlass" as never)).toHaveLength(1);
		const field = root.findByType("TextInput" as never);
		expect(field.props).toMatchObject({ value: "Fix the flaky test", placeholder: "Ask Codex", multiline: true, autoFocus: true });
		await act(async () => field.props.onChangeText("Fix it"));
		expect(props.onChangePrompt).toHaveBeenCalledWith("Fix it");
		expect(root.findAll((node) => node.props.children === "Settings").length).toBeGreaterThan(0);
		const attach = root.findByType("ChatAttachmentMenu" as never);
		expect(attach.props).toMatchObject({ disabled: false, canAttachFile: true });
		attach.props.onChoosePhoto();
		expect(props.onChoosePhoto).toHaveBeenCalledOnce();
	});

	it("starts the task from the chat's accent disc once there is text", async () => {
		const props = baseProps();
		const button = primary(await render(props));
		expect(button.props.testID).toBe("spawn-submit");
		expect(button.props.accessibilityLabel).toBe("Start task");
		expect(glyph(button)).toBe("send");
		expect(disc(button).props.style).toContainEqual({ backgroundColor: "#accent" });
		await act(async () => button.props.onPress());
		expect(props.onSpawn).toHaveBeenCalledOnce();
	});

	it("holds the disc with a spinner while the task starts", async () => {
		const button = primary(await render(baseProps({ busy: true, disabled: true })));
		expect(button.props.accessibilityLabel).toBe("Starting task");
		expect(button.props.disabled).toBe(true);
		expect(button.findAllByType("ActivityIndicator" as never)).toHaveLength(1);
		expect(renderer!.root.findByType("ChatAttachmentMenu" as never).props.disabled).toBe(true);
	});

	// As the chat's round button: it dictates while nothing is written, stops
	// while the mic is open, and a device that cannot dictate keeps Start.
	it("dictates with an empty prompt and stops while listening", async () => {
		let props = baseProps({ prompt: "" });
		let button = primary(await render(props));
		expect(button.props.testID).toBe("spawn-dictate");
		expect(button.props.accessibilityLabel).toBe("Dictate the task");
		expect(glyph(button)).toEqual({ ios: "mic.fill", android: "mic" });
		await act(async () => button.props.onPress());
		expect(fixtures.dismiss).toHaveBeenCalledOnce();
		expect(props.voice.onToggleLatched).toHaveBeenCalledOnce();
		expect(props.onSpawn).not.toHaveBeenCalled();

		props = baseProps({ voice: { ...baseProps().voice, state: "recording", mode: "latched" } });
		button = primary(await render(props));
		expect(button.props.accessibilityLabel).toBe("Stop dictating");
		expect(glyph(button)).toBe("stop");
		expect(disc(button).props.style).toContainEqual({ backgroundColor: "#red" });

		button = primary(await render(baseProps({ prompt: "", voice: { ...baseProps().voice, state: "unavailable" } })));
		expect(button.props.testID).toBe("spawn-submit");
	});
});
