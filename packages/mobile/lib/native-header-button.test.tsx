import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@expo/ui", () => ({ Host: "Host", Button: "Button" }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => ({ textSecondary: "secondary" }),
	useThemeState: () => ({ scheme: "dark" }),
}));

import { NativeHeaderButton, type NativeHeaderButtonIcon } from "./native-header-button";
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

describe("NativeHeaderButton fallback", () => {
	const expected: Record<NativeHeaderButtonIcon, { ios: string; android: string }> = {
		menu: { ios: "line.3.horizontal", android: "menu" },
		bell: symbols.notifications,
		close: symbols.close,
		check: symbols.checkmark,
		back: symbols.back,
		more: symbols.moreHorizontal,
		add: symbols.add,
		trash: symbols.delete,
	};

	// The fallback used to draw text glyphs (☰ × ✓ ‹ + 🗑 ◉), which a screen
	// reader read aloud instead of the control's name.
	it.each(Object.entries(expected))("draws %s as a system symbol named by the control", async (icon, glyph) => {
		const onPress = vi.fn();
		await act(async () => {
			renderer = create(<NativeHeaderButton icon={icon as NativeHeaderButtonIcon} label="Do the thing" onPress={onPress} />);
		});

		const button = renderer!.root.findByType("Button" as never);
		expect(button.props.testID).toBe(`header-${icon}`);
		expect(button.props.label).toBeUndefined();
		const symbol = button.findByType("SymbolView" as never);
		expect(symbol.props.name).toEqual(glyph);
		expect(symbol.props.accessibilityLabel).toBe("Do the thing");

		await act(async () => button.props.onPress());
		expect(onPress).toHaveBeenCalledOnce();
	});
});
