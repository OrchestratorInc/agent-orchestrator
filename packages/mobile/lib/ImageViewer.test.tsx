import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	platform: { OS: "android" },
	gestures: {} as Record<string, Record<string, (...args: unknown[]) => void>>,
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Image: host("Image"),
		Modal: ({ children, visible, ...props }: { children?: React.ReactNode; visible: boolean }) => visible ? React.createElement("Modal", props, children) : null,
		Platform: fixtures.platform,
		StyleSheet: { create: (styles: object) => styles, absoluteFill: { position: "absolute" } },
		View: host("View"),
		useWindowDimensions: () => ({ width: 400, height: 800 }),
	};
});
vi.mock("react-native-gesture-handler", async () => {
	const React = await import("react");
	// Each gesture records its callbacks under its kind, so a test can drive it.
	const builder = (kind: string) => {
		const callbacks: Record<string, (...args: unknown[]) => void> = {};
		fixtures.gestures[kind] = callbacks;
		const chain: Record<string, unknown> = new Proxy({}, {
			get: (_target, key: string) => (callback: unknown) => {
				if (typeof callback === "function") callbacks[key] = callback as (...args: unknown[]) => void;
				return chain;
			},
		});
		return chain;
	};
	return {
		Gesture: { Pinch: () => builder("pinch"), Pan: () => builder("pan"), Tap: () => builder("tap"), Simultaneous: () => ({}) },
		GestureDetector: ({ children }: { children?: React.ReactNode }) => children,
		GestureHandlerRootView: ({ children }: { children?: React.ReactNode }) => React.createElement("GestureHandlerRootView", null, children),
	};
});
vi.mock("react-native-reanimated", () => ({
	default: { View: "AnimatedView" },
	useSharedValue: (value: number) => ({ value }),
	useAnimatedStyle: (style: () => object) => style(),
	withSpring: (value: number) => value,
	withTiming: (value: number, _config: object, done?: (finished: boolean) => void) => { done?.(true); return value; },
}));
vi.mock("react-native-worklets", () => ({ scheduleOnRN: (fn: () => void) => fn() }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 44, right: 0, bottom: 34, left: 0 }) }));
vi.mock("./native-header-button", () => ({ NativeHeaderButton: "NativeHeaderButton" }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({ bgBase: "#bgBase" }) }));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => false }));

import { ImageViewer } from "./ImageViewer";

const source = { uri: "http://qa/api/v1/sessions/s/preview/files/a.png", headers: { Authorization: "Bearer t" } };
let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
	fixtures.platform.OS = "android";
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("ImageViewer", () => {
	it("hands Android an array source, the only form that carries the auth header", async () => {
		await act(async () => { renderer = create(<ImageViewer visible name="a.png" source={source} onClose={vi.fn()} />); });
		expect(renderer!.root.findByType("Image" as never).props.source).toEqual([source]);
		fixtures.platform.OS = "ios";
		await act(async () => renderer!.update(<ImageViewer visible name="b.png" source={source} onClose={vi.fn()} />));
		expect(renderer!.root.findByType("Image" as never).props.source).toEqual(source);
	});

	it("closes from the system's close control, Back and the accessibility escape", async () => {
		const onClose = vi.fn();
		await act(async () => { renderer = create(<ImageViewer visible name="a.png" source={source} onClose={onClose} />); });
		const close = renderer!.root.findByType("NativeHeaderButton" as never);
		expect(close.props).toMatchObject({ icon: "close", label: "Close image" });
		expect(renderer!.root.findByType("Modal" as never).props.onRequestClose).toBe(onClose);
		expect(renderer!.root.findAll((node) => node.props.onAccessibilityEscape === onClose)).not.toHaveLength(0);
	});

	it("sends the unzoomed image away on a long enough drag, and settles a short one", async () => {
		const onClose = vi.fn();
		await act(async () => { renderer = create(<ImageViewer visible name="a.png" source={source} onClose={onClose} />); });
		const pan = fixtures.gestures.pan;
		pan.onStart();
		pan.onUpdate({ translationX: 0, translationY: 40 });
		pan.onEnd({ velocityY: 100 });
		expect(onClose).not.toHaveBeenCalled();
		pan.onStart();
		pan.onUpdate({ translationX: 0, translationY: 200 });
		pan.onEnd({ velocityY: 100 });
		expect(onClose).toHaveBeenCalledOnce();
	});
});
