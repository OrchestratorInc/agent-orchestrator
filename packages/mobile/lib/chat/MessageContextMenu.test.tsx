import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { type ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	os: "android",
	press: vi.fn(),
}));
vi.mock("react-native", () => ({
	View: "View",
	Platform: { get OS() { return fixtures.os; } },
}));
vi.mock("@expo/ui/community/menu", () => ({ MenuView: "MenuView" }));
vi.mock("../haptics", () => ({ haptics: { press: fixtures.press } }));
vi.mock("../ThemeProvider", () => ({ useThemeState: () => ({ scheme: "dark" }) }));

import { MessageContextMenu } from "./MessageContextMenu";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.os = "android";
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

function render(canEdit: boolean, onAction = vi.fn()) {
	act(() => { renderer = create(<MessageContextMenu canEdit={canEdit} onAction={onAction}><Text>bubble</Text></MessageContextMenu>); });
	return onAction;
}
const menu = () => renderer!.root.findByType("MenuView" as never);
const bubble = () => menu().findByType("View" as never);

it("opens the system menu on long-press with Edit and Copy", () => {
	render(true);
	expect(menu().props.shouldOpenOnLongPress).toBe(true);
	expect(menu().props.colorScheme).toBe("dark");
	expect(menu().props.actions).toEqual([
		{ id: "edit", title: "Edit", image: "pencil" },
		{ id: "copy", title: "Copy", image: "doc.on.doc" },
	]);
	// The bubble is drawn once, inside the trigger; nothing is cloned above a scrim.
	expect(renderer!.root.findAll((node) => node.props.children === "bubble")).toHaveLength(1);
});

it("offers only Copy when the message cannot be edited", () => {
	render(false);
	expect(menu().props.actions.map((action: { id: string }) => action.id)).toEqual(["copy"]);
	expect(bubble().props.accessibilityActions).toEqual([{ name: "copy", label: "Copy" }]);
});

it("reports the chosen action", () => {
	const onAction = render(true);
	act(() => menu().props.onPressAction({ nativeEvent: { event: "edit" } }));
	expect(onAction).toHaveBeenCalledExactlyOnceWith("edit");
});

it("gives the Android long press its haptic when the menu opens", () => {
	render(true);
	expect(fixtures.press).not.toHaveBeenCalled();
	act(() => menu().props.onOpenMenu());
	expect(fixtures.press).toHaveBeenCalledOnce();
});

it("offers Edit and Copy to TalkBack on the bubble itself", () => {
	const onAction = render(true);
	expect(bubble().props.accessible).toBe(true);
	expect(bubble().props.accessibilityActions).toEqual([{ name: "edit", label: "Edit" }, { name: "copy", label: "Copy" }]);
	act(() => bubble().props.onAccessibilityAction({ nativeEvent: { actionName: "copy" } }));
	expect(onAction).toHaveBeenCalledExactlyOnceWith("copy");
});

it("leaves the bubble's own elements to VoiceOver on iOS", () => {
	fixtures.os = "ios";
	render(true);
	expect(bubble().props.accessible).toBe(false);
	expect(bubble().props.accessibilityActions).toBeUndefined();
});
