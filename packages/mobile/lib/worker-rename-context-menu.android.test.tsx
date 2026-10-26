import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { createElement, useImperativeHandle } from "react";
import { afterEach, expect, it, vi } from "vitest";

const menu = vi.hoisted(() => ({ show: vi.fn() }));
const haptics = vi.hoisted(() => ({ press: vi.fn() }));

vi.mock("react-native", () => ({ Platform: { OS: "android" }, Pressable: "Pressable" }));
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

// The menu listens for the long press on a Pressable of its own, which the
// row's Pressable inside it never lets see one.
it("opens the native menu from the row's own long press", () => {
	const onPress = vi.fn();
	const row = mount(onPress).root.findByType("Pressable" as never);
	act(() => row.props.onLongPress());
	expect(menu.show).toHaveBeenCalledTimes(1);
	expect(haptics.press).toHaveBeenCalledTimes(1);
	expect(onPress).not.toHaveBeenCalled();
});

it("answers a touch with the Material ripple rather than a fade", () => {
	const row = mount().root.findByType("Pressable" as never);
	expect(row.props.android_ripple).toMatchObject({ color: "accent-tint", borderless: false });
	expect(row.props.style({ pressed: true })).toEqual([{ minHeight: 76 }, undefined]);
});

it("lists every action with its Material Symbol, and Archive as destructive", () => {
	const view = mount();
	const actions = view.root.findByType("MenuView" as never).props.actions;
	expect(actions.map((action: { image: string }) => action.image)).toEqual(["drawable:open", "drawable:pin", "drawable:delete"]);
	expect(actions.at(-1)).toMatchObject({ id: "delete", attributes: { destructive: true }, titleColor: "red", imageColor: "red" });
	expect(view.root.findByType("MenuView" as never).props.shouldOpenOnLongPress).toBe(true);
});

it("routes a chosen action by its id", () => {
	const onAction = vi.fn();
	const view = mount(vi.fn(), onAction);
	act(() => view.root.findByType("MenuView" as never).props.onPressAction({ nativeEvent: { event: "pin" } }));
	expect(onAction).toHaveBeenCalledWith("pin");
});
