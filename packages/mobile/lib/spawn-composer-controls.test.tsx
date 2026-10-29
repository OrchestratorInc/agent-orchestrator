import type { ReactNode } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@expo/ui", async () => {
	const React = await import("react");
	const Picker = ({ children, ...props }: { children?: ReactNode }) => React.createElement("Picker", props, children);
	Picker.Item = "PickerItem";
	return { Host: "Host", Button: "Button", Picker };
});
vi.mock("react-native", () => ({ Platform: { OS: "ios" }, StyleSheet: { create: (styles: object) => styles }, TextInput: "TextInput", View: "View" }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => ({ textSecondary: "secondary" }),
	useThemeState: () => ({ scheme: "dark" }),
}));

import { SpawnComposerControls } from "./spawn-composer-controls";
import { symbols } from "./symbols";

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

describe("SpawnComposerControls fallback", () => {
	// The attach buttons used to be emoji labels (🖼 📎), read aloud as "framed
	// picture" and "paperclip" rather than as what they do.
	it("draws the attach buttons as system symbols named by their action", async () => {
		const onChoosePhoto = vi.fn();
		const onChooseFile = vi.fn();
		const props = {
			hosts: [{ id: "host-a", label: "Alpha" }], hostId: "host-a", onSelectHost: vi.fn(),
			projects: [], projectId: null, onSelectProject: vi.fn(),
			agents: [], harness: "", onSelectHarness: vi.fn(), models: [], agentModels: {}, modelSelection: "__auto__", modelLabel: "Default model", onSelectModel: vi.fn(), onSelectAgentModel: vi.fn(), usesDefaultModel: true, onBrowseModels: vi.fn(), prompt: "", onChangePrompt: vi.fn(), placeholder: "Ask Codex", attachments: null, hasAttachments: false,
			approvalModes: [{ id: "auto", label: "Auto" }], approvalMode: "auto", onSelectApprovalMode: vi.fn(),
			onChoosePhoto, onChooseFile, voice: { state: "idle", mode: "push", onPressIn: vi.fn(), onPressOut: vi.fn(), onToggleLatched: vi.fn() }, onSpawn: vi.fn(), busy: false, disabled: false,
		} as unknown as Parameters<typeof SpawnComposerControls>[0];
		await act(async () => { renderer = create(<SpawnComposerControls {...props} />); });

		const button = (label: string) => renderer!.root.findAllByType("Button" as never).find((node) =>
			node.findAllByType("SymbolView" as never).some((symbol) => symbol.props.accessibilityLabel === label),
		);
		const photo = button("Choose photo");
		const file = button("Choose file");
		expect(photo?.findByType("SymbolView" as never).props.name).toEqual(symbols.photo);
		expect(file?.findByType("SymbolView" as never).props.name).toEqual(symbols.attach);
		expect(photo?.props.label).toBeUndefined();
		expect(file?.props.label).toBeUndefined();

		await act(async () => photo?.props.onPress());
		expect(onChoosePhoto).toHaveBeenCalledOnce();
		await act(async () => file?.props.onPress());
		expect(onChooseFile).toHaveBeenCalledOnce();
	});
});
