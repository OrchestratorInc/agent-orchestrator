import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";
import { machineAppearancePalette, machineSymbolNames } from "./machineAppearance";
import { machineMenuImage } from "./machine-menu-image";
import { symbols } from "./symbols";
import { materialDrawableNames } from "./toolbarIcon.types";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", () => ({
	StyleSheet: { create: (styles: unknown) => styles },
	Text: "Text",
	View: "View",
}));
vi.mock("./ThemeProvider", () => {
	const theme = { textSecondary: "gray", red: "red", amber: "amber", textTertiary: "silver" };
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
		const glyph = renderer.root.findByType("SymbolView" as never);
		const glyphSurface = renderer.root.find((node) => (node.type as unknown) === "View" && Array.isArray(node.props.style) && node.props.style.some((value: { width?: number }) => value?.width === 22));
		const surfaceStyle = Object.assign({}, ...glyphSurface.props.style);
		expect(glyph.props.name).toEqual(symbols.server);
		expect(glyph.props.tintColor).toBe("gray");
		expect(glyph.props.size).toBe(16);
		expect(surfaceStyle.backgroundColor).toBeUndefined();
	});

	it("puts the freshness dot before the name and speaks the state", () => {
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(<MachineIdentity name="Mac" appearance={{ symbol: "laptop" }} freshness="unreachable" />);
		});
		const identity = renderer.root.findByProps({ testID: "machine-identity" });
		expect(identity.props.accessibilityLabel).toBe("Machine Mac, can't reach");
		const dot = renderer.root.find((node) => node.props.testID === "machine-identity-freshness" && (node.type as unknown) === "View");
		expect(dot.props.style).toContainEqual({ backgroundColor: "red" });
		expect(dot.props.accessibilityElementsHidden).toBe(true);
	});

	// The project header has nothing else saying which machine it is; rows do.
	it("is its own accessibility element only when asked", () => {
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(<MachineIdentity name="Mac" appearance={{ symbol: "laptop" }} freshness="lagging" accessible />);
		});
		expect(renderer.root.findByProps({ testID: "machine-identity" }).props).toMatchObject({ accessible: true, accessibilityLabel: "Machine Mac, updates delayed" });
		act(() => {
			renderer.update(<MachineIdentity name="Mac" appearance={{ symbol: "laptop" }} freshness="lagging" />);
		});
		expect(renderer.root.findByProps({ testID: "machine-identity" }).props.accessible).toBeUndefined();
	});

	it("wears no dot while live", () => {
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(<MachineIdentity name="Mac" appearance={{ symbol: "laptop" }} freshness="live" />);
		});
		expect(renderer.root.findAll((node) => node.props.testID === "machine-identity-freshness")).toHaveLength(0);
		expect(renderer.root.findByProps({ testID: "machine-identity" }).props.accessibilityLabel).toBe("Machine Mac");
	});
});

describe("machine menu images", () => {
	it("names each machine's SF Symbol for iOS menus", () => {
		for (const shape of machineAppearancePalette.symbols) {
			expect(machineMenuImage(shape)).toBe(symbols[machineSymbolNames[shape]].ios);
		}
	});

	// Android menus and Compose icons take bundled drawables, one per shape,
	// from the shared registry under the same Material Symbol the glyph draws.
	it("draws each machine's Material Symbol from the shared drawable registry on Android", () => {
		for (const shape of machineAppearancePalette.symbols) {
			expect(materialDrawableNames).toContain(symbols[machineSymbolNames[shape]].android);
		}
	});
});
