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
		useImperativeHandle(ref, () => ({ measureInWindow: (callback: (x: number, y: number, w: number, h: number) => void) => callback(0, 200, 400, 76) }));
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
vi.mock("./haptics", () => ({ haptics: { press: fixtures.press, tap: vi.fn() } }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme) }));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => true }));

import { LongPressMenu, type LongPressMenuAction } from "./long-press-menu";

type Id = "worker" | "pr" | "github";
const ACTIONS: LongPressMenuAction<Id>[] = [
	{ id: "worker", title: "Open worker", icon: "message-square", systemImage: "bubble.left" },
	{ id: "pr", title: "Open pull request", icon: "git-pull-request", systemImage: "point.topleft.down.to.point.bottomright.curvepath" },
	{ id: "github", title: "Open on GitHub", icon: "external-link", systemImage: "arrow.up.right.square" },
];

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

const Text = "Text" as unknown as (props: { children: ReactNode }) => ReactNode;
function render(onPress = vi.fn(), onAction = vi.fn()) {
	act(() => {
		renderer = create(<LongPressMenu actions={ACTIONS} onAction={onAction} onPress={onPress} accessibilityLabel="PR 12" accessibilityHint="Long press for more" style={{ padding: 1 }}><Text>card</Text></LongPressMenu>);
	});
	const trigger = renderer!.root.find((node) => (node.type as unknown) === "Pressable" && typeof node.props.onLongPress === "function");
	return { trigger, onPress, onAction };
}
const modal = () => renderer!.root.findByType("Modal" as never);

it("opens the target on a tap without showing the menu", () => {
	const { trigger, onPress, onAction } = render();
	expect(trigger.props.accessibilityRole).toBe("button");
	act(() => trigger.props.onPress());
	expect(onPress).toHaveBeenCalledOnce();
	expect(onAction).not.toHaveBeenCalled();
	expect(modal().props.visible).toBe(false);
});

it("lists every action on long-press and reports the chosen one", () => {
	const { trigger, onAction } = render();
	act(() => trigger.props.onLongPress());
	expect(modal().props.visible).toBe(true);
	const items = renderer!.root.findAll((node) => node.props.accessibilityRole === "menuitem" && (node.type as unknown) === "Pressable");
	expect(items.map((node) => node.props.accessibilityLabel)).toEqual(["Open worker", "Open pull request", "Open on GitHub"]);
	// The card is drawn again above the dimmed list.
	expect(renderer!.root.findAll((node) => node.props.children === "card")).toHaveLength(2);
	act(() => items[0].props.onPress());
	expect(onAction).toHaveBeenCalledExactlyOnceWith("worker");
	expect(modal().props.visible).toBe(false);
});

it("offers the same actions to screen readers", () => {
	const { trigger, onAction } = render();
	expect(trigger.props.accessibilityActions).toEqual(ACTIONS.map((action) => ({ name: action.id, label: action.title })));
	act(() => trigger.props.onAccessibilityAction({ nativeEvent: { actionName: "github" } }));
	expect(onAction).toHaveBeenCalledExactlyOnceWith("github");
});
