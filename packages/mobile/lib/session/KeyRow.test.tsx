import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	platform: { OS: "ios" },
	theme: new Proxy({ fontMono: "Mono" }, { get: (target, key) => (key in target ? target[key as "fontMono"] : `#${String(key)}`) }),
	tap: vi.fn(),
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Platform: fixtures.platform, Pressable: host("Pressable"), StyleSheet: { create: (styles: object) => styles }, Text: host("Text"), View: host("View") };
});
vi.mock("../ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemedStyles: (factory: (t: unknown) => unknown) => factory(fixtures.theme),
}));
vi.mock("../haptics", () => ({ haptics: { tap: fixtures.tap } }));
vi.mock("../pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: undefined, pressed: { opacity: 0.7 } }) }));

import { KeyRow } from "./KeyRow";
import { KEY_REPEAT } from "./keys";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.platform.OS = "ios";
	fixtures.tap.mockReset();
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.useRealTimers();
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

async function render() {
	const onKey = vi.fn();
	await act(async () => { renderer = create(<KeyRow onKey={onKey} />); });
	const key = (label: string) => renderer!.root.findByProps({ accessibilityLabel: label });
	return { onKey, key };
}

describe("the terminal key row", () => {
	it("names each of the eight keys, keeping a chord's letter after its symbol", async () => {
		const { key } = await render();
		expect(renderer!.root.findAllByType("Pressable" as never).map((k) => k.props.accessibilityLabel)).toEqual(["Escape", "Tab", "Interrupt", "Left", "Up", "Down", "Right", "Enter"]);
		expect(key("Interrupt").findByType("Text" as never).props.children).toBe("C");
	});

	it("spells Escape on Android, where Material has no symbol", async () => {
		fixtures.platform.OS = "android";
		const { key } = await render();
		expect(key("Escape").findByType("Text" as never).props.children).toBe("esc");
	});

	it("sends a key once per tap", async () => {
		const { key, onKey } = await render();
		act(() => {
			key("Enter").props.onPressIn();
			key("Enter").props.onPressOut();
			key("Enter").props.onPress();
		});
		expect(onKey.mock.calls).toEqual([["\r"]]);
		expect(fixtures.tap).toHaveBeenCalledOnce();
	});

	it("repeats an arrow while it is held, and stops when it is let go", async () => {
		const { key, onKey } = await render();
		act(() => key("Up").props.onPressIn());
		expect(onKey).toHaveBeenCalledTimes(1);
		act(() => { vi.advanceTimersByTime(KEY_REPEAT.delayMs - 1); });
		expect(onKey).toHaveBeenCalledTimes(1);
		act(() => { vi.advanceTimersByTime(1 + 3 * KEY_REPEAT.intervalMs); });
		expect(onKey).toHaveBeenCalledTimes(5);
		act(() => {
			key("Up").props.onPressOut();
			key("Up").props.onPress();
		});
		act(() => { vi.advanceTimersByTime(10 * KEY_REPEAT.intervalMs); });
		expect(onKey).toHaveBeenCalledTimes(5);
		expect(onKey.mock.calls.every(([seq]) => seq === "\x1b[A")).toBe(true);
		// One haptic for the press, not one per repeat.
		expect(fixtures.tap).toHaveBeenCalledOnce();
	});

	it("still answers a screen reader's activation, which comes without a press-in", async () => {
		const { key, onKey } = await render();
		// A held arrow that slid off before release gets no onPress …
		act(() => {
			key("Left").props.onPressIn();
			key("Left").props.onPressOut();
		});
		act(() => { vi.runOnlyPendingTimers(); });
		// … and must not swallow the next activation.
		act(() => key("Left").props.onPress());
		expect(onKey.mock.calls).toEqual([["\x1b[D"], ["\x1b[D"]]);
	});
});
