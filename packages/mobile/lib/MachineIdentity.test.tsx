import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", () => ({
	StyleSheet: { create: (styles: unknown) => styles },
	Text: "Text",
	View: "View",
}));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("react-native-svg", () => ({ default: "Svg", Path: "Path", Rect: "Rect" }));
vi.mock("./ThemeProvider", () => {
	const theme = {
		blue: "blue", green: "green", purple: "purple", orange: "orange", amber: "amber",
		tintBlue: "blue-tint", tintGreen: "green-tint", tintPurple: "purple-tint", tintOrange: "orange-tint", tintAmber: "amber-tint",
		textSecondary: "gray",
	};
	return {
		useTheme: () => theme,
		useThemedStyles: (factory: (value: typeof theme) => unknown) => factory(theme),
	};
});

import { MachineIdentity } from "./MachineIdentity";

describe("MachineIdentity", () => {
	it("keeps the symbol after a truncating machine name on one line", () => {
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(<MachineIdentity name="A very long development machine name" appearance={{ symbol: "server" }} />);
		});
		const identity = renderer.root.findByProps({ testID: "machine-identity" });
		const name = renderer.root.findByProps({ testID: "machine-identity-name" });
		const symbol = renderer.root.findByProps({ testID: "machine-identity-symbol" });

		expect(name.props.numberOfLines).toBe(1);
		expect(name.props.ellipsizeMode).toBe("tail");
		expect(name.props.style.flexShrink).toBe(1);
		expect(identity.children[0]).toBe(name);
		expect(identity.children[1]).toBe(symbol);
		expect(symbol.props.appearance).toEqual({ symbol: "server" });
		const glyph = renderer.root.find((node) => (node.type as unknown) === "Feather");
		const glyphSurface = renderer.root.find((node) => (node.type as unknown) === "View" && Array.isArray(node.props.style) && node.props.style.some((value: { width?: number }) => value?.width === 22));
		const surfaceStyle = Object.assign({}, ...glyphSurface.props.style);
		expect(glyph.props.color).toBe("gray");
		expect(glyph.props.size).toBe(16);
		expect(surfaceStyle.backgroundColor).toBeUndefined();
	});

	it("draws the Mini PC with its dedicated machine silhouette", () => {
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(<MachineIdentity name="Mini" appearance={{ symbol: "mac-mini" as never }} />);
		});

		const slab = renderer.root.find((node) => (node.type as unknown) === "Rect");
		expect(slab.props).toMatchObject({ width: 20, height: 8, x: 2, y: 8, rx: 2 });
	});
});
