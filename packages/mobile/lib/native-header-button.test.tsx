import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

vi.mock("@expo/ui", () => ({ Host: "Host", Button: "Button" }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => ({ textSecondary: "secondary" }),
	useThemeState: () => ({ scheme: "dark" }),
}));

import { NativeHeaderButton } from "./native-header-button";
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

// The fallback used to draw text glyphs (☰ × ✓ ‹ + 🗑 ◉), which a screen
// reader read aloud instead of the control's name.
it("draws a system symbol named by the control, and presses through", async () => {
	const onPress = vi.fn();
	await act(async () => {
		renderer = create(<NativeHeaderButton icon="trash" label="Do the thing" onPress={onPress} />);
	});
	const button = renderer!.root.findByType("Button" as never);
	expect(button.props.testID).toBe("header-trash");
	expect(button.props.label).toBeUndefined();
	const symbol = button.findByType("SymbolView" as never);
	expect(symbol.props.name).toEqual(symbols.delete);
	expect(symbol.props.accessibilityLabel).toBe("Do the thing");
	await act(async () => button.props.onPress());
	expect(onPress).toHaveBeenCalledOnce();
});
