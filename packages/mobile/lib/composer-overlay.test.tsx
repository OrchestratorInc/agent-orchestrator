import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }), os: "ios" }));

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
		useAnimatedStyle: () => ({}),
		useSharedValue: (value: unknown) => ({ value }),
		withTiming: (value: unknown) => value,
	};
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }) }));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./effort-slider-veil", () => ({ EffortSliderVeil: "EffortSliderVeil" }));
vi.mock("./gradient-text", () => ({ GradientText: "GradientText" }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme) }));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => false }));

import { ComposerOverlay } from "./composer-overlay";

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
