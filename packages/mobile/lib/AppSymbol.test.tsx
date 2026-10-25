import { readFileSync } from "node:fs";
import { join } from "node:path";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

const fixtures = vi.hoisted(() => ({ fontScale: 1 }));

vi.mock("react-native", () => ({
	View: "View",
	useWindowDimensions: () => ({ width: 390, height: 844, scale: 3, fontScale: fixtures.fontScale }),
}));
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({ textSecondary: "secondary", red: "red" }) }));

import { AppSymbol as AndroidSymbol } from "./AppSymbol.android";
import { AppSymbol as IOSSymbol } from "./AppSymbol";
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
	it("puts accessibility and layout on a wrapper, since the symbol view drops them", () => {
		const renderer = render(<AndroidSymbol name="notifications" accessibilityLabel="Notifications" style={{ margin: 4 }} testID="glyph" />);
		const wrapper = renderer.root.findByType("View" as never);
		const view = symbolView(renderer);

		expect(wrapper.props).toMatchObject({ testID: "glyph", accessible: true, accessibilityRole: "image", accessibilityLabel: "Notifications" });
		expect(wrapper.props.style).toEqual([{ width: 24, height: 24 }, { margin: 4 }]);
		expect(view.props.name).toEqual(symbols.notifications);
		expect(view.props.tintColor).toBe("secondary");
		expect(view.props.accessibilityLabel).toBeUndefined();
	});

	it("hides a decorative glyph and its font character from TalkBack", () => {
		const wrapper = render(<AndroidSymbol name="add" />).root.findByType("View" as never);

		expect(wrapper.props.accessible).toBe(false);
		expect(wrapper.props.importantForAccessibility).toBe("no-hide-descendants");
	});

	it("keeps the glyph its own size at a large system font scale", () => {
		fixtures.fontScale = 2;
		try {
			const view = symbolView(render(<AndroidSymbol name="add" size={24} />));

			// The font character is scaled back up by the system to exactly the box.
			expect(view.props.size).toBe(12);
			expect(view.props.style).toEqual({ width: 24, height: 24 });
		} finally {
			fixtures.fontScale = 1;
		}
	});
});

describe("symbol registry", () => {
	it("names every glyph on both platforms", () => {
		for (const [name, symbol] of Object.entries(symbols)) {
			expect(symbol.ios, name).toMatch(/\S/);
			expect(symbol.android, name).toMatch(/^[a-z0-9_]+$/);
		}
	});

	it("loads Android's symbol face with the app's fonts, under the splash", () => {
		const layout = readFileSync(join(__dirname, "../app/_layout.tsx"), "utf8");

		expect(layout).toMatch(/useFonts\(\{[^}]*\.\.\.symbolFonts \}\)/);
	});
});
