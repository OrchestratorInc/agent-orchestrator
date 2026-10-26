import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { createElement, useImperativeHandle, useRef } from "react";
import { afterEach, expect, it, vi } from "vitest";
import type { WorkerRowInteractionProps } from "./worker-row-interaction.types";

const swipeable = vi.hoisted(() => ({ close: vi.fn() }));
// The full-swipe pan's callbacks and settings, captured from its builder.
const watch = vi.hoisted(() => ({ handlers: {} as Record<string, (event?: any) => void>, enabled: true }));
const haptics = vi.hoisted(() => ({ armed: vi.fn() }));

vi.mock("react-native", () => ({
	Platform: { OS: "ios" },
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 0.5 },
	View: "View",
}));
vi.mock("react-native-gesture-handler", () => {
	const pan = () => {
		const self: Record<string, unknown> = {
			enabled: (value: boolean) => { watch.enabled = value; return self; },
			activeOffsetX: () => self,
		};
		for (const method of ["onStart", "onUpdate", "onEnd", "onFinalize"]) {
			self[method] = (callback: (event?: any) => void) => { watch.handlers[method] = callback; return self; };
		}
		return self;
	};
	return {
		Gesture: { Pan: pan },
		GestureDetector: ({ children }: { children: React.ReactNode }) => createElement("GestureDetector", null, children),
	};
});
// Gesture Handler's swipeable, reduced to its props and its imperative close.
vi.mock("react-native-gesture-handler/ReanimatedSwipeable", () => ({
	default: function SwipeBoundary({ ref, ...props }: Record<string, any>) {
		useImperativeHandle(ref, () => ({ close: swipeable.close }));
		const translation = useRef({ value: -60 }).current;
		return createElement("SwipeBoundary", props, props.renderRightActions({ value: 0 }, translation), props.children);
	},
}));
vi.mock("react-native-reanimated", () => ({
	default: { View: "AnimatedView" },
	useSharedValue: (initial: unknown) => useRef({ value: initial }).current,
	useAnimatedStyle: (style: () => unknown) => style(),
}));
vi.mock("react-native-worklets", () => ({ scheduleOnRN: (fn: (...args: unknown[]) => void, ...args: unknown[]) => fn(...args) }));
vi.mock("./haptics", () => ({ haptics }));
vi.mock("./worker-rename-context-menu", () => ({ WorkerRowContextMenu: "ContextMenu" }));
vi.mock("./ThemeProvider", () => {
	const theme = { bgElevated: "elevated", borderSubtle: "divider", tintRed: "red-tint", red: "red", textSecondary: "secondary" };
	return { useTheme: () => theme, useThemedStyles: (factory: (value: typeof theme) => unknown) => factory(theme) };
});

import { WorkerRowInteraction } from "./worker-row-interaction";

let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	act(() => renderer?.unmount());
	swipeable.close.mockClear();
	haptics.armed.mockClear();
	watch.handlers = {};
});
const props: WorkerRowInteractionProps = {
	sessionId: "worker", enabled: true, children: <></>, rightActions: createElement("RailControls"),
	shellStyle: {}, foregroundStyle: {}, rowStyle: {},
	accessibilityLabel: "Worker", onPress() {},
	actions: [{ id: "open", title: "Open" }, { id: "delete", title: "Archive session", destructive: true }],
	onAction() {}, onSwipeOpen() {}, onSwipeClose() {},
};
function mount(over: Partial<WorkerRowInteractionProps> = {}) {
	Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
	act(() => { renderer = create(<WorkerRowInteraction {...props} {...over} />); });
	// The row is a phone's width.
	act(() => renderer!.root.find((node) => node.type === ("View" as never) && node.props.onLayout).props.onLayout({ nativeEvent: { layout: { width: 390 } } }));
	return renderer!;
}
const boundary = (view: ReactTestRenderer) => view.root.findByType("SwipeBoundary" as never);
const railControls = (view: ReactTestRenderer) => view.root.findAll((node) => node.type === ("RailControls" as never));
const drag = (translationX: number) => {
	act(() => watch.handlers.onStart());
	act(() => watch.handlers.onUpdate({ translationX }));
};
const release = () => {
	act(() => watch.handlers.onEnd());
	act(() => watch.handlers.onFinalize());
};

it("does not build the rail's controls behind a resting row but keeps the rail measurable", () => {
	const view = mount();
	expect(railControls(view)).toHaveLength(0);
	const rail = view.root.findAll((node) => node.type === ("View" as never) && node.props.style?.width === 128);
	expect(rail).toHaveLength(1);
	expect(rail[0].props.style).toMatchObject({ justifyContent: "center" });
});

it("mounts the controls when a drag starts and keeps them until the row settles closed", () => {
	const onSwipeOpen = vi.fn();
	const onSwipeClose = vi.fn();
	const view = mount({ onSwipeOpen, onSwipeClose });
	act(() => boundary(view).props.onSwipeableOpenStartDrag("left"));
	expect(railControls(view)).toHaveLength(1);
	act(() => boundary(view).props.onSwipeableWillOpen("left"));
	expect(onSwipeOpen).toHaveBeenCalledWith("worker", expect.any(Function));
	// A close request starts the motion; only its end may remove the controls.
	act(() => boundary(view).props.onSwipeableWillClose("right"));
	expect(railControls(view)).toHaveLength(1);
	act(() => boundary(view).props.onSwipeableClose("right"));
	expect(railControls(view)).toHaveLength(0);
	expect(onSwipeClose).toHaveBeenCalledWith("worker");
});

it("releases the controls after a short swipe snaps back without opening", () => {
	const view = mount();
	act(() => boundary(view).props.onSwipeableOpenStartDrag("left"));
	act(() => boundary(view).props.onSwipeableClose("right"));
	expect(railControls(view)).toHaveLength(0);
});

// A resting row has nothing to reveal on its left, so a rightward drag stays
// with the list and with iOS's edge-swipe back. An open row closes either way.
it("claims a rightward drag only while the rail is open", () => {
	const view = mount();
	expect(boundary(view).props.dragOffsetFromLeftEdge).toBeGreaterThan(1000);
	expect(boundary(view).props.dragOffsetFromRightEdge).toBe(16);
	act(() => boundary(view).props.onSwipeableWillOpen("left"));
	expect(boundary(view).props.dragOffsetFromLeftEdge).toBe(16);
	act(() => boundary(view).props.onSwipeableWillClose("right"));
	expect(boundary(view).props.dragOffsetFromLeftEdge).toBeGreaterThan(1000);
});

it("closes its rail when another row opens one", () => {
	const view = mount();
	act(() => { view.update(<WorkerRowInteraction {...props} activeSwipeId="other" />); });
	expect(swipeable.close).toHaveBeenCalled();
});

it("closes the rail before a menu action takes over", () => {
	const onAction = vi.fn();
	const view = mount({ onAction });
	act(() => view.root.findByType("ContextMenu" as never).props.onAction("rename"));
	expect(swipeable.close).toHaveBeenCalled();
	expect(onAction).toHaveBeenCalledWith("rename");
});

it("drops the menu, the swipe and the full swipe while the row is being renamed", () => {
	const view = mount({ enabled: false });
	expect(boundary(view).props.enabled).toBe(false);
	expect(view.root.findAllByType("ContextMenu" as never)).toHaveLength(0);
	expect(watch.enabled).toBe(false);
});

it("archives on a full swipe, with one haptic as it arms", () => {
	const onAction = vi.fn();
	const view = mount({ onAction });
	expect(boundary(view).props.overshootRight).toBe(true);
	drag(-150);
	expect(haptics.armed).not.toHaveBeenCalled();
	drag(-260);
	act(() => watch.handlers.onUpdate({ translationX: -300 }));
	expect(haptics.armed).toHaveBeenCalledTimes(1);
	release();
	expect(swipeable.close).toHaveBeenCalled();
	expect(onAction).toHaveBeenCalledWith("delete");
});

it("does not archive a drag that only opens the rail, or one carried back before release", () => {
	const onAction = vi.fn();
	mount({ onAction });
	drag(-140);
	release();
	drag(-300);
	act(() => watch.handlers.onUpdate({ translationX: -100 }));
	release();
	expect(onAction).not.toHaveBeenCalled();
});

it("measures a full swipe from the open rail when the drag starts there", () => {
	const onAction = vi.fn();
	const view = mount({ onAction });
	act(() => boundary(view).props.onSwipeableWillOpen("left"));
	// 128 already uncovered, plus 110 more, passes 60% of 390.
	drag(-110);
	release();
	expect(onAction).toHaveBeenCalledWith("delete");
});

// The fill follows the row past the rail. Armed, it takes the destructive tint
// and shows the archive glyph, so the state reads by shape as well as colour.
it("tints the uncovered rail and shows the archive glyph while a full swipe is armed", () => {
	const view = mount();
	const [fill, glyph] = view.root.findAllByType("AnimatedView" as never);
	expect(fill.props.style[1]).toMatchObject({ width: 128, backgroundColor: "elevated" });
	expect(glyph.props.style[1]).toEqual({ opacity: 0 });
	expect(glyph.findByType("SymbolView" as never).props).toMatchObject({ name: { ios: "archivebox", android: "archive" }, tintColor: "red" });
	drag(-300);
	act(() => view.update(<WorkerRowInteraction {...props} />));
	const [armedFill, armedGlyph] = view.root.findAllByType("AnimatedView" as never);
	expect(armedFill.props.style[1]).toMatchObject({ backgroundColor: "red-tint" });
	expect(armedGlyph.props.style[1]).toEqual({ opacity: 1 });
});
