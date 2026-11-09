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
	it("presses through Material's extended FAB, named for TalkBack in both states", async () => {
		const onPress = vi.fn();
		await act(async () => { renderer = create(<AndroidAddFab label="New task" onPress={onPress} bottomInset={0} testID="spawn-worker" />); });
		const fab = renderer!.root.findByType("ExtendedFloatingActionButton" as never);
		act(() => fab.props.onClick());
		expect(onPress).toHaveBeenCalledOnce();
		// The Text slot never reaches the accessibility tree, so the glyph names it folded too.
		await act(async () => renderer!.update(<AndroidAddFab label="New task" onPress={onPress} bottomInset={0} expanded={false} />));
		expect(renderer!.root.findByType("FabIconSlot" as never).findByType("Icon" as never).props.contentDescription).toBe("New task");
		expect(addFabClearance).toBeGreaterThan(0);
	});
});

// iOS has no FAB idiom: its New task is the tab bar's own round button.
it("draws nothing on iOS", async () => {
	await act(async () => { renderer = create(<AddFab label="New task" onPress={vi.fn()} bottomInset={0} />); });
	expect(renderer!.toJSON()).toBeNull();
	expect(iosClearance).toBe(0);
});
