import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ platform: "android" as "android" | "ios" }));

vi.mock("react-native", () => ({
	Platform: { get OS() { return fixtures.platform; } },
	StyleSheet: { create: (styles: object) => styles },
	Pressable: "Pressable",
	View: "View",
}));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./cupertinoChevron", () => ({ CupertinoChevron: "CupertinoChevron" }));
vi.mock("./pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: undefined, pressed: {} }) }));
vi.mock("./ThemeProvider", () => {
	const theme = { bgSurface: "#surface", borderDefault: "#border", textPrimary: "#text" };
	return { useTheme: () => theme, useThemedStyles: (make: (t: typeof theme) => object) => make(theme) };
});

import { HeaderPill, pillBackOptions } from "./headerPill";

let renderer: ReactTestRenderer | undefined;

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	fixtures.platform = "android";
});

describe("pillBackOptions", () => {
	// An empty leading slot let native-stack draw the system's arrow beside the pill.
	it("takes the bar's own back button out on Android, leaving the slot filled", () => {
		const options = pillBackOptions();
		expect(options.headerBackVisible).toBe(false);
		expect(options.headerLeft?.({ canGoBack: true })).not.toBeNull();
	});

	it("leaves iOS its system back button", () => {
		fixtures.platform = "ios";
		expect(pillBackOptions()).toEqual({});
	});
});

describe("HeaderPill", () => {
	it("leads with a back button when given the way back", () => {
		const onBack = vi.fn();
		act(() => { renderer = create(<HeaderPill onBack={onBack}>title</HeaderPill>); });
		const back = renderer!.root.findByProps({ testID: "header-back" });
		expect(back.props.accessibilityLabel).toBe("Back");
		act(() => back.props.onPress());
		expect(onBack).toHaveBeenCalledOnce();
	});

	it("holds only its content without one", () => {
		act(() => { renderer = create(<HeaderPill>title</HeaderPill>); });
		expect(renderer!.root.findAllByProps({ testID: "header-back" })).toHaveLength(0);
	});
});
