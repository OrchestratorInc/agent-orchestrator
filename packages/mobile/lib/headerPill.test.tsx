import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ platform: "android" as "android" | "ios", router: { canGoBack: () => true, back: () => {}, replace: () => {} } }));

vi.mock("react-native", () => ({
	Platform: { get OS() { return fixtures.platform; } },
	StyleSheet: { create: (styles: object) => styles },
	Pressable: "Pressable",
	Text: "Text",
	View: "View",
}));
vi.mock("expo-router", () => ({ useRouter: () => fixtures.router }));
vi.mock("expo-router/react-navigation", () => ({ useHeaderHeight: () => 80 }));
vi.mock("./frost", () => ({ Frost: "Frost", BAR_SEAM: 16, pillFrostStops: (height: number, tail: number) => [[0, height], [1, tail]] }));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./cupertinoChevron", () => ({ CupertinoChevron: "CupertinoChevron" }));
vi.mock("./pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: undefined, pressed: {} }) }));
vi.mock("./ThemeProvider", () => {
	const theme = { bgSurface: "#surface", borderDefault: "#border", textPrimary: "#text" };
	return { useTheme: () => theme, useThemedStyles: (make: (t: typeof theme) => object) => make(theme) };
});

import { HeaderPill, pillBackOptions, pillHeaderOptions, pillScreenLayout } from "./headerPill";
import { usePillHeaderInset } from "./pillHeaderInset";

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

describe("pillHeaderOptions", () => {
	it("sets the page's title on a pill that leads with the way back, on Android", () => {
		const back = vi.fn();
		fixtures.router = { canGoBack: () => true, back, replace: () => {} };
		const options = pillHeaderOptions();
		expect(options.headerBackVisible).toBe(false);
		// Clear, so the page scrolls up under the pill.
		expect(options).toMatchObject({ headerTransparent: true, headerStyle: { backgroundColor: "transparent" } });
		const Title = options.headerTitle as (props: { children: string }) => React.ReactElement;
		act(() => { renderer = create(<Title>Settings</Title>); });
		const title = renderer!.root.findByType("Text" as never);
		expect(title.props).toMatchObject({ children: "Settings", accessibilityRole: "header", numberOfLines: 1 });
		act(() => renderer!.root.find((node) => node.props.testID === "header-back").props.onPress());
		expect(back).toHaveBeenCalledOnce();
	});

	it("leaves iOS its system bar", () => {
		fixtures.platform = "ios";
		expect(pillHeaderOptions()).toEqual({});
	});
});

describe("pillScreenLayout", () => {
	// pillScreenLayout is fixed at import, on Android (the mocked platform).
	it("lays the frost over the page, the bar's height plus a short seam, and tells the page the bar's height", () => {
		let inset = -1;
		function Page() {
			inset = usePillHeaderInset();
			return null;
		}
		act(() => { renderer = create(pillScreenLayout!({ children: <Page /> })); });
		expect(inset).toBe(80);
		const frost = renderer!.root.findByType("Frost" as never);
		expect(frost.props).toMatchObject({ from: "top", stops: [[0, 80], [1, 16]] });
		expect(Object.assign({}, ...[frost.props.style].flat())).toMatchObject({ position: "absolute", top: 0, height: 96 });
	});
});
