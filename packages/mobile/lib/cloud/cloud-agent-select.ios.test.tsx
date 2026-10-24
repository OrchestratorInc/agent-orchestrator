import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const fixture = vi.hoisted(() => ({ scheme: "dark" as "dark" | "light" }));
const haptic = vi.hoisted(() => vi.fn());
const logoUris = vi.hoisted(() => vi.fn((_agents: readonly { id: string; label: string }[]) => ({ codex: "codex-uri", "claude-code": "claude-uri" })));
vi.mock("react-native", () => ({ View: "View", StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 } }));
vi.mock("@expo/ui", () => ({ Host: "Host" }));
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	return { Button: "Button", HStack: "HStack", Image: "Image", Spacer: "Spacer", Text: "Text",
		Menu: ({ label, children, ...props }: { label: React.ReactNode; children: React.ReactNode }) => React.createElement("Menu", props, label, children) };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => ({ ...Object.fromEntries([
	"accessibilityIdentifier", "accessibilityLabel", "buttonStyle", "contentShape", "font", "foregroundStyle", "frame", "lineLimit", "menuOrder", "opacity", "tint", "truncationMode",
].map(name => [name, (...args: unknown[]) => ({ name, args })])), shapes: { rectangle: () => "rectangle" } }));
vi.mock("../spawn-composer-controls.ios", () => ({ HarnessImage: "HarnessImage", useHarnessLogoUris: logoUris }));
vi.mock("../haptics", () => ({ haptics: { select: haptic } }));
vi.mock("../ThemeProvider", async () => {
	const { darkTheme, lightTheme } = await import("../theme");
	return { useTheme: () => fixture.scheme === "dark" ? darkTheme : lightTheme, useThemeState: () => ({ scheme: fixture.scheme }) };
});
import { CloudAgentSelect } from "./cloud-agent-select.ios";
import type { CloudAgentSelectProps } from "./cloud-agent-select";

let renderer: ReactTestRenderer | undefined;
const options = [{ value: "codex", label: "Codex" }, { value: "claude-code", label: "Claude Code" }];
const props = (overrides: Partial<CloudAgentSelectProps> = {}): CloudAgentSelectProps => ({ label: "Worker agent", value: "codex", options, enabled: true, onChange: vi.fn(), onOpen: vi.fn(), ...overrides });
async function mount(overrides: Partial<CloudAgentSelectProps> = {}) {
	await act(async () => { renderer = create(<CloudAgentSelect {...props(overrides)} />); });
	return renderer!.root;
}
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); fixture.scheme = "dark"; haptic.mockClear(); logoUris.mockClear();
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});
afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount()); renderer = undefined;
	vi.restoreAllMocks(); vi.unstubAllGlobals();
});

it.each(["dark", "light"] as const)("uses the task sheet’s native menu, branded options and selected checkmark in %s mode", async (scheme) => {
	fixture.scheme = scheme;
	const onChange = vi.fn();
	const root = await mount({ onChange });
	expect(root.findByType("Host" as never).props.colorScheme).toBe(scheme);
	const choices = root.findAllByType("Button" as never);
	expect(choices).toHaveLength(2);
	expect(choices.map(node => node.findByType("HarnessImage" as never).props.harness)).toEqual(["codex", "claude-code"]);
	expect(choices[0].findByType("Image" as never).props.systemName).toBe("checkmark");
	expect(choices[1].findAllByType("Image" as never)).toHaveLength(0);
	await act(async () => choices[1].props.onPress());
	expect(onChange).toHaveBeenCalledExactlyOnceWith("claude-code");
	expect(haptic).toHaveBeenCalledOnce();
});

it.each([{ enabled: false }, { options: [] }])("keeps unavailable choices readable without an active menu", async (overrides) => {
	const root = await mount(overrides);
	expect(root.findAllByType("Menu" as never)).toHaveLength(0);
	expect(root.findAllByType("Text" as never).map(node => node.props.children)).toContain("Worker");
});

it.each(["disabled", "disconnected", "unmounted"])("ignores a queued native selection after it becomes %s", async (reason) => {
	const onChange = vi.fn();
	const root = await mount({ onChange });
	const queued = root.findAllByType("Button" as never)[1].props.onPress;
	await act(async () => {
		if (reason === "unmounted") { renderer!.unmount(); renderer = undefined; }
		else renderer!.update(<CloudAgentSelect {...props({ onChange, ...(reason === "disabled" ? { enabled: false } : { options: [options[0]] }) })} />);
	});
	await act(async () => queued());
	expect(onChange).not.toHaveBeenCalled(); expect(haptic).not.toHaveBeenCalled();
});

it("ignores the already selected option", async () => {
	const onChange = vi.fn(); const root = await mount({ onChange });
	await act(async () => root.findAllByType("Button" as never)[0].props.onPress());
	expect(onChange).not.toHaveBeenCalled(); expect(haptic).not.toHaveBeenCalled();
});

it("keeps logo-loader inputs stable across its own rerenders so asset resolution cannot loop", async () => {
	const input = props();
	await act(async () => { renderer = create(<CloudAgentSelect {...input} />); });
	const first = logoUris.mock.calls[0];
	await act(async () => renderer!.update(<CloudAgentSelect {...input} />));
	expect(logoUris.mock.calls.at(-1)?.[0]).toBe(first[0]);
});
