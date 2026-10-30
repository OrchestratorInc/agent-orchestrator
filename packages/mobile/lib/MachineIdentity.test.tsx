import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";
import { machineAppearancePalette, machineSymbolNames } from "./machineAppearance";
import { machineMenuImage } from "./machine-menu-image";
import { symbols } from "./symbols";
import { materialDrawableNames } from "./toolbarIcon.types";
import { type } from "./tokens";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", () => ({
	StyleSheet: { create: (styles: unknown) => styles },
	Text: "Text",
	View: "View",
}));
vi.mock("./ThemeProvider", () => {
	const theme = { textSecondary: "gray" };
	return {
		useTheme: () => theme,
		useThemedStyles: (factory: (value: typeof theme) => unknown) => factory(theme),
	};
});

import { MachineAvatar, MachineIdentity } from "./MachineIdentity";

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

	// It shares a row's metadata line, which is footnote, the 13pt floor.
	it("names the machine at the row metadata's footnote step", () => {
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(<MachineIdentity name="Studio" appearance={{ symbol: "desktop" }} />);
		});
		const name = renderer.root.findByProps({ testID: "machine-identity-name" });
		expect(name.props.style).toMatchObject({ fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight });
	});

	// The machine shapes are system symbols on both platforms, not drawings.
	it.each(machineAppearancePalette.symbols)("draws %s as its system symbol", (shape) => {
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(<MachineAvatar appearance={{ symbol: shape }} size={32} color="accent" />);
		});
		const glyph = renderer.root.findByType("SymbolView" as never);
		expect(glyph.props.name).toEqual(symbols[machineSymbolNames[shape]]);
		expect(glyph.props.tintColor).toBe("accent");
		expect(glyph.props.size).toBe(18);
	});

	it("draws the Mini PC as its own machine, not a generic monitor", () => {
		expect(symbols[machineSymbolNames["mac-mini"]]).toEqual({ ios: "macmini", android: "developer_board" });
		expect(symbols[machineSymbolNames["mac-studio"]]).toEqual({ ios: "macstudio", android: "host" });
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
		const android = readFileSync(fileURLToPath(new URL("./machine-menu-image.android.ts", import.meta.url)), "utf8");
		expect(android).toContain("materialDrawable(symbols[machineSymbolNames[symbol]].android)");
	});
});
