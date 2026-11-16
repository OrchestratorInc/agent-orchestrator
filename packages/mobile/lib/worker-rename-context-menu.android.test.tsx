import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { createElement, useImperativeHandle } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const menu = vi.hoisted(() => ({ show: vi.fn() }));
const haptics = vi.hoisted(() => ({ press: vi.fn() }));

vi.mock("react-native", () => ({ Platform: { OS: "android" }, Pressable: "Pressable", View: "View", StyleSheet: { create: <T,>(styles: T) => styles } }));
vi.mock("@expo/ui/community/menu", () => ({
	MenuView: function MenuView({ ref, ...props }: Record<string, any>) {
		useImperativeHandle(ref, () => ({ show: menu.show }));
		return createElement("MenuView", props, props.children);
	},
}));
vi.mock("./haptics", () => ({ haptics }));
vi.mock("./worker-action-drawables", () => ({ workerActionDrawable: (id: string) => `drawable:${id}` }));
vi.mock("./ThemeProvider", () => {
	const theme = { red: "red", tintRed: "red-tint", accentTint: "accent-tint", textPrimary: "primary", textSecondary: "secondary", bgElevatedHover: "hover" };
	return { useTheme: () => theme, useThemeState: () => ({ scheme: "dark", theme }) };
});

import { WorkerRowContextMenu } from "./worker-rename-context-menu.android";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("requestAnimationFrame", () => 1);
	vi.stubGlobal("cancelAnimationFrame", () => {});
});
afterEach(() => {
	act(() => renderer?.unmount());
	menu.show.mockClear();
	haptics.press.mockClear();
	vi.useRealTimers();
	vi.unstubAllGlobals();
});
function mount(onPress = vi.fn(), onAction = vi.fn()) {
	Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
	act(() => {
		renderer = create(
			<WorkerRowContextMenu
				onPress={onPress}
				onAction={onAction}
				actions={[{ id: "open", title: "Open" }, { id: "pin", title: "Pin" }, { id: "delete", title: "Archive session", destructive: true }]}
				accessibilityLabel="Polish workers"
				style={{ minHeight: 76 }}
			>
				{createElement("RowContent")}
			</WorkerRowContextMenu>,
		);
	});
	return renderer!;
}

function longPress(view: ReactTestRenderer, x = 0, y = 0) {
	const row = view.root.findByType("Pressable" as never);
	act(() => row.props.onPressIn({ nativeEvent: { locationX: x, locationY: y } }));
	act(() => row.props.onLongPress());
}

// Every scroll frame on Android re-lays out the whole mounted tree, and the
// Compose menu was most of a resting row's nodes. It exists only while open.
it("builds no menu until the row is long-pressed", () => {
	const view = mount();
	expect(view.root.findAllByType("MenuView" as never)).toHaveLength(0);
});

// Anchored to the whole row, Compose opened the menu against the screen's
// left edge wherever the finger was; its anchor moves under the finger first.
it("opens the native menu where the row was long-pressed", () => {
	let frame: (() => void) | undefined;
	vi.stubGlobal("requestAnimationFrame", (callback: () => void) => { frame = callback; return 1; });
	const onPress = vi.fn();
	const view = mount(onPress);
	longPress(view, 310, 42);
	expect(haptics.press).toHaveBeenCalledTimes(1);
	expect(view.root.findByType("MenuView" as never).props.style[1]).toEqual({ left: 310, top: 42 });
	expect(menu.show).not.toHaveBeenCalled();
	act(() => frame?.());
	expect(menu.show).toHaveBeenCalledTimes(1);
	expect(onPress).not.toHaveBeenCalled();
});

// Torn down after Compose has played its close, not under it.
it("removes the menu once it has closed", () => {
	vi.useFakeTimers();
	const view = mount();
	longPress(view);
	act(() => view.root.findByType("MenuView" as never).props.onCloseMenu());
	expect(view.root.findAllByType("MenuView" as never)).toHaveLength(1);
	act(() => { vi.runAllTimers(); });
	expect(view.root.findAllByType("MenuView" as never)).toHaveLength(0);
});

// A long press that lands while the last menu is still closing opens a menu
// again rather than being torn down with the old one.
it("keeps a menu reopened while the last one was closing", () => {
	vi.useFakeTimers();
	const view = mount();
	longPress(view);
	act(() => view.root.findByType("MenuView" as never).props.onCloseMenu());
	longPress(view, 20, 10);
	act(() => { vi.runAllTimers(); });
	expect(view.root.findByType("MenuView" as never).props.style[1]).toEqual({ left: 20, top: 10 });
});

it("marks Archive as destructive", () => {
	const view = mount();
	longPress(view);
	const actions = view.root.findByType("MenuView" as never).props.actions;
	expect(actions.at(-1)).toMatchObject({ id: "delete", attributes: { destructive: true } });
});

// TalkBack cannot long-press the row into its menu, so the menu's actions are
// the row's own accessibility actions too.
it("offers the menu's actions to TalkBack", () => {
	const onAction = vi.fn();
	const row = mount(vi.fn(), onAction).root.findByType("Pressable" as never);
	expect(row.props.accessibilityActions).toEqual([{ name: "pin", label: "Pin" }, { name: "delete", label: "Archive session" }]);
	act(() => row.props.onAccessibilityAction({ nativeEvent: { actionName: "delete" } }));
	act(() => row.props.onAccessibilityAction({ nativeEvent: { actionName: "longpress" } }));
	expect(onAction.mock.calls).toEqual([["delete"]]);
});

it("routes a chosen action by its id", () => {
	const onAction = vi.fn();
	const view = mount(vi.fn(), onAction);
	longPress(view);
	act(() => view.root.findByType("MenuView" as never).props.onPressAction({ nativeEvent: { event: "pin" } }));
	expect(onAction).toHaveBeenCalledWith("pin");
});
