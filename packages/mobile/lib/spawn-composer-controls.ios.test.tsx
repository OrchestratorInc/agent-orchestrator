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
	return {
		EffortGaugeButton: (props: object) => React.createElement("EffortGaugeButton", props),
		effortGaugeToggles: (fast?: { on: boolean }, ultracode?: { on: boolean }) => (fast || ultracode ? { fast: fast?.on, ultracode: ultracode?.on } : undefined),
	};
});
vi.mock("./effort-slider", async () => {
	const React = await import("react");
	return { EffortSliderOverlay: (props: object) => React.createElement("EffortSlider", props) };
});
vi.mock("expo-asset", () => ({ Asset: { fromModule: vi.fn() } }));
// The chat's own pickers, tested on their own; here, what New task hands them.
vi.mock("./model-wheel", async () => {
	const React = await import("react");
	return { ModelWheelHost: (props: object) => React.createElement("ModelWheelHost", props) };
});
vi.mock("./permission-picker", async () => {
	const React = await import("react");
	return {
		PermissionPickerButton: (props: object) => React.createElement("PermissionPickerButton", props),
		PermissionPickerHost: (props: object) => React.createElement("PermissionPickerHost", props),
	};
});
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
			"accessibilityIdentifier", "accessibilityLabel", "aspectRatio", "backgroundOverlay", "buttonStyle", "clipShape", "contentShape", "controlSize", "disabled", "dynamicTypeSize", "font",
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
import { HarnessImage, SpawnComposerControls } from "./spawn-composer-controls.ios";
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
	// The composer lays out as on a phone: the effort slider floats that high.
	const composer = renderer!.root.findAll((node) => typeof node.type === "string" && typeof node.props.onLayout === "function")[0];
	await act(async () => composer.props.onLayout({ nativeEvent: { layout: { x: 0, y: 0, width: 360, height: 120 } } }));
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

describe("iOS harness mark", () => {
	const drawn = async (harness: string) => {
		await act(async () => { renderer = create(<HarnessImage harness={harness} />); });
		const image = renderer!.root.findByType("Image" as never);
		return { assetName: image.props.assetName, systemName: image.props.systemName };
	};

	it("draws a mark the binary's catalog has by name", async () => {
		expect(await drawn("codex")).toEqual({ assetName: "HarnessLogo-codex", systemName: undefined });
	});

	// gemini has a mark, but its image set only reaches users with the next store
	// build; asking an older catalog for it would draw an empty slot.
	it("draws the terminal symbol for a harness whose image set the binary may lack", async () => {
		expect(await drawn("gemini")).toEqual({ assetName: undefined, systemName: "terminal" });
		expect(await drawn("some-new-agent")).toEqual({ assetName: undefined, systemName: "terminal" });
	});
});

describe("iOS spawn selectors", () => {
	// Each machine with its glyph, as each project has its folder.
	it("draws a computer for each machine and a cloud for Orchestrator Cloud, in the menu and on the selector", async () => {
		const root = await render(baseProps({ hosts: [{ id: "host-a", label: "Alpha" }, { id: "orchestrator-cloud", label: "Cloud", icon: "cloud" }], hostId: "orchestrator-cloud" }));
		const menu = identified(root, "spawn-host");
		expect(choices(menu).map((choice) => [choiceLabel(choice), choice.props.systemImage])).toEqual([["Alpha", "laptopcomputer"], ["Cloud", "cloud"]]);
	});

	it("marks New cloud project with a folder and a plus", async () => {
		const root = await render(baseProps({ projects: [{ id: "p", label: "Web", icon: "folder" }, { id: "new-cloud-project", label: "New cloud project", icon: "folder-plus", sectionBreakBefore: true }] }));
		const menu = identified(root, "spawn-project");
		expect(choices(menu).map((choice) => choice.props.systemImage)).toEqual(["folder", "folder.badge.plus"]);
	});

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
	// As in chat: the capsule opens the model wheels over the keyboard, every
	// agent and its models, the default first and starred; closing them on a
	// model picks the agent and the model at once.
	it("opens the chat's model wheels from the capsule, and picks an agent and its model from them", async () => {
		const props = baseProps({ agentModels: { codex: { models: [{ id: "gpt-5", label: "GPT-5" }], older: [{ id: "gpt-4", label: "GPT-4" }], total: 2, defaultLabel: "GPT-6.1-Sol" }, aider: { models: [], total: 0 } } });
		const root = await render(props);
		const wheels = () => root.findByType("ModelWheelHost" as never);
		expect(wheels().props.slider.isOpen()).toBe(false);
		await act(async () => identified(root, "spawn-harness").props.onPress());
		expect(wheels().props.slider.isOpen()).toBe(true);
		expect(wheels().props.agents.map((agent: { id: string; models: { key: string; label: string }[] }) => [agent.id, agent.models.map((model) => [model.key, model.label])])).toEqual([
			["codex", [["__auto__", "GPT-6.1-Sol"], ["gpt-5", "GPT-5"], ["gpt-4", "GPT-4"]]],
			["aider", [["__auto__", "Default model"]]],
		]);
		await act(async () => wheels().props.choose("aider", "__auto__"));
		expect(props.onSelectAgentModel).toHaveBeenCalledWith("aider", "__auto__");
	});

	// The trigger names one thing: the model once one is
	// picked, the agent while it runs its default. VoiceOver hears both.
	it("shows the harness mark with the picked model, or the agent on its default", async () => {
		const shown = async (props: Props) => {
			const root = await render(props);
			const menu = identified(root, "spawn-harness");
			let face: ReactTestRenderer | undefined;
			await act(async () => { face = create(menu.props.children); });
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

	// As in chat: the approval mode's own icon opens the permission picker.
	it("opens the chat's permission picker from the approval mode's icon", async () => {
		const props = baseProps({ approvalMode: "bypass-permissions" });
		const root = await render(props);
		expect(root.findByType("PermissionPickerButton" as never).props.testID).toBe("spawn-approval");
		const picker = root.findByType("PermissionPickerHost" as never);
		expect(picker.props.choices.map((choice: { label: string; selected: boolean }) => [choice.label, choice.selected])).toEqual([["Auto", false], ["Bypass permissions", true]]);
		await act(async () => picker.props.onChoose("auto"));
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

	// Fast mode rides on the slider as its bolt, as in chat, where the task can start in it.
	it("hands the effort slider Fast mode's bolt only where the task offers it", async () => {
		const fast = { on: true, toggle: vi.fn() };
		const root = await render(baseProps({ fast }));
		await act(async () => gaugeOf(root).props.onPress());
		const slider = root.findAll((node) => hostType(node) === "EffortSlider")[0];
		expect(slider.props.fast).toBe(fast);
		act(() => renderer?.unmount());
		const plain = await render(baseProps());
		await act(async () => gaugeOf(plain).props.onPress());
		expect(plain.findAll((node) => hostType(node) === "EffortSlider")[0].props.fast).toBeUndefined();
	});

	// Claude Code's Ultracode rides on the slider as its switch, as in chat.
	it("hands the effort slider Ultracode's switch only where the task offers it", async () => {
		const ultracode = { on: true, toggle: vi.fn() };
		const root = await render(baseProps({ ultracode }));
		await act(async () => gaugeOf(root).props.onPress());
		expect(root.findAll((node) => hostType(node) === "EffortSlider")[0].props.ultracode).toBe(ultracode);
		act(() => renderer?.unmount());
		const plain = await render(baseProps());
		await act(async () => gaugeOf(plain).props.onPress());
		expect(plain.findAll((node) => hostType(node) === "EffortSlider")[0].props.ultracode).toBeUndefined();
	});

	// Claude Haiku takes no effort, but its Ultracode is still on the slider.
	it("keeps the speedometer for a model with no levels while the slider carries a switch", async () => {
		const ultracode = { on: false, toggle: vi.fn() };
		const root = await render(baseProps({ effortChoices: [], ultracode }));
		// The gauge is handed the switches, to show and name them.
		expect(gaugeOf(root).props.toggles).toEqual({ fast: undefined, ultracode: false });
		await act(async () => gaugeOf(root).props.onPress());
		const slider = root.findAll((node) => hostType(node) === "EffortSlider")[0];
		expect(slider.props).toMatchObject({ choices: [], ultracode, visible: true });
	});

	it("draws no speedometer for a model that takes no effort", async () => {
		const root = await render(baseProps({ effortChoices: [] }));
		expect(gaugeOf(root)).toBeUndefined();
	});

	// A plain-style Menu hears taps only inside its label, so each trigger's face
	// is at least a touch target; its text scales with Dynamic Type.
	it("answers taps across a full touch target and scales text with Dynamic Type", async () => {
		const root = await render(baseProps());
		// The approval mode is the chat's own button, measured in permission-picker's tests.
		for (const id of ["spawn-host", "spawn-project", "spawn-harness"]) {
			let face: ReactTestRenderer | undefined;
			const trigger = identified(root, id);
			await act(async () => { face = create(trigger.props.label ?? trigger.props.children); });
			const top = face!.root.findAll((node) => hostType(node) !== "")[0];
			const frame = modifier(top, "frame")?.args[0] as { minHeight?: number; height?: number };
			expect(frame.minHeight ?? frame.height).toBeGreaterThanOrEqual(touchTarget);
			expect(modifier(top, "contentShape")?.args).toEqual([{ shape: "rectangle" }]);
			for (const text of face!.root.findAll((node) => hostType(node) === "Text")) expect(modifier(text, "font")?.args[0]).not.toHaveProperty("size");
			await act(async () => face!.unmount());
		}
	});

	it("stops the controls row's text at the first accessibility size, where the capsule still holds it", async () => {
		const root = await render(baseProps());
		const row = root.findAll((node) => hostType(node) === "HStack" && node.findAll((child) => child.props.modifiers?.some?.((m: Modifier) => m.args?.[0] === "spawn-harness")).length > 0)[0];
		expect(modifier(row, "dynamicTypeSize")?.args).toEqual([{ max: "accessibility1" }]);
		// The machine and project rows above keep the reader's full setting.
		const selector = root.findAll((node) => hostType(node) === "VStack")[0];
		expect(selector.findAll((node) => modifier(node, "dynamicTypeSize") !== undefined)).toHaveLength(0);
	});
});
