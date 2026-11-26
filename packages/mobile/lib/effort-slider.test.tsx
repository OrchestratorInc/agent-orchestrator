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
	os: "ios",
}));

vi.mock("expo-router", () => ({ useNavigation: () => ({ setOptions: fixtures.setOptions }) }));

vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme) }));
vi.mock("./haptics", () => ({ haptics: { detent: fixtures.detent, select: () => {} } }));
vi.mock("lucide-react-native/icons/zap", () => ({ default: "Zap" }));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./effort-slider-veil", () => ({ EffortSliderVeil: "EffortSliderVeil" }));
vi.mock("./gradient-text", async () => {
	const React = await import("react");
	return { GradientText: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("GradientText", props, children) };
});
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => fixtures.reduceMotion }));
// Over the keyboard, the overlay is a window of its own; here it is drawn in place.
vi.mock("react-native-keyboard-controller", async () => {
	const React = await import("react");
	return {
		OverKeyboardView: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("OverKeyboardView", props, children),
		useReanimatedKeyboardAnimation: () => ({ height: { value: 0 }, progress: { value: 0 } }),
	};
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 0, left: 0, right: 0 }) }));
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { BackHandler: { addEventListener: () => ({ remove: () => {} }) }, Platform: { get OS() { return fixtures.os; } }, StyleSheet: { create: (styles: object) => styles, absoluteFill: {} }, Pressable: host("Pressable"), Text: host("Text"), View: host("View"), useWindowDimensions: () => ({ width: 400, height: 800 }) };
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
		GestureHandlerRootView: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("GestureHandlerRootView", props, children),
	};
});
// The drag's worklets call JS through scheduleOnRN; here it calls at once.
vi.mock("react-native-worklets", () => ({ scheduleOnRN: (fn: (...args: unknown[]) => void, ...args: unknown[]) => fn(...args) }));
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
		interpolateColor: (value: number, input: number[], output: string[]) => (value <= input[0] ? output[0] : output[output.length - 1]),
		withTiming: (value: number) => {
			fixtures.glides.push(value);
			return fixtures.midGlide ? { glidingTo: value } : value;
		},
	};
});

import { EffortSlider, EffortSliderOverlay } from "./effort-slider";
import { markTouchInside, rootTouchStart } from "./touchOutside";

const levels = (selected?: string) => ["low", "medium", "high", "xhigh", "max"].map((value) => ({ value, label: value[0].toUpperCase() + value.slice(1), selected: value === selected }));

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { fixtures.detent.mockClear(); fixtures.setOptions.mockClear(); fixtures.midGlide = false; fixtures.glides = []; fixtures.os = "ios"; fixtures.reduceMotion = false; });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; });

const hostType = (node: ReactTestInstance): string => (typeof node.type === "string" ? node.type : "");

async function render(selected?: string) {
	const onChoose = vi.fn();
	const onRelease = vi.fn();
	await act(async () => { renderer = create(<EffortSlider choices={levels(selected)} onChoose={onChoose} onRelease={onRelease} />); });
	const root = renderer!.root;
	const track = () => root.findAll((node) => hostType(node) === "View" && node.props.testID === "effort-slider")[0];
	// A 240-wide track: stops at 20, 70, 120, 170, 220.
	// The track measures itself; the card around it carries the accessibility.
	const trackView = () => root.findAll((node) => hostType(node) === "View" && node.props.testID === "effort-slider-track")[0];
	await act(async () => trackView().props.onLayout({ nativeEvent: { layout: { width: 240 } } }));
	// The blob is the last animated view; its style function reads the shared value.
	const blobCentre = () => {
		const style = root.findAll((node) => hostType(node) === "AnimatedView" && node.props.testID === "effort-slider-blob")[0].props.style[1]() as { left: number };
		return style.left + 16;
	};
	// The pan's callbacks, from the latest render; `x` is from the track's left edge.
	const pan = () => (root.findAll((node) => hostType(node) === "GestureDetector")[0].props.gesture as { handlers: Record<string, (event?: { x: number }) => unknown> }).handlers;
	const drag = {
		begin: (x: number) => { pan().begin({ x }); },
		move: (x: number) => { pan().update({ x }); },
		end: () => { pan().finalize(); },
	};
	return {
		root,
		track,
		pan,
		drag,
		label: () => root.findAll((node) => hostType(node) === "Text" && node.props.testID === "effort-slider-value")[0].props.children,
		blobCentre,
		onChoose,
		onRelease,
	};
}

describe("effort slider", () => {
	it("pulls the blob toward the nearest level while dragging, ticking as it lands on each", async () => {
		// Positions only: no touch-down glide to catch up from.
		fixtures.reduceMotion = true;
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
		// Each level crossed shows and ticks; only the release chooses, once.
		expect(fixtures.detent).toHaveBeenCalledTimes(2);
		expect(label()).toBe("Max");
		expect(onChoose).not.toHaveBeenCalled();
		await act(async () => drag.end());
		expect(onChoose.mock.calls.map(([value]) => value)).toEqual(["max"]);
	});

	// The blob and the fill move by layout, in the same commit; the lit dots
	// ride inside the fill, clipped by it, so a dot is lit exactly where the
	// fill covers it.
	it("moves the fill with the blob, the lit dots clipped inside it", async () => {
		fixtures.reduceMotion = true;
		const { drag, root } = await render("low");
		const fill = () => root.findAll((node) => hostType(node) === "AnimatedView" && node.props.testID === "effort-slider-fill")[0];
		const fillWidth = () => (fill().props.style[1]() as { width: number }).width;
		// On Low (20): to just past the blob's right edge.
		expect(fillWidth()).toBe(20 + 16 + 4);
		await act(async () => { drag.begin(20); drag.move(170); });
		expect(fillWidth()).toBe(170 + 16 + 4);
		// A faint dot per level on the track, and a lit one per level inside the fill.
		const lit = fill().findAll((node) => hostType(node) === "View").map((dot) => (dot.props.style[1] as { backgroundColor: string }).backgroundColor);
		expect(lit).toEqual(Array(5).fill("#textSecondary"));
	});

	// The touch-down glide is still running when the finger moves on: the blob
	// closes on the finger over a few moves, then holds it, never trailing.
	it("catches the blob up to a moving finger after touch down, then holds it", async () => {
		const { drag, blobCentre } = await render("low");
		await act(async () => drag.begin(170));
		await act(async () => drag.move(170));
		const first = blobCentre();
		expect(first).toBeGreaterThanOrEqual(170 - 1);
		for (let i = 0; i < 12; i++) await act(async () => drag.move(170));
		expect(blobCentre()).toBe(170);
	});

	// A re-render while the blob moves on the UI thread hands React's stale
	// position back to the native view; the track must hold still through the
	// label changing at every level crossed.
	it("keeps the track from re-rendering while the level changes under a finger", async () => {
		fixtures.reduceMotion = true;
		const { drag, root, label } = await render("low");
		const blob = () => root.findAll((node) => hostType(node) === "AnimatedView" && node.props.testID === "effort-slider-blob")[0];
		const before = blob().props.style;
		await act(async () => { drag.begin(20); drag.move(170); });
		expect(label()).toBe("Xhigh");
		// The same style array: the blob's element was not rebuilt.
		expect(blob().props.style).toBe(before);
	});

	// Claude Code's Ultracode is drawn in desktop's gradient, the other levels plainly.
	it("draws Ultracode in desktop's gradient, and other levels plainly", async () => {
		const withUltra = (selected: string) => [...levels(selected === "ultracode" ? undefined : selected), { value: "ultracode", label: "Ultracode", selected: selected === "ultracode" }];
		await act(async () => { renderer = create(<EffortSlider choices={withUltra("ultracode")} onChoose={() => {}} />); });
		const gradient = () => renderer!.root.findAll((node) => hostType(node) === "GradientText");
		expect(gradient()).toHaveLength(1);
		expect(gradient()[0].props.children).toBe("Ultracode");
		await act(async () => renderer!.update(<EffortSlider choices={withUltra("high")} onChoose={() => {}} />));
		expect(gradient()).toHaveLength(0);
	});

	// Closed mid-drag (the keyboard, back), the level the finger was on is
	// still chosen, before the host hears the close.
	it("chooses the level under the finger when closed mid-drag", async () => {
		const { drag, onChoose } = await render("low");
		await act(async () => { drag.begin(20); drag.move(170); });
		expect(onChoose).not.toHaveBeenCalled();
		act(() => renderer!.unmount());
		renderer = undefined;
		expect(onChoose.mock.calls.map(([value]) => value)).toEqual(["xhigh"]);
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
		const overlay = renderer!.root.findAll((node) => hostType(node) === "AnimatedView")[0];
		expect(overlay.props.onTouchStart).toBe(markTouchInside);
		// A touch on the chat beside the card, or through the veil, is outside it.
		expect(overlay.props.pointerEvents).toBe("box-none");
		expect(overlay.findAll((node) => hostType(node) === "EffortSliderVeil")).toHaveLength(1);
	});

	it("holds the screen's back swipe while it is up, so a drag to the right stays on the slider", async () => {
		await act(async () => { renderer = create(<EffortSliderOverlay choices={levels("low")} onChoose={() => {}} bottom="100%" />); });
		expect(fixtures.setOptions.mock.calls).toEqual([[{ gestureEnabled: false }]]);
		await act(async () => renderer!.unmount());
		renderer = undefined;
		expect(fixtures.setOptions.mock.calls.at(-1)).toEqual([{ gestureEnabled: true }]);
	});

	// The option is iOS's alone, and setting it re-renders the whole screen
	// under the slider as it opens.
	it("leaves the screen's options alone on Android, which has no back swipe to hold", async () => {
		fixtures.os = "android";
		await act(async () => { renderer = create(<EffortSliderOverlay choices={levels("low")} onChoose={() => {}} bottom="100%" />); });
		await act(async () => renderer!.unmount());
		renderer = undefined;
		expect(fixtures.setOptions).not.toHaveBeenCalled();
	});

	it("heads the track with what it sets, then the level it is on, kept from screen readers", async () => {
		await render("medium");
		const texts = renderer!.root.findAll((node) => hostType(node) === "Text").map((node) => node.props.children);
		expect(texts).toEqual(["Effort", "Medium"]);
		const heading = renderer!.root.findAll((node) => hostType(node) === "View" && node.props.importantForAccessibility === "no-hide-descendants")[0];
		expect(heading.findAll((node) => hostType(node) === "AppSymbol")[0].props.name).toBe("chevronForward");
	});

	// Fast mode as a bolt at the heading's right, filled while on, as desktop marks it.
	it("flips fast mode from a bolt beside the heading, filled while it is on", async () => {
		const toggle = vi.fn();
		await act(async () => { renderer = create(<EffortSlider choices={levels("medium")} onChoose={() => {}} fast={{ on: false, toggle }} />); });
		const bolt = () => renderer!.root.findAll((node) => hostType(node) === "Pressable" && node.props.testID === "effort-slider-fast")[0];
		const zap = () => renderer!.root.findAll((node) => hostType(node) === "Zap")[0];
		expect(bolt().props.accessibilityState).toEqual({ checked: false });
		expect(zap().props).toMatchObject({ color: "#textSecondary", fill: "none" });
		act(() => bolt().props.onPress());
		expect(toggle).toHaveBeenCalledOnce();
		await act(async () => renderer!.update(<EffortSlider choices={levels("medium")} onChoose={() => {}} fast={{ on: true, toggle }} />));
		expect(bolt().props.accessibilityState).toEqual({ checked: true });
		expect(zap().props).toMatchObject({ color: "#amber", fill: "#amber" });
	});

	it("draws no bolt for a model with no fast mode", async () => {
		await render("medium");
		expect(renderer!.root.findAll((node) => hostType(node) === "Zap")).toHaveLength(0);
	});

	// Kept mounted between opens: hidden, it takes no touch, reads to no
	// screen reader, and leaves the screen's back swipe alone.
	// As ChatGPT's: over the keyboard, in a window of its own, so the keys stay
	// up and the field keeps its focus; a tap anywhere else only puts it away.
	it("opens over the keyboard in its own window, any other tap putting it away", async () => {
		const onDismiss = vi.fn();
		await act(async () => { renderer = create(<EffortSliderOverlay overKeyboard onDismiss={onDismiss} choices={levels("low")} onChoose={() => {}} />); });
		const window = renderer!.root.findAll((node) => hostType(node) === "OverKeyboardView")[0];
		expect(window.props.visible).toBe(true);
		// A touch in the window still bubbles to the app root through React's
		// tree: the content's touches are marked as its own, so they close nothing.
		const content = window.findAll((node) => hostType(node) === "View" && typeof node.props.onTouchStart === "function")[0];
		act(() => { content.props.onTouchStart(); rootTouchStart(); });
		expect(onDismiss).not.toHaveBeenCalled();
		const backdrop = window.findAll((node) => hostType(node) === "Pressable")[0];
		act(() => backdrop.props.onPressIn());
		expect(onDismiss).toHaveBeenCalledOnce();
		// The window brings its own gesture handler root, so the drag runs on
		// the UI thread there as anywhere else.
		const gestureRoot = window.findAll((node) => hostType(node) === "GestureHandlerRootView")[0];
		expect(gestureRoot.findAll((node) => hostType(node) === "GestureDetector")).toHaveLength(1);
		await act(async () => renderer!.update(<EffortSliderOverlay overKeyboard visible={false} onDismiss={onDismiss} choices={levels("low")} onChoose={() => {}} />));
		expect(renderer!.root.findAll((node) => hostType(node) === "OverKeyboardView")[0].props.visible).toBe(false);
	});

	it("takes no touches and holds nothing while hidden", async () => {
		await act(async () => { renderer = create(<EffortSliderOverlay visible={false} choices={levels("low")} onChoose={() => {}} bottom="100%" />); });
		const overlay = () => renderer!.root.findAll((node) => hostType(node) === "AnimatedView")[0];
		expect(overlay().props.pointerEvents).toBe("none");
		expect(overlay().props.onTouchStart).toBeUndefined();
		expect(overlay().props.importantForAccessibility).toBe("no-hide-descendants");
		expect(fixtures.setOptions).not.toHaveBeenCalled();
		await act(async () => renderer!.update(<EffortSliderOverlay visible choices={levels("low")} onChoose={() => {}} bottom="100%" />));
		expect(overlay().props.pointerEvents).toBe("box-none");
		expect(fixtures.setOptions.mock.calls).toEqual([[{ gestureEnabled: false }]]);
		await act(async () => renderer!.update(<EffortSliderOverlay visible={false} choices={levels("low")} onChoose={() => {}} bottom="100%" />));
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

	// Kept mounted across model changes; a model with fewer levels, or none,
	// must not read past them before the slider catches up.
	it("survives the choices shrinking under a kept level, and re-places the blob", async () => {
		const { label, blobCentre } = await render("max");
		expect(label()).toBe("Max");
		const two = [{ value: "low", label: "Low", selected: false }, { value: "high", label: "High", selected: true }];
		await act(async () => renderer!.update(<EffortSlider choices={two} onChoose={() => {}} />));
		expect(label()).toBe("High");
		// Two stops on the 240 track: 20 and 220.
		expect(blobCentre()).toBe(220);
		await act(async () => renderer!.update(<EffortSlider choices={[]} onChoose={() => {}} />));
		expect(label()).toBe("Default");
	});

	it("re-places the blob when the level count changes under the same index", async () => {
		const { blobCentre } = await render("medium");
		// Index 1 of five on the 240 track sits at 70.
		expect(blobCentre()).toBe(70);
		const three = [{ value: "low", label: "Low", selected: false }, { value: "medium", label: "Medium", selected: true }, { value: "high", label: "High", selected: false }];
		await act(async () => renderer!.update(<EffortSlider choices={three} onChoose={() => {}} />));
		expect(blobCentre()).toBe(120);
	});
});
