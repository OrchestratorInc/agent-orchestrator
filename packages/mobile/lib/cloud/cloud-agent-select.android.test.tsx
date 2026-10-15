import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

// Keep the installed Android MenuView's real trigger/open state logic. Only
// Compose rendering and RN's native views are substituted in this Node test.
vi.mock("@expo/ui/community/menu", () => vi.importActual("../../node_modules/@expo/ui/src/community/menu/MenuView.android.tsx"));
vi.mock("../../node_modules/@expo/ui/src/jetpack-compose/Host", () => ({ Host: "NativeHost" }));
vi.mock("../../node_modules/@expo/ui/src/jetpack-compose/RNHostView", () => ({ RNHostView: "RNHostView" }));
vi.mock("../../node_modules/@expo/ui/src/jetpack-compose/Divider", () => ({ HorizontalDivider: "Divider" }));
vi.mock("../../node_modules/@expo/ui/src/jetpack-compose/Icon", () => ({ Icon: "NativeIcon" }));
vi.mock("../../node_modules/@expo/ui/src/jetpack-compose/Text", () => ({ Text: "NativeText" }));
vi.mock("../../node_modules/@expo/ui/src/jetpack-compose/colors", () => ({ useMaterialColors: () => ({ error: "#f00" }) }));
vi.mock("../../node_modules/@expo/ui/src/jetpack-compose/DropdownMenu", async () => {
	const React = await import("react");
	return { DropdownMenu: Object.assign(({ children, ...props }: { children?: React.ReactNode }) => React.createElement("NativeDropdownMenu", props, children), { Trigger: "NativeMenuTrigger", Items: "NativeMenuItems" }) };
});
vi.mock("../../node_modules/@expo/ui/src/jetpack-compose/DropdownMenu/DropdownMenuItem", async () => {
	const React = await import("react");
	return { DropdownMenuItem: Object.assign(({ children, ...props }: { children?: React.ReactNode }) => React.createElement("NativeDropdownMenuItem", props, children), { Text: "NativeItemText", LeadingIcon: "NativeItemLeadingIcon", TrailingIcon: "NativeItemTrailingIcon" }) };
});
vi.mock("react-native", async () => {
	const React = await import("react");
	return {
		Image: "Image", Text: "Text", View: "View", Platform: { OS: "android" },
		StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
		// RN Pressability claims the responder for every enabled Pressable,
		// even when onPress is absent. Preserve that rule at this boundary.
		Pressable: ({ children, ...props }: { children?: React.ReactNode; disabled?: boolean }) => React.createElement("Pressable", { ...props, onStartShouldSetResponder: () => !props.disabled }, children),
	};
});
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../haptics", () => ({ haptics: { select: vi.fn() } }));
vi.mock("../harnessLogoAssets", () => ({ logoFor: () => 1 }));
vi.mock("../ThemeProvider", async () => {
	const { darkTheme } = await import("../theme");
	return { useTheme: () => darkTheme, useThemeState: () => ({ scheme: "dark" }), useThemedStyles: (makeStyles: (theme: typeof darkTheme) => unknown) => makeStyles(darkTheme) };
});

import { CloudAgentSelect } from "./cloud-agent-select";

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

function tap(target: ReactTestInstance) {
	for (let node: ReactTestInstance | null = target; node; node = node.parent) {
		if (node.props.onStartShouldSetResponder?.()) {
			node.props.onPress?.();
			return;
		}
	}
}

it("opens the real Android native menu when tapping the visible agent row", async () => {
	await act(async () => {
		renderer = create(<CloudAgentSelect label="Worker agent" value="codex" options={[{ value: "codex", label: "Codex" }, { value: "claude-code", label: "Claude Code" }]} onChange={vi.fn()} enabled />);
	});
	const root = renderer!.root;
	expect(root.findByType("NativeDropdownMenu" as never).props.expanded).toBe(false);
	const trigger = root.findAll((node) => typeof node.type === "string" && node.props.testID === "Worker agent")[0];
	await act(async () => tap(trigger));
	expect(root.findByType("NativeDropdownMenu" as never).props.expanded).toBe(true);
});

it("opens the real Android native menu for TalkBack activation of the visible row", async () => {
	await act(async () => {
		renderer = create(<CloudAgentSelect label="Worker agent" value="codex" options={[{ value: "codex", label: "Codex" }]} onChange={vi.fn()} enabled />);
	});
	const root = renderer!.root;
	const trigger = root.findByProps({ testID: "Worker agent" });
	await act(async () => trigger.props.onAccessibilityAction?.({ nativeEvent: { actionName: "activate" } }));
	expect(root.findByType("NativeDropdownMenu" as never).props.expanded).toBe(true);
	expect(trigger.props.accessibilityActions.map((action: { name: string }) => action.name)).toEqual(["activate"]);
});

it("ignores accessibility actions other than activation", async () => {
	await act(async () => {
		renderer = create(<CloudAgentSelect label="Worker agent" value="codex" options={[{ value: "codex", label: "Codex" }]} onChange={vi.fn()} enabled />);
	});
	const root = renderer!.root;
	const trigger = root.findByProps({ testID: "Worker agent" });
	await act(async () => trigger.props.onAccessibilityAction?.({ nativeEvent: { actionName: "longpress" } }));
	expect(root.findByType("NativeDropdownMenu" as never).props.expanded).toBe(false);
});
