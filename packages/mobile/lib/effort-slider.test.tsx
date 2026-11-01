import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }),
	detent: vi.fn(),
	reduceMotion: false,
}));

vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme) }));
vi.mock("./haptics", () => ({ haptics: { detent: fixtures.detent } }));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => fixtures.reduceMotion }));
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { BackHandler: { addEventListener: () => ({ remove: () => {} }) }, StyleSheet: { create: (styles: object) => styles }, Text: host("Text"), View: host("View") };
});
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	return {
		default: { View: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("AnimatedView", props, children) },
		Easing: { out: (value: unknown) => value, cubic: null },
		FadeIn: { duration: () => "fade-in" },
		FadeOut: { duration: () => "fade-out" },
		useSharedValue: (value: unknown) => React.useRef({ value }).current,
		useAnimatedStyle: (style: () => unknown) => style,
		withTiming: (value: number) => value,
	};
});

import { EffortSlider, EffortSliderOverlay } from "./effort-slider";
import { markTouchInside } from "./touchOutside";

const levels = (selected?: string) => ["low", "medium", "high", "xhigh", "max"].map((value) => ({ value, label: value[0].toUpperCase() + value.slice(1), selected: value === selected }));

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { fixtures.detent.mockClear(); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; });

const hostType = (node: ReactTestInstance): string => (typeof node.type === "string" ? node.type : "");

async function render(selected?: string) {
	const onChoose = vi.fn();
	const onRelease = vi.fn();
	await act(async () => { renderer = create(<EffortSlider choices={levels(selected)} onChoose={onChoose} onRelease={onRelease} />); });
	const root = renderer!.root;
	const track = () => root.findAll((node) => hostType(node) === "View" && node.props.testID === "effort-slider")[0];
	// A 240-wide track: stops at 20, 70, 120, 170, 220.
	await act(async () => track().props.onLayout({ nativeEvent: { layout: { width: 240 } } }));
	const animated = () => root.findAll((node) => hostType(node) === "AnimatedView");
	// The blob is the last animated view; its style function reads the shared value.
	const blobCentre = () => {
		const style = animated().at(-1)!.props.style[1]() as { transform: { translateX: number }[] };
		return style.transform[0].translateX + 16;
	};
	return {
		track,
		touch: (x: number) => ({ nativeEvent: { locationX: x } }),
		label: () => root.findAll((node) => hostType(node) === "Text")[0].props.children,
		dots: () => root.findAll((node) => hostType(node) === "View" && node.props.pointerEvents === "none"),
		animated,
		blobCentre,
		onChoose,
		onRelease,
	};
}

describe("effort slider", () => {
	it("draws a fat track with a dot per level, lit in the theme's accent up to the blob", async () => {
		const { track, dots, animated, blobCentre, label } = await render("high");
		expect(track().props.style).toMatchObject({ height: 40, borderRadius: 20, backgroundColor: "#bgElevatedHover" });
		expect(dots()).toHaveLength(5);
		expect(dots().map((dot) => dot.props.style[1].backgroundColor)).toEqual(["#textSecondary", "#textSecondary", "#textSecondary", "#textFaint", "#textFaint"]);
		expect(animated()[0].props.style[0]).toMatchObject({ backgroundColor: "#accentBorder" });
		expect(animated()[1].props.style[0]).toMatchObject({ width: 32, height: 32, backgroundColor: "#ffffff" });
		expect(blobCentre()).toBe(120);
		expect(label()).toBe("High");
	});

	it("pulls the blob toward the nearest level while dragging, ticking as it lands on each", async () => {
		const { track, touch, blobCentre, label, onChoose } = await render("high");
		await act(async () => track().props.onResponderGrant(touch(120)));
		// A little off a level, the blob barely moves.
		await act(async () => track().props.onResponderMove(touch(130)));
		expect(blobCentre()).toBeCloseTo(124);
		expect(onChoose).not.toHaveBeenCalled();
		// Past halfway it is drawn onto the next one.
		await act(async () => track().props.onResponderMove(touch(150)));
		expect(blobCentre()).toBeCloseTo(154);
		await act(async () => track().props.onResponderMove(touch(236)));
		expect(blobCentre()).toBe(220);
		expect(onChoose.mock.calls.map(([value]) => value)).toEqual(["xhigh", "max"]);
		expect(fixtures.detent).toHaveBeenCalledTimes(2);
		expect(label()).toBe("Max");
	});

	it("glides onto the level when let go, and says the level is settled", async () => {
		const { track, touch, blobCentre, onRelease } = await render("high");
		await act(async () => track().props.onResponderGrant(touch(60)));
		await act(async () => track().props.onResponderMove(touch(82)));
		expect(blobCentre()).not.toBe(70);
		expect(onRelease).not.toHaveBeenCalled();
		await act(async () => track().props.onResponderRelease());
		expect(blobCentre()).toBe(70);
		expect(onRelease).toHaveBeenCalledOnce();
	});

	it("floats as a card whose touches are its own, never a tap outside it", async () => {
		await act(async () => { renderer = create(<EffortSliderOverlay choices={levels("high")} onChoose={() => {}} bottom="100%" />); });
		const card = renderer!.root.findAll((node) => hostType(node) === "AnimatedView")[0];
		expect(card.props.style[0]).toMatchObject({ position: "absolute", backgroundColor: "#bgElevated" });
		expect(card.props.style[1]).toEqual({ bottom: "100%" });
		expect(card.props.onTouchStart).toBe(markTouchInside);
	});

	it("steps a level at a time for a screen reader, and stops at the ends", async () => {
		const { track, onChoose } = await render("xhigh");
		expect(track().props.accessibilityRole).toBe("adjustable");
		expect(track().props.accessibilityValue).toEqual({ text: "Xhigh" });
		await act(async () => track().props.onAccessibilityAction({ nativeEvent: { actionName: "increment" } }));
		await act(async () => track().props.onAccessibilityAction({ nativeEvent: { actionName: "increment" } }));
		expect(onChoose.mock.calls.map(([value]) => value)).toEqual(["max"]);
	});
});
