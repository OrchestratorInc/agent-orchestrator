import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { createElement, useImperativeHandle } from "react";
import { afterEach, expect, it, vi } from "vitest";
import type { WorkerRowInteractionProps } from "./worker-row-interaction.types";

const swipeable = vi.hoisted(() => ({ close: vi.fn() }));

vi.mock("react-native", () => ({
	Platform: { OS: "ios" },
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 0.5 },
	View: "View",
}));
// Gesture Handler's swipeable, reduced to its props and its imperative close.
vi.mock("react-native-gesture-handler/ReanimatedSwipeable", () => ({
	default: function SwipeBoundary({ ref, ...props }: Record<string, any>) {
		useImperativeHandle(ref, () => ({ close: swipeable.close }));
		return createElement("SwipeBoundary", props, props.renderRightActions(), props.children);
	},
}));
vi.mock("./worker-rename-context-menu", () => ({ WorkerRowContextMenu: "ContextMenu" }));
vi.mock("./ThemeProvider", () => ({
	useThemedStyles: (factory: (theme: Record<string, string>) => unknown) => factory({ bgElevated: "elevated", borderSubtle: "divider" }),
}));

import { WorkerRowInteraction } from "./worker-row-interaction";

let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	act(() => renderer?.unmount());
	swipeable.close.mockClear();
});
const props: WorkerRowInteractionProps = {
	sessionId: "worker", enabled: true, children: <></>, rightActions: createElement("RailControls"),
	shellStyle: {}, foregroundStyle: {}, rowStyle: {},
	accessibilityLabel: "Worker", onPress() {},
	actions: [], onAction() {}, onSwipeOpen() {}, onSwipeClose() {},
};
function mount(over: Partial<WorkerRowInteractionProps> = {}) {
	Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
	act(() => { renderer = create(<WorkerRowInteraction {...props} {...over} />); });
	return renderer!;
}
const boundary = (view: ReactTestRenderer) => view.root.findByType("SwipeBoundary" as never);
const railControls = (view: ReactTestRenderer) => view.root.findAll((node) => node.type === ("RailControls" as never));

it("does not build the rail's controls behind a resting row but keeps the rail measurable", () => {
	const view = mount();
	expect(railControls(view)).toHaveLength(0);
	expect(boundary(view).props.renderRightActions().props.style).toMatchObject({ width: 128, justifyContent: "center" });
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

it("drops the menu and the swipe while the row is being renamed", () => {
	const view = mount({ enabled: false });
	expect(boundary(view).props.enabled).toBe(false);
	expect(view.root.findAllByType("ContextMenu" as never)).toHaveLength(0);
});
