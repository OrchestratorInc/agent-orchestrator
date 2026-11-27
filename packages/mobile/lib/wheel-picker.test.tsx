import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }), detent: vi.fn() }));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { StyleSheet: { create: (styles: object) => styles }, Text: host("Text"), View: host("View") };
});
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		// A list that builds its first rows, as the real one builds those near the band.
		default: {
			View: host("AnimatedView"),
			FlatList: ({ data, renderItem, keyExtractor, ListHeaderComponent, ListFooterComponent, ...props }: { data: number[]; renderItem: (info: { item: number }) => React.ReactNode; keyExtractor: (item: number) => string; ListHeaderComponent?: React.ReactNode; ListFooterComponent?: React.ReactNode }) =>
				React.createElement("AnimatedFlatList", { ...props, data }, ListHeaderComponent,
					...data.slice(0, 30).map((item) => React.createElement(React.Fragment, { key: keyExtractor(item) }, renderItem({ item }))),
					ListFooterComponent),
		},
		Extrapolation: { CLAMP: "clamp" },
		interpolate: () => 0,
		runOnJS: (fn: unknown) => fn,
		runOnUI: (fn: unknown) => fn,
		scrollTo: () => {},
		useAnimatedReaction: () => {},
		useAnimatedRef: () => ({ current: null }),
		useAnimatedScrollHandler: () => () => {},
		useAnimatedStyle: () => ({}),
		useSharedValue: (value: unknown) => ({ value }),
	};
});
vi.mock("lucide-react-native/icons/star", () => ({ default: "Star" }));
vi.mock("./AgentLogo", () => ({ AgentLogo: "AgentLogo" }));
vi.mock("./haptics", () => ({ haptics: { detent: fixtures.detent } }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme) }));

import { WHEEL_ROW, WheelPicker, wheelEntry, wheelLoop } from "./wheel-picker";

const items = [{ key: "opus", label: "Opus" }, { key: "sonnet", label: "Sonnet" }, { key: "haiku", label: "Haiku" }];

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); fixtures.detent.mockClear(); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

const hostType = (node: ReactTestInstance): string => (typeof node.type === "string" ? node.type : "");

describe("wheel picker", () => {
	// VoiceOver's and TalkBack's swipe up and down on an adjustable element.
	it("steps a row at a time for a screen reader, reads the row in the band, and stops at the ends", () => {
		const onSettle = vi.fn();
		act(() => { renderer = create(<WheelPicker items={items} index={0} onSettle={onSettle} accessibilityLabel="Model" />); });
		const wheel = () => renderer!.root.findAll((node) => hostType(node) === "AnimatedFlatList")[0];
		expect(wheel().props).toMatchObject({ accessible: true, accessibilityRole: "adjustable", accessibilityLabel: "Model", accessibilityValue: { text: "Opus" } });
		act(() => wheel().props.onAccessibilityAction({ nativeEvent: { actionName: "decrement" } }));
		expect(onSettle).not.toHaveBeenCalled();
		act(() => wheel().props.onAccessibilityAction({ nativeEvent: { actionName: "increment" } }));
		expect(onSettle).toHaveBeenLastCalledWith(1);
		act(() => renderer!.update(<WheelPicker items={items} index={2} onSettle={onSettle} accessibilityLabel="Model" />));
		expect(wheel().props.accessibilityValue).toEqual({ text: "Haiku" });
		act(() => wheel().props.onAccessibilityAction({ nativeEvent: { actionName: "increment" } }));
		expect(onSettle).toHaveBeenCalledOnce();
		act(() => wheel().props.onAccessibilityAction({ nativeEvent: { actionName: "decrement" } }));
		expect(onSettle).toHaveBeenLastCalledWith(1);
	});

	// Regression guard: the wheels sit over a focused composer. A scroll view's
	// default blurred the field on any tap on a wheel (its empty rows included,
	// right where a tap to put the picker away lands), dropping the keyboard.
	it("keeps the composer focused when a wheel is tapped", () => {
		act(() => { renderer = create(<WheelPicker items={items} index={0} onSettle={() => {}} />); });
		const wheel = renderer!.root.findAll((node) => hostType(node) === "AnimatedFlatList")[0];
		expect(wheel.props.keyboardShouldPersistTaps).toBe("always");
	});

	// The empty rows above the first item and below the last read as the chat
	// around the picker, so a tap there puts it away.
	it("puts the picker away on a tap on its empty rows, not on a drag or a tap that stops a coasting wheel", () => {
		const onEmptyTap = vi.fn();
		act(() => { renderer = create(<WheelPicker items={items} index={0} onSettle={() => {}} onEmptyTap={onEmptyTap} testID="w" />); });
		const empty = (where: "above" | "below") => renderer!.root.findAll((node) => hostType(node) === "View" && node.props.testID === `w-empty-${where}`)[0];
		const wheel = () => renderer!.root.findAll((node) => hostType(node) === "AnimatedFlatList")[0];
		act(() => { empty("above").props.onTouchStart(); empty("above").props.onTouchEnd(); });
		expect(onEmptyTap).toHaveBeenCalledOnce();
		act(() => { empty("below").props.onTouchStart(); empty("below").props.onTouchEnd(); });
		expect(onEmptyTap).toHaveBeenCalledTimes(2);
		// A drag cancels the touch rather than ending it.
		act(() => { empty("above").props.onTouchStart(); wheel().props.onScrollBeginDrag(); });
		expect(empty("above").props.onTouchCancel).toBeUndefined();
		expect(onEmptyTap).toHaveBeenCalledTimes(2);
		// A tap while the wheel coasts only stops it.
		act(() => { wheel().props.onMomentumScrollBegin(); empty("above").props.onTouchStart(); empty("above").props.onTouchEnd(); });
		expect(onEmptyTap).toHaveBeenCalledTimes(2);
	});

	it("takes no screen reader steps while locked", () => {
		const onSettle = vi.fn();
		act(() => { renderer = create(<WheelPicker items={items} index={0} onSettle={onSettle} disabled />); });
		const wheel = renderer!.root.findAll((node) => hostType(node) === "AnimatedFlatList")[0];
		act(() => wheel.props.onAccessibilityAction({ nativeEvent: { actionName: "increment" } }));
		expect(onSettle).not.toHaveBeenCalled();
	});

	// A wheel goes round: past the last entry comes the first again, either way.
	it("loops a list as long as the rows on show, resting far from either end", () => {
		const { copies, base } = wheelLoop(items.length);
		expect(copies % 2).toBe(1);
		// Far more rows either way than a fling travels, so it never jumps back.
		expect(base).toBeGreaterThanOrEqual(2000);
		expect(base).toBe(((copies - 1) / 2) * items.length);
		expect([-1, 0, 2, 3, 4].map((row) => wheelEntry(row, 3))).toEqual([2, 0, 2, 0, 1]);

		const onSettle = vi.fn();
		act(() => { renderer = create(<WheelPicker items={items} index={1} onSettle={onSettle} testID="w" />); });
		const wheel = () => renderer!.root.findAll((node) => hostType(node) === "AnimatedFlatList")[0];
		expect(wheel().props.contentOffset).toEqual({ x: 0, y: (base + 1) * WHEEL_ROW });
		// Laid out as copies, of which the list only builds the rows near the band.
		expect(wheel().props.data).toHaveLength(copies * items.length);
		expect(wheel().props.initialScrollIndex).toBe(base);
		// Flung up past Haiku, the last, it comes to rest on Opus, the first.
		act(() => wheel().props.onMomentumScrollEnd({ nativeEvent: { contentOffset: { y: (base + 3) * WHEEL_ROW } } }));
		expect(onSettle).toHaveBeenLastCalledWith(0);
		// And down past Opus onto Haiku.
		act(() => wheel().props.onMomentumScrollEnd({ nativeEvent: { contentOffset: { y: (base - 1) * WHEEL_ROW } } }));
		expect(onSettle).toHaveBeenLastCalledWith(2);
	});

	// Two entries over three rows would show one of them twice at once.
	it("does not loop a list shorter than the rows on show", () => {
		expect(wheelLoop(2)).toEqual({ copies: 1, base: 0 });
		const onSettle = vi.fn();
		act(() => { renderer = create(<WheelPicker items={items.slice(0, 2)} index={1} onSettle={onSettle} />); });
		const wheel = renderer!.root.findAll((node) => hostType(node) === "AnimatedFlatList")[0];
		act(() => wheel.props.onMomentumScrollEnd({ nativeEvent: { contentOffset: { y: 5 * WHEEL_ROW } } }));
		expect(onSettle).toHaveBeenLastCalledWith(1);
	});
});
