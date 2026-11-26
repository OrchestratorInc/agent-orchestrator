import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { createElement, useRef } from "react";
import { afterEach, expect, it, vi } from "vitest";
import type { WorkerRowInteractionProps } from "./worker-row-interaction.types";

// The row's gestures as built on its last render: the pan's settings and
// callbacks, the tap's, and how many detectors the row mounted.
const gestures = vi.hoisted(() => ({
	pan: { handlers: {} as Record<string, (event?: any, success?: boolean) => void>, enabled: true, activeOffsetX: [] as number[] },
	tap: { handlers: {} as Record<string, (event?: any, success?: boolean) => void>, enabled: true },
	race: [] as string[],
}));
// Each spring the row starts, finished only when the test says so.
const springs = vi.hoisted(() => ({ pending: [] as { to: number; done?: (finished: boolean) => void }[] }));
const haptics = vi.hoisted(() => ({ armed: vi.fn() }));

vi.mock("react-native", () => ({
	Platform: { OS: "ios" },
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 0.5, absoluteFill: { position: "absolute" } },
	View: "View",
}));
vi.mock("react-native-gesture-handler", () => {
	const builder = (kind: "pan" | "tap") => () => {
		const record = gestures[kind];
		// Cleared in place: the tests hold on to this object.
		for (const name of Object.keys(record.handlers)) delete record.handlers[name];
		const self: Record<string, unknown> = { kind };
		self.enabled = (value: boolean) => { record.enabled = value; return self; };
		self.activeOffsetX = (value: number[]) => { gestures.pan.activeOffsetX = value; return self; };
		self.shouldCancelWhenOutside = () => self;
		for (const method of ["onStart", "onUpdate", "onEnd", "onFinalize"]) {
			self[method] = (callback: (event?: any, success?: boolean) => void) => { record.handlers[method] = callback; return self; };
		}
		return self;
	};
	return {
		Gesture: {
			Pan: builder("pan"),
			Tap: builder("tap"),
			Race: (...parts: { kind: string }[]) => { gestures.race = parts.map((part) => part.kind); return { race: parts }; },
		},
		GestureDetector: ({ children }: { children: React.ReactNode }) => createElement("GestureDetector", null, children),
	};
});
vi.mock("react-native-reanimated", () => ({
	default: { View: "AnimatedView" },
	useSharedValue: (initial: unknown) => useRef({ value: initial }).current,
	// Read when the test looks, as the UI thread would draw it.
	useAnimatedStyle: (style: () => Record<string, unknown>) => Object.defineProperties({}, Object.fromEntries(
		Object.keys(style()).map((key) => [key, { enumerable: true, get: () => style()[key] }]),
	)),
	useDerivedValue: (value: () => unknown) => ({ value: value() }),
	withTiming: (value: unknown) => value,
	withSpring: (to: number, _config: unknown, done?: (finished: boolean) => void) => { springs.pending.push({ to, done }); return to; },
	Easing: { out: (easing: unknown) => easing, cubic: "cubic" },
	ReduceMotion: { System: "system" },
	cancelAnimation: () => {},
}));
vi.mock("react-native-worklets", () => ({
	scheduleOnRN: (fn: (...args: unknown[]) => void, ...args: unknown[]) => fn(...args),
	scheduleOnUI: (fn: (...args: unknown[]) => void, ...args: unknown[]) => fn(...args),
}));
vi.mock("./haptics", () => ({ haptics }));
vi.mock("./worker-rename-context-menu", () => ({ WorkerRowContextMenu: "ContextMenu" }));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./ThemeProvider", () => {
	const theme = { bgBase: "base", red: "red", onDestructive: "on-red" };
	return { useTheme: () => theme, useThemedStyles: (factory: (value: typeof theme) => unknown) => factory(theme) };
});

import { WorkerRowInteraction } from "./worker-row-interaction";

let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	act(() => renderer?.unmount());
	haptics.armed.mockClear();
	springs.pending = [];
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
const railControls = (view: ReactTestRenderer) => view.root.findAll((node) => node.type === ("RailControls" as never));
const animatedLayers = (view: ReactTestRenderer) => view.root.findAll((node) => node.type === ("AnimatedView" as never));
const foreground = (view: ReactTestRenderer) => view.root.find((node) => node.type === ("AnimatedView" as never) && node.props.testID === "worker-row-foreground");
const pan = gestures.pan.handlers;
const begin = () => act(() => pan.onStart());
const move = (translationX: number) => act(() => pan.onUpdate({ translationX }));
// Gesture Handler ends an active pan with success true when the finger lifts,
// and false when the system or another handler cancels it.
const release = (translationX: number, success = true, velocityX = 0) => {
	act(() => pan.onEnd({ translationX, velocityX }, success));
	act(() => pan.onFinalize({}, success));
};
const drag = (translationX: number, velocityX = 0) => {
	begin();
	move(translationX);
	release(translationX, true, velocityX);
};
// Lets every spring started so far reach its target.
const settle = () => act(() => {
	const done = springs.pending;
	springs.pending = [];
	done.forEach((spring) => spring.done?.(true));
});

// A board switch builds every row it shows, so a resting row must cost little
// more than its content: one detector, and no rail until a drag uncovers it.
it("mounts one gesture detector and no rail while the row rests", () => {
	const view = mount();
	expect(view.root.findAll((node) => node.type === ("GestureDetector" as never))).toHaveLength(1);
	expect(gestures.race).toEqual(["pan", "tap"]);
	expect(railControls(view)).toHaveLength(0);
	expect(animatedLayers(view)).toHaveLength(1);
});

it("mounts the rail when a drag starts and keeps it until the row settles closed", () => {
	const onSwipeOpen = vi.fn();
	const onSwipeClose = vi.fn();
	const view = mount({ onSwipeOpen, onSwipeClose });
	begin();
	expect(railControls(view)).toHaveLength(1);
	move(-60);
	release(-60);
	expect(onSwipeOpen).toHaveBeenCalledWith("worker", expect.any(Function));
	settle();
	// A tap on the open row closes it; only the end of that motion removes the rail.
	act(() => gestures.tap.handlers.onStart());
	expect(railControls(view)).toHaveLength(1);
	expect(onSwipeClose).not.toHaveBeenCalled();
	settle();
	expect(railControls(view)).toHaveLength(0);
	expect(onSwipeClose).toHaveBeenCalledWith("worker");
});

it("releases the rail after a short swipe snaps back without opening", () => {
	const onSwipeOpen = vi.fn();
	const view = mount({ onSwipeOpen });
	drag(-20);
	settle();
	expect(railControls(view)).toHaveLength(0);
	expect(onSwipeOpen).not.toHaveBeenCalled();
});

it("releases the rail after a drag that comes back exactly to rest", () => {
	const onSwipeClose = vi.fn();
	const view = mount({ onSwipeClose });
	begin();
	move(-30);
	move(0);
	release(0);
	settle();
	expect(railControls(view)).toHaveLength(0);
	expect(onSwipeClose).toHaveBeenCalledWith("worker");
});

// A finger that catches the row while it is still moving drags it from where it
// is, not from where it was heading.
it("starts a drag from where the row is, not where it was settling", () => {
	const view = mount();
	begin();
	move(-30);
	// A second drag starts before the first one ended.
	begin();
	move(20);
	expect(foreground(view).props.style).toContainEqual({ transform: [{ translateX: -10 }] });
});

it("follows the drag and opens on a flick shorter than half the rail", () => {
	const onSwipeOpen = vi.fn();
	const view = mount({ onSwipeOpen });
	begin();
	move(-30);
	expect(foreground(view).props.style).toContainEqual({ transform: [{ translateX: -30 }] });
	release(-20, true, -400);
	expect(springs.pending.at(-1)?.to).toBe(-72);
	expect(onSwipeOpen).toHaveBeenCalled();
});

// A resting row has nothing to reveal on its left, so a rightward drag stays
// with the list and with iOS's edge-swipe back. An open row closes either way;
// while it is open a tap closes it rather than reaching the row underneath.
it("claims a rightward drag and taps only while the rail is open", () => {
	const view = mount();
	expect(gestures.pan.activeOffsetX[0]).toBe(-16);
	expect(gestures.pan.activeOffsetX[1]).toBeGreaterThan(1000);
	expect(gestures.tap.enabled).toBe(false);
	expect(foreground(view).props.pointerEvents).toBe("auto");
	drag(-60);
	expect(gestures.pan.activeOffsetX).toEqual([-16, 16]);
	expect(gestures.tap.enabled).toBe(true);
	expect(foreground(view).props.pointerEvents).toBe("box-only");
	act(() => gestures.tap.handlers.onStart());
	expect(springs.pending.at(-1)?.to).toBe(0);
	expect(gestures.pan.activeOffsetX[1]).toBeGreaterThan(1000);
});

it("closes its open rail when another row opens one, and leaves a resting row alone", () => {
	const onSwipeClose = vi.fn();
	const view = mount({ onSwipeClose });
	act(() => { view.update(<WorkerRowInteraction {...props} onSwipeClose={onSwipeClose} activeSwipeId="other" />); });
	expect(springs.pending).toHaveLength(0);
	drag(-60);
	settle();
	act(() => { view.update(<WorkerRowInteraction {...props} onSwipeClose={onSwipeClose} activeSwipeId="another" />); });
	expect(springs.pending.at(-1)?.to).toBe(0);
	settle();
	expect(onSwipeClose).toHaveBeenCalledWith("worker");
});

it("closes the rail before a menu action takes over", () => {
	const onAction = vi.fn();
	const view = mount({ onAction });
	drag(-60);
	settle();
	act(() => view.root.findByType("ContextMenu" as never).props.onAction("rename"));
	expect(springs.pending.at(-1)?.to).toBe(0);
	expect(onAction).toHaveBeenCalledWith("rename");
});

it("drops the menu, the swipe and the full swipe while the row is being renamed", () => {
	const view = mount({ enabled: false });
	expect(gestures.pan.enabled).toBe(false);
	expect(view.root.findAllByType("ContextMenu" as never)).toHaveLength(0);
});

it("archives on a full swipe, with one haptic as it arms", () => {
	const onAction = vi.fn();
	mount({ onAction });
	begin();
	move(-100);
	expect(haptics.armed).not.toHaveBeenCalled();
	move(-260);
	move(-300);
	expect(haptics.armed).toHaveBeenCalledTimes(1);
	release(-300);
	expect(springs.pending.at(-1)?.to).toBe(0);
	expect(onAction).toHaveBeenCalledWith("delete");
});

it("does not archive a drag that only opens the rail, is carried back, or is cancelled", () => {
	const onAction = vi.fn();
	mount({ onAction });
	drag(-100);
	settle();
	act(() => gestures.tap.handlers.onStart());
	settle();
	begin();
	move(-300);
	move(-100);
	release(-100);
	settle();
	act(() => gestures.tap.handlers.onStart());
	settle();
	begin();
	move(-300);
	release(-300, false);
	settle();
	expect(onAction).not.toHaveBeenCalled();
});

it("measures a full swipe from the open rail when the drag starts there", () => {
	const onAction = vi.fn();
	mount({ onAction });
	drag(-60);
	settle();
	// The 72pt rail already uncovered, plus 70 more, passes the arm point,
	// about 60pt past the rail on a 390pt row.
	begin();
	move(-70);
	release(-70);
	expect(onAction).toHaveBeenCalledWith("delete");
});

it("stops at the rail on a row that cannot be archived by a swipe", () => {
	const view = mount({ actions: [{ id: "open", title: "Open" }] });
	begin();
	move(-300);
	expect(foreground(view).props.style).toContainEqual({ transform: [{ translateX: -72 }] });
});
