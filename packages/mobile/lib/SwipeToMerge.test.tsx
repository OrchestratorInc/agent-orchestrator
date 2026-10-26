import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";

const gesture = vi.hoisted(() => ({ callbacks: {} as Record<string, (...args: any[]) => void> }));
const feel = vi.hoisted(() => ({ tap: vi.fn(), select: vi.fn(), detent: vi.fn(), armed: vi.fn(), press: vi.fn(), error: vi.fn() }));
vi.mock("react-native", () => ({ View: "View", Text: "Text", ActivityIndicator: "ActivityIndicator", Platform: { OS: "ios" }, StyleSheet: { create: (styles: object) => styles } }));
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
		interpolate: () => 0,
		interpolateColor: () => "",
		useSharedValue: (value: unknown) => React.useRef({ value }).current,
		useAnimatedStyle: (style: () => unknown) => style,
		withSpring: (value: number) => value,
		withTiming: (value: number, _options: unknown, done?: (finished: boolean) => void) => { if (done) queueMicrotask(() => done(true)); return value; },
	};
});
vi.mock("react-native-worklets", () => ({ scheduleOnRN: (fn: (...args: unknown[]) => void, ...args: unknown[]) => fn(...args) }));
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({}, { get: (_, key) => `#${String(key)}` });
	return { useTheme: () => theme, useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => true }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./haptics", () => ({ haptics: feel }));

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

// A brand moment, but in the theme's own colours: no GitHub hex values, no
// gradient sheens, and nothing that animates on its own forever.
it("draws in the theme's status colours, with no gradients or endless shimmer", () => {
	const source = readFileSync(fileURLToPath(new URL("./SwipeToMerge.tsx", import.meta.url)), "utf8");
	expect(source).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
	expect(source).not.toContain("rgba(");
	expect(source).not.toContain("linear-gradient");
	expect(source).not.toContain("withRepeat");
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	act(() => { renderer = create(<SwipeToMerge label="Swipe to merge PR #1" busyLabel="Merging…" onMerge={async () => false} onAccessibilityActivate={() => {}} />); });
	const track = renderer!.root.findAll((node) => typeof node.props.onLayout === "function")[0];
	expect(track.props.style).toMatchObject({ backgroundColor: "#tintGreen" });
	const label = renderer!.root.findByType("Text" as never);
	expect(label.props.style).toContainEqual({ color: "#green" });
});

// A fixed single-line track cut the label off at large text sizes.
it("lets the label wrap to two lines on a track that grows with it", () => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	act(() => { renderer = create(<SwipeToMerge label="Swipe to merge PR #1" busyLabel="Merging…" onMerge={async () => false} onAccessibilityActivate={() => {}} />); });
	const track = renderer!.root.findAll((node) => typeof node.props.onLayout === "function")[0];
	expect(track.props.style.minHeight).toBe(56);
	expect(track.props.style.height).toBeUndefined();
	expect(renderer!.root.findByType("Text" as never).props.numberOfLines).toBe(2);
});

it("fills the bar while the merge runs, with a spinner on the knob", () => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	act(() => { renderer = create(<SwipeToMerge label="Swipe to merge PR #1" busyLabel="Merging…" busy onMerge={async () => true} onAccessibilityActivate={() => {}} />); });
	const label = renderer!.root.findByType("Text" as never);
	expect(label.props.children).toBe("Merging…");
	expect(label.props.style).toContainEqual({ color: "#onStatus" });
	expect(renderer!.root.findAllByType("ActivityIndicator" as never)).toHaveLength(1);
	const control = renderer!.root.findAll((node) => node.props.accessible === true)[0];
	expect(control.props.accessibilityState).toEqual({ busy: true, disabled: true });
	expect(control.props.accessibilityActions).toEqual([]);
});
