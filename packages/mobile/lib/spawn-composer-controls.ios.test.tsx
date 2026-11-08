import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }),
	select: vi.fn(),
	tap: vi.fn(),
}));

vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemeState: () => ({ scheme: "dark" }), useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme) }));
vi.mock("./harnessLogoAssets", () => ({ logoFor: () => undefined }));
vi.mock("./haptics", () => ({ haptics: { select: fixtures.select, tap: fixtures.tap } }));
vi.mock("./effort-gauge", async () => {
	const React = await import("react");
	return { EffortGaugeButton: (props: object) => React.createElement("EffortGaugeButton", props) };
});
vi.mock("./effort-slider", async () => {
	const React = await import("react");
	return { EffortSliderOverlay: (props: object) => React.createElement("EffortSlider", props) };
});
vi.mock("expo-asset", () => ({ Asset: { fromModule: vi.fn() } }));
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		BackHandler: { addEventListener: () => ({ remove: () => {} }) },
		Keyboard: { addListener: () => ({ remove: () => {} }) },
		Platform: { OS: "ios", Version: "26.0" },
		StyleSheet: { create: (styles: object) => styles },
		View: host("View"),
	};
});
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return {
		Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Host", props, children),
		RNHostView: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("RNHostView", props, children),
	};
});
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Button: host("Button"), Divider: host("Divider"), Group: host("Group"), HStack: host("HStack"), Image: host("Image"), Label: host("Label"), Menu: host("Menu"), RNHostView: host("RNHostView"),
		ProgressView: host("ProgressView"), Section: host("Section"), Spacer: host("Spacer"), Text: host("Text"), Toggle: host("Toggle"), VStack: host("VStack"),
	};
});
vi.mock("@expo/ui/swift-ui/modifiers", () => {
	const record = (name: string) => (...args: unknown[]) => ({ modifier: name, args });
	return {
		...Object.fromEntries([
			"accessibilityIdentifier", "accessibilityLabel", "aspectRatio", "backgroundOverlay", "buttonStyle", "clipShape", "contentShape", "controlSize", "disabled", "font",
			"foregroundStyle", "frame", "glassEffect", "labelStyle", "layoutPriority", "lineLimit", "menuOrder", "opacity", "padding", "resizable", "tint", "truncationMode",
		].map((name) => [name, record(name)])),
		shapes: { rectangle: () => ({ shape: "rectangle" }) },
	};
});

vi.mock("./spawn-composer", async () => {
	const React = await import("react");
	// The shared shell is tested on its own; here it only holds the settings.
	return { SpawnComposer: ({ settings, ...props }: { settings?: React.ReactNode }) => React.createElement("SpawnComposer", props, settings) };
});
import { SpawnComposerControls } from "./spawn-composer-controls.ios";
import { rootTouchStart } from "./touchOutside";
import { touchTarget } from "./tokens";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
	fixtures.select.mockClear();
	fixtures.tap.mockClear();
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

type Props = Parameters<typeof SpawnComposerControls>[0];
type Modifier = { modifier: string; args: unknown[] };

const baseProps = (overrides: Partial<Props> = {}): Props => ({
	hosts: [{ id: "host-a", label: "Alpha" }, { id: "host-b", label: "Beta" }], hostId: "host-a", onSelectHost: vi.fn(),
	projects: [{ id: "project-1", label: "Project", icon: "folder" }, { id: "standalone", label: "Standalone agent", icon: "message-square-plus", sectionBreakBefore: true }],
	projectId: "project-1", onSelectProject: vi.fn(), agents: [{ id: "codex", label: "Codex" }, { id: "aider", label: "Aider" }], harness: "codex",
	onSelectHarness: vi.fn(), models: [{ id: "gpt-5", label: "GPT-5" }],
	agentModels: { codex: { models: [{ id: "gpt-5", label: "GPT-5" }], total: 1 }, aider: { models: [], total: 0 } },
	onSelectAgentModel: vi.fn(), usesDefaultModel: true, prompt: "", onChangePrompt: vi.fn(), placeholder: "Ask Codex", attachments: null,
	onBrowseModels: vi.fn(),
	approvalModes: [{ id: "auto", label: "Auto", shortLabel: "Auto" }, { id: "bypass-permissions", label: "Bypass permissions", shortLabel: "Bypass" }],
	approvalMode: "auto", onSelectApprovalMode: vi.fn(),
	effortChoices: [{ value: "low", label: "Low", selected: false }, { value: "high", label: "High", selected: true }],
	onSelectEffort: vi.fn(), modelSelection: "__auto__", modelLabel: "Default model", onSelectModel: vi.fn(), onChoosePhoto: vi.fn(),
	onChooseFile: vi.fn(), voice: { state: "idle", partial: "", subscribeLevel: () => () => {}, onToggleLatched: vi.fn() }, onSpawn: vi.fn(), busy: false,
	disabled: false,
	...overrides,
});

async function render(props: Props) {
	await act(async () => { renderer = create(<SpawnComposerControls {...props} />); });
	return renderer!.root;
}

const hostType = (node: ReactTestInstance): string => (typeof node.type === "string" ? node.type : "");
const modifiers = (node: ReactTestInstance) => (node.props.modifiers ?? []) as Modifier[];
const modifier = (node: ReactTestInstance, name: string) => modifiers(node).find((entry) => entry.modifier === name);
const gaugeOf = (root: ReactTestInstance) => root.findAll((node) => hostType(node) === "EffortGaugeButton" && node.props.testID === "spawn-effort")[0];
const identified = (root: ReactTestInstance, id: string) =>
	root.findAll((node) => hostType(node) !== "" && modifiers(node).some((entry) => entry.modifier === "accessibilityIdentifier" && entry.args[0] === id))[0];
const choices = (menu: ReactTestInstance) => menu.findAll((node) => hostType(node) === "Toggle");
const choiceLabel = (choice: ReactTestInstance): string => choice.props.label ?? choice.findByType("Label" as never).props.title;

describe("iOS spawn selectors", () => {
	it("checks the current machine with the system's checkable item and selects another", async () => {
		const props = baseProps();
		const root = await render(props);
		const menu = identified(root, "spawn-host");
		expect(hostType(menu)).toBe("Menu");
		expect(choices(menu).map((choice) => [choiceLabel(choice), choice.props.isOn])).toEqual([["Alpha", true], ["Beta", false]]);

		await act(async () => choices(menu)[1].props.onIsOnChange(true));
		expect(props.onSelectHost).toHaveBeenCalledWith("host-b");
		expect(fixtures.select).toHaveBeenCalledOnce();

		// Turning the current choice off again is not a choice.
		await act(async () => choices(menu)[0].props.onIsOnChange(false));
		expect(props.onSelectHost).toHaveBeenCalledOnce();
	});

	it("separates Standalone agent from the projects", async () => {
		const props = baseProps();
		const root = await render(props);
		const menu = identified(root, "spawn-project");
		const rows = menu.findAll((node) => hostType(node) === "Toggle" || hostType(node) === "Divider");
		expect(rows.map((node) => hostType(node) === "Divider" ? "divider" : [node.props.label, node.props.isOn])).toEqual([["Project", true], "divider", ["Standalone agent", false]]);
		await act(async () => choices(menu)[1].props.onIsOnChange(true));
		expect(props.onSelectProject).toHaveBeenCalledWith("standalone");
	});

	// Each agent opens onto its own models, so picking a model picks its agent.
	// The task's own agent's models sit at the menu's top level, the current one
	// checked, so changing just the model is one tap; other agents open below.
	it("lists the current agent's models at the top, checked, and nests the other agents", async () => {
		const props = baseProps({ modelSelection: "gpt-5", modelLabel: "GPT-5" });
		const root = await render(props);
		const sections = identified(root, "spawn-harness").findAll((node) => hostType(node) === "Section");
		expect(sections.map((section) => section.props.title)).toEqual(["Codex", "Other agents"]);
		expect(choices(sections[0]).map((choice) => [choiceLabel(choice), choice.props.isOn])).toEqual([["Default model", false], ["GPT-5", true]]);
		await act(async () => choices(sections[0])[0].props.onIsOnChange(true));
		expect(props.onSelectAgentModel).toHaveBeenCalledWith("codex", "__auto__");
		expect(root.findAll((node) => modifier(node, "accessibilityIdentifier")?.args[0] === "spawn-model-codex")).toHaveLength(0);
		const aider = identified(root, "spawn-model-aider");
		expect(aider.props.label.props.title).toBe("Aider");
		// An agent without a list yet still offers its default model.
		expect(choices(aider).map((choice) => [choiceLabel(choice), choice.props.isOn])).toEqual([["Default model", false]]);
		await act(async () => choices(aider)[0].props.onIsOnChange(true));
		expect(props.onSelectAgentModel).toHaveBeenCalledWith("aider", "__auto__");
	});

	// The trigger names one thing: the model once one is
	// picked, the agent while it runs its default. VoiceOver hears both.
	it("shows the harness mark with the picked model, or the agent on its default", async () => {
		const shown = async (props: Props) => {
			const root = await render(props);
			const menu = identified(root, "spawn-harness");
			let face: ReactTestRenderer | undefined;
			await act(async () => { face = create(menu.props.label); });
			const texts = face!.root.findAll((node) => hostType(node) === "Text").map((node) => node.props.children);
			const label = modifier(menu, "accessibilityLabel")?.args[0];
			await act(async () => face!.unmount());
			await act(async () => renderer?.unmount());
			renderer = undefined;
			return { texts, label };
		};
		expect(await shown(baseProps({ modelLabel: "Default model" }))).toEqual({ texts: ["Codex"], label: "Codex" });
		expect(await shown(baseProps({ modelSelection: "gpt-5", modelLabel: "GPT-5", usesDefaultModel: false }))).toEqual({ texts: ["GPT-5"], label: "Codex, GPT-5" });
		// On its default, the trigger names the model the catalog marks as it.
		const named = { codex: { models: [{ id: "gpt-5", label: "GPT-5" }], total: 1, defaultLabel: "GPT-6.1-Sol" }, aider: { models: [], total: 0 } };
		// VoiceOver says the model the capsule shows, not the generic "Default model".
		expect(await shown(baseProps({ modelLabel: "Default model", agentModels: named }))).toMatchObject({ texts: ["GPT-6.1-Sol"], label: "Codex, GPT-6.1-Sol" });
		// A model the project pins is named, though nothing was picked here.
		expect((await shown(baseProps({ modelLabel: "GPT-5", usesDefaultModel: false }))).texts).toEqual(["GPT-5"]);
	});

	it("names the default row by the catalog's default and opens a long catalog whole", async () => {
		const props = baseProps({ agentModels: { codex: { models: [{ id: "gpt-5", label: "GPT-5" }], total: 8929, defaultLabel: "GPT-6.1-Sol" }, aider: { models: [], total: 0 } } });
		const root = await render(props);
		const codex = identified(root, "spawn-harness").findAll((node) => hostType(node) === "Section" && node.props.title === "Codex")[0];
		expect(choiceLabel(choices(codex)[0])).toBe("GPT-6.1-Sol · Default");
		const all = codex.findAll((node) => hostType(node) === "Button" && String(node.props.label).startsWith("All models"))[0];
		expect(all.props.label).toBe(`All models (${(8929).toLocaleString()})`);
		await act(async () => all.props.onPress());
		expect(props.onBrowseModels).toHaveBeenCalledWith("codex");
		// A catalog the menu lists whole has no "All models".
		expect(identified(root, "spawn-model-aider").findAll((node) => hostType(node) === "Button")).toHaveLength(0);
	});

	// As in chat: each family's newest at the top, older versions one level down.
	it("folds older models one level down under Older models", async () => {
		const props = baseProps({ agentModels: { codex: { models: [{ id: "fable-5-1", label: "Fable 5.1" }], older: [{ id: "fable-5", label: "Fable 5" }], total: 2 }, aider: { models: [], total: 0 } } });
		const root = await render(props);
		const codex = identified(root, "spawn-harness").findAll((node) => hostType(node) === "Section" && node.props.title === "Codex")[0];
		const older = codex.findAll((node) => hostType(node) === "Menu" && node.props.label === "Older models")[0];
		expect(choices(older).map(choiceLabel)).toEqual(["Fable 5"]);
		expect(choices(codex).map(choiceLabel)).toEqual(["Default model", "Fable 5.1", "Fable 5"]);
		await act(async () => choices(older)[0].props.onIsOnChange(true));
		expect(props.onSelectAgentModel).toHaveBeenCalledWith("codex", "fable-5");
		// Every model is listed, so there is no "All models".
		expect(codex.findAll((node) => hostType(node) === "Button" && String(node.props.label).startsWith("All models"))).toHaveLength(0);
	});

	it("picks the approval mode from its own symbol at the controls row's end", async () => {
		const props = baseProps({ approvalMode: "bypass-permissions" });
		const root = await render(props);
		const menu = identified(root, "spawn-approval");
		expect(modifier(menu, "accessibilityLabel")?.args).toEqual(["Approval, Bypass permissions"]);
		expect(choices(menu).map((choice) => [choiceLabel(choice), choice.props.isOn])).toEqual([["Auto", false], ["Bypass permissions", true]]);
		await act(async () => choices(menu)[0].props.onIsOnChange(true));
		expect(props.onSelectApprovalMode).toHaveBeenCalledWith("auto");
	});

	// The speedometer opens the slider above the composer; the slider picks
	// the level and stays up until a tap lands outside it.
	it("opens the effort slider from a speedometer beside the model", async () => {
		const props = baseProps();
		const root = await render(props);
		const gauge = gaugeOf(root);
		expect(gauge.props.choices).toEqual(props.effortChoices);
		expect(root.findAll((node) => hostType(node) === "EffortSlider")).toHaveLength(0);
		await act(async () => gauge.props.onPress());
		const slider = root.findAll((node) => hostType(node) === "EffortSlider")[0];
		expect(gaugeOf(root).props.open).toBe(true);
		await act(async () => slider.props.onChoose("low"));
		expect(props.onSelectEffort).toHaveBeenCalledWith("low");
		// A tap anywhere outside it closes it.
		await act(async () => rootTouchStart());
		expect(root.findAll((node) => hostType(node) === "EffortSlider")).toHaveLength(0);
	});

	it("draws no speedometer for a model that takes no effort", async () => {
		const root = await render(baseProps({ effortChoices: [] }));
		expect(gaugeOf(root)).toBeUndefined();
	});

	// A plain-style Menu hears taps only inside its label, so each trigger's face
	// is at least a touch target; its text scales with Dynamic Type.
	it("answers taps across a full touch target and scales text with Dynamic Type", async () => {
		const root = await render(baseProps());
		for (const id of ["spawn-host", "spawn-project", "spawn-harness", "spawn-approval"]) {
			let face: ReactTestRenderer | undefined;
			await act(async () => { face = create(identified(root, id).props.label); });
			const top = face!.root.findAll((node) => hostType(node) !== "")[0];
			const frame = modifier(top, "frame")?.args[0] as { minHeight?: number; height?: number };
			expect(frame.minHeight ?? frame.height).toBeGreaterThanOrEqual(touchTarget);
			expect(modifier(top, "contentShape")?.args).toEqual([{ shape: "rectangle" }]);
			for (const text of face!.root.findAll((node) => hostType(node) === "Text")) expect(modifier(text, "font")?.args[0]).not.toHaveProperty("size");
			await act(async () => face!.unmount());
		}
	});
});
