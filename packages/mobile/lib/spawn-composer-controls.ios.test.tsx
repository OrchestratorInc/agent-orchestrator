import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: () => "#000000" }),
}));

vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("./harnessLogoAssets", () => ({ logoFor: () => undefined }));
vi.mock("./haptics", () => ({ haptics: { select: vi.fn(), tap: vi.fn() } }));
vi.mock("expo-asset", () => ({ Asset: { fromModule: vi.fn() } }));
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) =>
		React.createElement(name, props, children);
	return {
		Pressable: host("Pressable"),
		StyleSheet: { create: (styles: object) => styles },
		Text: host("RNText"),
		View: host("View"),
	};
});
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return { Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Host", props, children) };
});
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) =>
		React.createElement(name, props, children);
	return {
		Button: host("Button"),
		HStack: host("HStack"),
		Image: host("Image"),
		Menu: host("Menu"),
		Spacer: host("Spacer"),
		Text: host("Text"),
		VStack: host("VStack"),
	};
});
vi.mock("@expo/ui/swift-ui/modifiers", () => ({
	accessibilityIdentifier: vi.fn(), aspectRatio: vi.fn(), buttonStyle: vi.fn(),
	containerRelativeFrame: vi.fn(), font: vi.fn(), frame: vi.fn(), glassEffect: vi.fn(),
	labelStyle: vi.fn(), opacity: vi.fn(), padding: vi.fn(), resizable: vi.fn(), tint: vi.fn(),
}));

import { SpawnComposerControls } from "./spawn-composer-controls.ios";

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

describe("SpawnComposerControls on iOS", () => {
	it("keeps the Cloud harness picker while hiding attachment and model controls", async () => {
		await act(async () => {
			renderer = create(<SpawnComposerControls
				projects={[{ id: "project-1", label: "Tap" }]}
				projectId="project-1"
				onSelectProject={vi.fn()}
				agents={[{ id: "claude-code", label: "Claude Code" }]}
				harness="claude-code"
				onSelectHarness={vi.fn()}
				models={[]}
				modelSelection="__auto__"
				modelLabel="Automatic"
				onSelectModel={vi.fn()}
				onAttach={vi.fn()}
				onSpawn={vi.fn()}
				busy={false}
				disabled={false}
				showAttachments={false}
				showModels={false}
			/>);
		});

		const text = renderer!.root
			.findAll((node) => String(node.type) === "Text")
			.map((node) => node.props.children);
		expect(text).toContain("Claude Code");
		expect(text).not.toContain("Automatic");
		const buttons = renderer!.root.findAll((node) => String(node.type) === "Button");
		expect(buttons.some((node) => node.props.label === "Attach file")).toBe(false);
	});
});
