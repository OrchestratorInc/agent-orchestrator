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
vi.mock("./haptics", () => ({ haptics: { select: fixtures.select } }));
vi.mock("./harnessLogoAssets", () => ({ logoFor: (harness?: string) => (harness === "codex" || harness === "opencode" ? `logo:${harness}` : null) }));
vi.mock("./toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
vi.mock("react-native", async () => {
	const React = await import("react");
	return {
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
		Row: host("Row"),
		Spacer: host("Spacer"),
		Text: host("ComposeText"),
		TextButton: host("TextButton"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => {
	const record = (name: string) => (...args: unknown[]) => ({ modifier: name, args });
	return {
		background: record("background"),
		clip: record("clip"),
		fillMaxWidth: record("fillMaxWidth"),
		height: record("height"),
		padding: record("padding"),
		semantics: record("semantics"),
		size: record("size"),
		testID: record("testID"),
		weight: record("weight"),
		Shapes: { RoundedCorner: (radius: number) => ({ type: "roundedCorner", radius }) },
	};
});

import { SpawnComposerControls } from "./spawn-composer-controls.android";

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
	hosts: [{ id: "host-a", label: "Alpha" }, { id: "host-b", label: "Beta" }],
	hostId: "host-a",
	onSelectHost: vi.fn(),
	projects: [{ id: "project-1", label: "Project", icon: "folder" }, { id: "standalone", label: "Standalone agent", icon: "message-square-plus", sectionBreakBefore: true }],
	projectId: "project-1",
	onSelectProject: vi.fn(),
	agents: [{ id: "codex", label: "Codex" }, { id: "opencode", label: "OpenCode" }, { id: "aider", label: "Aider" }],
	harness: "codex",
	onSelectHarness: vi.fn(),
	models: [{ id: "gpt-5", label: "GPT-5" }],
	agentModels: { codex: { models: [{ id: "gpt-5", label: "GPT-5" }], total: 1 }, aider: { models: [], total: 0 } },
	onSelectAgentModel: vi.fn(),
	usesDefaultModel: true,
	promptEmpty: false,
	onBrowseModels: vi.fn(),
	approvalModes: [{ id: "auto", label: "Auto", shortLabel: "Auto" }, { id: "bypass-permissions", label: "Bypass permissions", shortLabel: "Bypass" }],
	approvalMode: "auto",
	onSelectApprovalMode: vi.fn(),
	modelSelection: "__auto__",
	modelLabel: "Default model",
	onSelectModel: vi.fn(),
	onChoosePhoto: vi.fn(),
	onChooseFile: vi.fn(),
	voice: { state: "idle", mode: "push", onPressIn: vi.fn(), onPressOut: vi.fn(), onToggleLatched: vi.fn() },
	onSpawn: vi.fn(),
	busy: false,
	disabled: false,
	...overrides,
});

async function render(props: Props) {
	await act(async () => { renderer = create(<SpawnComposerControls {...props} />); });
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

const modifier = (node: ReactTestInstance, name: string) => (node.props.modifiers as { modifier: string; args: unknown[] }[] | undefined)?.find((entry) => entry.modifier === name);

describe("Android spawn selectors", () => {
	// Laid out as on iOS: the machine on its own line, then the project and the
	// approval mode, each a compact text button naming its choice.
	it("opens each selector as a dropdown under a text button that names it", async () => {
		const root = await render(baseProps());
		for (const tag of ["spawn-host", "spawn-project", "spawn-approval"]) {
			expect(String(trigger(root, tag).type)).toBe("TextButton");
			// Its label's width, not an equal share of the row.
			expect(menuOf(trigger(root, tag)).props.modifiers).toBeUndefined();
		}
		expect(text(trigger(root, "spawn-host"))).toBe("Alpha");
		expect(modifier(trigger(root, "spawn-host"), "semantics")?.args).toEqual([{ contentDescription: "Run on Alpha" }]);
		expect(text(trigger(root, "spawn-project"))).toBe("Project");
		expect(text(trigger(root, "spawn-approval"))).toBe("Auto");
		// No inline option list: the choices live in the menus, closed until asked for.
		expect(root.findAll((node) => hostType(node) === "DropdownMenu").every((menu) => menu.props.expanded === false)).toBe(true);
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
		await act(async () => trigger(root, "spawn-approval").props.onClick());
		const menu = menuOf(trigger(root, "spawn-approval"));
		expect(items(menu).map(text)).toEqual(["Auto", "Bypass permissions"]);
		await act(async () => item(menu, "Auto").props.onClick());
		expect(props.onSelectApprovalMode).not.toHaveBeenCalled();
		expect(fixtures.select).not.toHaveBeenCalled();
		expect(menuOf(trigger(root, "spawn-approval")).props.expanded).toBe(false);
	});

	it("separates Standalone agent from the projects and marks each with its symbol", async () => {
		const root = await render(baseProps());
		const menu = menuOf(trigger(root, "spawn-project"));
		const rows = menu.findByType("DropdownMenuItems" as never).findAll((node) => hostType(node) === "DropdownMenuItem" || hostType(node) === "HorizontalDivider");
		expect(rows.map((node) => hostType(node) === "HorizontalDivider" ? "divider" : text(node))).toEqual(["Project", "divider", "Standalone agent"]);
		const glyph = (node: ReactTestInstance) => node.findByType("DropdownMenuItemLeadingIcon" as never).findByType("Icon" as never).props.source;
		expect(glyph(item(menu, "Project"))).toBe("drawable:folder");
		expect(glyph(item(menu, "Standalone agent"))).toBe("drawable:add_comment");
	});
});

describe("Android spawn model", () => {
	// Who runs the task is one filled capsule, the circles' height: the
	// harness's mark and the model it runs.
	it("names the harness's mark and model in a filled capsule", async () => {
		const root = await render(baseProps());
		const capsule = trigger(root, "spawn-harness");
		expect(String(capsule.type)).toBe("Button");
		expect(capsule.props.colors).toEqual({ containerColor: "#bgElevated", contentColor: "#textPrimary" });
		expect(modifier(capsule, "height")?.args).toEqual([48]);
		expect(text(capsule)).toBe("Codex");
		expect(capsule.findAllByType("Icon" as never)[0].props).toMatchObject({ source: "logo:codex", tint: null });
	});

	it("names the catalog's default, or a model picked or pinned, on the capsule", async () => {
		const named = { codex: { models: [{ id: "gpt-5", label: "GPT-5" }], total: 1, defaultLabel: "GPT-6.1-Sol" }, aider: { models: [], total: 0 } };
		expect(text(trigger(await render(baseProps({ agentModels: named })), "spawn-harness"))).toBe("GPT-6.1-Sol");
		await act(async () => renderer?.unmount());
		expect(text(trigger(await render(baseProps({ usesDefaultModel: false, modelSelection: "gpt-5", modelLabel: "GPT-5" })), "spawn-harness"))).toBe("GPT-5");
	});

	// Each agent opens one level down onto its models, so an agent and its model
	// are picked in one step.
	it("nests each agent's models under it and picks both at once", async () => {
		const props = baseProps({ modelSelection: "gpt-5", modelLabel: "GPT-5", usesDefaultModel: false });
		const root = await render(props);
		await act(async () => trigger(root, "spawn-harness").props.onClick());
		const codexRow = trigger(root, "spawn-model-codex");
		expect(text(codexRow)).toBe("Codex");
		await act(async () => codexRow.props.onClick());
		const codex = menuOf(trigger(root, "spawn-model-codex"));
		expect(codex.props.expanded).toBe(true);
		const models = codex.findByType("DropdownMenuItems" as never);
		expect(items(models).map((node) => [text(node), isSelected(node)])).toEqual([["Default model", false], ["GPT-5", true]]);

		await act(async () => item(models, "Default model").props.onClick());
		expect(props.onSelectAgentModel).toHaveBeenCalledWith("codex", "__auto__");
		expect(fixtures.select).toHaveBeenCalledOnce();
		expect(menuOf(trigger(root, "spawn-harness")).props.expanded).toBe(false);
	});

	it("draws harness marks in their own colours, on a chip only where one is needed", async () => {
		const root = await render(baseProps());
		const mark = (agent: string) => trigger(root, `spawn-model-${agent}`).findByType("DropdownMenuItemLeadingIcon" as never);
		expect(mark("codex").findByType("Icon" as never).props).toMatchObject({ source: "logo:codex", tint: null });
		expect(mark("opencode").findAllByType("Box" as never)).toHaveLength(1);
		expect(mark("opencode").findByType("Icon" as never).props).toMatchObject({ source: "logo:opencode", tint: null });
		expect(mark("aider").findByType("Icon" as never).props.source).toBe("drawable:terminal");
	});

	it("opens a catalog too long for its menu whole", async () => {
		const props = baseProps({ agentModels: { codex: { models: [{ id: "gpt-5", label: "GPT-5" }], total: 8929, defaultLabel: "GPT-6.1-Sol" }, aider: { models: [], total: 0 } } });
		const root = await render(props);
		const models = menuOf(trigger(root, "spawn-model-codex")).findByType("DropdownMenuItems" as never);
		expect(items(models).map(text)).toEqual(["Default · GPT-6.1-Sol", "GPT-5", `All models (${(8929).toLocaleString()})`]);
		await act(async () => item(models, `All models (${(8929).toLocaleString()})`).props.onClick());
		expect(props.onBrowseModels).toHaveBeenCalledWith("codex");
	});
});

describe("Android spawn actions", () => {
	it("offers both a photo and a file from a filled attach circle", async () => {
		const props = baseProps();
		const root = await render(props);
		const attach = trigger(root, "spawn-attachment");
		expect(String(attach.type)).toBe("FilledIconButton");
		expect(modifier(attach, "size")?.args).toEqual([48, 48]);
		expect(attach.findByType("Icon" as never).props.contentDescription).toBe("Attach");

		await act(async () => attach.props.onClick());
		const menu = menuOf(trigger(root, "spawn-attachment"));
		expect(menu.props.expanded).toBe(true);
		expect(items(menu).map(text)).toEqual(["Choose photo", "Choose file"]);
		await act(async () => item(menu, "Choose photo").props.onClick());
		expect(props.onChoosePhoto).toHaveBeenCalledOnce();
		await act(async () => trigger(root, "spawn-attachment").props.onClick());
		await act(async () => item(menuOf(trigger(root, "spawn-attachment")), "Choose file").props.onClick());
		expect(props.onChooseFile).toHaveBeenCalledOnce();
		expect(fixtures.select).not.toHaveBeenCalled();
	});

	it("starts from a filled round arrow with the accent's own fill and ink", async () => {
		const props = baseProps();
		const root = await render(props);
		const start = trigger(root, "spawn-submit");
		expect(String(start.type)).toBe("FilledIconButton");
		expect(start.props.enabled).toBe(true);
		expect(start.props.colors).toEqual({ containerColor: "#accent", contentColor: "#onAccent", disabledContainerColor: "#bgElevated", disabledContentColor: "#textFaint" });
		expect(modifier(start, "size")?.args).toEqual([48, 48]);
		expect(start.findByType("Icon" as never).props).toMatchObject({ source: "drawable:arrow_upward", contentDescription: "Start task" });
		await act(async () => start.props.onClick());
		expect(props.onSpawn).toHaveBeenCalledOnce();
	});

	it("disables itself and shows progress while the task starts", async () => {
		const root = await render(baseProps({ busy: true, disabled: true }));
		const start = trigger(root, "spawn-submit");
		expect(start.props.enabled).toBe(false);
		expect(start.findAllByType("CircularProgressIndicator" as never)).toHaveLength(1);
	});

	// The round button follows the prompt: it dictates while there is nothing
	// written and stops while the mic is open, in the recording red.
	it("dictates with an empty prompt and stops while listening", async () => {
		let props = baseProps({ promptEmpty: true });
		let root = await render(props);
		let button = trigger(root, "spawn-dictate");
		expect(button.findByType("Icon" as never).props).toMatchObject({ source: "drawable:mic", contentDescription: "Dictate the task" });
		await act(async () => button.props.onClick());
		expect(props.voice.onToggleLatched).toHaveBeenCalledOnce();
		expect(props.onSpawn).not.toHaveBeenCalled();
		await act(async () => renderer?.unmount());

		props = baseProps({ voice: { ...baseProps().voice, state: "recording", mode: "latched" } });
		root = await render(props);
		button = trigger(root, "spawn-dictate");
		expect(button.findByType("Icon" as never).props.source).toBe("drawable:stop");
		expect(button.props.colors.containerColor).toBe("#red");
	});
});
