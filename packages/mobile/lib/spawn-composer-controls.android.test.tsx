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

describe("Android spawn machine selector", () => {
	it("shows the connected default and selects another machine", async () => {
		const onSelectHost = vi.fn();
		const props = {
			hosts: [{ id: "host-a", label: "Alpha" }, { id: "host-b", label: "Beta" }],
			hostId: "host-a",
			onSelectHost,
			projects: [{ id: "project-1", label: "Project" }], projectId: "project-1", onSelectProject: vi.fn(),
			agents: [], harness: "", onSelectHarness: vi.fn(), models: [], modelSelection: "__auto__", modelLabel: "Automatic", onSelectModel: vi.fn(),
			onChoosePhoto: vi.fn(), onChooseFile: vi.fn(), voice: { state: "idle", mode: "push", onPressIn: vi.fn(), onPressOut: vi.fn() }, onSpawn: vi.fn(), busy: false, disabled: true,
		} as unknown as Parameters<typeof SpawnComposerControls>[0];
		await act(async () => { renderer = create(<SpawnComposerControls {...props} />); });

		const runOn = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Run on Alpha")[0];
		expect(runOn).toBeDefined();
		await act(async () => runOn.props.onPress());
		const beta = renderer!.root.findAll((node) => String(node.type) === "Pressable").find((node) =>
			node.findAll((child) => String(child.type) === "Text" && child.props.children === "Beta").length > 0,
		);
		await act(async () => beta?.props.onPress());
		expect(onSelectHost).toHaveBeenCalledWith("host-b");
	});

	it("offers both a photo and a file from the attach button", async () => {
		const onChoosePhoto = vi.fn();
		const onChooseFile = vi.fn();
		const props = {
			hosts: [{ id: "host-a", label: "Alpha" }], hostId: "host-a", onSelectHost: vi.fn(),
			projects: [{ id: "project-1", label: "Project" }], projectId: "project-1", onSelectProject: vi.fn(),
			agents: [], harness: "", onSelectHarness: vi.fn(), models: [], modelSelection: "__auto__", modelLabel: "Automatic", onSelectModel: vi.fn(),
			onChoosePhoto, onChooseFile, voice: { state: "idle", mode: "push", onPressIn: vi.fn(), onPressOut: vi.fn() }, onSpawn: vi.fn(), busy: false, disabled: true,
		} as unknown as Parameters<typeof SpawnComposerControls>[0];
		await act(async () => { renderer = create(<SpawnComposerControls {...props} />); });

		const openAttach = async () => {
			const attach = renderer!.root.findAll((node) => String(node.type) === "Pressable" && node.props.accessibilityLabel === "Attach")[0];
			await act(async () => attach.props.onPress());
		};
		const choose = async (label: string) => {
			const row = renderer!.root.findAll((node) => String(node.type) === "Pressable").find((node) =>
				node.findAll((child) => String(child.type) === "Text" && child.props.children === label).length > 0,
			);
			await act(async () => row?.props.onPress());
		};

		await openAttach();
		await choose("Choose photo");
		expect(onChoosePhoto).toHaveBeenCalledOnce();
		await openAttach();
		await choose("Choose file");
		expect(onChooseFile).toHaveBeenCalledOnce();
	});
});
