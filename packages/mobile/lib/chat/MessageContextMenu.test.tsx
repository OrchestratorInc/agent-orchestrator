import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { type ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	os: "android",
	press: vi.fn(),
	show: vi.fn(),
}));
vi.mock("react-native", () => ({
	View: "View",
	Pressable: "Pressable",
	StyleSheet: { create: <T,>(styles: T) => styles },
	Platform: { get OS() { return fixtures.os; } },
}));
vi.mock("@expo/ui/community/menu", async () => {
	const React = await import("react");
	// The Compose menu: shown through its ref, as the app opens it.
	const MenuView = React.forwardRef((props: Record<string, unknown>, ref) => {
		React.useImperativeHandle(ref, () => ({ show: fixtures.show }));
		return React.createElement("MenuView", props, props.children as ReactNode);
	});
	return { MenuView };
});
vi.mock("../haptics", () => ({ haptics: { press: fixtures.press } }));
vi.mock("../ThemeProvider", () => ({ useThemeState: () => ({ scheme: "dark" }) }));

import { MessageContextMenu } from "./MessageContextMenu";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.stubGlobal("requestAnimationFrame", (callback: () => void) => setTimeout(callback, 0));
	vi.stubGlobal("cancelAnimationFrame", (id: ReturnType<typeof setTimeout>) => clearTimeout(id));
	vi.useFakeTimers();
	fixtures.os = "android";
	fixtures.press.mockClear();
	fixtures.show.mockClear();
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.useRealTimers();
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

const Text = "Text" as unknown as (props: { children: ReactNode }) => ReactNode;

function render(canEdit: boolean, onAction = vi.fn()) {
	act(() => { renderer = create(<MessageContextMenu canEdit={canEdit} onAction={onAction}><Text>bubble</Text></MessageContextMenu>); });
	return onAction;
}
const menus = () => renderer!.root.findAll((node) => (node.type as unknown) === "MenuView");
const menu = () => menus()[0]!;
const trigger = () => renderer!.root.findByProps({ testID: "message-menu-trigger" });

it("mounts no Compose menu on Android until a long press opens one under the finger", () => {
	render(true);
	expect(menus()).toHaveLength(0);
	act(() => {
		trigger().props.onPressIn({ nativeEvent: { locationX: 40, locationY: 12 } });
		trigger().props.onLongPress();
	});
	expect(fixtures.press).toHaveBeenCalledOnce();
	expect(menu().props.actions.map((action: { title: string }) => action.title)).toEqual(["Edit", "Copy"]);
	expect(menu().props.style).toContainEqual({ left: 40, top: 12 });
	act(() => { vi.advanceTimersByTime(16); });
	expect(fixtures.show).toHaveBeenCalledOnce();
	// The bubble is drawn once, under the trigger; nothing is cloned into the menu.
	expect(renderer!.root.findAll((node) => node.props.children === "bubble")).toHaveLength(1);
});

it("reports the chosen action, and takes the menu away once it has closed", () => {
	const onAction = render(true);
	act(() => trigger().props.onLongPress());
	act(() => menu().props.onPressAction({ nativeEvent: { event: "edit" } }));
	expect(onAction).toHaveBeenCalledExactlyOnceWith("edit");
	act(() => menu().props.onCloseMenu());
	act(() => { vi.advanceTimersByTime(400); });
	expect(menus()).toHaveLength(0);
});

it("offers only Copy when the message cannot be edited", () => {
	render(false);
	expect(trigger().props.accessibilityActions).toEqual([{ name: "copy", label: "Copy" }]);
	act(() => trigger().props.onLongPress());
	expect(menu().props.actions.map((action: { id: string }) => action.id)).toEqual(["copy"]);
});

it("offers Edit and Copy to TalkBack on the bubble itself", () => {
	const onAction = render(true);
	expect(trigger().props.accessible).toBe(true);
	expect(trigger().props.accessibilityActions).toEqual([{ name: "edit", label: "Edit" }, { name: "copy", label: "Copy" }]);
	act(() => trigger().props.onAccessibilityAction({ nativeEvent: { actionName: "copy" } }));
	expect(onAction).toHaveBeenCalledExactlyOnceWith("copy");
});

it("on iOS makes the bubble the system menu's own trigger and leaves its elements to VoiceOver", () => {
	fixtures.os = "ios";
	const onAction = render(true);
	expect(menu().props.shouldOpenOnLongPress).toBe(true);
	expect(menu().props.actions.map((action: { title: string }) => action.title)).toEqual(["Edit", "Copy"]);
	const bubble = menu().findByType("View" as never);
	expect(bubble.props.accessible).toBe(false);
	expect(bubble.props.accessibilityActions).toBeUndefined();
	act(() => menu().props.onPressAction({ nativeEvent: { event: "copy" } }));
	expect(onAction).toHaveBeenCalledExactlyOnceWith("copy");
});
