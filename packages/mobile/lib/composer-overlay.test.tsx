import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }), os: "ios", shown: { value: 0 } }));

vi.mock("expo-router", () => ({ useNavigation: () => ({ setOptions: () => {} }) }));
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		DeviceEventEmitter: { addListener: () => ({ remove: () => {} }) },
		Platform: { get OS() { return fixtures.os; }, Version: 34 },
		Pressable: host("Pressable"),
		StyleSheet: { create: (styles: object) => styles, absoluteFill: {}, hairlineWidth: 1 },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("react-native-gesture-handler", async () => {
	const React = await import("react");
	return { GestureHandlerRootView: ({ children }: { children?: React.ReactNode }) => React.createElement("GestureHandlerRootView", null, children) };
});
vi.mock("react-native-keyboard-controller", async () => {
	const React = await import("react");
	return {
		OverKeyboardView: ({ children }: { children?: React.ReactNode }) => React.createElement("OverKeyboardView", null, children),
		useReanimatedKeyboardAnimation: () => ({ height: { value: 0 } }),
	};
});
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	return {
		default: { View: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("AnimatedView", props, children) },
		Easing: { out: (value: unknown) => value, cubic: null },
		// The style's worklet itself, so a test can read it at any point of the reveal.
		useAnimatedStyle: (style: () => unknown) => style,
		useSharedValue: (value: number) => { fixtures.shown.value = value; return fixtures.shown; },
		withTiming: (value: unknown) => value,
	};
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }) }));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./effort-slider-veil", () => ({ EffortSliderVeil: "EffortSliderVeil" }));
vi.mock("./gradient-text", () => ({ GradientText: "GradientText" }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme) }));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => false }));

import { ComposerOverlay, overlayStages, VEIL_LEAD } from "./composer-overlay";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); fixtures.os = "ios"; });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

const hostType = (node: ReactTestInstance): string => (typeof node.type === "string" ? node.type : "");

describe("composer overlay over the keyboard", () => {
	// The touch areas that put it away are hidden from screen readers, so
	// VoiceOver needs its escape gesture to do it.
	it("puts itself away on VoiceOver's escape while up, and not while hidden", () => {
		const onDismiss = vi.fn();
		act(() => { renderer = create(<ComposerOverlay overKeyboard visible onDismiss={onDismiss}><></></ComposerOverlay>); });
		const escapable = () => renderer!.root.findAll((node) => hostType(node) === "AnimatedView" && node.props.onAccessibilityEscape !== undefined);
		expect(escapable()).toHaveLength(1);
		act(() => escapable()[0].props.onAccessibilityEscape());
		expect(onDismiss).toHaveBeenCalledOnce();
		act(() => renderer!.update(<ComposerOverlay overKeyboard visible={false} onDismiss={onDismiss}><></></ComposerOverlay>));
		expect(escapable()).toHaveLength(0);
	});
});

// Faded as one, the content and the composer under it were each half there
// mid-fade, one over the other. The veil comes first and goes last.
describe("composer overlay's staged reveal", () => {
	it("brings the veil in over its lead, then the content over the rest", () => {
		expect(overlayStages(0)).toEqual({ veil: 0, content: 0 });
		expect(overlayStages(VEIL_LEAD / 2)).toEqual({ veil: 0.5, content: 0 });
		expect(overlayStages(VEIL_LEAD)).toEqual({ veil: 1, content: 0 });
		expect(overlayStages(VEIL_LEAD + (1 - VEIL_LEAD) / 2).content).toBeCloseTo(0.5);
		expect(overlayStages(1)).toEqual({ veil: 1, content: 1 });
	});

	it("holds both stages within 0 and 1 past either end", () => {
		expect(overlayStages(-0.2)).toEqual({ veil: 0, content: 0 });
		expect(overlayStages(1.2)).toEqual({ veil: 1, content: 1 });
	});

	type Style = () => { opacity: number; transform?: [{ translateY: number }, { scale: number }] };
	const animatedStyle = (node: ReactTestInstance): Style => ([node.props.style].flat() as unknown[]).find((part) => typeof part === "function") as Style;
	const holdsVeil = (node: ReactTestInstance) => node.findAll((child) => hostType(child) === "EffortSliderVeil").length > 0;
	const layers = () => {
		const animated = renderer!.root.findAll((node) => hostType(node) === "AnimatedView" && typeof animatedStyle(node) === "function");
		const veil = animated.filter((node) => holdsVeil(node) && node.props.pointerEvents === "none");
		const content = animated.filter((node) => !holdsVeil(node));
		expect([veil.length, content.length]).toEqual([1, 1]);
		return { veil: animatedStyle(veil[0]), content: animatedStyle(content[0]) };
	};

	it.each([["over the keyboard", true], ["floating", false]])("fades the veil and the content on styles of their own, %s", (_where, overKeyboard) => {
		act(() => { renderer = create(<ComposerOverlay overKeyboard={overKeyboard} visible onDismiss={() => {}}><></></ComposerOverlay>); });
		const { veil, content } = layers();
		expect(veil).not.toBe(content);
		fixtures.shown.value = VEIL_LEAD;
		expect(veil().opacity).toBe(1);
		expect(content()).toEqual({ opacity: 0, transform: [{ translateY: 6 }, { scale: 0.98 }] });
		fixtures.shown.value = 1;
		expect(content()).toEqual({ opacity: 1, transform: [{ translateY: 0 }, { scale: 1 }] });
		fixtures.shown.value = 0;
		expect(veil().opacity).toBe(0);
	});
});
