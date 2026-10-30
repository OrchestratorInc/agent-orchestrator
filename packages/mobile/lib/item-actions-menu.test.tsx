import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({ StyleSheet: { create: (styles: object) => styles }, View: "View" }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => ({ bgElevated: "#elevated", red: "#red", textPrimary: "#primary", textSecondary: "#secondary" }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("./Spinner", () => ({ Spinner: "Spinner" }));
vi.mock("./toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return { Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Host", props, children) };
});
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Host: host("ComposeHost"),
		DropdownMenu: Object.assign(host("DropdownMenu"), { Trigger: host("MenuTrigger"), Items: host("MenuItems") }),
		DropdownMenuItem: Object.assign(host("DropdownMenuItem"), { Text: host("ItemText"), LeadingIcon: host("ItemLeadingIcon") }),
		Icon: host("Icon"),
		IconButton: host("IconButton"),
		Text: host("ComposeText"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({ testID: (tag: string) => ({ testID: tag }) }));
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Button: host("SwiftButton"), Image: host("Image"), Menu: host("Menu") };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => {
	const record = (name: string) => (...args: unknown[]) => ({ modifier: name, args });
	return {
		accessibilityLabel: record("accessibilityLabel"),
		buttonStyle: record("buttonStyle"),
		contentShape: record("contentShape"),
		disabled: record("disabled"),
		frame: record("frame"),
		shapes: { rectangle: () => "rectangle" },
		tint: record("tint"),
	};
});

import { ItemActionsMenu } from "./item-actions-menu";
import { ItemActionsMenu as IOSItemActionsMenu } from "./item-actions-menu.ios";
import type { ItemAction } from "./item-actions-menu.types";
import { touchTarget } from "./tokens";

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

async function render(element: React.ReactElement) {
	await act(async () => { renderer = create(element); });
	return renderer!.root;
}

// Four actions: more than React Native's Android Alert keeps, which is what
// used to drop the last one and the Cancel button with it.
function fourActions() {
	const onPress = { open: vi.fn(), send: vi.fn(), resolve: vi.fn(), stop: vi.fn() };
	const actions: ItemAction[] = [
		{ id: "open", label: "Open on GitHub", icon: "openExternal", onPress: onPress.open },
		{ id: "send", label: "Send to worker", icon: "send", onPress: onPress.send },
		{ id: "resolve", label: "Resolve", icon: "resolve", onPress: onPress.resolve },
		{ id: "stop", label: "Stop reviewer session", icon: "stop", destructive: true, onPress: onPress.stop },
	];
	return { actions, onPress };
}

describe("ItemActionsMenu on Android", () => {
	it("anchors a Material dropdown to an icon button named for screen readers", async () => {
		const { actions } = fourActions();
		const root = await render(<ItemActionsMenu accessibilityLabel="Actions for the review" actions={actions} />);
		const trigger = root.findByType("MenuTrigger" as never).findByType("IconButton" as never);
		expect(trigger.props.enabled).toBe(true);
		expect(trigger.findByType("Icon" as never).props).toMatchObject({ source: "drawable:more_horiz", contentDescription: "Actions for the review" });
		expect(root.findByType("DropdownMenu" as never).props.expanded).toBe(false);
	});

	it("lists every action, however many there are, each with its Material Symbol", async () => {
		const { actions } = fourActions();
		const root = await render(<ItemActionsMenu accessibilityLabel="Actions" actions={actions} />);
		const items = root.findByType("MenuItems" as never).findAllByType("DropdownMenuItem" as never);
		expect(items.map((item) => item.findByType("ComposeText" as never).props.children)).toEqual(["Open on GitHub", "Send to worker", "Resolve", "Stop reviewer session"]);
		expect(items.map((item) => item.findByType("ItemLeadingIcon" as never).findByType("Icon" as never).props.source)).toEqual([
			"drawable:open_in_new",
			"drawable:send",
			"drawable:check_circle",
			"drawable:power_settings_new",
		]);
	});

	it("draws a destructive action in red", async () => {
		const { actions } = fourActions();
		const root = await render(<ItemActionsMenu accessibilityLabel="Actions" actions={actions} />);
		const items = root.findAllByType("DropdownMenuItem" as never);
		expect(items[3].props.elementColors).toEqual({ textColor: "#red", leadingIconColor: "#red" });
		expect(items[0].props.elementColors).toEqual({ textColor: "#primary", leadingIconColor: "#secondary" });
	});

	it("opens on the button, runs the chosen action and closes", async () => {
		const { actions, onPress } = fourActions();
		const root = await render(<ItemActionsMenu accessibilityLabel="Actions" actions={actions} />);
		act(() => root.findByType("IconButton" as never).props.onClick());
		expect(root.findByType("DropdownMenu" as never).props.expanded).toBe(true);
		act(() => root.findAllByType("DropdownMenuItem" as never)[3].props.onClick());
		expect(onPress.stop).toHaveBeenCalledOnce();
		expect(root.findByType("DropdownMenu" as never).props.expanded).toBe(false);
	});

	it("closes on Back or a tap outside without running anything", async () => {
		const { actions, onPress } = fourActions();
		const root = await render(<ItemActionsMenu accessibilityLabel="Actions" actions={actions} />);
		act(() => root.findByType("IconButton" as never).props.onClick());
		act(() => root.findByType("DropdownMenu" as never).props.onDismissRequest());
		expect(root.findByType("DropdownMenu" as never).props.expanded).toBe(false);
		expect(Object.values(onPress).every((fn) => fn.mock.calls.length === 0)).toBe(true);
	});

	it("does not reopen by itself when a lock that closed it lifts", async () => {
		const { actions } = fourActions();
		const root = await render(<ItemActionsMenu accessibilityLabel="Actions" actions={actions} />);
		act(() => root.findByType("IconButton" as never).props.onClick());
		await act(async () => renderer!.update(<ItemActionsMenu accessibilityLabel="Actions" actions={actions} disabled />));
		expect(root.findByType("DropdownMenu" as never).props.expanded).toBe(false);
		await act(async () => renderer!.update(<ItemActionsMenu accessibilityLabel="Actions" actions={actions} />));
		expect(root.findByType("DropdownMenu" as never).props.expanded).toBe(false);
	});

	it("stays shut while disabled, and shows a spinner while an action runs", async () => {
		const { actions } = fourActions();
		let root = await render(<ItemActionsMenu accessibilityLabel="Actions" actions={actions} disabled />);
		expect(root.findByType("IconButton" as never).props.enabled).toBe(false);
		act(() => root.findByType("IconButton" as never).props.onClick());
		expect(root.findByType("DropdownMenu" as never).props.expanded).toBe(false);
		await act(async () => renderer?.unmount());
		root = await render(<ItemActionsMenu accessibilityLabel="Actions" actions={actions} loading />);
		expect(root.findAllByType("IconButton" as never)).toHaveLength(0);
		expect(root.findAllByType("Spinner" as never)).toHaveLength(1);
	});
});

describe("ItemActionsMenu on iOS", () => {
	it("is a SwiftUI pull-down with a destructive role and a full touch target", async () => {
		const { actions, onPress } = fourActions();
		const root = await render(<IOSItemActionsMenu accessibilityLabel="Actions for the review" actions={actions} />);
		expect(root.findByType("Host" as never).props.style).toMatchObject({ width: touchTarget, height: touchTarget });
		const menu = root.findByType("Menu" as never);
		expect(menu.props.modifiers).toContainEqual({ modifier: "accessibilityLabel", args: ["Actions for the review"] });
		expect(menu.props.modifiers).toContainEqual({ modifier: "disabled", args: [false] });
		const buttons = root.findAllByType("SwiftButton" as never);
		expect(buttons.map((button) => [button.props.label, button.props.systemImage, button.props.role])).toEqual([
			["Open on GitHub", "arrow.up.right.square", undefined],
			["Send to worker", "paperplane", undefined],
			["Resolve", "checkmark.circle", undefined],
			["Stop reviewer session", "power", "destructive"],
		]);
		buttons[1].props.onPress();
		expect(onPress.send).toHaveBeenCalledOnce();
	});
});
