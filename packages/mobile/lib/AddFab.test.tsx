import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({ StyleSheet: { create: (styles: object) => styles } }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => ({ accent: "#accent", onAccent: "#onAccent", bgElevated: "#bgElevated", textPrimary: "#textPrimary" }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("./toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Host: host("ComposeHost"),
		ExtendedFloatingActionButton: Object.assign(host("ExtendedFloatingActionButton"), { Icon: host("FabIconSlot"), Text: host("FabTextSlot") }),
		Icon: host("Icon"),
		Text: host("ComposeText"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({
	paddingAll: (all: number) => ({ paddingAll: all }),
	testID: (tag: string) => ({ testID: tag }),
}));
import { AddFab, addFabClearance as iosClearance } from "./AddFab";
import { AddFab as AndroidAddFab, addFabClearance } from "./AddFab.android";

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

describe("AddFab on Android", () => {
	it("is Material's extended FAB with the add symbol and its label, named for TalkBack in both states", async () => {
		const onPress = vi.fn();
		await act(async () => { renderer = create(<AndroidAddFab label="New task" onPress={onPress} bottomInset={0} testID="spawn-worker" />); });
		const fab = renderer!.root.findByType("ExtendedFloatingActionButton" as never);
		expect(fab.props).toMatchObject({ containerColor: "#bgElevated", expanded: true });
		expect(fab.props.modifiers).toContainEqual({ testID: "spawn-worker" });
		const icon = fab.findByType("FabIconSlot" as never).findByType("Icon" as never);
		expect(icon.props).toMatchObject({ source: "drawable:add", tint: "#textPrimary", contentDescription: "New task" });
		expect(fab.findByType("FabTextSlot" as never).findByType("ComposeText" as never).props).toMatchObject({ children: "New task", color: "#textPrimary" });
		act(() => fab.props.onClick());
		expect(onPress).toHaveBeenCalledOnce();
		// The Text slot never reaches the accessibility tree, so the glyph names it folded too.
		await act(async () => renderer!.update(<AndroidAddFab label="New task" onPress={onPress} bottomInset={0} expanded={false} />));
		expect(renderer!.root.findByType("FabIconSlot" as never).findByType("Icon" as never).props.contentDescription).toBe("New task");
	});

	// Bottom-end, 16dp in, lifted by whatever the page has to clear; the host is
	// a little larger than the button so its shadow is not cut off.
	it("sits 16dp in from the bottom-end corner, above what the page has to clear", async () => {
		await act(async () => { renderer = create(<AndroidAddFab label="New task" onPress={vi.fn()} bottomInset={24} />); });
		const host = renderer!.root.findByType("ComposeHost" as never);
		const style = Object.assign({}, ...host.props.style);
		const padding = renderer!.root.findByType("ExtendedFloatingActionButton" as never).props.modifiers[0].paddingAll;
		expect(style).toMatchObject({ position: "absolute", end: 16 - padding, bottom: 24 + 16 - padding });
		expect(addFabClearance).toBe(56 + 16);
	});
});

// iOS has no FAB idiom: its New task is the tab bar's own round button.
it("draws nothing on iOS", async () => {
	await act(async () => { renderer = create(<AddFab label="New task" onPress={vi.fn()} bottomInset={0} />); });
	expect(renderer!.toJSON()).toBeNull();
	expect(iosClearance).toBe(0);
});
