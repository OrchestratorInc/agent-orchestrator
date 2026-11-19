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
// The pan's builder keeps its callbacks, so a test can play a drag through them.
vi.mock("react-native-gesture-handler", async () => {
	const React = await import("react");
	const pan = () => {
		const handlers: Record<string, (event?: { x: number }) => void> = {};
		const builder = {
			handlers,
			runOnJS: () => builder,
			minDistance: (distance: number) => { handlers.minDistance = () => distance; return builder; },
			onBegin: (fn: () => void) => { handlers.begin = fn; return builder; },
			onUpdate: (fn: () => void) => { handlers.update = fn; return builder; },
			onFinalize: (fn: () => void) => { handlers.finalize = fn; return builder; },
		};
		return builder;
	};
	return {
		Gesture: { Pan: pan },
		GestureDetector: ({ gesture, children }: { gesture: unknown; children?: React.ReactNode }) => React.createElement("GestureDetector", { gesture }, children),
	};
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
	// The blob is the last animated view; its style function reads the shared value.
	const blobCentre = () => {
		const style = root.findAll((node) => hostType(node) === "AnimatedView").at(-1)!.props.style[1]() as { transform: { translateX: number }[] };
		return style.transform[0].translateX + 16;
	};
	// The pan's callbacks, from the latest render; `x` is from the track's left edge.
	const pan = () => (root.findAll((node) => hostType(node) === "GestureDetector")[0].props.gesture as { handlers: Record<string, (event?: { x: number }) => unknown> }).handlers;
	const drag = {
		begin: (x: number) => { pan().begin({ x }); },
		move: (x: number) => { pan().update({ x }); },
		end: () => { pan().finalize(); },
	};
	return {
		track,
		pan,
		drag,
		label: () => root.findAll((node) => hostType(node) === "Text")[0].props.children,
		blobCentre,
		onChoose,
		onRelease,
	};
}

describe("effort slider", () => {
	it("pulls the blob toward the nearest level while dragging, ticking as it lands on each", async () => {
		const { drag, blobCentre, label, onChoose } = await render("high");
		await act(async () => drag.begin(120));
		// A little off a level, the blob barely moves.
		await act(async () => drag.move(130));
		expect(blobCentre()).toBeCloseTo(124);
		expect(onChoose).not.toHaveBeenCalled();
		// Past halfway it is drawn onto the next one.
		await act(async () => drag.move(150));
		expect(blobCentre()).toBeCloseTo(154);
		await act(async () => drag.move(236));
		expect(blobCentre()).toBe(220);
		expect(onChoose.mock.calls.map(([value]) => value)).toEqual(["xhigh", "max"]);
		expect(fixtures.detent).toHaveBeenCalledTimes(2);
		expect(label()).toBe("Max");
	});

	it("glides onto the level when let go, and says the level is settled", async () => {
		const { drag, blobCentre, onRelease } = await render("high");
		await act(async () => drag.begin(60));
		await act(async () => drag.move(82));
		expect(blobCentre()).not.toBe(70);
		expect(onRelease).not.toHaveBeenCalled();
		await act(async () => drag.end());
		expect(blobCentre()).toBe(70);
		expect(onRelease).toHaveBeenCalledOnce();
	});

	it("lands a quick tap on the tapped level, not back where the blob was mid-glide", async () => {
		const { drag, onChoose } = await render("low");
		fixtures.midGlide = true;
		await act(async () => drag.begin(220));
		await act(async () => drag.end());
		expect(onChoose.mock.calls.map(([value]) => value)).toEqual(["max"]);
		expect(fixtures.glides.at(-1)).toBe(220);
	});

	// On Android the conversation under the floating slider took the drag as a
	// scroll. A pan with no distance to travel holds the touch from touch down.
	it("claims the touch as it lands, before the list under it can scroll", async () => {
		const { pan, drag, label, onRelease } = await render("low");
		expect(pan().minDistance()).toBe(0);
		await act(async () => drag.begin(170));
		expect(label()).toBe("Xhigh");
		await act(async () => drag.end());
		// A pan that ends twice (cancelled, then finalized) settles once.
		await act(async () => drag.end());
		expect(onRelease).toHaveBeenCalledOnce();
	});

	it("keeps its own touches from counting as a tap outside it", async () => {
		await act(async () => { renderer = create(<EffortSliderOverlay choices={levels("high")} onChoose={() => {}} bottom="100%" />); });
		expect(renderer!.root.findAll((node) => hostType(node) === "AnimatedView")[0].props.onTouchStart).toBe(markTouchInside);
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
