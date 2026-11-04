import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";

const gesture = vi.hoisted(() => ({ callbacks: {} as Record<string, (...args: any[]) => void> }));
const feel = vi.hoisted(() => ({ tap: vi.fn(), select: vi.fn(), detent: vi.fn(), armed: vi.fn(), press: vi.fn(), error: vi.fn() }));
vi.mock("react-native", () => ({ View: "View", Text: "Text", ActivityIndicator: "ActivityIndicator", StyleSheet: { create: (styles: object) => styles } }));
vi.mock("react-native-gesture-handler", () => ({
	GestureDetector: "GestureDetector",
	Gesture: { Pan: () => {
		const pan: Record<string, (...args: any[]) => unknown> = {};
		for (const method of ["enabled", "activeOffsetX", "failOffsetY", "onBegin", "onUpdate", "onEnd", "onFinalize"]) {
			pan[method] = (callback) => { if (method.startsWith("on")) gesture.callbacks[method] = callback as never; return pan; };
		}
		return pan;
	} },
}));
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	return {
		default: { View: "AnimatedView" },
		Easing: { out: (value: unknown) => value, inOut: (value: unknown) => value, cubic: null, quad: null },
		cancelAnimation: () => {},
		interpolate: () => 0,
		interpolateColor: () => "",
		useSharedValue: (value: unknown) => React.useRef({ value }).current,
		useAnimatedStyle: (style: () => unknown) => style,
		withRepeat: (value: unknown) => value,
		withSequence: (value: unknown) => value,
		withSpring: (value: number) => value,
		withTiming: (value: number, _options: unknown, done?: (finished: boolean) => void) => { if (done) queueMicrotask(() => done(true)); return value; },
	};
});
vi.mock("react-native-worklets", () => ({ scheduleOnRN: (fn: (...args: unknown[]) => void, ...args: unknown[]) => fn(...args) }));
vi.mock("./ThemeProvider", () => ({ useThemeState: () => ({ scheme: "dark" }) }));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => true }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./haptics", () => ({ haptics: feel }));
vi.mock("./tokens", () => ({ type: { subheadline: { fontSize: 15, lineHeight: 20 } }, space: { xs: 4 } }));

import { SwipeToMerge } from "./SwipeToMerge";

let renderer: ReactTestRenderer | undefined;
afterEach(() => { act(() => renderer?.unmount()); vi.unstubAllGlobals(); gesture.callbacks = {}; Object.values(feel).forEach((fn) => fn.mockClear()); });

function mount(onMerge: () => Promise<boolean>) {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	act(() => { renderer = create(<SwipeToMerge label="Swipe to merge PR #1" busyLabel="Merging…" onMerge={onMerge} onAccessibilityActivate={() => {}} />); });
	// A 300pt track leaves the knob 244pt of travel.
	const track = renderer!.root.findAll((node) => typeof node.props.onLayout === "function")[0];
	act(() => track.props.onLayout({ nativeEvent: { layout: { width: 300 } } }));
}

it("counts the distance a fast flick covers before the pan activates", async () => {
	const onMerge = vi.fn(async () => false);
	mount(onMerge);
	// Android reports translation from the activation point, so a fast flick's
	// first jump never shows up in translationX; the finger still reached the end.
	await act(async () => {
		gesture.callbacks.onBegin({ absoluteX: 20, translationX: 0 });
		gesture.callbacks.onUpdate({ absoluteX: 90, translationX: 0 });
		gesture.callbacks.onUpdate({ absoluteX: 200, translationX: 110 });
		gesture.callbacks.onUpdate({ absoluteX: 250, translationX: 160 });
		gesture.callbacks.onEnd({ absoluteX: 270, translationX: 180 });
	});
	expect(onMerge).toHaveBeenCalledTimes(1);
});

it("does not merge when the finger stops short of the end", async () => {
	const onMerge = vi.fn(async () => false);
	mount(onMerge);
	await act(async () => {
		gesture.callbacks.onBegin({ absoluteX: 20, translationX: 0 });
		gesture.callbacks.onUpdate({ absoluteX: 150, translationX: 130 });
		gesture.callbacks.onEnd({ absoluteX: 180, translationX: 160 });
	});
	expect(onMerge).not.toHaveBeenCalled();
});

it("ticks like a ratchet while dragging, bumps when armed, and lands on release", async () => {
	const onMerge = vi.fn(async () => false);
	mount(onMerge);
	await act(async () => {
		gesture.callbacks.onBegin({ absoluteX: 20 });
		expect(feel.tap).toHaveBeenCalledTimes(1);
		// 244pt of travel: halfway passes detents 1–5.
		gesture.callbacks.onUpdate({ absoluteX: 20 + 122 });
		expect(feel.detent).toHaveBeenCalledTimes(1);
		gesture.callbacks.onUpdate({ absoluteX: 20 + 150 });
		expect(feel.detent).toHaveBeenCalledTimes(2);
		expect(feel.armed).not.toHaveBeenCalled();
		// Past 85%: the armed bump replaces that frame's detent tick.
		gesture.callbacks.onUpdate({ absoluteX: 20 + 220 });
		expect(feel.armed).toHaveBeenCalledTimes(1);
		expect(feel.detent).toHaveBeenCalledTimes(2);
		gesture.callbacks.onEnd({ absoluteX: 20 + 240 });
	});
	expect(feel.press).toHaveBeenCalledTimes(1);
	expect(onMerge).toHaveBeenCalledTimes(1);
});

it("stays on screen as a merged bar labelled with the PR number", () => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	act(() => { renderer = create(<SwipeToMerge label="Swipe to merge PR #1" busyLabel="Merging…" merged mergedLabel="Merged PR #1" onMerge={async () => true} onAccessibilityActivate={() => {}} />); });
	const texts = renderer!.root.findAll((node) => (node.type as unknown) === "Text").map((node) => node.props.children);
	expect(texts).toContain("Merged PR #1");
	expect(texts).not.toContain("Swipe to merge PR #1");
	const control = renderer!.root.findAll((node) => node.props.accessible === true)[0];
	expect(control.props.accessibilityLabel).toBe("Merged PR #1");
	expect(control.props.accessibilityActions).toEqual([]);
	expect(control.props.accessibilityHint).toBeUndefined();
});
