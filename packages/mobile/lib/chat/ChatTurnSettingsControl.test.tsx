import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { forwardRef, useImperativeHandle, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatConfigOption, ChatModel, ConversationSnapshot } from "./types";

const fixtures = vi.hoisted(() => ({ show: vi.fn(), select: vi.fn() }));
vi.mock("@expo/ui/community/menu", async () => {
	const { createElement } = await import("react");
	const MenuView = forwardRef<unknown, { children?: ReactNode }>(function MenuView(props, ref) {
		useImperativeHandle(ref, () => ({ show: fixtures.show }));
		return createElement("MenuView", props, props.children);
	});
	return { MenuView };
});
vi.mock("react-native", () => ({
	Pressable: "Pressable",
	StyleSheet: { create: <T,>(styles: T) => styles },
	Text: "Text",
	View: "View",
}));
vi.mock("../AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("../haptics", () => ({ haptics: { select: fixtures.select } }));
vi.mock("../pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: { color: "ripple" }, pressed: undefined }) }));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => ({}),
	useThemedStyles: (factory: (theme: object) => object) => factory({}),
	useThemeState: () => ({ scheme: "dark" }),
}));

import { ChatTurnSettingsControl } from "./ChatTurnSettingsControl";

const snapshot = (over: Partial<ConversationSnapshot> = {}): ConversationSnapshot => ({
	conversationId: "c",
	sessionId: "s",
	harness: "claude-code",
	mode: "chat",
	controller: { state: "ready" },
	latestSequence: 0,
	oldestSequence: 0,
	hasMoreBefore: false,
	turns: [],
	items: [],
	settings: { model: "opus", approvalMode: "auto" },
	capabilities: [],
	...over,
});
const models: ChatModel[] = [
	{ id: "opus", displayName: "Opus", efforts: ["low", "high"], defaultEffort: "low" },
	{ id: "haiku", displayName: "Haiku" },
] as ChatModel[];
const fast: ChatConfigOption = { id: "fast", name: "Fast mode", type: "boolean", currentBoolean: false, choices: [] };

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.show.mockClear();
	fixtures.select.mockClear();
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

function render(over: { snapshot?: ConversationSnapshot; models?: ChatModel[]; options?: ChatConfigOption[]; disabled?: boolean } = {}) {
	const handlers = {
		onSettings: vi.fn(() => Promise.resolve()),
		onOption: vi.fn(() => Promise.resolve([] as ChatConfigOption[])),
		onOpenFallback: vi.fn(),
	};
	act(() => {
		renderer = create(<ChatTurnSettingsControl snapshot={over.snapshot ?? snapshot()} models={over.models ?? models} options={over.options ?? [fast]} disabled={over.disabled} {...handlers} />);
	});
	return handlers;
}
const menu = () => renderer!.root.findByType("MenuView" as never);
const trigger = () => renderer!.root.findByType("Pressable" as never);

describe("Android turn settings menu", () => {
	it("offers a submenu per setting whose title carries its value, choices ticked", () => {
		render();
		const actions = menu().props.actions;
		expect(actions.map((action: { title: string }) => action.title)).toEqual(["Fast mode", "Model · Opus", "Effort · Low", "Approvals · Ask when unsure", "All turn settings"]);
		expect(actions[0]).toMatchObject({ state: "off", image: "bolt" });
		expect(actions[1].image).toBe("square.stack.3d.up");
		expect(actions[1].subactions).toEqual([
			{ id: "1:0", title: "Opus", state: "on" },
			{ id: "1:1", title: "Haiku", state: "off" },
		]);
	});

	it("opens the menu from the whole summary row", () => {
		render();
		expect(trigger().props.accessibilityLabel).toBe("Turn settings, Opus · Low · Ask when unsure");
		expect(trigger().props.android_ripple).toEqual({ color: "ripple" });
		act(() => trigger().props.onPress());
		expect(fixtures.show).toHaveBeenCalledOnce();
	});

	it("saves a choice to the setting it belongs to", () => {
		const { onSettings, onOption } = render();
		act(() => menu().props.onPressAction({ nativeEvent: { event: "1:1" } }));
		expect(onSettings).toHaveBeenCalledExactlyOnceWith({ model: "haiku", approvalMode: "auto", reasoningEffort: undefined });
		act(() => menu().props.onPressAction({ nativeEvent: { event: "0" } }));
		expect(onOption).toHaveBeenCalledExactlyOnceWith("fast", { enabled: true });
		expect(fixtures.select).toHaveBeenCalledTimes(2);
	});

	it("opens the full sheet from the menu's last item", () => {
		const { onOpenFallback, onSettings, onOption } = render();
		act(() => menu().props.onPressAction({ nativeEvent: { event: "all-settings" } }));
		expect(onOpenFallback).toHaveBeenCalledOnce();
		expect(onSettings).not.toHaveBeenCalled();
		expect(onOption).not.toHaveBeenCalled();
	});

	it("disables the row while the conversation cannot take a change", () => {
		render({ disabled: true });
		expect(trigger().props.disabled).toBe(true);
		expect(trigger().props.accessibilityState).toEqual({ disabled: true });
	});
});
