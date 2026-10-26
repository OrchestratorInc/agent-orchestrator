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
vi.mock("./voice/MicKey", async () => { const React = await import("react"); return { MicKey: (props: object) => React.createElement("MicKey", props) }; });
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
		ProgressView: host("ProgressView"), Spacer: host("Spacer"), Text: host("Text"), Toggle: host("Toggle"), VStack: host("VStack"),
	};
});
vi.mock("@expo/ui/swift-ui/modifiers", () => {
	const record = (name: string) => (...args: unknown[]) => ({ modifier: name, args });
	return Object.fromEntries([
		"accessibilityIdentifier", "accessibilityLabel", "aspectRatio", "backgroundOverlay", "buttonStyle", "clipShape", "controlSize", "disabled", "font",
		"foregroundStyle", "frame", "glassEffect", "labelStyle", "layoutPriority", "lineLimit", "menuOrder", "opacity", "padding", "resizable", "tint", "truncationMode",
	].map((name) => [name, record(name)]));
});

import { SpawnComposerControls } from "./spawn-composer-controls.ios";

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
	modelSelection: "__auto__",
	modelLabel: "Automatic",
	onSelectModel: vi.fn(),
	onChoosePhoto: vi.fn(),
	onChooseFile: vi.fn(),
	voice: { state: "idle", mode: "push", onPressIn: vi.fn(), onPressOut: vi.fn() },
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

	it("offers Automatic and every model, checking the current one", async () => {
		const props = baseProps({ modelSelection: "gpt-5", modelLabel: "GPT-5" });
		const root = await render(props);
		const menu = identified(root, "spawn-model");
		expect(choices(menu).map((choice) => [choiceLabel(choice), choice.props.isOn])).toEqual([["Automatic", false], ["GPT-5", true]]);
		await act(async () => choices(menu)[0].props.onIsOnChange(true));
		expect(props.onSelectModel).toHaveBeenCalledWith("__auto__");
	});

	it("labels each agent with its mark", async () => {
		const props = baseProps();
		const root = await render(props);
		const menu = identified(root, "spawn-harness");
		expect(choices(menu).map((choice) => [choiceLabel(choice), choice.props.isOn])).toEqual([["Codex", true], ["Aider", false]]);
		await act(async () => choices(menu)[1].props.onIsOnChange(true));
		expect(props.onSelectHarness).toHaveBeenCalledWith("aider");
	});

	it("lets the attach button choose a photo or a file", async () => {
		const props = baseProps();
		const root = await render(props);
		const menu = identified(root, "spawn-attachment");
		expect(menu.props).toMatchObject({ label: "Attach", systemImage: "paperclip" });
		const buttons = menu.findAll((node) => hostType(node) === "Button");
		expect(buttons.map((node) => [node.props.label, node.props.systemImage])).toEqual([["Choose Photo", "photo"], ["Choose File", "doc"]]);
		await act(async () => buttons[0].props.onPress());
		await act(async () => buttons[1].props.onPress());
		expect(props.onChoosePhoto).toHaveBeenCalledOnce();
		expect(props.onChooseFile).toHaveBeenCalledOnce();
		// Opening a system picker is not a moment worth a haptic.
		expect(fixtures.tap).not.toHaveBeenCalled();
	});

	it("sizes every selector to its content and scales its text with Dynamic Type", async () => {
		const root = await render(baseProps());
		const texts = root.findAll((node) => hostType(node) === "Text");
		expect(texts.length).toBeGreaterThan(0);
		for (const text of texts) {
			const font = modifier(text, "font");
			expect(font?.args[0]).toHaveProperty("textStyle");
			expect(font?.args[0]).not.toHaveProperty("size");
		}
		for (const id of ["spawn-host", "spawn-project", "spawn-harness"]) {
			const frame = modifier(identified(root, id), "frame");
			expect(frame).toBeUndefined();
		}
		expect(root.findByType("Host" as never).props.matchContents).toEqual({ vertical: true });
	});
});

describe("iOS spawn Start", () => {
	it("starts from a prominent native button tinted with the accent", async () => {
		const props = baseProps();
		const root = await render(props);
		const start = root.findAll((node) => hostType(node) === "Button" && node.props.testID === "spawn-submit")[0];
		expect(modifier(start, "buttonStyle")?.args).toEqual(["glassProminent"]);
		expect(modifier(start, "controlSize")?.args).toEqual(["large"]);
		expect(modifier(start, "tint")?.args).toEqual(["#accent"]);
		expect(modifier(start, "disabled")?.args).toEqual([false]);
		expect(modifier(start, "accessibilityLabel")?.args).toEqual(["Start task"]);
		expect(start.findByType("Text" as never).props.children).toBe("Start task");
		expect(modifier(start.findByType("Text" as never), "foregroundStyle")?.args).toEqual(["#onAccent"]);

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
		expect(start.findByType("Text" as never).props.children).toBe("Starting…");
		// The dimmed label is the system's, not the accent's ink.
		expect(modifier(start.findByType("Text" as never), "foregroundStyle")).toBeUndefined();
	});
});
