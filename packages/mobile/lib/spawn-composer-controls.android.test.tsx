import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: () => "#000000" }),
}));

vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme }));
vi.mock("./voice/MicKey", async () => {
	const React = await import("react");
	return { MicKey: (props: object) => React.createElement("MicKey", props) };
});
vi.mock("./AgentLogo", async () => {
	const React = await import("react");
	return { AgentLogo: (props: object) => React.createElement("AgentLogo", props) };
});
vi.mock("./icons", async () => {
	const React = await import("react");
	return { Feather: (props: object) => React.createElement("Feather", props) };
});
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) =>
		React.createElement(name, props, children);
	return {
		ActivityIndicator: host("ActivityIndicator"),
		Pressable: host("Pressable"),
		ScrollView: host("ScrollView"),
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
		Text: host("Text"),
		View: host("View"),
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
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("SpawnComposerControls", () => {
	it("opens the destination choice inside the composer", async () => {
		const onSelectDestination = vi.fn();
		const props = {
			destinations: [{ source: { kind: "local", id: "desktop-1" }, label: "Paired desktop", available: true }, { source: { kind: "cloud", id: "org-1" }, label: "Cloud", available: true }],
			destination: null,
			onSelectDestination,
			projects: [], projectId: null, onSelectProject: vi.fn(), agents: [], harness: "", onSelectHarness: vi.fn(),
			models: [], modelSelection: "__auto__", modelLabel: "Automatic", onSelectModel: vi.fn(), onAttach: vi.fn(),
			voice: { state: "idle", mode: "push", onPressIn: vi.fn(), onPressOut: vi.fn() }, onSpawn: vi.fn(), busy: false, disabled: true,
		} as unknown as Parameters<typeof SpawnComposerControls>[0];
		await act(async () => { renderer = create(<SpawnComposerControls {...props} />); });
		const destination = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Choose where to run")[0];
		expect(destination).toBeDefined();
		expect(destination.findAll((node) => String(node.type) === "Feather")[0].props.name).toBe("monitor");
		const project = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Choose project")[0];
		const selectorRow = renderer!.root.findAll((node) => String(node.type) === "View")
			.find((node) => node.props.style?.flexDirection === "row" && node.findAll((child) => String(child.type) === "Pressable" && ["Choose where to run", "Choose project"].includes(child.props.accessibilityLabel)).length === 2);
		expect(selectorRow?.props.style).toMatchObject({ flexDirection: "row" });
		expect(destination.props.style).toContainEqual(expect.objectContaining({ flex: 1, minWidth: 0 }));
		expect(project.props.style).toContainEqual(expect.objectContaining({ flex: 1, minWidth: 0 }));
		await act(async () => destination.props.onPress());
		expect(renderer!.root.findAll((node) => String(node.type) === "Text").some((node) => node.props.children === "Destination")).toBe(true);
		const cloud = renderer!.root.findAll((node) => String(node.type) === "Pressable").find((node) => node.findAll((child) => String(child.type) === "Text" && child.props.children === "Cloud").length > 0);
		await act(async () => cloud?.props.onPress());
		expect(onSelectDestination).toHaveBeenCalledWith({ kind: "cloud", id: "org-1" });
	});
	it("shows Cloud models but not unsupported attachments", async () => {
		const props = {
			destinations: [{ source: { kind: "cloud", id: "org-1" }, label: "Cloud", available: true }],
			destination: { kind: "cloud", id: "org-1" },
			onSelectDestination: vi.fn(),
			projects: [{ id: "project-1", label: "Tap" }],
			projectId: "project-1",
			onSelectProject: vi.fn(),
			agents: [{ id: "claude-code", label: "Claude Code" }],
			harness: "claude-code",
			onSelectHarness: vi.fn(),
			models: [],
			modelSelection: "__auto__",
			modelLabel: "Automatic",
			onSelectModel: vi.fn(),
			onAttach: vi.fn(),
			voice: { state: "idle", mode: "push", onPressIn: vi.fn(), onPressOut: vi.fn() },
			onSpawn: vi.fn(),
			busy: false,
			disabled: false,
			showAttachments: false,
			showModels: true,
		} as unknown as Parameters<typeof SpawnComposerControls>[0];

		await act(async () => { renderer = create(<SpawnComposerControls {...props} />); });

		const labels = renderer!.root
			.findAll((node) => String(node.type) === "Pressable")
			.map((node) => node.props.accessibilityLabel)
			.filter(Boolean);
		expect(labels).toContain("Claude Code");
		expect(labels).toContain("Cloud");
		const destination = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Cloud")[0];
		expect(destination.findAll((node) => String(node.type) === "Feather")[0].props.name).toBe("cloud");
		expect(labels).not.toContain("Attach a file");
		expect(labels).toContain("Automatic");
	});
});

describe("Android spawn machine selector", () => {
	it("shows the connected default and selects another machine", async () => {
		const onSelectDestination = vi.fn();
		const props = {
			destinations: [{ source: { kind: "local", id: "host-a" }, label: "Alpha", available: true }, { source: { kind: "local", id: "host-b" }, label: "Beta", available: true }, { source: { kind: "local", id: "host-c" }, label: "Gamma", available: false }],
			destination: { kind: "local", id: "host-a" },
			onSelectDestination,
			projects: [{ id: "project-1", label: "Project" }], projectId: "project-1", onSelectProject: vi.fn(),
			agents: [], harness: "", onSelectHarness: vi.fn(), models: [], modelSelection: "__auto__", modelLabel: "Automatic", onSelectModel: vi.fn(),
			onAttach: vi.fn(), voice: { state: "idle", mode: "push", onPressIn: vi.fn(), onPressOut: vi.fn() }, onSpawn: vi.fn(), busy: false, disabled: true,
		} as unknown as Parameters<typeof SpawnComposerControls>[0];
		await act(async () => { renderer = create(<SpawnComposerControls {...props} />); });

		const destination = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Alpha")[0];
		expect(destination).toBeDefined();
		expect(destination.findAll((node) => String(node.type) === "Feather")[0].props.name).toBe("monitor");
		await act(async () => destination.props.onPress());
		const unavailable = renderer!.root.findAll((node) => String(node.type) === "Pressable").find((node) =>
			node.findAll((child) => String(child.type) === "Text" && child.props.children === "Gamma · Unavailable").length > 0,
		);
		expect(unavailable).toBeDefined();
		await act(async () => unavailable!.props.onPress());
		expect(onSelectDestination).not.toHaveBeenCalled();
		const beta = renderer!.root.findAll((node) => String(node.type) === "Pressable").find((node) =>
			node.findAll((child) => String(child.type) === "Text" && child.props.children === "Beta").length > 0,
		);
		await act(async () => beta?.props.onPress());
		expect(onSelectDestination).toHaveBeenCalledWith({ kind: "local", id: "host-b" });
	});
});
