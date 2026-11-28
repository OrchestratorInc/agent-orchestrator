import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }),
	select: vi.fn(),
}));

vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemeState: () => ({ scheme: "dark" }),
	useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme),
}));
vi.mock("./haptics", () => ({ haptics: { select: fixtures.select, tap: () => {} } }));
vi.mock("./harnessLogoAssets", () => ({ logoFor: (harness?: string) => (harness === "codex" || harness === "opencode" ? `logo:${harness}` : null) }));
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
vi.mock("./toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
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
const backHandlers = vi.hoisted(() => new Set<() => boolean>());
vi.mock("react-native", async () => {
	const React = await import("react");
	return {
		Keyboard: { addListener: () => ({ remove: () => {} }) },
		BackHandler: {
			addEventListener: (_event: string, handler: () => boolean) => {
				backHandlers.add(handler);
				return { remove: () => backHandlers.delete(handler) };
			},
		},
		PixelRatio: { getFontScale: () => 1 },
		Platform: { OS: "android" },
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 0.5 },
		View: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("View", props, children),
	};
});
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Box: host("Box"),
		Button: host("Button"),
		CircularProgressIndicator: host("CircularProgressIndicator"),
		Column: host("Column"),
		DropdownMenu: Object.assign(host("DropdownMenu"), { Trigger: host("DropdownMenuTrigger"), Items: host("DropdownMenuItems") }),
		DropdownMenuItem: Object.assign(host("DropdownMenuItem"), {
			Text: host("DropdownMenuItemText"),
			LeadingIcon: host("DropdownMenuItemLeadingIcon"),
			TrailingIcon: host("DropdownMenuItemTrailingIcon"),
		}),
		Host: host("ComposeHost"),
		HorizontalDivider: host("HorizontalDivider"),
		FilledIconButton: host("FilledIconButton"),
		Icon: host("Icon"),
		IconButton: host("IconButton"),
		RNHostView: host("RNHostView"),
		Row: host("Row"),
		Spacer: host("Spacer"),
		Text: host("ComposeText"),
		TextButton: host("TextButton"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => {
	const record = (name: string) => (...args: unknown[]) => ({ modifier: name, args });
	return {
		align: record("align"),
		background: record("background"),
		clip: record("clip"),
		fillMaxWidth: record("fillMaxWidth"),
		height: record("height"),
		onSizeChanged: (handler: (size: { width: number; height: number }) => void) => ({ modifier: "onSizeChanged", args: [], handler }),
		padding: record("padding"),
		semantics: record("semantics"),
		size: record("size"),
		testID: record("testID"),
		weight: record("weight"),
		width: record("width"),
		Shapes: { RoundedCorner: (radius: number) => ({ type: "roundedCorner", radius }) },
	};
});

vi.mock("./spawn-composer", async () => {
	const React = await import("react");
	// The shared shell is tested on its own; here it only holds the settings.
	return { SpawnComposer: ({ settings, ...props }: { settings?: React.ReactNode }) => React.createElement("SpawnComposer", props, settings) };
});
import { SpawnComposerControls } from "./spawn-composer-controls.android";
import { rootTouchStart } from "./touchOutside";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
	fixtures.select.mockClear();
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

type Props = Parameters<typeof SpawnComposerControls>[0];

const baseProps = (overrides: Partial<Props> = {}): Props => ({
	hosts: [{ id: "host-a", label: "Alpha" }, { id: "host-b", label: "Beta" }], hostId: "host-a", onSelectHost: vi.fn(),
	projects: [{ id: "project-1", label: "Project", icon: "folder" }, { id: "standalone", label: "Standalone agent", icon: "message-square-plus", sectionBreakBefore: true }],
	projectId: "project-1", onSelectProject: vi.fn(),
	agents: [{ id: "codex", label: "Codex" }, { id: "opencode", label: "OpenCode" }, { id: "aider", label: "Aider" }],
	harness: "codex", onSelectHarness: vi.fn(), models: [{ id: "gpt-5", label: "GPT-5" }],
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

/** The host element's name, so component wrappers never match. */
const hostType = (node: ReactTestInstance): string => (typeof node.type === "string" ? node.type : "");
const tagged = (node: ReactTestInstance, tag: string) =>
	(node.props.modifiers as { modifier: string; args: unknown[] }[] | undefined)?.some((modifier) => modifier.modifier === "testID" && modifier.args[0] === tag) ?? false;
const trigger = (root: ReactTestInstance, tag: string) => root.findAll((node) => hostType(node) !== "" && tagged(node, tag))[0];
const menuOf = (node: ReactTestInstance) => {
	let current: ReactTestInstance | null = node;
	while (current && hostType(current) !== "DropdownMenu") current = current.parent;
	return current!;
};
const text = (node: ReactTestInstance) => node.findAll((child) => hostType(child) === "ComposeText").map((child) => child.props.children).join("");
const items = (menu: ReactTestInstance) => menu.findAll((node) => hostType(node) === "DropdownMenuItem");
const item = (menu: ReactTestInstance, label: string) => items(menu).find((node) => text(node) === label)!;
const isSelected = (node: ReactTestInstance) =>
	node.findAll((child) => hostType(child) === "DropdownMenuItemTrailingIcon").some((slot) => slot.findByType("Icon" as never).props.contentDescription === "Selected");

const gaugeOf = (root: ReactTestInstance) => root.findAll((node) => hostType(node) === "EffortGaugeButton" && node.props.testID === "spawn-effort")[0];
const modifier = (node: ReactTestInstance, name: string) => (node.props.modifiers as { modifier: string; args: unknown[] }[] | undefined)?.find((entry) => entry.modifier === name);

describe("Android spawn selectors", () => {
	it("names each selector's choice for TalkBack, with every menu closed until asked for", async () => {
		const root = await render(baseProps());
		expect(text(trigger(root, "spawn-host"))).toBe("Alpha");
		expect(modifier(trigger(root, "spawn-host"), "semantics")?.args).toEqual([{ contentDescription: "Run on Alpha" }]);
		expect(text(trigger(root, "spawn-project"))).toBe("Project");
		expect(modifier(trigger(root, "spawn-project"), "semantics")?.args).toEqual([{ contentDescription: "Project, Project" }]);
		expect(root.findAll((node) => hostType(node) === "DropdownMenu").every((menu) => menu.props.expanded === false)).toBe(true);
	});

	// Each machine with its glyph, as each project has its folder.
	it("draws a computer for each machine and a cloud for Orchestrator Cloud in the machine menu", async () => {
		const root = await render(baseProps({ hosts: [{ id: "host-a", label: "Alpha" }, { id: "orchestrator-cloud", label: "Cloud", icon: "cloud" }], hostId: "orchestrator-cloud" }));
		await act(async () => trigger(root, "spawn-host").props.onClick());
		const menu = menuOf(trigger(root, "spawn-host"));
		const glyph = (label: string) => item(menu, label).findAll((node) => hostType(node) === "Icon").map((node) => node.props.source)[0];
		expect(glyph("Alpha")).toBe("drawable:laptop");
		expect(glyph("Cloud")).toBe("drawable:cloud");
	});

	it("selects another machine from its menu and closes it", async () => {
		const props = baseProps();
		const root = await render(props);
		await act(async () => trigger(root, "spawn-host").props.onClick());
		const menu = menuOf(trigger(root, "spawn-host"));
		expect(menu.props.expanded).toBe(true);
		expect(items(menu).map((node) => [text(node), isSelected(node)])).toEqual([["Alpha", true], ["Beta", false]]);

		await act(async () => item(menu, "Beta").props.onClick());
		expect(props.onSelectHost).toHaveBeenCalledWith("host-b");
		expect(fixtures.select).toHaveBeenCalledOnce();
		expect(menuOf(trigger(root, "spawn-host")).props.expanded).toBe(false);
	});

	it("closes without a change when the current choice is picked again", async () => {
		const props = baseProps();
		const root = await render(props);
		await act(async () => trigger(root, "spawn-host").props.onClick());
		const menu = menuOf(trigger(root, "spawn-host"));
		expect(items(menu).map(text)).toEqual(["Alpha", "Beta"]);
		await act(async () => item(menu, "Alpha").props.onClick());
		expect(props.onSelectHost).not.toHaveBeenCalled();
		expect(fixtures.select).not.toHaveBeenCalled();
		expect(menuOf(trigger(root, "spawn-host")).props.expanded).toBe(false);
	});

	it("separates Standalone agent from the projects", async () => {
		const root = await render(baseProps());
		const menu = menuOf(trigger(root, "spawn-project"));
		const rows = menu.findByType("DropdownMenuItems" as never).findAll((node) => hostType(node) === "DropdownMenuItem" || hostType(node) === "HorizontalDivider");
		expect(rows.map((node) => hostType(node) === "HorizontalDivider" ? "divider" : text(node))).toEqual(["Project", "divider", "Standalone agent"]);
	});
});

describe("Android spawn model", () => {
	it("names the agent on its default, the catalog's default, or a model picked or pinned, on the capsule", async () => {
		expect(text(trigger(await render(baseProps()), "spawn-harness"))).toBe("Codex");
		await act(async () => renderer?.unmount());
		const named = { codex: { models: [{ id: "gpt-5", label: "GPT-5" }], total: 1, defaultLabel: "GPT-6.1-Sol" }, aider: { models: [], total: 0 } };
		expect(text(trigger(await render(baseProps({ agentModels: named })), "spawn-harness"))).toBe("GPT-6.1-Sol");
		await act(async () => renderer?.unmount());
		expect(text(trigger(await render(baseProps({ usesDefaultModel: false, modelSelection: "gpt-5", modelLabel: "GPT-5" })), "spawn-harness"))).toBe("GPT-5");
	});

	// The capsule shows the model alone; TalkBack hears the agent with it, as VoiceOver does.
	it("names the capsule for TalkBack by its agent and the model it runs", async () => {
		expect(modifier(trigger(await render(baseProps()), "spawn-harness"), "semantics")?.args).toEqual([{ contentDescription: "Codex" }]);
		await act(async () => renderer?.unmount());
		const named = { codex: { models: [], total: 0, defaultLabel: "GPT-6.1-Sol" }, aider: { models: [], total: 0 } };
		expect(modifier(trigger(await render(baseProps({ agentModels: named })), "spawn-harness"), "semantics")?.args).toEqual([{ contentDescription: "Codex, GPT-6.1-Sol" }]);
	});

	// As in chat: the capsule opens the model wheels over the keyboard, every
	// agent and its models, the default first and starred; closing them on a
	// model picks the agent and the model at once.
	it("opens the chat's model wheels from the capsule, and picks an agent and its model from them", async () => {
		const props = baseProps({ agentModels: { codex: { models: [{ id: "gpt-5", label: "GPT-5" }], older: [{ id: "gpt-4", label: "GPT-4" }], total: 2, defaultLabel: "GPT-6.1-Sol" }, opencode: { models: [{ id: "grok", label: "Grok Code" }], total: 1 }, aider: { models: [], total: 0 } } });
		const root = await render(props);
		const wheels = () => root.findByType("ModelWheelHost" as never);
		expect(wheels().props.slider.isOpen()).toBe(false);
		await act(async () => trigger(root, "spawn-harness").props.onClick());
		expect(wheels().props.slider.isOpen()).toBe(true);
		expect(wheels().props.agents.map((agent: { id: string; models: { key: string; label: string; starred?: boolean }[] }) => [agent.id, agent.models.map((model) => [model.key, model.label, Boolean(model.starred)])])).toEqual([
			["codex", [["__auto__", "GPT-6.1-Sol", true], ["gpt-5", "GPT-5", false], ["gpt-4", "GPT-4", false]]],
			["opencode", [["__auto__", "Default model", true], ["grok", "Grok Code", false]]],
			["aider", [["__auto__", "Default model", true]]],
		]);
		expect(wheels().props).toMatchObject({ chosenAgent: "codex", chosenModel: "__auto__", settings: [] });
		await act(async () => wheels().props.choose("opencode", "grok"));
		expect(props.onSelectAgentModel).toHaveBeenCalledWith("opencode", "grok");
		// The pair already in force changes nothing.
		await act(async () => wheels().props.choose("codex", "__auto__"));
		expect(props.onSelectAgentModel).toHaveBeenCalledOnce();
	});

	// As in chat: the approval mode's own icon opens the permission picker.
	it("opens the chat's permission picker from the approval mode's icon", async () => {
		const props = baseProps();
		const root = await render(props);
		const button = root.findByType("PermissionPickerButton" as never);
		expect(button.props.testID).toBe("spawn-approval");
		const picker = root.findByType("PermissionPickerHost" as never);
		expect(picker.props.choices.map((choice: { value: string; label: string; selected: boolean }) => [choice.value, choice.label, choice.selected])).toEqual([["auto", "Auto", true], ["bypass-permissions", "Bypass permissions", false]]);
		await act(async () => picker.props.onChoose("bypass-permissions"));
		expect(props.onSelectApprovalMode).toHaveBeenCalledWith("bypass-permissions");
		await act(async () => picker.props.onChoose("auto"));
		expect(props.onSelectApprovalMode).toHaveBeenCalledOnce();
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

	// A focusable popup takes focus from the prompt, and Android puts the
	// keyboard away; back, which such a menu no longer hears, closes it.
	it("keeps the keyboard up while a menu is open, and closes the menu or slider on back", async () => {
		const root = await render(baseProps());
		expect(root.findAll((node) => hostType(node) === "DropdownMenu").every((menu) => menu.props.properties?.focusable === false)).toBe(true);
		await act(async () => trigger(root, "spawn-host").props.onClick());
		expect(menuOf(trigger(root, "spawn-host")).props.expanded).toBe(true);
		expect(backHandlers.size).toBe(1);
		await act(async () => { [...backHandlers][0](); });
		expect(menuOf(trigger(root, "spawn-host")).props.expanded).toBe(false);
		expect(backHandlers.size).toBe(0);

		await act(async () => gaugeOf(root).props.onPress());
		expect(root.findAll((node) => hostType(node) === "EffortSlider")).toHaveLength(1);
		await act(async () => { [...backHandlers][0](); });
		expect(root.findAll((node) => hostType(node) === "EffortSlider")).toHaveLength(0);
	});

	it("draws no speedometer for a model that takes no effort", async () => {
		const root = await render(baseProps({ effortChoices: [] }));
		expect(gaugeOf(root)).toBeUndefined();
	});

	// The capsule takes what the speedometer and the approval mode leave: a long
	// name pushed both out of the row, and Material's 8dp above and below the
	// name cut it off at larger font sizes.
	const hostParent = (node: ReactTestInstance) => {
		let current = node.parent;
		while (current && hostType(current) === "") current = current.parent;
		return current!;
	};

	it("measures the capsule after the speedometer and the approval mode, with no padding above or below its name", async () => {
		const root = await render(baseProps());
		const capsule = trigger(root, "spawn-harness");
		expect(capsule.props.contentPadding).toMatchObject({ top: 0, bottom: 0 });
		expect(modifier(capsule, "height")?.args).toEqual([34]);
		const menu = menuOf(capsule);
		expect(modifier(menu, "weight")?.args).toEqual([1, { fill: false }]);
		const start = hostParent(menu);
		expect(hostType(start)).toBe("Row");
		expect(modifier(start, "weight")?.args).toEqual([1]);
		expect(start.findAll((node) => hostType(node) === "EffortGaugeButton" || node.props.testID === "spawn-effort").length).toBeGreaterThan(0);
		// The approval mode sits outside the weighted part, at the row's end.
		expect(start.findAll((node) => hostType(node) === "PermissionPickerButton")).toHaveLength(0);
		expect(root.findAll((node) => hostType(node) === "PermissionPickerButton")).toHaveLength(1);
	});

	it("keeps the arrow when the name ellipsizes, and draws the mark alone where a few letters would not fit", async () => {
		const root = await render(baseProps({ usesDefaultModel: false, modelSelection: "gpt-5", modelLabel: "Qwen3.5 27B Claude 4.6 Opus Reasoning Distilled" }));
		const arrow = () => trigger(root, "spawn-harness").findAll((node) => hostType(node) === "Icon" && node.props.source === "drawable:unfold_more")[0];
		expect(text(trigger(root, "spawn-harness"))).toBe("Qwen3.5 27B Claude 4.6 Opus Reasoning Distilled");
		// Drawn over the end of the name's box, which keeps room for it.
		expect(modifier(arrow(), "align")?.args).toEqual(["centerEnd"]);
		const report = (width: number) => act(async () => {
			const start = hostParent(menuOf(trigger(root, "spawn-harness")));
			(start.props.modifiers as { modifier: string; handler?: (size: { width: number; height: number }) => void }[]).find((entry) => entry.modifier === "onSizeChanged")!.handler!({ width, height: 48 });
		});
		await report(120);
		expect(text(trigger(root, "spawn-harness"))).toBe("");
		expect(arrow()).toBeTruthy();
		await report(300);
		expect(text(trigger(root, "spawn-harness"))).toBe("Qwen3.5 27B Claude 4.6 Opus Reasoning Distilled");
	});
});
