import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	platform: { OS: "ios" },
	theme: new Proxy({ fontMono: "Mono" }, { get: (target, key) => (key in target ? target[key as "fontMono"] : `#${String(key)}`) }),
	tap: vi.fn(),
	ripple: undefined as object | undefined,
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
vi.mock("../pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: fixtures.ripple, pressed: fixtures.ripple ? undefined : { opacity: 0.7 } }) }));

import { KeyRow } from "./KeyRow";
import { KEY_REPEAT } from "./keys";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.platform.OS = "ios";
	fixtures.ripple = undefined;
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
	it("draws eight borderless, touch-target-tall keys with system symbols", async () => {
		const { key } = await render();
		const keys = renderer!.root.findAllByType("Pressable" as never);
		expect(keys.map((k) => k.props.accessibilityLabel)).toEqual(["Escape", "Tab", "Interrupt", "Left", "Up", "Down", "Right", "Enter"]);
		const style = key("Tab").props.style({ pressed: false })[0];
		expect(style).toMatchObject({ height: 44, backgroundColor: "#accentTint" });
		expect(style.borderWidth).toBeUndefined();
		expect(key("Tab").findByType("SymbolView" as never).props.name.ios).toBe("arrow.right.to.line");
		expect(key("Escape").findByType("SymbolView" as never).props.name.ios).toBe("escape");
		// A chord keeps its letter after the control symbol.
		expect(key("Interrupt").findByType("Text" as never).props.children).toBe("C");
	});

	it("darkens a pressed key on iOS instead of outlining it", async () => {
		const { key } = await render();
		expect(key("Enter").props.style({ pressed: true })).toContainEqual({ backgroundColor: "#accentPressed" });
	});

	it("ripples on Android, and spells Escape where Material has no symbol", async () => {
		fixtures.platform.OS = "android";
		fixtures.ripple = { color: "#ripple" };
		const { key } = await render();
		expect(key("Enter").props.android_ripple).toEqual({ color: "#ripple" });
		expect(key("Enter").props.style({ pressed: true })).not.toContainEqual({ backgroundColor: "#accentPressed" });
		expect(key("Escape").findAllByType("SymbolView" as never)).toHaveLength(0);
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
