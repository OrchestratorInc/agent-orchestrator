import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }), detent: vi.fn() }));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { StyleSheet: { create: (styles: object) => styles }, Text: host("Text"), View: host("View") };
});
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		default: { View: host("AnimatedView"), ScrollView: host("AnimatedScrollView") },
		Extrapolation: { CLAMP: "clamp" },
		interpolate: () => 0,
		runOnJS: (fn: unknown) => fn,
		useAnimatedReaction: () => {},
		useAnimatedRef: () => ({ current: null }),
		useAnimatedScrollHandler: () => () => {},
		useAnimatedStyle: () => ({}),
		useSharedValue: (value: unknown) => ({ value }),
	};
});
vi.mock("lucide-react-native/icons/star", () => ({ default: "Star" }));
vi.mock("./AgentLogo", () => ({ AgentLogo: "AgentLogo" }));
vi.mock("./haptics", () => ({ haptics: { detent: fixtures.detent } }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme) }));

import { WheelPicker } from "./wheel-picker";

const items = [{ key: "opus", label: "Opus" }, { key: "sonnet", label: "Sonnet" }, { key: "haiku", label: "Haiku" }];

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); fixtures.detent.mockClear(); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

const hostType = (node: ReactTestInstance): string => (typeof node.type === "string" ? node.type : "");

describe("wheel picker", () => {
	// VoiceOver's and TalkBack's swipe up and down on an adjustable element.
	it("steps a row at a time for a screen reader, reads the row in the band, and stops at the ends", () => {
		const onSettle = vi.fn();
		act(() => { renderer = create(<WheelPicker items={items} index={0} onSettle={onSettle} accessibilityLabel="Model" />); });
		const wheel = () => renderer!.root.findAll((node) => hostType(node) === "AnimatedScrollView")[0];
		expect(wheel().props).toMatchObject({ accessible: true, accessibilityRole: "adjustable", accessibilityLabel: "Model", accessibilityValue: { text: "Opus" } });
		act(() => wheel().props.onAccessibilityAction({ nativeEvent: { actionName: "decrement" } }));
		expect(onSettle).not.toHaveBeenCalled();
		act(() => wheel().props.onAccessibilityAction({ nativeEvent: { actionName: "increment" } }));
		expect(onSettle).toHaveBeenLastCalledWith(1);
		act(() => renderer!.update(<WheelPicker items={items} index={2} onSettle={onSettle} accessibilityLabel="Model" />));
		expect(wheel().props.accessibilityValue).toEqual({ text: "Haiku" });
		act(() => wheel().props.onAccessibilityAction({ nativeEvent: { actionName: "increment" } }));
		expect(onSettle).toHaveBeenCalledOnce();
		act(() => wheel().props.onAccessibilityAction({ nativeEvent: { actionName: "decrement" } }));
		expect(onSettle).toHaveBeenLastCalledWith(1);
	});

	// Regression guard: the wheels sit over a focused composer. A scroll view's
	// default blurred the field on any tap on a wheel (its empty rows included,
	// right where a tap to put the picker away lands), dropping the keyboard.
	it("keeps the composer focused when a wheel is tapped", () => {
		act(() => { renderer = create(<WheelPicker items={items} index={0} onSettle={() => {}} />); });
		const wheel = renderer!.root.findAll((node) => hostType(node) === "AnimatedScrollView")[0];
		expect(wheel.props.keyboardShouldPersistTaps).toBe("always");
	});

	it("takes no screen reader steps while locked", () => {
		const onSettle = vi.fn();
		act(() => { renderer = create(<WheelPicker items={items} index={0} onSettle={onSettle} disabled />); });
		const wheel = renderer!.root.findAll((node) => hostType(node) === "AnimatedScrollView")[0];
		act(() => wheel.props.onAccessibilityAction({ nativeEvent: { actionName: "increment" } }));
		expect(onSettle).not.toHaveBeenCalled();
	});
});
