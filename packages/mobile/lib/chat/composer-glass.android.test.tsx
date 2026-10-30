import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({
	Platform: { OS: "android" },
	StyleSheet: { create: (styles: object) => styles, hairlineWidth: 0.5 },
	View: "View",
}));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({ bgSurface: "#bgSurface", borderDefault: "#borderDefault" }) }));

import { ComposerGlass, composerGlassSupported } from "./composer-glass.android";

let renderer: ReactTestRenderer | undefined;

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

describe("Android composer glass", () => {
	it("draws the composer's surface itself, so the pills drop their own fill", () => {
		expect(composerGlassSupported).toBe(true);
	});

	// The sheet fills the pill it is drawn behind, at the pill's own radius:
	// a sheet one step off the page with a hairline rim, under the controls'
	// card-coloured circles and capsule.
	it("fills its pill with the sheet colour and a rim, at the pill's radius, and takes no touches", () => {
		act(() => { renderer = create(<ComposerGlass radius={26} />); });
		const sheet = renderer!.root.findByType("View" as never);
		expect(sheet.props.pointerEvents).toBe("none");
		expect(Object.assign({}, ...sheet.props.style)).toEqual({
			position: "absolute", top: 0, right: 0, bottom: 0, left: 0,
			borderWidth: 1,
			borderCurve: "continuous",
			borderRadius: 26,
			backgroundColor: "#bgSurface",
			borderColor: "#borderDefault",
		});
	});
});
