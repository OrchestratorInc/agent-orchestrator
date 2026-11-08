import type React from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	platform: "android" as "android" | "ios",
	setOptions: vi.fn(),
	writes: [] as string[],
	values: 0,
}));

vi.mock("expo-router", () => ({ useNavigation: () => ({ setOptions: fixtures.setOptions }) }));
vi.mock("expo-router/react-navigation", () => ({ useHeaderHeight: () => 80 }));
vi.mock("./frost", () => ({ Frost: "Frost", FROST_TAIL: 32, frostStops: (height: number) => [[0, height]] }));
vi.mock("react-native", () => ({
	Platform: { get OS() { return fixtures.platform; } },
	StyleSheet: { create: (styles: object) => styles, hairlineWidth: 0.5 },
	View: "View",
}));
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		default: { Text: host("AnimatedText"), View: host("AnimatedView") },
		// Records each write under the value's place in creation order (the
		// title's collapse first, then the frost's), so a test can tell a
		// crossing from a repeat.
		useSharedValue: (initial: number) => {
			const id = React.useRef(fixtures.values++).current;
			return React.useRef({
				_value: initial,
				get value() { return this._value; },
				set value(next: number) { fixtures.writes.push(`${id === 0 ? "title" : "frost"}:${next}`); this._value = next; },
			}).current;
		},
		useAnimatedStyle: () => ({}),
		withTiming: (value: number) => value,
	};
});
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({ textPrimary: "#textPrimary", borderDefault: "#borderDefault" }) }));

import { LARGE_TITLE_COLLAPSE_AT, largeTitleCollapsed, useLargeTitle } from "./largeTitle";

type Result = ReturnType<typeof useLargeTitle>;

let renderer: ReactTestRenderer | undefined;

function renderHook(onScroll?: Parameters<typeof useLargeTitle>[1]): { current: Result } {
	const ref = { current: undefined as unknown as Result };
	function Probe() {
		ref.current = useLargeTitle("Workers", onScroll);
		return null;
	}
	act(() => { renderer = create(<Probe />); });
	return ref;
}

function mount(element: React.ReactElement): ReactTestRenderer {
	let mounted: ReactTestRenderer | undefined;
	act(() => { mounted = create(element); });
	return mounted!;
}

const scrollTo = (y: number) => ({ nativeEvent: { contentOffset: { x: 0, y } } }) as never;

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	fixtures.platform = "android";
	fixtures.setOptions.mockClear();
	fixtures.writes = [];
	fixtures.values = 0;
});

describe("largeTitleCollapsed", () => {
	it("scales the title down once the list is past the threshold", () => {
		expect(largeTitleCollapsed(0)).toBe(false);
		expect(largeTitleCollapsed(LARGE_TITLE_COLLAPSE_AT)).toBe(false);
		expect(largeTitleCollapsed(LARGE_TITLE_COLLAPSE_AT + 1)).toBe(true);
	});

	it("keeps the big title through a pull to refresh", () => {
		expect(largeTitleCollapsed(-80)).toBe(false);
	});
});

describe("useLargeTitle", () => {
	it("leaves iOS to its native large title and passes its scroll handler through", () => {
		fixtures.platform = "ios";
		const onScroll = vi.fn();
		const result = renderHook(onScroll);
		expect(result.current).toEqual({ header: null, edge: null, onScroll, topInset: 0 });
		expect(fixtures.setOptions).not.toHaveBeenCalled();
	});

	it("sets the large title in the bar itself and holds the list clear of the bar on Android", () => {
		const result = renderHook();
		expect(result.current.topInset).toBe(80);
		const header = mount(<>{result.current.header}</>);
		expect(header.root.findByType("View" as never).props.style).toEqual({ height: 80 });
		expect(fixtures.setOptions).toHaveBeenCalledOnce();
		expect(fixtures.setOptions.mock.calls[0][0]).toMatchObject({ headerTransparent: true, headerStyle: { backgroundColor: "transparent" } });
		const BarTitle = fixtures.setOptions.mock.calls[0][0].headerTitle;
		const bar = mount(<BarTitle />);
		const title = bar.root.findByType("AnimatedText" as never).props;
		expect(title).toMatchObject({ children: "Workers", accessibilityRole: "header" });
		// As heavy as iOS's large title, in the app's own face rather than its semibold.
		expect(title.style[0]).toMatchObject({ fontFamily: "DMSans_700Bold", fontSize: 26 });
		// A bold weight would send Android looking for a bold style of the family, which is not registered.
		expect(title.style[0]).not.toHaveProperty("fontWeight");
	});

	it("scales the title on its crossings alone, and still calls the page's own handler on every move", () => {
		const onScroll = vi.fn();
		const result = renderHook(onScroll);
		for (const y of [10, 20, 40, 60, 80, 20, 5, 0]) result.current.onScroll!(scrollTo(y));
		expect(fixtures.writes).toEqual(["title:1", "title:0"]);
		expect(onScroll).toHaveBeenCalledTimes(8);
	});

	it("always frosts the bar and its tail, even at rest, so the bar never reads as see-through", () => {
		const result = renderHook();
		const edge = mount(<>{result.current.edge}</>);
		const frost = edge.root.findByType("Frost" as never);
		expect(frost.props).toMatchObject({ from: "top", stops: [[0, 80]] });
		expect(Object.assign({}, ...frost.props.style).height).toBe(80 + 32);
		expect(edge.root.findAllByType("AnimatedView" as never)).toHaveLength(0);
	});
});
