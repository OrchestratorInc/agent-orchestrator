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
	Platform: { OS: "android" },
	Pressable: "Pressable",
	StyleSheet: { create: <T,>(styles: T) => styles, hairlineWidth: 1 },
	Text: "Text",
	View: "View",
}));
vi.mock("../AgentLogo", () => ({ AgentLogo: "AgentLogo" }));
vi.mock("../effort-gauge", () => ({ EffortGaugeButton: "EffortGaugeButton" }));
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

const levels = [{ value: "low", label: "Low", selected: true }, { value: "high", label: "High", selected: false }];

function render(over: { snapshot?: ConversationSnapshot; models?: ChatModel[]; options?: ChatConfigOption[]; disabled?: boolean; effort?: { choices: typeof levels; open: boolean; onToggle(): void } | null } = {}) {
	const handlers = {
		onSettings: vi.fn(() => Promise.resolve()),
		onOption: vi.fn(() => Promise.resolve([] as ChatConfigOption[])),
		onToggleEffort: vi.fn(),
	};
	const effort = over.effort === null ? undefined : over.effort ?? { choices: levels, open: false, onToggle: handlers.onToggleEffort };
	act(() => {
		renderer = create(<ChatTurnSettingsControl snapshot={over.snapshot ?? snapshot()} models={over.models ?? models} options={over.options ?? [fast]} disabled={over.disabled} onSettings={handlers.onSettings} onOption={handlers.onOption} effort={effort} />);
	});
	return handlers;
}
const gauge = () => renderer!.root.findAll((node) => node.type === ("EffortGaugeButton" as never) && node.props.testID === "chat-effort")[0];
const trigger = (id: string) => renderer!.root.find((node) => node.type === ("Pressable" as never) && node.props.testID === id);
const menu = (id: string) => renderer!.root.find((node) => node.type === ("MenuView" as never) && node.props.testID === `${id}-menu`);
const titles = (id: string) => menu(id).props.actions.map((action: { title: string; state?: string }) => [action.title, action.state]);
const pick = (id: string, event: string) => act(() => menu(id).props.onPressAction({ nativeEvent: { event } }));

describe("Android chat controls row", () => {
	// The new task's row: the model's capsule, the effort's speedometer and the
	// approval mode's shield, each opening its own menu.
	it("names the model on its capsule and lists the agent's models, then any other setting", () => {
		render();
		expect(trigger("chat-model").props.accessibilityLabel).toBe("Model, Opus");
		// Led by the agent's mark, as iOS's capsule is.
		expect(trigger("chat-model").findByType("AgentLogo" as never).props).toEqual({ harness: "claude-code", size: 20 });
		expect(titles("chat-model")).toEqual([["Opus", "on"], ["Haiku", "off"], ["Fast mode", "off"]]);
		expect(menu("chat-model").props.actions[2].image).toBe("bolt");
		act(() => trigger("chat-model").props.onPress());
		expect(fixtures.show).toHaveBeenCalledOnce();
	});

	// The speedometer lit to the level, which opens the composer's slider.
	it("draws the effort's speedometer, which opens the composer's slider", () => {
		const { onToggleEffort } = render();
		expect(gauge().props).toMatchObject({ choices: levels, open: false, disabled: false });
		act(() => gauge().props.onPress());
		expect(onToggleEffort).toHaveBeenCalledOnce();
	});

	it("draws no speedometer for a model that takes no effort", () => {
		render({ effort: null });
		expect(gauge()).toBeUndefined();
		render({ effort: { choices: [], open: false, onToggle: () => {} } });
		expect(gauge()).toBeUndefined();
	});

	it("offers the approval modes from the shield", () => {
		render();
		expect(trigger("chat-approval").props.accessibilityLabel).toBe("Approvals, Ask when unsure");
		expect(titles("chat-approval").find(([, state]: [string, string]) => state === "on")).toEqual(["Ask when unsure", "on"]);
	});

	it("saves each choice to the setting it belongs to", () => {
		const { onSettings, onOption } = render();
		pick("chat-model", "model:1");
		expect(onSettings).toHaveBeenLastCalledWith({ model: "haiku", approvalMode: "auto", reasoningEffort: undefined });
		pick("chat-model", "other:0:0");
		expect(onOption).toHaveBeenCalledExactlyOnceWith("fast", { enabled: true });
		expect(fixtures.select).toHaveBeenCalledTimes(2);
	});

	it("disables every control while the conversation cannot take a change", () => {
		render({ disabled: true });
		expect(gauge().props.disabled).toBe(true);
		for (const id of ["chat-model", "chat-approval"]) {
			expect(trigger(id).props.disabled).toBe(true);
			expect(trigger(id).props.accessibilityState).toEqual({ disabled: true });
		}
	});
});
