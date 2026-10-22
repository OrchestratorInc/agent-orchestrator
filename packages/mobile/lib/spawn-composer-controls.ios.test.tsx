import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ theme: new Proxy({}, { get: () => "#000000" }) }));

vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemeState: () => ({ scheme: "dark" }) }));
vi.mock("./harnessLogoAssets", () => ({ logoFor: () => undefined }));
vi.mock("./haptics", () => ({ haptics: { select: vi.fn(), tap: vi.fn() } }));
vi.mock("./voice/MicKey", async () => { const React = await import("react"); return { MicKey: (props: object) => React.createElement("MicKey", props) }; });
vi.mock("expo-asset", () => ({ Asset: { fromModule: vi.fn() } }));
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Pressable: host("Pressable"), StyleSheet: { create: (styles: object) => styles }, Text: host("RNText"), View: host("View") };
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
	return { Button: host("Button"), Divider: host("Divider"), Group: host("Group"), HStack: host("HStack"), Image: host("Image"), Menu: host("Menu"), Spacer: host("Spacer"), Text: host("Text"), VStack: host("VStack") };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => ({
	accessibilityIdentifier: vi.fn(), aspectRatio: vi.fn(), backgroundOverlay: vi.fn(), buttonStyle: vi.fn(), clipShape: vi.fn(), containerRelativeFrame: vi.fn(), font: vi.fn(), frame: vi.fn(), glassEffect: vi.fn(), labelStyle: vi.fn(), layoutPriority: vi.fn(), lineLimit: vi.fn(), menuOrder: vi.fn(), opacity: vi.fn(), padding: vi.fn(), resizable: vi.fn(), tint: vi.fn(), truncationMode: vi.fn(),
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

describe("iOS spawn machine selector", () => {
	it("places a checked Run on menu beside the project selector", async () => {
		const onSelectHost = vi.fn();
		await act(async () => {
			renderer = create(<SpawnComposerControls
				hosts={[{ id: "host-a", label: "Alpha" }, { id: "host-b", label: "Beta" }]}
				hostId="host-a"
				onSelectHost={onSelectHost}
				projects={[{ id: "project-1", label: "Project" }]}
				projectId="project-1"
				onSelectProject={vi.fn()}
				agents={[]}
				harness=""
				onSelectHarness={vi.fn()}
				models={[]}
				modelSelection="__auto__"
				modelLabel="Automatic"
				onSelectModel={vi.fn()}
				onAttach={vi.fn()}
				voice={{ state: "idle", mode: "push", onPressIn: vi.fn(), onPressOut: vi.fn() }}
				onSpawn={vi.fn()}
				busy={false}
				disabled
			/>);
		});

		const menus = renderer!.root.findAll((node) => String(node.type) === "Menu");
		expect(menus).toHaveLength(4);
		const hostButtons = menus[0].findAll((node) => String(node.type) === "Button");
		expect(hostButtons.map((node) => [node.props.label, node.props.systemImage])).toEqual([
			["Alpha", "checkmark"],
			["Beta", undefined],
		]);
		await act(async () => hostButtons[1].props.onPress());
		expect(onSelectHost).toHaveBeenCalledWith("host-b");
	});
});
