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
			destinations: [{ source: { kind: "local", id: "desktop-1" }, label: "Local · Paired desktop", available: true }, { source: { kind: "cloud", id: "org-1" }, label: "Cloud", available: true }],
			destination: null,
			onSelectDestination,
			projects: [], projectId: null, onSelectProject: vi.fn(), agents: [], harness: "", onSelectHarness: vi.fn(),
			models: [], modelSelection: "__auto__", modelLabel: "Automatic", onSelectModel: vi.fn(), onAttach: vi.fn(),
			voice: { state: "idle", mode: "push", onPressIn: vi.fn(), onPressOut: vi.fn() }, onSpawn: vi.fn(), busy: false, disabled: true,
		} as unknown as Parameters<typeof SpawnComposerControls>[0];
		await act(async () => { renderer = create(<SpawnComposerControls {...props} />); });
		const runOn = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Run on Choose where to run")[0];
		await act(async () => runOn.props.onPress());
		expect(renderer!.root.findAll((node) => String(node.type) === "Text").some((node) => node.props.children === "Run on")).toBe(true);
		const cloud = renderer!.root.findAll((node) => String(node.type) === "Pressable").find((node) => node.findAll((child) => String(child.type) === "Text" && child.props.children === "Cloud").length > 0);
		await act(async () => cloud?.props.onPress());
		expect(onSelectDestination).toHaveBeenCalledWith({ kind: "cloud", id: "org-1" });
	});
	it("hides local-only attachment and model controls for a Cloud spawn", async () => {
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
			showModels: false,
		} as unknown as Parameters<typeof SpawnComposerControls>[0];

		await act(async () => { renderer = create(<SpawnComposerControls {...props} />); });

		const labels = renderer!.root
			.findAll((node) => String(node.type) === "Pressable")
			.map((node) => node.props.accessibilityLabel)
			.filter(Boolean);
		expect(labels).toContain("Claude Code");
		expect(labels).not.toContain("Attach a file");
		expect(labels).not.toContain("Automatic");
	});
});
