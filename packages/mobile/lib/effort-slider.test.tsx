import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }),
	detent: vi.fn(),
	reduceMotion: false,
	// When set, a glide leaves the blob where it was for the JS thread, as a
	// glide still under way on the UI thread does.
	midGlide: false,
	glides: [] as number[],
	setOptions: vi.fn(),
}));

vi.mock("expo-router", () => ({ useNavigation: () => ({ setOptions: fixtures.setOptions }) }));

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
		useSharedValue: (initial: unknown) => React.useRef((() => {
			let value = initial;
			return {
				get value() { return value; },
				set value(next: unknown) { if (typeof next !== "object" || next === null) value = next; },
			};
		})()).current,
		useAnimatedStyle: (style: () => unknown) => style,
		withTiming: (value: number) => {
			fixtures.glides.push(value);
			return fixtures.midGlide ? { glidingTo: value } : value;
		},
	};
});

import { EffortSlider, EffortSliderOverlay } from "./effort-slider";
import { markTouchInside } from "./touchOutside";

const levels = (selected?: string) => ["low", "medium", "high", "xhigh", "max"].map((value) => ({ value, label: value[0].toUpperCase() + value.slice(1), selected: value === selected }));

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { fixtures.detent.mockClear(); fixtures.setOptions.mockClear(); fixtures.midGlide = false; fixtures.glides = []; });
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
		// The track sits 100 in from the page's edge.
		touch: (x: number) => ({ nativeEvent: { locationX: x, pageX: 100 + x } }),
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

	it("lands a quick tap on the tapped level, not back where the blob was mid-glide", async () => {
		const { track, touch, onChoose } = await render("low");
		fixtures.midGlide = true;
		await act(async () => track().props.onResponderGrant(touch(220)));
		await act(async () => track().props.onResponderRelease());
		expect(onChoose.mock.calls.map(([value]) => value)).toEqual(["max"]);
		expect(fixtures.glides.at(-1)).toBe(220);
	});

	it("follows the finger by its page position, whatever view iOS measures it against", async () => {
		const { track, touch, label } = await render("low");
		await act(async () => track().props.onResponderGrant(touch(20)));
		// Over another view, iOS's locationX can read near zero.
		await act(async () => track().props.onResponderMove({ nativeEvent: { locationX: 4, pageX: 100 + 170 } }));
		expect(label()).toBe("Xhigh");
	});

	it("floats as a card whose touches are its own, never a tap outside it", async () => {
		await act(async () => { renderer = create(<EffortSliderOverlay choices={levels("high")} onChoose={() => {}} bottom="100%" />); });
		const card = renderer!.root.findAll((node) => hostType(node) === "AnimatedView")[0];
		expect(card.props.style[0]).toMatchObject({ position: "absolute", backgroundColor: "#bgElevated" });
		expect(card.props.style[1]).toEqual({ bottom: "100%" });
		expect(card.props.onTouchStart).toBe(markTouchInside);
	});

	it("holds the screen's back swipe while it is up, so a drag to the right stays on the slider", async () => {
		await act(async () => { renderer = create(<EffortSliderOverlay choices={levels("low")} onChoose={() => {}} bottom="100%" />); });
		expect(fixtures.setOptions.mock.calls).toEqual([[{ gestureEnabled: false }]]);
		await act(async () => renderer!.unmount());
		renderer = undefined;
		expect(fixtures.setOptions.mock.calls.at(-1)).toEqual([{ gestureEnabled: true }]);
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
