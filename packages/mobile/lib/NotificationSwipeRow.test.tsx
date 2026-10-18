import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";

const gesture = vi.hoisted(() => ({ callbacks: {} as Record<string, (...args: any[]) => void> }));
vi.mock("react-native", () => ({ View: "View", StyleSheet: { create: (styles: object) => styles } }));
vi.mock("react-native-gesture-handler", () => ({
	GestureDetector: "GestureDetector",
	Gesture: { Pan: () => {
		const pan: Record<string, (...args: any[]) => unknown> = {};
		for (const method of ["activeOffsetX", "failOffsetY", "onBegin", "onUpdate", "onEnd", "onFinalize"]) {
			pan[method] = (callback) => { if (method.startsWith("on")) gesture.callbacks[method] = callback; return pan; };
		}
		return pan;
	} },
}));
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	return {
		default: { View: "AnimatedView" }, Easing: { out: (value: unknown) => value, cubic: null },
		useSharedValue: (value: unknown) => React.useRef({ value }).current,
		useAnimatedStyle: (style: () => unknown) => style,
		withSpring: (value: number) => value,
		withTiming: (value: number, _options: unknown, done: (finished: boolean) => void) => { queueMicrotask(() => done(true)); return value; },
	};
});
vi.mock("react-native-worklets", () => ({ scheduleOnRN: (fn: () => void) => fn() }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({ red: "red", bgBase: "black" }) }));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => false }));
vi.mock("./icons", () => ({ Feather: "Feather" }));

import { NotificationSwipeRow } from "./NotificationSwipeRow";

let renderer: ReactTestRenderer | undefined;
afterEach(() => { act(() => renderer?.unmount()); vi.unstubAllGlobals(); });

it("restores a refused clear and allows a later swipe after the host reconnects", async () => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const clear = vi.fn(() => false);
	act(() => { renderer = create(<NotificationSwipeRow onClear={clear}><></></NotificationSwipeRow>); });
	act(() => renderer!.root.findByType("View" as never).props.onLayout({ nativeEvent: { layout: { width: 300 } } }));
	async function swipe() {
		await act(async () => {
			gesture.callbacks.onBegin();
			gesture.callbacks.onUpdate({ translationX: -250 });
			gesture.callbacks.onEnd();
		});
	}
	await swipe();
	const animatedStyle = renderer!.root.findByType("AnimatedView" as never).props.style[2];
	expect(animatedStyle().transform).toEqual([{ translateX: 0 }]);
	clear.mockReturnValue(true);
	await swipe();
	expect(clear).toHaveBeenCalledTimes(2);
	expect(animatedStyle().transform).toEqual([{ translateX: -300 }]);
});
