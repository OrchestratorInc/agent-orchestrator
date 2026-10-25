import { readFileSync } from "node:fs";
import { join } from "node:path";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

const MATERIAL_FACE = "MaterialSymbols_400Regular";

const fixtures = vi.hoisted(() => ({ fontScale: 1, loadedFonts: ["MaterialSymbols_400Regular"] }));

vi.mock("react-native", () => ({
	Text: "Text",
	View: "View",
	useWindowDimensions: () => ({ width: 390, height: 844, scale: 3, fontScale: fixtures.fontScale }),
}));
vi.mock("expo-font", () => ({ isLoaded: (family: string) => fixtures.loadedFonts.includes(family) }));
// The real module requires the .ttf, which node cannot load.
vi.mock("expo-symbols/androidWeights/regular", () => ({ default: { name: "MaterialSymbols_400Regular", font: 1 } }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({ textSecondary: "secondary", red: "red" }) }));

import { AppSymbol as AndroidSymbol } from "./AppSymbol.android";
import { AppSymbol as IOSSymbol } from "./AppSymbol";
import { materialGlyph } from "./symbolCodepoints";
import { symbolFonts as androidSymbolFonts } from "./symbolFonts.android";
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
	it("draws the registry pair in the chrome tint, hidden from screen readers", () => {
		const view = symbolView(render(<IOSSymbol name="settings" />));

		expect(view.props.name).toEqual({ ios: "gearshape", android: "settings" });
		expect(view.props.tintColor).toBe("secondary");
		expect(view.props.size).toBe(20);
		expect(view.props.accessible).toBe(false);
		expect(view.props.accessibilityElementsHidden).toBe(true);
		expect(view.props.importantForAccessibility).toBe("no-hide-descendants");
	});

	it("takes a one-off pair, a color, a weight and a label", () => {
		const view = symbolView(render(
			<IOSSymbol
				name={{ ios: "trash", android: "delete" }}
				color="red"
				weight="semibold"
				size={17}
				accessibilityLabel="Delete"
				testID="glyph"
			/>,
		));

		expect(view.props).toMatchObject({
			name: { ios: "trash", android: "delete" },
			tintColor: "red",
			weight: "semibold",
			size: 17,
			accessible: true,
			accessibilityRole: "image",
			accessibilityLabel: "Delete",
			testID: "glyph",
		});
		expect(view.props.accessibilityElementsHidden).toBeUndefined();
	});
});

describe("AppSymbol on Android", () => {
	it("draws the font character itself on the first render once the face is in", () => {
		const renderer = render(<AndroidSymbol name="notifications" accessibilityLabel="Notifications" style={{ margin: 4 }} testID="glyph" />);
		const wrapper = renderer.root.findByType("View" as never);
		const glyph = renderer.root.findByType("Text" as never);

		expect(wrapper.props).toMatchObject({ testID: "glyph", accessible: true, accessibilityRole: "image", accessibilityLabel: "Notifications" });
		expect(wrapper.props.style).toEqual([{ width: 24, height: 24 }, { margin: 4 }]);
		expect(glyph.props.children).toBe(String.fromCharCode(0xe7f4));
		expect(glyph.props.style).toEqual({ fontFamily: MATERIAL_FACE, color: "secondary", fontSize: 24, lineHeight: 24 });
		// No load-gated library view, so nothing paints an empty box first.
		expect(renderer.root.findAllByType("SymbolView" as never)).toHaveLength(0);
	});

	it("draws in the face the root layout loads under the splash", () => {
		const glyph = render(<AndroidSymbol name="add" />).root.findByType("Text" as never);

		expect(Object.keys(androidSymbolFonts)).toEqual([glyph.props.style.fontFamily]);
	});

	it("hides a decorative glyph and its font character from TalkBack", () => {
		const wrapper = render(<AndroidSymbol name="add" />).root.findByType("View" as never);

		expect(wrapper.props.accessible).toBe(false);
		expect(wrapper.props.importantForAccessibility).toBe("no-hide-descendants");
	});

	it("keeps the glyph its own size at a large system font scale", () => {
		fixtures.fontScale = 2;
		try {
			const glyph = render(<AndroidSymbol name="add" size={24} />).root.findByType("Text" as never);

			expect(glyph.props.allowFontScaling).toBe(false);
			expect(glyph.props.style).toMatchObject({ fontSize: 24, lineHeight: 24 });
		} finally {
			fixtures.fontScale = 1;
		}
	});

	it("falls back to the symbol view until the face has loaded", () => {
		fixtures.loadedFonts = [];
		fixtures.fontScale = 2;
		try {
			const renderer = render(<AndroidSymbol name="add" color="red" size={24} />);
			const view = symbolView(renderer);

			expect(renderer.root.findAllByType("Text" as never)).toHaveLength(0);
			expect(view.props.name).toEqual(symbols.add);
			expect(view.props.tintColor).toBe("red");
			// Its font character follows the system scale, so the scale is divided
			// back out and the system brings it up to exactly the box.
			expect(view.props.size).toBe(12);
			expect(view.props.style).toEqual({ width: 24, height: 24 });
			expect(view.props.accessibilityLabel).toBeUndefined();
		} finally {
			fixtures.loadedFonts = [MATERIAL_FACE];
			fixtures.fontScale = 1;
		}
	});

	it("falls back to the symbol view for a one-off name outside the registry", () => {
		const renderer = render(<AndroidSymbol name={{ ios: "tray.2", android: "inbox" }} />);

		expect(symbolView(renderer).props.name).toEqual({ ios: "tray.2", android: "inbox" });
		expect(renderer.root.findAllByType("Text" as never)).toHaveLength(0);
	});
});

describe("symbol registry", () => {
	it("names every glyph on both platforms", () => {
		for (const [name, symbol] of Object.entries(symbols)) {
			expect(symbol.ios, name).toMatch(/\S/);
			expect(symbol.android, name).toMatch(/^[a-z0-9_]+$/);
		}
	});

	it("knows the font codepoint of every Android glyph", () => {
		// The map expo-symbols draws from, which it does not export.
		const library: Record<string, number> = JSON.parse(
			readFileSync(join(__dirname, "../node_modules/expo-symbols/build/android/symbols.json"), "utf8"),
		);
		for (const [name, symbol] of Object.entries(symbols)) {
			const expected = library[symbol.android];
			expect(materialGlyph(symbol.android)?.charCodeAt(0), `${name}: ${symbol.android} is 0x${expected?.toString(16)}`).toBe(expected);
		}
	});

	it("loads Android's symbol face with the app's fonts, under the splash", () => {
		const layout = readFileSync(join(__dirname, "../app/_layout.tsx"), "utf8");

		expect(layout).toMatch(/useFonts\(\{[^}]*\.\.\.symbolFonts \}\)/);
	});
});
