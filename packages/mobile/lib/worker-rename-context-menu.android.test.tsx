import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { createElement, useImperativeHandle } from "react";
import { afterEach, expect, it, vi } from "vitest";

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
afterEach(() => {
	act(() => renderer?.unmount());
	menu.show.mockClear();
	haptics.press.mockClear();
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

// Anchored to the whole row, Compose opened the menu against the screen's
// left edge wherever the finger was; its anchor moves under the finger first.
it("opens the native menu where the row was long-pressed", () => {
	let frame: (() => void) | undefined;
	vi.stubGlobal("requestAnimationFrame", (callback: () => void) => { frame = callback; return 1; });
	const onPress = vi.fn();
	const view = mount(onPress);
	const row = view.root.findByType("Pressable" as never);
	act(() => row.props.onPressIn({ nativeEvent: { locationX: 310, locationY: 42 } }));
	act(() => row.props.onLongPress());
	expect(haptics.press).toHaveBeenCalledTimes(1);
	expect(view.root.findByType("MenuView" as never).props.style[1]).toEqual({ left: 310, top: 42 });
	expect(menu.show).not.toHaveBeenCalled();
	act(() => frame?.());
	expect(menu.show).toHaveBeenCalledTimes(1);
	expect(onPress).not.toHaveBeenCalled();
	vi.unstubAllGlobals();
});

it("marks Archive as destructive", () => {
	const actions = mount().root.findByType("MenuView" as never).props.actions;
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
	act(() => view.root.findByType("MenuView" as never).props.onPressAction({ nativeEvent: { event: "pin" } }));
	expect(onAction).toHaveBeenCalledWith("pin");
});
