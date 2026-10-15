import { useState } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixture = vi.hoisted(() => ({ scheme: "dark" as "dark" | "light" }));
const selectionAsync = vi.hoisted(() => vi.fn(async () => {}));

// Only native boundaries are replaced; the selector, logo and haptic wrapper
// render and handle selection as they do in the app.
vi.mock("react-native", () => ({
	Image: "Image", Pressable: "Pressable", Text: "Text", View: "View",
	Platform: { OS: "ios" },
	StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
}));
vi.mock("@expo/ui/community/menu", () => ({ MenuView: "MenuView" }));
vi.mock("expo-haptics", () => ({ selectionAsync }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../harnessLogoAssets", () => ({ logoFor: (agent: string) => `asset-${agent}` }));
vi.mock("../ThemeProvider", async () => {
	const { darkTheme, lightTheme } = await import("../theme");
	const current = () => fixture.scheme === "dark" ? darkTheme : lightTheme;
	return { useTheme: current, useThemeState: () => ({ scheme: fixture.scheme }), useThemedStyles: (makeStyles: (theme: typeof darkTheme) => unknown) => makeStyles(current()) };
});

import { CloudAgentSelect, type CloudAgentSelectProps } from "./cloud-agent-select";

const options = [{ value: "codex", label: "Codex" }, { value: "claude-code", label: "Claude Code" }];
let renderer: ReactTestRenderer | undefined;

const props = (overrides: Partial<CloudAgentSelectProps> = {}): CloudAgentSelectProps => ({
	label: "Worker agent", value: "codex", options, enabled: true, onChange: vi.fn(), ...overrides,
});
async function mount(overrides: Partial<CloudAgentSelectProps> = {}) {
	await act(async () => { renderer = create(<CloudAgentSelect {...props(overrides)} />); });
	return renderer!.root;
}
function menu() { return renderer!.root.findByType("MenuView" as never); }
function select(event: string) { menu().props.onPressAction({ nativeEvent: { event } }); }

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixture.scheme = "dark";
	selectionAsync.mockClear();
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

describe("Cloud agent select", () => {
	it.each(["dark", "light"] as const)("shows the role and selected agent together with a single checked choice in %s mode", async (scheme) => {
		fixture.scheme = scheme;
		const root = await mount();
		const trigger = root.findByProps({ accessibilityLabel: "Worker agent: Codex" });
		expect(trigger.findAllByType("Text" as never).map((node) => node.props.children)).toEqual(["Worker", "Codex"]);
		expect(trigger.findByType("Image" as never).props.source).toBe("asset-codex");
		expect(trigger.findByType("Feather" as never).props.name).toBe("chevron-down");
		expect(menu().props.actions).toEqual([
			{ id: "codex", title: "Codex", state: "on" },
			{ id: "claude-code", title: "Claude Code", state: "off" },
		]);
		expect(trigger.props.testID).toBe("Worker agent");
		expect(menu().props.colorScheme).toBe(scheme);
	});

	it("selects a supplied agent once and updates its name, logo and native checkmark", async () => {
		const changed = vi.fn();
		function Form() {
			const [value, setValue] = useState("codex");
			return <CloudAgentSelect {...props({ label: "Orchestrator agent", value, onChange: (next) => { changed(next); setValue(next); } })} />;
		}
		await act(async () => { renderer = create(<Form />); });
		await act(async () => select("claude-code"));
		expect(changed).toHaveBeenCalledExactlyOnceWith("claude-code");
		expect(selectionAsync).toHaveBeenCalledOnce();
		const trigger = renderer!.root.findByProps({ accessibilityLabel: "Orchestrator agent: Claude Code" });
		expect(trigger.findAllByType("Text" as never).map((node) => node.props.children)).toEqual(["Orchestrator", "Claude Code"]);
		expect(trigger.findByType("Image" as never).props.source).toBe("asset-claude-code");
		expect(menu().props.actions.filter((action: { state: string }) => action.state === "on").map((action: { id: string }) => action.id)).toEqual(["claude-code"]);
	});

	it("ignores the selected choice and events for agents outside the supplied options", async () => {
		const onChange = vi.fn();
		await mount({ onChange });
		await act(async () => { select("codex"); select("opencode"); });
		expect(onChange).not.toHaveBeenCalled();
		expect(selectionAsync).not.toHaveBeenCalled();
	});

	it("keeps a disabled row readable without an active native menu", async () => {
		const root = await mount({ enabled: false });
		const trigger = root.findByProps({ accessibilityLabel: "Worker agent: Codex" });
		expect(trigger.props.accessibilityState).toEqual({ disabled: true });
		expect(root.findAllByType("MenuView" as never)).toHaveLength(0);
		expect(trigger.findByType("Image" as never).props.source).toBe("asset-codex");
	});

	it("ignores a queued native selection after the row becomes disabled", async () => {
		const onChange = vi.fn();
		await mount({ onChange });
		const queuedSelection = menu().props.onPressAction;
		await act(async () => { renderer!.update(<CloudAgentSelect {...props({ onChange, enabled: false })} />); });
		await act(async () => { queuedSelection({ nativeEvent: { event: "claude-code" } }); });
		expect(onChange).not.toHaveBeenCalled();
		expect(selectionAsync).not.toHaveBeenCalled();
	});

	it("ignores a queued choice removed from the connected options", async () => {
		const onChange = vi.fn();
		await mount({ onChange });
		const queuedSelection = menu().props.onPressAction;
		await act(async () => { renderer!.update(<CloudAgentSelect {...props({ onChange, options: [options[0]] })} />); });
		await act(async () => { queuedSelection({ nativeEvent: { event: "claude-code" } }); });
		expect(onChange).not.toHaveBeenCalled();
		expect(selectionAsync).not.toHaveBeenCalled();
	});

	it("ignores a queued native selection after the row unmounts", async () => {
		const onChange = vi.fn();
		await mount({ onChange });
		const queuedSelection = menu().props.onPressAction;
		await act(async () => { renderer!.unmount(); });
		renderer = undefined;
		await act(async () => { queuedSelection({ nativeEvent: { event: "claude-code" } }); });
		expect(onChange).not.toHaveBeenCalled();
		expect(selectionAsync).not.toHaveBeenCalled();
	});

	it("does not present a disconnected stored value as a selected agent", async () => {
		const onChange = vi.fn();
		const root = await mount({ value: "opencode", onChange });
		const trigger = root.findByProps({ accessibilityLabel: "Worker agent: Choose agent" });
		expect(trigger.findAllByType("Image" as never)).toHaveLength(0);
		expect(trigger.findAllByType("Text" as never).map((node) => node.props.children)).toEqual(["Worker", "Choose agent"]);
		expect(menu().props.actions.every((action: { state: string }) => action.state === "off")).toBe(true);
		await act(async () => select("codex"));
		expect(onChange).toHaveBeenCalledExactlyOnceWith("codex");
	});

	it("disables an empty choice list while keeping the role visible", async () => {
		const root = await mount({ options: [] });
		const trigger = root.findByProps({ accessibilityLabel: "Worker agent: Choose agent" });
		expect(trigger.props.accessibilityState).toEqual({ disabled: true });
		expect(root.findAllByType("MenuView" as never)).toHaveLength(0);
	});
});
