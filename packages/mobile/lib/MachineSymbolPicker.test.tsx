import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const fixtures = vi.hoisted(() => ({
	platform: { OS: "ios" },
	select: vi.fn(),
	theme: { accent: "accent", accentTint: "accent-tint", textSecondary: "gray", tintRed: "red-tint", onAccent: "#000000", bgElevatedHover: "hover" },
}));

vi.mock("react-native", () => ({
	Platform: fixtures.platform,
	Pressable: "Pressable",
	StyleSheet: { create: (styles: unknown) => styles },
	Text: "Text",
	View: "View",
}));
vi.mock("./haptics", () => ({ haptics: { select: fixtures.select } }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemedStyles: (factory: (value: typeof fixtures.theme) => unknown) => factory(fixtures.theme),
}));

import { MachineSymbolPicker } from "./MachineSymbolPicker";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	fixtures.platform.OS = "ios";
	fixtures.select.mockClear();
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

function render(onSelect = vi.fn(), selected: "laptop" | "server" = "laptop") {
	act(() => {
		renderer = create(<MachineSymbolPicker testIDPrefix="pick" selected={selected} onSelect={onSelect} />);
	});
	return { onSelect, choice: (symbol: string) => renderer!.root.findByProps({ testID: `pick-${symbol}` }) };
}

function flat(style: unknown): Record<string, unknown> {
	if (typeof style === "function") return flat(style({ pressed: false }));
	if (Array.isArray(style)) return Object.assign({}, ...style.map(flat));
	return (style as Record<string, unknown>) ?? {};
}

describe("MachineSymbolPicker", () => {
	it("offers every shape as a radio with a full touch target", () => {
		const { choice } = render();
		for (const symbol of ["server", "cloud", "linux", "desktop", "laptop", "mac-mini", "mac-studio"]) {
			const cell = choice(symbol);
			expect(cell.props.accessibilityRole).toBe("radio");
			expect(flat(cell.props.style)).toMatchObject({ width: 44, height: 44 });
		}
		expect(renderer!.root.findByProps({ accessibilityRole: "radiogroup" })).toBeTruthy();
	});

	// The choice is ringed and filled, with the glyph in the accent, rather
	// than a faint border alone.
	it("marks the chosen shape with a ring, a fill and an accent glyph", () => {
		const { choice } = render();
		const selected = choice("laptop");
		expect(selected.props.accessibilityState).toMatchObject({ selected: true, checked: true });
		expect(flat(selected.props.style)).toMatchObject({ borderColor: "accent", backgroundColor: "accent-tint", borderWidth: 2 });
		expect(selected.findByType("SymbolView" as never).props.tintColor).toBe("accent");
		const other = choice("server");
		expect(other.props.accessibilityState).toMatchObject({ selected: false, checked: false });
		expect(flat(other.props.style).borderColor).toBe("transparent");
		expect(other.findByType("SymbolView" as never).props.tintColor).toBe("gray");
	});

	it("selects a new shape once, with a selection tick, and ignores the current one", () => {
		const { choice, onSelect } = render();
		act(() => choice("laptop").props.onPress());
		expect(onSelect).not.toHaveBeenCalled();
		act(() => choice("cloud").props.onPress());
		expect(onSelect).toHaveBeenCalledExactlyOnceWith("cloud");
		expect(fixtures.select).toHaveBeenCalledOnce();
	});

	it("ripples on Android instead of fading", () => {
		fixtures.platform.OS = "android";
		const { choice } = render();
		const cell = choice("cloud");
		expect(cell.props.android_ripple).toMatchObject({ color: "accent-tint" });
		expect(flat(cell.props.style({ pressed: true })).opacity).toBeUndefined();
	});
});
