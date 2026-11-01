import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ platform: "android" as "android" | "ios" }));

vi.mock("react-native", () => ({
	Platform: { get OS() { return fixtures.platform; } },
	StyleSheet: { create: (styles: object) => styles },
	View: "View",
}));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 24, bottom: 24, left: 0, right: 0 }) }));
vi.mock("react-native-svg", () => ({ default: "Svg", Defs: "Defs", LinearGradient: "LinearGradient", Rect: "Rect", Stop: "Stop" }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({ bgBase: "#bgBase" }) }));

import { Frost, FROST_TAIL, frostStops, NAVIGATION_BAR_HEIGHT, useTabBarFrost } from "./frost";

let renderer: ReactTestRenderer | undefined;

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	fixtures.platform = "android";
});

describe("frostStops", () => {
	it("is solid through the bar and dissolves over the tail", () => {
		const stops = frostStops(104);
		const bar = 104 / (104 + FROST_TAIL);
		expect(stops[0]).toEqual([0, 1]);
		expect(stops.find(([offset]) => offset === bar)?.[1]).toBe(1);
		expect(stops.at(-1)).toEqual([1, 0]);
		// Never thickens on the way out, and never jumps back.
		for (let index = 1; index < stops.length; index++) {
			expect(stops[index][0]).toBeGreaterThan(stops[index - 1][0]);
			expect(stops[index][1]).toBeLessThanOrEqual(stops[index - 1][1]);
		}
	});
});

describe("Frost", () => {
	it("lays the page's colour from the edge it names, and takes no touches", () => {
		act(() => { renderer = create(<Frost from="bottom" stops={[[0, 1], [1, 0]]} />); });
		expect(renderer!.root.findByType("View" as never).props.pointerEvents).toBe("none");
		expect(renderer!.root.findByType("LinearGradient" as never).props).toMatchObject({ y1: "1", y2: "0" });
		expect(renderer!.root.findAllByType("Stop" as never).map((stop) => [stop.props.offset, stop.props.stopColor, stop.props.stopOpacity])).toEqual([[0, "#bgBase", 1], [1, "#bgBase", 0]]);
	});
});

describe("useTabBarFrost", () => {
	function renderHook() {
		const ref = { current: undefined as unknown as ReturnType<typeof useTabBarFrost> };
		function Probe() {
			ref.current = useTabBarFrost();
			return null;
		}
		act(() => { renderer = create(<Probe />); });
		return ref.current;
	}

	// Material's 80dp bar pads the system navigation below its items.
	it("clears Android's bar and the system navigation under it, and frosts both", () => {
		const { inset, frost } = renderHook();
		expect(inset).toBe(NAVIGATION_BAR_HEIGHT + 24);
		expect(frost!.props).toMatchObject({ from: "bottom", stops: frostStops(NAVIGATION_BAR_HEIGHT + 24) });
	});

	it("leaves iOS to the system's glass", () => {
		fixtures.platform = "ios";
		expect(renderHook()).toEqual({ inset: 0, frost: null });
	});
});
