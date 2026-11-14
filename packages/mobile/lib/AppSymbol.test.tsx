import { readFileSync } from "node:fs";
import { join } from "node:path";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

const MATERIAL_FACE = "MaterialSymbols_400Regular";

const fixtures = vi.hoisted(() => ({ loadedFonts: ["MaterialSymbols_400Regular"] }));

vi.mock("react-native", () => ({
	Text: "Text",
	View: "View",
	useWindowDimensions: () => ({ width: 390, height: 844, scale: 3, fontScale: 1 }),
}));
vi.mock("expo-font", () => ({ isLoaded: (family: string) => fixtures.loadedFonts.includes(family) }));
// The real module requires the .ttf, which node cannot load.
vi.mock("expo-symbols/androidWeights/regular", () => ({ default: { name: "MaterialSymbols_400Regular", font: 1 } }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({ textSecondary: "secondary", red: "red" }) }));
vi.mock("react-native-svg", () => ({ default: "Svg", Path: "Path", Rect: "Rect" }));

import { AppSymbol as AndroidSymbol } from "./AppSymbol.android";
import { AppSymbol as IOSSymbol } from "./AppSymbol";
import { materialGlyph } from "./symbolCodepoints";
import { symbols } from "./symbols";

function render(element: React.ReactElement) {
	let renderer!: ReactTestRenderer;
	act(() => {
		renderer = create(element);
	});
	return renderer;
}

function symbolView(renderer: ReactTestRenderer): ReactTestInstance {
	return renderer.root.findByType("SymbolView" as never);
}

describe("AppSymbol on iOS", () => {
	it("hides a decorative glyph from screen readers", () => {
		const view = symbolView(render(<IOSSymbol name="settings" />));

		expect(view.props.name).toEqual({ ios: "gearshape", android: "settings" });
		expect(view.props.accessible).toBe(false);
		expect(view.props.accessibilityElementsHidden).toBe(true);
	});

	it("exposes a labelled glyph as an image", () => {
		const view = symbolView(render(<IOSSymbol name={{ ios: "trash", android: "delete" }} accessibilityLabel="Delete" />));

		expect(view.props).toMatchObject({ accessible: true, accessibilityRole: "image", accessibilityLabel: "Delete" });
		expect(view.props.accessibilityElementsHidden).toBeUndefined();
	});
});

describe("AppSymbol on Android", () => {
	it("draws the font character itself on the first render once the face is in", () => {
		const renderer = render(<AndroidSymbol name="notifications" accessibilityLabel="Notifications" testID="glyph" />);
		const wrapper = renderer.root.findByType("View" as never);
		const glyph = renderer.root.findByType("Text" as never);

		expect(wrapper.props).toMatchObject({ testID: "glyph", accessible: true, accessibilityRole: "image", accessibilityLabel: "Notifications" });
		expect(glyph.props.children).toBe(String.fromCharCode(0xe7f4));
		expect(glyph.props.style.fontFamily).toBe(MATERIAL_FACE);
		// No load-gated library view, so nothing paints an empty box first.
		expect(renderer.root.findAllByType("SymbolView" as never)).toHaveLength(0);
	});

	it("hides a decorative glyph and its font character from TalkBack", () => {
		const wrapper = render(<AndroidSymbol name="add" />).root.findByType("View" as never);

		expect(wrapper.props.accessible).toBe(false);
		expect(wrapper.props.importantForAccessibility).toBe("no-hide-descendants");
	});

	it("falls back to the symbol view until the face has loaded", () => {
		fixtures.loadedFonts = [];
		try {
			const renderer = render(<AndroidSymbol name="add" />);

			expect(renderer.root.findAllByType("Text" as never)).toHaveLength(0);
			expect(symbolView(renderer).props.name).toEqual(symbols.add);
		} finally {
			fixtures.loadedFonts = [MATERIAL_FACE];
		}
	});

	it("falls back to the symbol view for a one-off name outside the registry", () => {
		const renderer = render(<AndroidSymbol name={{ ios: "tray.2", android: "move_to_inbox" }} />);

		expect(symbolView(renderer).props.name).toEqual({ ios: "tray.2", android: "move_to_inbox" });
		expect(renderer.root.findAllByType("Text" as never)).toHaveLength(0);
	});
});

describe("symbol registry", () => {
	it("knows the font codepoint of every Android glyph", () => {
		// The map expo-symbols draws from, which it does not export.
		const library: Record<string, number> = JSON.parse(
			readFileSync(join(__dirname, "../node_modules/expo-symbols/build/android/symbols.json"), "utf8"),
		);
		for (const [name, symbol] of Object.entries(symbols)) {
			expect(symbol.ios, name).toMatch(/\S/);
			const expected = library[symbol.android];
			expect(materialGlyph(symbol.android)?.charCodeAt(0), `${name}: ${symbol.android} is 0x${expected?.toString(16)}`).toBe(expected);
		}
	});
});
