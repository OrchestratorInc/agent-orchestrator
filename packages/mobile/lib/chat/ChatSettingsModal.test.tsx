import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { type ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

// React Native's own Jest setup (packages/jest-preset/jest/setup.js) sets both:
// the first lets act() flush without warnings, the second silences
// react-test-renderer's deprecation warning in a React Native test environment.
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const { haptics, sliders, platform } = vi.hoisted(() => ({
	haptics: { select: vi.fn() },
	/** How many native sliders have mounted; a remount rebuilds the Compose slider. */
	sliders: { mounted: 0 },
	platform: { OS: "android" },
}));

vi.mock("@expo/ui", async () => {
	const { createElement, useEffect } = await import("react");
	const host = (name: string) => (props: { children?: ReactNode }) => createElement(name, props, props.children);
	function Slider(props: object) {
		useEffect(() => { sliders.mounted += 1; }, []);
		return createElement("Slider", props);
	}
	const FieldGroup = Object.assign(host("FieldGroup"), { Section: host("FieldGroupSection"), SectionFooter: host("FieldGroupSectionFooter") });
	return { Column: "Column", FieldGroup, Host: "Host", Slider, Switch: "Switch" };
});
vi.mock("@expo/ui/jetpack-compose", async () => {
	const { createElement } = await import("react");
	const host = (name: string) => (props: { children?: ReactNode }) => createElement(name, props, props.children);
	return {
		Column: "ComposeColumn",
		DropdownMenu: Object.assign(host("DropdownMenu"), { Trigger: host("DropdownMenuTrigger"), Items: host("DropdownMenuItems") }),
		DropdownMenuItem: Object.assign(host("DropdownMenuItem"), { LeadingIcon: host("DropdownMenuItemLeadingIcon"), Text: host("DropdownMenuItemText") }),
		RadioButton: "RadioButton",
		Row: "ComposeRow",
		Text: "ComposeText",
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({
	alpha: (value: number) => ({ alpha: value }),
	clickable: (handler: () => void) => ({ clickable: handler }),
	fillMaxWidth: () => ({ fillMaxWidth: true }),
	testID: (id: string) => ({ testID: id }),
}));
vi.mock("@expo/ui/swift-ui", () => ({ Picker: "SwiftUIPicker", Text: "SwiftUIText", VStack: "VStack" }));
vi.mock("@expo/ui/swift-ui/modifiers", () => ({
	disabled: (value: boolean) => ({ disabled: value }),
	font: (params: object) => ({ font: params }),
	foregroundStyle: (style: unknown) => ({ foregroundStyle: style }),
	labelsHidden: () => ({ labelsHidden: true }),
	pickerStyle: (style: string) => ({ pickerStyle: style }),
	refreshable: (handler: () => Promise<void>) => ({ refreshable: handler }),
	scrollContentBackground: (visible: string) => ({ scrollContentBackground: visible }),
	tag: (value: string) => ({ tag: value }),
}));
vi.mock("react-native", () => ({
	Platform: { get OS() { return platform.OS; } },
	RefreshControl: "RefreshControl",
	ScrollView: "ScrollView",
	StyleSheet: { create: (styles: unknown) => styles },
	Text: "Text",
	View: "View",
}));
vi.mock("../AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("../haptics", () => ({ haptics }));
vi.mock("../refreshControlColors", () => ({ refreshControlColors: () => ({ tintColor: "blue" }) }));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => ({ accent: "blue", textSecondary: "gray" }),
	useThemedStyles: (factory: (theme: Record<string, string>) => unknown) => factory({ accent: "blue", textSecondary: "gray" }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("../ui", () => ({ SheetHeader: "SheetHeader" }));

import { ChatSettingsSheet, EffortSlider } from "./ChatSettingsModal";
import type { ChatConfigOption, ChatModel, ConversationSnapshot } from "./types";

const choices = [
	{ value: "low", label: "Low" },
	{ value: "medium", label: "Medium" },
	{ value: "high", label: "High" },
];

const settled = () => vi.fn((_value: string) => Promise.resolve());

/** A write the test answers by hand, as the sheet does once the daemon replies. */
function pendingWrite() {
	let answer!: () => void;
	const promise = new Promise<void>((resolve) => { answer = resolve; });
	return { promise, answer };
}

function slider(renderer: ReactTestRenderer) {
	return renderer.root.find((node) => (node.type as unknown) === "Slider" && node.props.testID === "turn-settings-effort");
}

/** What the user sees: the thumb's position and the level named above it. */
function shown(renderer: ReactTestRenderer) {
	const value = renderer.root.find((node) => (node.type as unknown) === "ComposeText" && node.props.modifiers?.some((modifier: { testID?: string }) => modifier.testID === "turn-settings-effort-value"));
	return { value: slider(renderer).props.value, label: value.props.children };
}

describe("EffortSlider", () => {
	afterEach(() => {
		vi.useRealTimers();
		vi.restoreAllMocks();
		haptics.select.mockClear();
		sliders.mounted = 0;
	});

	it("does not write when an unlisted effort is mounted or refreshed", () => {
		vi.useFakeTimers();
		const onChange = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="default" unplaced="Not reported" onChange={onChange} />);
		});
		act(() => {
			vi.advanceTimersByTime(300);
		});
		expect(onChange).not.toHaveBeenCalled();

		act(() => {
			renderer.update(<EffortSlider choices={[...choices]} selected="ultra" unplaced="Ultra" onChange={onChange} />);
		});
		act(() => {
			vi.advanceTimersByTime(300);
		});
		expect(onChange).not.toHaveBeenCalled();

		act(() => renderer.unmount());
	});

	it("writes a level once only after the user moves the slider", () => {
		vi.useFakeTimers();
		const onChange = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="default" unplaced="Not reported" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		act(() => {
			vi.advanceTimersByTime(179);
		});
		expect(onChange).not.toHaveBeenCalled();

		act(() => {
			vi.advanceTimersByTime(1);
		});
		expect(onChange).toHaveBeenCalledOnce();
		expect(onChange).toHaveBeenCalledWith("high");
		expect(haptics.select).toHaveBeenCalledOnce();

		act(() => {
			vi.advanceTimersByTime(300);
		});
		expect(onChange).toHaveBeenCalledOnce();
		act(() => renderer.unmount());
	});

	it("does not retry a failed write when the sheet rerenders", async () => {
		vi.useFakeTimers();
		const onChange = settled();
		const afterFailure = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="default" unplaced="Not reported" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		await act(async () => { vi.advanceTimersByTime(180); });
		expect(onChange).toHaveBeenCalledOnce();
		expect(onChange).toHaveBeenCalledWith("high");

		// The server rejected the change. The route shows an error and keeps the
		// old selected value, so a new render must not start another write.
		act(() => {
			renderer.update(<EffortSlider choices={[...choices]} selected="default" unplaced="Not reported" onChange={afterFailure} />);
		});
		act(() => { vi.advanceTimersByTime(500); });
		expect(onChange).toHaveBeenCalledOnce();
		expect(afterFailure).not.toHaveBeenCalled();
		act(() => renderer.unmount());
	});

	it("keeps a user's pending choice through an unrelated sheet rerender", () => {
		vi.useFakeTimers();
		const oldCallback = settled();
		const latestCallback = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="default" unplaced="Not reported" onChange={oldCallback} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		act(() => { vi.advanceTimersByTime(100); });
		act(() => {
			renderer.update(<EffortSlider choices={[...choices]} selected="default" unplaced="Not reported" onChange={latestCallback} />);
		});
		act(() => { vi.advanceTimersByTime(80); });
		expect(oldCallback).not.toHaveBeenCalled();
		expect(latestCallback).toHaveBeenCalledOnce();
		expect(latestCallback).toHaveBeenCalledWith("high");
		act(() => renderer.unmount());
	});

	it("goes back to the saved level when the write is rejected", async () => {
		vi.useFakeTimers();
		const write = pendingWrite();
		const onChange = vi.fn((_value: string) => write.promise);
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		expect(shown(renderer)).toEqual({ value: 2, label: "High" });
		act(() => { vi.advanceTimersByTime(180); });
		expect(onChange).toHaveBeenCalledWith("high");

		// The route disables the sheet while it saves; the move stays on screen.
		act(() => {
			renderer.update(<EffortSlider choices={choices} selected="low" unplaced="Low" disabled onChange={onChange} />);
		});
		expect(shown(renderer)).toEqual({ value: 2, label: "High" });

		// Rejected: the route re-enables the sheet with the old value and settles.
		act(() => {
			renderer.update(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		expect(sliders.mounted).toBe(1);
		await act(async () => write.answer());
		expect(shown(renderer)).toEqual({ value: 0, label: "Low" });
		// The Compose slider keeps a dragged thumb under a held finger whatever
		// value it is given, so it is rebuilt on the saved level.
		expect(sliders.mounted).toBe(2);
		act(() => { vi.advanceTimersByTime(500); });
		expect(onChange).toHaveBeenCalledOnce();
		act(() => renderer.unmount());
	});

	it("goes back to the unplaced label when a write from an unlisted effort is rejected", async () => {
		vi.useFakeTimers();
		const onChange = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="default" unplaced="Not reported" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(1));
		await act(async () => { vi.advanceTimersByTime(180); });
		expect(onChange).toHaveBeenCalledWith("medium");
		expect(shown(renderer)).toEqual({ value: 0, label: "Not reported" });
		expect(sliders.mounted).toBe(2);
		act(() => renderer.unmount());
	});

	it("shows the saved level after the write succeeds", async () => {
		vi.useFakeTimers();
		const write = pendingWrite();
		const onChange = vi.fn((_value: string) => write.promise);
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		act(() => { vi.advanceTimersByTime(180); });
		act(() => {
			renderer.update(<EffortSlider choices={choices} selected="low" unplaced="Low" disabled onChange={onChange} />);
		});
		act(() => {
			renderer.update(<EffortSlider choices={choices} selected="high" unplaced="High" onChange={onChange} />);
		});
		await act(async () => write.answer());
		expect(shown(renderer)).toEqual({ value: 2, label: "High" });
		// The thumb is already where the user left it: nothing to rebuild.
		expect(sliders.mounted).toBe(1);
		act(() => { vi.advanceTimersByTime(500); });
		expect(onChange).toHaveBeenCalledOnce();
		act(() => renderer.unmount());
	});

	it("drops a move from the screen when the sheet disables the slider before it is sent", () => {
		vi.useFakeTimers();
		const onChange = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		act(() => { vi.advanceTimersByTime(100); });
		// A refresh starts before the 180 ms pause ends.
		act(() => {
			renderer.update(<EffortSlider choices={choices} selected="low" unplaced="Low" disabled onChange={onChange} />);
		});
		expect(shown(renderer)).toEqual({ value: 0, label: "Low" });
		expect(sliders.mounted).toBe(2);
		act(() => { vi.advanceTimersByTime(500); });
		expect(onChange).not.toHaveBeenCalled();
		act(() => {
			renderer.update(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		expect(shown(renderer)).toEqual({ value: 0, label: "Low" });
		expect(sliders.mounted).toBe(2);
		act(() => renderer.unmount());
	});

	it("shows a newly saved level in place of a move that was not sent", () => {
		vi.useFakeTimers();
		const onChange = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		act(() => { vi.advanceTimersByTime(100); });
		act(() => {
			renderer.update(<EffortSlider choices={choices} selected="medium" unplaced="Medium" onChange={onChange} />);
		});
		expect(shown(renderer)).toEqual({ value: 1, label: "Medium" });
		expect(sliders.mounted).toBe(2);
		act(() => { vi.advanceTimersByTime(500); });
		expect(onChange).not.toHaveBeenCalled();
		act(() => renderer.unmount());
	});

	it("drops the move when the user slides back onto the saved level", () => {
		vi.useFakeTimers();
		const onChange = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		act(() => { vi.advanceTimersByTime(100); });
		act(() => slider(renderer).props.onValueChange(0));
		expect(shown(renderer)).toEqual({ value: 0, label: "Low" });
		// The finger is on the saved level, so the slider it is dragging stays.
		expect(sliders.mounted).toBe(1);
		act(() => { vi.advanceTimersByTime(500); });
		expect(onChange).not.toHaveBeenCalled();
		act(() => renderer.unmount());
	});

	it("keeps a newer move on screen when an older write settles first", async () => {
		vi.useFakeTimers();
		const first = pendingWrite();
		const second = pendingWrite();
		const onChange = vi.fn((_value: string) => Promise.resolve())
			.mockImplementationOnce(() => first.promise)
			.mockImplementationOnce(() => second.promise);
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(1));
		act(() => { vi.advanceTimersByTime(180); });
		// The thumb moved again before the sheet disabled it for the first save.
		act(() => slider(renderer).props.onValueChange(2));
		act(() => { vi.advanceTimersByTime(180); });
		expect(onChange.mock.calls).toEqual([["medium"], ["high"]]);

		await act(async () => first.answer());
		expect(shown(renderer)).toEqual({ value: 2, label: "High" });
		expect(sliders.mounted).toBe(1);
		await act(async () => second.answer());
		expect(shown(renderer)).toEqual({ value: 0, label: "Low" });
		expect(sliders.mounted).toBe(2);
		act(() => renderer.unmount());
	});
});

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
	{ id: "opus", displayName: "Opus", description: "Most capable", default: true, efforts: ["low", "high"], defaultEffort: "low" },
	{ id: "haiku", displayName: "Haiku", default: false },
];
const options: ChatConfigOption[] = [
	{ id: "fast", name: "Fast mode", type: "boolean", currentBoolean: true, choices: [] },
	{ id: "web", name: "Web search", description: "Lets the agent look things up", type: "boolean", currentBoolean: false, choices: [] },
];

function sheet(over: Partial<Parameters<typeof ChatSettingsSheet>[0]> = {}) {
	const handlers = {
		onRefresh: vi.fn(() => Promise.resolve()),
		onSettings: vi.fn((_settings: unknown) => Promise.resolve()),
		onOption: vi.fn((_id: string, _value: unknown) => Promise.resolve()),
	};
	let renderer!: ReactTestRenderer;
	act(() => {
		renderer = create(<ChatSettingsSheet snapshot={snapshot()} models={models} options={options} {...handlers} {...over} />);
	});
	return { renderer, ...handlers };
}
const sectionTitles = (renderer: ReactTestRenderer) => renderer.root.findAll((node) => (node.type as unknown) === "FieldGroupSection").map((node) => node.props.title);
const footers = (renderer: ReactTestRenderer) => renderer.root.findAll((node) => (node.type as unknown) === "FieldGroupSectionFooter").map((node) => node.findByType("ComposeText" as never).props.children);

describe("turn settings sheet on Android", () => {
	afterEach(() => {
		platform.OS = "android";
		haptics.select.mockClear();
	});

	it("groups the controls in sentence-case sections with their hints in the footers", () => {
		const { renderer } = sheet();
		expect(sectionTitles(renderer)).toEqual([undefined, "Model", "Reasoning effort", "Permissions", undefined]);
		expect(footers(renderer)).toEqual([
			"Faster responses on supported models",
			"Most capable",
			"More effort can improve harder tasks",
			"The agent requests approval when it needs it",
			"Lets the agent look things up",
		]);
		expect(renderer.root.findAllByType("Switch" as never).map((node) => [node.props.label, node.props.value])).toEqual([["Fast mode", true], ["Web search", false]]);
		act(() => renderer.unmount());
	});

	it("footers a provider setting with its description and the current choice's", () => {
		const model: ChatConfigOption = { id: "model", name: "Model", description: "AI model to use", category: "model", type: "select", currentValue: "gpt", choices: [{ value: "gpt", name: "GPT", description: "Balanced" }, { value: "o3", name: "o3" }] };
		const { renderer } = sheet({ snapshot: snapshot({ capabilities: ["config_options"] }), options: [model] });
		const footer = renderer.root.findAll((node) => (node.type as unknown) === "FieldGroupSectionFooter")[0];
		expect(footer.findAllByType("ComposeText" as never).map((node) => node.props.children)).toEqual(["AI model to use", "Balanced"]);
		act(() => renderer.unmount());
	});

	it("picks from a Material menu whose current choice is the filled radio", () => {
		const { renderer, onSettings } = sheet();
		const model = renderer.root.findAll((node) => (node.type as unknown) === "DropdownMenu")[0];
		expect(model.findAll((node) => (node.type as unknown) === "ComposeText").slice(0, 2).map((node) => node.props.children)).toEqual(["Model", "Opus"]);
		expect(model.findAll((node) => (node.type as unknown) === "RadioButton").map((node) => node.props.selected)).toEqual([true, false]);
		const trigger = model.find((node) => (node.type as unknown) === "ComposeColumn");
		const open = trigger.props.modifiers.find((modifier: { clickable?: () => void }) => modifier.clickable).clickable;
		act(() => open());
		expect(model.props.expanded).toBe(true);
		act(() => model.findAll((node) => (node.type as unknown) === "DropdownMenuItem")[1].props.onClick());
		expect(onSettings).toHaveBeenCalledExactlyOnceWith({ model: "haiku", approvalMode: "auto", reasoningEffort: undefined });
		expect(haptics.select).toHaveBeenCalledOnce();
		act(() => renderer.unmount());
	});

	it("refreshes the catalog from a pull on the list", () => {
		const { renderer, onRefresh } = sheet({ refreshing: true });
		const refresh = renderer.root.findByType("ScrollView" as never).props.refreshControl;
		expect(refresh.props.refreshing).toBe(true);
		refresh.props.onRefresh();
		expect(onRefresh).toHaveBeenCalledOnce();
		expect(renderer.root.findAll((node) => node.props.accessibilityLabel === "Refresh turn settings")).toHaveLength(0);
		act(() => renderer.unmount());
	});
});

describe("turn settings sheet on either platform", () => {
	afterEach(() => {
		platform.OS = "android";
	});

	const notice = (renderer: ReactTestRenderer) => renderer.root.find((node) => (node.type as unknown) === "View" && node.props.accessibilityRole === "alert");
	const texts = (node: ReturnType<typeof notice>) => node.findAllByType("Text" as never).map((text) => text.props.children);

	it.each(["ios", "android"])("says on %s which model was requested and why another answers", (os) => {
		platform.OS = os;
		const at = "2026-10-26T00:00:00Z";
		const { renderer } = sheet({ snapshot: snapshot({ modelReroute: { fromModel: "Opus", toModel: "Sonnet", reason: "Opus is at capacity.", at } }) });
		const rerouted = notice(renderer);
		expect(rerouted.props.accessible).toBe(true);
		expect(texts(rerouted)).toEqual(["Currently answered by Sonnet", "Opus was requested. Opus is at capacity."]);
		act(() => renderer.update(<ChatSettingsSheet snapshot={snapshot({ modelReroute: { toModel: "Sonnet", at } })} models={models} options={options} onRefresh={vi.fn()} onSettings={vi.fn()} onOption={vi.fn()} />));
		expect(texts(notice(renderer))).toEqual(["Currently answered by Sonnet", "The provider selected a fallback model for this conversation."]);
		act(() => renderer.unmount());
	});
});

describe("turn settings sheet on iOS", () => {
	afterEach(() => {
		platform.OS = "android";
		haptics.select.mockClear();
	});

	it("is a Form of inline pickers, refreshed by pulling it down", async () => {
		platform.OS = "ios";
		const { renderer, onRefresh, onSettings } = sheet();
		const form = renderer.root.find((node) => (node.type as unknown) === "FieldGroup");
		const refreshable = form.props.modifiers.find((modifier: { refreshable?: () => Promise<void> }) => modifier.refreshable).refreshable;
		await refreshable();
		expect(onRefresh).toHaveBeenCalledOnce();
		const pickers = renderer.root.findAllByType("SwiftUIPicker" as never);
		expect(pickers.map((picker) => picker.props.label)).toEqual(["Model", "Reasoning effort", "Permission mode"]);
		expect(pickers[0].props.modifiers).toEqual([{ pickerStyle: "inline" }, { labelsHidden: true }, { disabled: false }]);
		expect(renderer.root.findAll((node) => (node.type as unknown) === "Slider")).toHaveLength(0);
		act(() => pickers[1].props.onSelectionChange("high"));
		expect(onSettings).toHaveBeenCalledExactlyOnceWith({ model: "opus", approvalMode: "auto", reasoningEffort: "high" });
		act(() => renderer.unmount());
	});

	it("gives an agent picker, a second option of a kind and execution modes their own named sections", () => {
		platform.OS = "ios";
		const provider: ChatConfigOption[] = [
			{ id: "agent", name: "Agent", description: "Which agent answers", category: "model", type: "select", currentValue: "build", choices: [{ value: "build", name: "Build" }, { value: "plan", name: "Plan" }] },
			{ id: "model", name: "Model", description: "AI model to use", category: "model", type: "select", currentValue: "gpt", choices: [{ value: "gpt", name: "GPT" }] },
			{ id: "effort", name: "Effort", description: "How hard it thinks", category: "thought_level", type: "select", currentValue: "high", choices: [{ value: "high", name: "High" }] },
			{ id: "reasoning_summary", name: "Reasoning summary", category: "thought_level", type: "select", currentValue: "auto", choices: [{ value: "auto", name: "Auto" }, { value: "none", name: "None" }] },
			{ id: "mode", name: "Mode", category: "mode", type: "select", currentValue: "build", choices: [{ value: "build", name: "build" }, { value: "plan", name: "plan" }] },
		];
		const { renderer, onOption, onSettings } = sheet({ snapshot: snapshot({ capabilities: ["config_options"] }), options: provider });
		expect(sectionTitles(renderer)).toEqual(["Model", "Agent", "Reasoning effort", "Reasoning summary", "Permissions", "Mode"]);
		const iosFooters = renderer.root.findAll((node) => (node.type as unknown) === "FieldGroupSectionFooter").map((node) => node.findByType("SwiftUIText" as never).props.children);
		// The provider's own descriptions; iOS lists each choice's under it.
		expect(iosFooters).toEqual(["AI model to use", "Which agent answers", "How hard it thinks"]);
		const pickers = renderer.root.findAllByType("SwiftUIPicker" as never);
		expect(pickers.map((picker) => picker.props.label)).toEqual(["Model", "Agent", "Reasoning effort", "Reasoning summary", "Permission mode", "Mode"]);
		// The provider's model, not the native catalog, and the agent's own choices.
		expect(pickers[0].props.children.map((choice: { key: string }) => choice.key)).toEqual(["gpt"]);
		act(() => pickers[1].props.onSelectionChange("plan"));
		expect(onOption).toHaveBeenLastCalledWith("agent", { value: "plan" });
		// Build/plan sets no permissions, so the approvals stay reachable.
		act(() => pickers[4].props.onSelectionChange("bypass-permissions"));
		expect(onSettings).toHaveBeenCalledExactlyOnceWith({ model: "opus", approvalMode: "bypass-permissions" });
		act(() => renderer.unmount());
	});

	it("names an agent picker that comes alone as itself, not as the model", () => {
		platform.OS = "ios";
		const agent: ChatConfigOption = { id: "agent", name: "Agent", category: "model", type: "select", currentValue: "build", choices: [{ value: "build", name: "Build" }, { value: "plan", name: "Plan" }] };
		const { renderer } = sheet({ snapshot: snapshot({ capabilities: ["config_options"] }), options: [agent] });
		expect(sectionTitles(renderer)).toEqual(["Agent", "Reasoning effort", "Permissions"]);
		act(() => renderer.unmount());
	});

	it("rebuilds its pickers on the saved value once a save comes back", () => {
		platform.OS = "ios";
		const { renderer, ...handlers } = sheet();
		const before = renderer.root.findAllByType("SwiftUIPicker" as never)[0];
		act(() => renderer.update(<ChatSettingsSheet snapshot={snapshot()} models={models} options={options} disabled {...handlers} />));
		expect(renderer.root.findAllByType("SwiftUIPicker" as never)[0]).toBe(before);
		act(() => renderer.update(<ChatSettingsSheet snapshot={snapshot()} models={models} options={options} {...handlers} />));
		expect(renderer.root.findAllByType("SwiftUIPicker" as never)[0]).not.toBe(before);
		act(() => renderer.unmount());
	});
});
