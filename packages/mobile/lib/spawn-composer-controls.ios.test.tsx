import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }),
	select: vi.fn(),
	tap: vi.fn(),
}));

vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemeState: () => ({ scheme: "dark" }) }));
vi.mock("./harnessLogoAssets", () => ({ logoFor: () => undefined }));
vi.mock("./haptics", () => ({ haptics: { select: fixtures.select, tap: fixtures.tap } }));
vi.mock("expo-asset", () => ({ Asset: { fromModule: vi.fn() } }));
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Platform: { OS: "ios", Version: "26.0" }, StyleSheet: { create: (styles: object) => styles }, View: host("View") };
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
		Button: host("Button"), Divider: host("Divider"), Group: host("Group"), HStack: host("HStack"), Image: host("Image"), Label: host("Label"), Menu: host("Menu"),
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

import { SpawnComposerControls } from "./spawn-composer-controls.ios";
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
	hosts: [{ id: "host-a", label: "Alpha" }, { id: "host-b", label: "Beta" }],
	hostId: "host-a",
	onSelectHost: vi.fn(),
	projects: [{ id: "project-1", label: "Project", icon: "folder" }, { id: "standalone", label: "Standalone agent", icon: "message-square-plus", sectionBreakBefore: true }],
	projectId: "project-1",
	onSelectProject: vi.fn(),
	agents: [{ id: "codex", label: "Codex" }, { id: "aider", label: "Aider" }],
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

const hostType = (node: ReactTestInstance): string => (typeof node.type === "string" ? node.type : "");
const modifiers = (node: ReactTestInstance) => (node.props.modifiers ?? []) as Modifier[];
const modifier = (node: ReactTestInstance, name: string) => modifiers(node).find((entry) => entry.modifier === name);
const identified = (root: ReactTestInstance, id: string) =>
	root.findAll((node) => hostType(node) !== "" && modifiers(node).some((entry) => entry.modifier === "accessibilityIdentifier" && entry.args[0] === id))[0];
// A Menu's `label` is a prop, not a child, so its element is read unrendered.
type LabelElement = { type: unknown; props: { modifiers?: Modifier[]; children?: unknown } };
const modifierOf = (element: LabelElement, name: string) => (element.props.modifiers ?? []).find((entry) => entry.modifier === name);
const choices = (menu: ReactTestInstance) => menu.findAll((node) => hostType(node) === "Toggle");
const choiceLabel = (choice: ReactTestInstance): string => choice.props.label ?? choice.findByType("Label" as never).props.title;

/** The asset a harness mark element draws, rendered on its own. */
async function markOf(element: React.ReactElement): Promise<string | undefined> {
	let mark: ReactTestRenderer | undefined;
	await act(async () => { mark = create(element); });
	const name = mark!.root.findAll((node) => hostType(node) === "Image")[0]?.props.assetName;
	await act(async () => mark!.unmount());
	return name;
}

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

	it("separates Standalone agent from the projects and gives each row its symbol", async () => {
		const props = baseProps();
		const root = await render(props);
		const menu = identified(root, "spawn-project");
		const rows = menu.findAll((node) => hostType(node) === "Toggle" || hostType(node) === "Divider");
		expect(rows.map((node) => hostType(node) === "Divider" ? "divider" : [node.props.label, node.props.systemImage, node.props.isOn])).toEqual([
			["Project", "folder", true],
			"divider",
			["Standalone agent", "plus.bubble", false],
		]);
		await act(async () => choices(menu)[1].props.onIsOnChange(true));
		expect(props.onSelectProject).toHaveBeenCalledWith("standalone");
	});

	// Each agent opens onto its own models, so picking a model picks its agent.
	it("nests each agent's models under it, checking the current agent and model", async () => {
		const props = baseProps({ modelSelection: "gpt-5", modelLabel: "GPT-5" });
		const root = await render(props);
		const codex = identified(root, "spawn-model-codex");
		// Each agent keeps its mark in the menu: an asset-catalog image, which keeps
		// its size in an open submenu's header where a framed image did not.
		expect(codex.props.label.props.title).toBe("Codex");
		expect(await markOf(codex.props.label.props.icon)).toBe("HarnessLogo-codex");
		expect(choices(codex).map((choice) => [choiceLabel(choice), choice.props.isOn])).toEqual([["Default model", false], ["GPT-5", true]]);
		// An agent without a list yet still offers its default model.
		const aider = identified(root, "spawn-model-aider");
		expect(choices(aider).map((choice) => [choiceLabel(choice), choice.props.isOn])).toEqual([["Default model", false]]);
		await act(async () => choices(aider)[0].props.onIsOnChange(true));
		expect(props.onSelectAgentModel).toHaveBeenCalledWith("aider", "__auto__");
	});

	// The trigger is the harness's mark and one name: the model once one is
	// picked, the agent while it runs its default. VoiceOver hears both.
	it("shows the harness mark with the picked model, or the agent on its default", async () => {
		const shown = async (props: Props) => {
			const root = await render(props);
			const menu = identified(root, "spawn-harness");
			let face: ReactTestRenderer | undefined;
			await act(async () => { face = create(menu.props.label); });
			const texts = face!.root.findAll((node) => hostType(node) === "Text").map((node) => node.props.children);
			const mark = face!.root.findAll((node) => hostType(node) === "Image" && node.props.assetName)[0]?.props.assetName;
			const label = modifier(menu, "accessibilityLabel")?.args[0];
			await act(async () => face!.unmount());
			await act(async () => renderer?.unmount());
			renderer = undefined;
			return { texts, mark, label };
		};
		expect(await shown(baseProps({ modelLabel: "Default model" }))).toEqual({ texts: ["Codex"], mark: "HarnessLogo-codex", label: "Codex, Default model" });
		expect(await shown(baseProps({ modelSelection: "gpt-5", modelLabel: "GPT-5", usesDefaultModel: false }))).toEqual({ texts: ["GPT-5"], mark: "HarnessLogo-codex", label: "Codex, GPT-5" });
		// On its default, the trigger names the model the catalog marks as it.
		const named = { codex: { models: [{ id: "gpt-5", label: "GPT-5" }], total: 1, defaultLabel: "GPT-6.1-Sol" }, aider: { models: [], total: 0 } };
		expect((await shown(baseProps({ modelLabel: "Default model", agentModels: named }))).texts).toEqual(["GPT-6.1-Sol"]);
		// A model the project pins is named, though nothing was picked here.
		expect((await shown(baseProps({ modelLabel: "GPT-5", usesDefaultModel: false }))).texts).toEqual(["GPT-5"]);
	});

	it("names the default row by the catalog's default and opens a long catalog whole", async () => {
		const props = baseProps({ agentModels: { codex: { models: [{ id: "gpt-5", label: "GPT-5" }], total: 8929, defaultLabel: "GPT-6.1-Sol" }, aider: { models: [], total: 0 } } });
		const root = await render(props);
		const codex = identified(root, "spawn-model-codex");
		expect(choiceLabel(choices(codex)[0])).toBe("Default · GPT-6.1-Sol");
		const all = codex.findAll((node) => hostType(node) === "Button" && String(node.props.label).startsWith("All models"))[0];
		expect(all.props.label).toBe(`All models (${(8929).toLocaleString()})`);
		await act(async () => all.props.onPress());
		expect(props.onBrowseModels).toHaveBeenCalledWith("codex");
		// A catalog the menu lists whole has no "All models".
		expect(identified(root, "spawn-model-aider").findAll((node) => hostType(node) === "Button")).toHaveLength(0);
	});

	it("picks the approval mode on its own control, naming it shortly", async () => {
		const props = baseProps({ approvalMode: "bypass-permissions" });
		const root = await render(props);
		const menu = identified(root, "spawn-approval");
		expect(modifier(menu, "accessibilityLabel")?.args).toEqual(["Approval, Bypass permissions"]);
		expect(choices(menu).map((choice) => [choiceLabel(choice), choice.props.isOn])).toEqual([["Auto", false], ["Bypass permissions", true]]);
		await act(async () => choices(menu)[0].props.onIsOnChange(true));
		expect(props.onSelectApprovalMode).toHaveBeenCalledWith("auto");
	});

	it("sizes every selector to its content and scales its text with Dynamic Type", async () => {
		const root = await render(baseProps());
		// A Menu's face is a prop, so each is rendered on its own to read its text.
		for (const id of ["spawn-host", "spawn-project", "spawn-approval", "spawn-harness"]) {
			let face: ReactTestRenderer | undefined;
			await act(async () => { face = create(identified(root, id).props.label); });
			const texts = face!.root.findAll((node) => hostType(node) === "Text");
			expect(texts.length).toBeGreaterThan(0);
			for (const text of texts) {
				const font = modifier(text, "font");
				expect(font?.args[0]).toHaveProperty("textStyle");
				expect(font?.args[0]).not.toHaveProperty("size");
			}
			await act(async () => face!.unmount());
		}
		for (const id of ["spawn-host", "spawn-project", "spawn-approval", "spawn-harness"]) {
			const frame = modifier(identified(root, id), "frame");
			expect(frame).toBeUndefined();
		}
		expect(root.findByType("Host" as never).props.matchContents).toEqual({ vertical: true });
	});

	// A plain-style Menu hears taps only inside its label: a frame on the Menu
	// grows its layout, and gaps or a Spacer in the label are transparent.
	it("answers taps across a full touch target on every trigger", async () => {
		const root = await render(baseProps());
		// The per-agent model submenus are rows of the agent's menu, which the system draws.
		for (const id of ["spawn-host", "spawn-project", "spawn-harness", "spawn-approval"]) {
			const menu = identified(root, id);
			expect(modifier(menu, "frame")?.args[0] ?? {}).not.toHaveProperty("minHeight");
			let face: ReactTestRenderer | undefined;
			await act(async () => { face = create(menu.props.label); });
			const top = face!.root.findAll((node) => hostType(node) !== "")[0];
			// At least a touch target: the model's capsule is the circles' taller height.
			expect((modifier(top, "frame")?.args[0] as { minHeight: number }).minHeight).toBeGreaterThanOrEqual(touchTarget);
			expect(modifier(top, "contentShape")?.args).toEqual([{ shape: "rectangle" }]);
			await act(async () => face!.unmount());
		}
	});
});

describe("iOS spawn Start", () => {
	const glyph = (button: ReactTestInstance) => button.findAll((node) => hostType(node) === "Image")[0]?.props.systemName;

	// A disc of one fixed size in every state: a system round button sized
	// itself to its glyph, so the row's height moved as the glyph changed.
	const disc = (button: ReactTestInstance) => button.findAll((node) => hostType(node) === "HStack")[0];

	it("starts from a solid round arrow filled with the accent", async () => {
		const props = baseProps();
		const root = await render(props);
		const start = root.findAll((node) => hostType(node) === "Button" && node.props.testID === "spawn-submit")[0];
		expect(modifier(start, "buttonStyle")?.args).toEqual(["plain"]);
		expect(modifier(start, "disabled")?.args).toEqual([false]);
		expect(modifier(disc(start), "frame")?.args).toEqual([{ width: 50, height: 50 }]);
		expect(modifier(disc(start), "backgroundOverlay")?.args).toEqual([{ color: "#accent" }]);
		expect(modifier(disc(start), "clipShape")?.args).toEqual(["circle"]);
		expect(modifier(start, "accessibilityLabel")?.args).toEqual(["Start task"]);
		expect(glyph(start)).toBe("arrow.up");
		expect(modifier(start.findAll((node) => hostType(node) === "Image")[0], "foregroundStyle")?.args).toEqual(["#onAccent"]);

		await act(async () => start.props.onPress());
		expect(props.onSpawn).toHaveBeenCalledOnce();
		expect(fixtures.tap).not.toHaveBeenCalled();
		expect(root.findAll((node) => hostType(node) === "Pressable")).toHaveLength(0);
	});

	it("disables itself and shows the system spinner while the task starts", async () => {
		const root = await render(baseProps({ busy: true, disabled: true }));
		const start = root.findAll((node) => hostType(node) === "Button" && node.props.testID === "spawn-submit")[0];
		expect(modifier(start, "disabled")?.args).toEqual([true]);
		expect(modifier(start, "accessibilityLabel")?.args).toEqual(["Starting task"]);
		expect(start.findAllByType("ProgressView" as never)).toHaveLength(1);
		// Same disc, in the attach circle's grey while it cannot be pressed.
		expect(modifier(disc(start), "frame")?.args).toEqual([{ width: 50, height: 50 }]);
		expect(modifier(disc(start), "backgroundOverlay")?.args).toEqual([{ color: "#bgElevated" }]);
	});

	// The round button follows the prompt: it dictates while there is nothing
	// written, stops while the mic is open, and starts the task once there is text.
	it("dictates with an empty prompt, stops while listening, and starts once there is text", async () => {
		const primary = (root: ReactTestInstance) => root.findAll((node) => hostType(node) === "Button" && ["spawn-dictate", "spawn-submit"].includes(node.props.testID))[0];
		let props = baseProps({ promptEmpty: true });
		let root = await render(props);
		let button = primary(root);
		expect(button.props.testID).toBe("spawn-dictate");
		expect(glyph(button)).toBe("mic.fill");
		expect(modifier(button, "accessibilityLabel")?.args).toEqual(["Dictate the task"]);
		await act(async () => button.props.onPress());
		expect(props.voice.onToggleLatched).toHaveBeenCalledOnce();
		expect(props.onSpawn).not.toHaveBeenCalled();
		await act(async () => renderer?.unmount());

		props = baseProps({ promptEmpty: false, voice: { ...baseProps().voice, state: "recording", mode: "latched" } });
		root = await render(props);
		button = primary(root);
		expect(glyph(button)).toBe("stop.fill");
		expect(modifier(disc(button), "backgroundOverlay")?.args).toEqual([{ color: "#red" }]);
		expect(modifier(disc(button), "frame")?.args).toEqual([{ width: 50, height: 50 }]);
		await act(async () => renderer?.unmount());

		root = await render(baseProps({ promptEmpty: false }));
		expect(glyph(primary(root))).toBe("arrow.up");
		await act(async () => renderer?.unmount());

		// A device that cannot dictate keeps Start, empty prompt or not.
		root = await render(baseProps({ promptEmpty: true, voice: { ...baseProps().voice, state: "unavailable" } }));
		expect(primary(root).props.testID).toBe("spawn-submit");
	});
});
