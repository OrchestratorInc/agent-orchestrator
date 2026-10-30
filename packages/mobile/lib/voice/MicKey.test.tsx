import { createElement, type ComponentProps } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
const fixtures = vi.hoisted(() => ({ platform: { OS: "ios" }, reduceMotion: false }));
vi.mock("react-native", () => ({
	Platform: fixtures.platform,
	Pressable: "Pressable",
	View: "View",
	StyleSheet: { create: <T,>(styles: T) => styles, absoluteFill: {}, hairlineWidth: 0.5 },
}));
vi.mock("react-native-reanimated", () => ({
	default: { View: "AnimatedView" },
	cancelAnimation: () => {},
	Easing: { inOut: () => () => 0, ease: () => 0 },
	interpolate: () => 0,
	useAnimatedStyle: (factory: () => object) => factory(),
	useSharedValue: (value: number) => ({ value }),
	withRepeat: (value: unknown) => value,
	withTiming: (value: unknown) => value,
}));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn() } }));
vi.mock("../useReducedMotion", () => ({ useReducedMotion: () => fixtures.reduceMotion }));
vi.mock("../ThemeProvider", async () => {
	const { lightTheme } = await import("../theme");
	return { useTheme: () => lightTheme, useThemedStyles: (factory: (t: typeof lightTheme) => unknown) => factory(lightTheme) };
});

import { lightTheme } from "../theme";
import { MicKey } from "./MicKey";

type Props = ComponentProps<typeof MicKey>;
let renderer: ReactTestRenderer | undefined;
beforeEach(() => { fixtures.platform.OS = "ios"; fixtures.reduceMotion = false; });
afterEach(() => { if (renderer) act(() => renderer!.unmount()); renderer = undefined; });

function render(props: Partial<Props> = {}) {
	const all: Props = { state: "idle", mode: "push", onPressIn: vi.fn(), onPressOut: vi.fn(), onToggleLatched: vi.fn(), ...props };
	act(() => { renderer = create(createElement(MicKey, all)); });
	return { props: all, key: renderer!.root.findByType("Pressable" as never) };
}

it("draws the recording glyph in the ink made for the red fill", () => {
	render({ state: "recording" });
	const glyph = renderer!.root.findByType("SymbolView" as never);
	expect(glyph.props.tintColor).toBe(lightTheme.onDestructive);
	expect(glyph.props.tintColor).not.toBe(lightTheme.textPrimary);
});

it("breathes while recording, and holds still under Reduce Motion", () => {
	render({ state: "recording" });
	expect(renderer!.root.findAllByType("AnimatedView" as never)).toHaveLength(1);
	act(() => renderer!.unmount());
	fixtures.reduceMotion = true;
	render({ state: "recording" });
	expect(renderer!.root.findAllByType("AnimatedView" as never)).toHaveLength(0);
});

// A screen reader cannot hold the key, so its activation latches hands-free
// dictation instead of tapping it.
it("latches dictation from a screen reader's activation", () => {
	const onToggleLatched = vi.fn();
	const { props, key } = render({ onToggleLatched });
	expect(key.props.accessibilityActions).toEqual([{ name: "activate", label: "Dictate hands-free" }]);
	act(() => key.props.onAccessibilityAction({ nativeEvent: { actionName: "activate" } }));
	act(() => key.props.onAccessibilityTap());
	expect(onToggleLatched).toHaveBeenCalledTimes(2);
	expect(props.onPressIn).not.toHaveBeenCalled();
});

it("offers no activation while it cannot dictate", () => {
	const { key } = render({ state: "unavailable" });
	expect(key.props.accessibilityActions).toBeUndefined();
	expect(key.props.onAccessibilityTap).toBeUndefined();
});

it("ripples on Android instead of fading", () => {
	fixtures.platform.OS = "android";
	const { key } = render({ variant: "plain" });
	expect(key.props.android_ripple).toMatchObject({ borderless: true });
	expect(key.props.style({ pressed: true }).filter(Boolean)).not.toContainEqual({ opacity: expect.any(Number) });
});
