import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { forwardRef, useImperativeHandle, type ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	press: vi.fn(),
	theme: new Proxy({}, { get: () => "#000000" }),
}));
vi.mock("react-native", async () => {
	const { createElement } = await import("react");
	// The trigger measures itself through its ref; a fixed rectangle stands in for layout.
	const Pressable = forwardRef<unknown, { children?: ReactNode }>(function Pressable(props, ref) {
		useImperativeHandle(ref, () => ({ measureInWindow: (callback: (x: number, y: number, w: number, h: number) => void) => callback(120, 300, 240, 60) }));
		return createElement("Pressable", props, props.children);
	});
	class Value { setValue = vi.fn(); interpolate = vi.fn(() => 0); }
	return {
		View: "View", Text: "Text", Modal: "Modal", Pressable,
		Animated: { View: "AnimatedView", Value, timing: vi.fn(() => ({ start: vi.fn() })) },
		Easing: { out: vi.fn(), cubic: vi.fn() },
		StyleSheet: { create: (styles: object) => styles, absoluteFill: {}, hairlineWidth: 1 },
		useWindowDimensions: () => ({ width: 400, height: 900 }),
	};
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 40, bottom: 20, left: 0, right: 0 }) }));
vi.mock("../haptics", () => ({ haptics: { press: fixtures.press, tap: vi.fn() } }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme) }));
vi.mock("../useReducedMotion", () => ({ useReducedMotion: () => true }));

import { MessageContextMenu } from "./MessageContextMenu";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.press.mockClear();
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

function open(canEdit: boolean, onAction = vi.fn()) {
	act(() => { renderer = create(<MessageContextMenu canEdit={canEdit} onAction={onAction}><Text>bubble</Text></MessageContextMenu>); });
	const trigger = renderer!.root.find((node) => (node.type as unknown) === "Pressable" && typeof node.props.onLongPress === "function");
	act(() => trigger.props.onLongPress());
	return onAction;
}
const Text = "Text" as unknown as (props: { children: ReactNode }) => ReactNode;
const modal = () => renderer!.root.findByType("Modal" as never);
const items = () => renderer!.root.findAll((node) => node.props.accessibilityRole === "menuitem" && (node.type as unknown) === "Pressable").map((node) => node.props.accessibilityLabel);

it("lifts the bubble into a menu with Edit and Copy on long-press", () => {
	open(true);
	expect(modal().props.visible).toBe(true);
	expect(fixtures.press).toHaveBeenCalledTimes(1);
	expect(items()).toEqual(["Edit", "Copy"]);
	// The bubble is drawn again above the dimmed conversation.
	expect(renderer!.root.findAll((node) => node.props.children === "bubble")).toHaveLength(2);
});

it("offers only Copy when the message cannot be edited", () => {
	open(false);
	expect(items()).toEqual(["Copy"]);
});

it("closes and reports the chosen action", () => {
	const onAction = open(true);
	const edit = renderer!.root.find((node) => node.props.accessibilityLabel === "Edit" && (node.type as unknown) === "Pressable");
	act(() => edit.props.onPress());
	expect(onAction).toHaveBeenCalledExactlyOnceWith("edit");
	expect(modal().props.visible).toBe(false);
});

it("closes on the backdrop and on Android back without acting", () => {
	const onAction = open(true);
	act(() => renderer!.root.findByProps({ accessibilityLabel: "Close menu" }).props.onPress());
	expect(modal().props.visible).toBe(false);
	const trigger = renderer!.root.find((node) => (node.type as unknown) === "Pressable" && typeof node.props.onLongPress === "function");
	act(() => trigger.props.onLongPress());
	act(() => modal().props.onRequestClose());
	expect(modal().props.visible).toBe(false);
	expect(onAction).not.toHaveBeenCalled();
});
