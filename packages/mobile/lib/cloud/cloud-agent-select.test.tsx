import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({
	Image: "Image", Pressable: "Pressable", Text: "Text", View: "View",
	Platform: { OS: "android" },
	StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
}));
vi.mock("@expo/ui/community/menu", () => ({ MenuView: "MenuView" }));
vi.mock("../haptics", () => ({ haptics: { select: vi.fn() } }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../harnessLogoAssets", () => ({ logoFor: (agent: string) => `asset-${agent}` }));
vi.mock("../ThemeProvider", async () => {
	const { darkTheme } = await import("../theme");
	return { useTheme: () => darkTheme, useThemeState: () => ({ scheme: "dark" }), useThemedStyles: (makeStyles: (theme: typeof darkTheme) => unknown) => makeStyles(darkTheme) };
});

import { CloudAgentSelect, type CloudAgentSelectProps } from "./cloud-agent-select";

let renderer: ReactTestRenderer | undefined;
const options = [{ value: "codex", label: "Codex" }, { value: "claude-code", label: "Claude Code" }];
const props = (overrides: Partial<CloudAgentSelectProps> = {}): CloudAgentSelectProps => ({
	label: "Worker agent", value: "codex", options, enabled: true, onChange: vi.fn(), onOpen: vi.fn(), ...overrides,
});
async function mount(overrides: Partial<CloudAgentSelectProps> = {}) {
	await act(async () => { renderer = create(<CloudAgentSelect {...props(overrides)} />); });
	return renderer!.root;
}
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
	vi.restoreAllMocks(); vi.unstubAllGlobals();
});

it("opens the form-owned task-style choices from the complete branded row, not a system popup", async () => {
	const onOpen = vi.fn();
	const root = await mount({ onOpen });
	const row = root.findByType("Pressable" as never);
	expect(row.props.accessibilityLabel).toBe("Worker agent: Codex");
	expect(row.findAllByType("Text" as never).map(node => node.props.children)).toEqual(["Worker", "Codex"]);
	expect(row.findByType("Image" as never).props.source).toBe("asset-codex");
	await act(async () => row.props.onPress());
	expect(onOpen).toHaveBeenCalledOnce();
	expect(root.findAllByType("MenuView" as never)).toHaveLength(0);
});

it.each([{ enabled: false }, { options: [] }])("disables unavailable choices without hiding the role", async (overrides) => {
	const onOpen = vi.fn();
	const root = await mount({ ...overrides, onOpen });
	const row = root.findByType("Pressable" as never);
	expect(row.props.disabled).toBe(true);
	expect(row.props.accessibilityState).toEqual({ disabled: true });
	await act(async () => row.props.onPress());
	expect(onOpen).not.toHaveBeenCalled();
});

it("does not display a disconnected stored harness as selected", async () => {
	const root = await mount({ value: "opencode" });
	expect(root.findByType("Pressable" as never).props.accessibilityLabel).toBe("Worker agent: Choose agent");
	expect(root.findAllByType("Image" as never)).toHaveLength(0);
});
