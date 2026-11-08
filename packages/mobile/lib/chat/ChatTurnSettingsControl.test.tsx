import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatConfigOption, ChatModel, ConversationSnapshot } from "./types";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }),
	select: vi.fn(),
	back: new Set<() => boolean>(),
}));
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Button: host("Button"),
		DropdownMenu: Object.assign(host("DropdownMenu"), { Trigger: host("DropdownMenuTrigger"), Items: host("DropdownMenuItems") }),
		Host: host("ComposeHost"),
		HorizontalDivider: host("HorizontalDivider"),
		Icon: host("Icon"),
		IconButton: host("IconButton"),
		RNHostView: host("RNHostView"),
		Row: host("Row"),
		Spacer: host("Spacer"),
		Text: host("ComposeText"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => {
	const record = (name: string) => (...args: unknown[]) => ({ modifier: name, args });
	return { fillMaxWidth: record("fillMaxWidth"), height: record("height"), padding: record("padding"), semantics: record("semantics"), testID: record("testID"), weight: record("weight") };
});
vi.mock("../composeMenu.android", async () => {
	const React = await import("react");
	return {
		KEEP_KEYBOARD: { focusable: false },
		HarnessMark: (props: object) => React.createElement("HarnessMark", props),
		MenuRow: (props: object) => React.createElement("MenuRow", props),
		useCloseMenuOnBack: (open: boolean, close: () => void) => {
			React.useEffect(() => {
				if (!open) return;
				const handler = () => { close(); return true; };
				fixtures.back.add(handler);
				return () => { fixtures.back.delete(handler); };
			}, [open]);
		},
	};
});
vi.mock("react-native", () => ({
	Platform: { OS: "android" },
	StyleSheet: { create: <T,>(styles: T) => styles, hairlineWidth: 1 },
}));
vi.mock("../toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
vi.mock("../effort-gauge", () => ({ EffortGaugeButton: "EffortGaugeButton" }));
vi.mock("../haptics", () => ({ haptics: { select: fixtures.select } }));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
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
	fixtures.select.mockClear();
	fixtures.back.clear();
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
const hasTestID = (node: { props: { modifiers?: Array<{ modifier: string; args: unknown[] }> } }, id: string) =>
	node.props.modifiers?.some((modifier) => modifier.modifier === "testID" && modifier.args[0] === id) ?? false;
const trigger = (id: string) => renderer!.root.find((node) => (node.type === ("Button" as never) || node.type === ("IconButton" as never)) && hasTestID(node, id));
const menu = (id: string) => renderer!.root.findAll((node) => node.type === ("DropdownMenu" as never)).find((node) => node.findAll((child) => hasTestID(child, id)).length)!;
const rows = (id: string) => menu(id).findAll((node) => node.type === ("MenuRow" as never));
const titles = (id: string) => rows(id).map((row) => [row.props.label, row.props.selected ? "on" : "off"]);
const open = (id: string) => act(() => trigger(id).props.onClick());
const pick = (id: string, index: number) => act(() => rows(id)[index].props.onClick());

describe("Android chat controls row", () => {
	// The new task's row: the model's capsule, the effort's speedometer and the
	// approval mode's shield, each opening the new task's Material 3 dropdown.
	it("names the model on its capsule, for TalkBack too, and lists the agent's models, then its other settings", () => {
		render();
		expect(trigger("chat-model").findByType("ComposeText" as never).props.children).toBe("Opus");
		expect(trigger("chat-model").props.modifiers).toContainEqual({ modifier: "semantics", args: [{ contentDescription: "Model, Opus" }] });
		expect(menu("chat-model").props.expanded).toBe(false);
		expect(titles("chat-model")).toEqual([["Opus", "on"], ["Haiku", "off"], ["Fast mode", "off"]]);
		open("chat-model");
		expect(menu("chat-model").props.expanded).toBe(true);
	});

	// The speedometer lit to the level, which opens the composer's slider.
	it("draws the effort's speedometer, which opens the composer's slider", () => {
		const { onToggleEffort } = render();
		expect(gauge().props.disabled).toBe(false);
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
		expect(trigger("chat-approval").findByType("Icon" as never).props.contentDescription).toBe("Approval, Ask when unsure");
		expect(titles("chat-approval").find(([, state]) => state === "on")).toEqual(["Ask when unsure", "on"]);
	});

	// The M3 menu once listed models only, and with the settings sheet gone
	// Android had no way to reach Fast mode or a provider's other settings.
	it("toggles Fast mode from the model menu, ticked while it is on", () => {
		const { onOption } = render({ options: [{ ...fast, currentBoolean: true }] });
		const fastRow = () => rows("chat-model").find((row) => row.props.label === "Fast mode")!;
		expect(fastRow().props.selected).toBe(true);
		open("chat-model");
		act(() => fastRow().props.onClick());
		expect(onOption).toHaveBeenLastCalledWith("fast", { enabled: false });
		expect(menu("chat-model").props.expanded).toBe(false);
	});

	it("opens a provider's choice setting onto its choices, titled with the current one", () => {
		const verbosity: ChatConfigOption = { id: "verbosity", name: "Verbosity", type: "select", currentValue: "low", choices: [{ value: "low", name: "Low" }, { value: "high", name: "High" }] };
		const { onOption } = render({ options: [fast, verbosity] });
		open("chat-model");
		const setting = rows("chat-model").find((row) => row.props.label === "Verbosity: Low")!;
		act(() => setting.props.onClick());
		const submenu = renderer!.root.findAll((node) => node.type === ("DropdownMenu" as never)).find((node) => node.props.expanded && node.findAll((row) => row.type === ("MenuRow" as never) && row.props.label === "High").length && !node.findAll((row) => row.props.label === "Opus").length)!;
		const choices = submenu.findAll((node) => node.type === ("MenuRow" as never)).filter((row) => row !== setting);
		expect(choices.map((row) => [row.props.label, row.props.selected ? "on" : "off"])).toEqual([["Low", "on"], ["High", "off"]]);
		act(() => choices[1].props.onClick());
		expect(onOption).toHaveBeenLastCalledWith("verbosity", { value: "high" });
	});

	it("saves each choice to the setting it belongs to and closes its menu", () => {
		const { onSettings, onOption } = render();
		open("chat-model");
		pick("chat-model", 1);
		expect(onSettings).toHaveBeenLastCalledWith({ model: "haiku", approvalMode: "auto", reasoningEffort: undefined });
		expect(menu("chat-model").props.expanded).toBe(false);
		pick("chat-approval", 3);
		expect(onSettings).toHaveBeenLastCalledWith(expect.objectContaining({ approvalMode: "bypass-permissions" }));
		expect(onOption).not.toHaveBeenCalled();
		expect(fixtures.select).toHaveBeenCalledTimes(2);
	});

	// A long catalog keeps the newest of each family on top and its older
	// versions one level down.
	it("tucks older versions under Older models", () => {
		const claude = ["Opus 5.5", "Fable 5.1", "Sonnet 5.5", "Haiku 5.5", "Haiku 4.5", "Sonnet 5", "Opus 4.8"]
			.map((displayName, index) => ({ id: `m${index}`, displayName })) as ChatModel[];
		const { onSettings } = render({ models: claude, snapshot: snapshot({ settings: { model: "m0", approvalMode: "auto" } }) });
		const older = () => menu("chat-model").findAll((node) => node.type === ("DropdownMenu" as never))[1];
		const olderRows = () => older().findByType("DropdownMenuItems" as never).findAll((node) => node.type === ("MenuRow" as never));
		const olderTrigger = () => older().findByType("DropdownMenuTrigger" as never).findByType("MenuRow" as never);
		const top = rows("chat-model").filter((row) => !older().findAll((node) => node === row).length);
		expect(top.map((row) => row.props.label)).toEqual(["Opus 5.5", "Fable 5.1", "Sonnet 5.5", "Haiku 5.5", "Fast mode"]);
		expect(olderTrigger().props.label).toBe("Older models");
		expect(olderRows().map((row) => row.props.label)).toEqual(["Haiku 4.5", "Sonnet 5", "Opus 4.8"]);
		open("chat-model");
		act(() => olderTrigger().props.onClick());
		expect(older().props.expanded).toBe(true);
		expect(menu("chat-model").props.expanded).toBe(true);
		act(() => olderRows()[2].props.onClick());
		expect(onSettings).toHaveBeenLastCalledWith({ model: "m6", approvalMode: "auto", reasoningEffort: undefined });
		expect(menu("chat-model").props.expanded).toBe(false);
	});

	it("writes nothing when the current choice is picked again", () => {
		const { onSettings } = render();
		pick("chat-model", 0);
		expect(onSettings).not.toHaveBeenCalled();
		expect(fixtures.select).not.toHaveBeenCalled();
	});

	it("closes an open menu on back, since it does not take focus", () => {
		render();
		open("chat-approval");
		act(() => { for (const handler of [...fixtures.back]) handler(); });
		expect(menu("chat-approval").props.expanded).toBe(false);
	});

	it("disables every control while the conversation cannot take a change", () => {
		render({ disabled: true });
		expect(gauge().props.disabled).toBe(true);
		for (const id of ["chat-model", "chat-approval"]) expect(trigger(id).props.enabled).toBe(false);
	});
});
