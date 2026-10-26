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
vi.mock("./spawn-composer-drawables.android", () => ({
	spawnDrawables: new Proxy({}, { get: (_target, key) => `drawable:${String(key)}` }),
}));
vi.mock("./voice/MicKey", async () => {
	const React = await import("react");
	return { MicKey: (props: object) => React.createElement("MicKey", props) };
});
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
		DropdownMenu: Object.assign(host("DropdownMenu"), { Trigger: host("DropdownMenuTrigger"), Items: host("DropdownMenuItems") }),
		DropdownMenuItem: Object.assign(host("DropdownMenuItem"), {
			Text: host("DropdownMenuItemText"),
			LeadingIcon: host("DropdownMenuItemLeadingIcon"),
			TrailingIcon: host("DropdownMenuItemTrailingIcon"),
		}),
		Host: host("ComposeHost"),
		HorizontalDivider: host("HorizontalDivider"),
		Icon: host("Icon"),
		IconButton: host("IconButton"),
		Row: host("Row"),
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
		padding: record("padding"),
		size: record("size"),
		testID: record("testID"),
		weight: record("weight"),
		Shapes: { RoundedCorner: (radius: number) => ({ type: "roundedCorner", radius }) },
	};
});

import { SpawnComposerControls } from "./spawn-composer-controls.android";
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

describe("Android spawn selectors", () => {
	it("opens each choice as a dropdown under a text button that names it", async () => {
		const root = await render(baseProps());
		for (const tag of ["spawn-host", "spawn-project", "spawn-harness", "spawn-model"]) {
			expect(String(trigger(root, tag).type)).toBe("TextButton");
			expect(menuOf(trigger(root, tag)).props.modifiers).toContainEqual({ modifier: "weight", args: [1] });
		}
		expect(text(trigger(root, "spawn-host"))).toBe("Run on Alpha");
		expect(text(trigger(root, "spawn-project"))).toBe("Project");
		expect(text(trigger(root, "spawn-harness"))).toBe("Codex");
		expect(text(trigger(root, "spawn-model"))).toBe("Automatic");
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
		await act(async () => trigger(root, "spawn-model").props.onClick());
		const menu = menuOf(trigger(root, "spawn-model"));
		expect(items(menu).map(text)).toEqual(["Automatic", "GPT-5"]);
		await act(async () => item(menu, "Automatic").props.onClick());
		expect(props.onSelectModel).not.toHaveBeenCalled();
		expect(fixtures.select).not.toHaveBeenCalled();
		expect(menuOf(trigger(root, "spawn-model")).props.expanded).toBe(false);
	});

	it("separates Standalone agent from the projects and marks each with its symbol", async () => {
		const root = await render(baseProps());
		const menu = menuOf(trigger(root, "spawn-project"));
		const rows = menu.findByType("DropdownMenuItems" as never).findAll((node) => hostType(node) === "DropdownMenuItem" || hostType(node) === "HorizontalDivider");
		expect(rows.map((node) => hostType(node) === "HorizontalDivider" ? "divider" : text(node))).toEqual(["Project", "divider", "Standalone agent"]);
		const glyph = (node: ReactTestInstance) => node.findByType("DropdownMenuItemLeadingIcon" as never).findByType("Icon" as never).props.source;
		expect(glyph(item(menu, "Project"))).toBe("drawable:folder");
		expect(glyph(item(menu, "Standalone agent"))).toBe("drawable:standalone");
	});

	it("draws harness marks in their own colours, on a chip only where one is needed", async () => {
		const root = await render(baseProps());
		const menu = menuOf(trigger(root, "spawn-harness"));
		const mark = (label: string) => item(menu, label).findByType("DropdownMenuItemLeadingIcon" as never);
		expect(mark("Codex").findByType("Icon" as never).props).toMatchObject({ source: "logo:codex", tint: null });
		expect(mark("OpenCode").findAllByType("Box" as never)).toHaveLength(1);
		expect(mark("OpenCode").findByType("Icon" as never).props).toMatchObject({ source: "logo:opencode", tint: null });
		expect(mark("Aider").findByType("Icon" as never).props.source).toBe("drawable:agent");
	});

	it("offers both a photo and a file from the attach button", async () => {
		const props = baseProps();
		const root = await render(props);
		const attach = trigger(root, "spawn-attachment");
		expect(String(attach.type)).toBe("IconButton");
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

	it("keeps the hold-to-talk mic at the touch target", async () => {
		const root = await render(baseProps());
		expect(root.findByType("MicKey" as never).props).toMatchObject({ variant: "plain", size: touchTarget });
	});
});

describe("Android spawn Start", () => {
	it("starts from a filled Material button with the accent's own fill and ink", async () => {
		const props = baseProps();
		const root = await render(props);
		const start = trigger(root, "spawn-submit");
		expect(String(start.type)).toBe("Button");
		expect(start.props.enabled).toBe(true);
		expect(start.props.colors).toEqual({ containerColor: "#accent", contentColor: "#onAccent", disabledContainerColor: "#bgElevatedHover", disabledContentColor: "#textFaint" });
		expect(text(start)).toBe("Start task");
		await act(async () => start.props.onClick());
		expect(props.onSpawn).toHaveBeenCalledOnce();
	});

	it("disables itself and shows progress while the task starts", async () => {
		const root = await render(baseProps({ busy: true, disabled: true }));
		const start = trigger(root, "spawn-submit");
		expect(start.props.enabled).toBe(false);
		expect(start.findAllByType("CircularProgressIndicator" as never)).toHaveLength(1);
		// TalkBack reads the button's text, so it is the name iOS announces.
		expect(text(start)).toBe("Starting task");
	});
});
