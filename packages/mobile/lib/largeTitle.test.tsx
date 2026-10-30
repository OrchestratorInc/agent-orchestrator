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
	Text: "Text",
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
	it("hands the name to the bar once the big title has scrolled under it", () => {
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

	it("sets the large title in the list, clear of the bar, and gives the bar a title of its own on Android", () => {
		const result = renderHook();
		expect(result.current.topInset).toBe(80);
		const header = mount(<>{result.current.header}</>);
		const large = header.root.findByType("Text" as never);
		expect(large.props).toMatchObject({ accessibilityRole: "header", children: "Workers" });
		expect(Object.assign({}, ...large.props.style).paddingTop).toBe(80 + 4);
		expect(fixtures.setOptions).toHaveBeenCalledOnce();
		expect(fixtures.setOptions.mock.calls[0][0]).toMatchObject({ headerTransparent: true, headerStyle: { backgroundColor: "transparent" } });
		const BarTitle = fixtures.setOptions.mock.calls[0][0].headerTitle;
		const bar = mount(<BarTitle />);
		expect(bar.root.findByType("AnimatedText" as never).props).toMatchObject({ children: "Workers", importantForAccessibility: "no" });
	});

	// The frost comes up on the first move and goes at rest; the bar's title
	// takes over only once the big one is under it.
	it("moves the frost and the title on their crossings alone, and still calls the page's own handler on every move", () => {
		const onScroll = vi.fn();
		const result = renderHook(onScroll);
		for (const y of [10, 20, 40, 60, 80, 20, 5, 0]) result.current.onScroll!(scrollTo(y));
		expect(fixtures.writes).toEqual(["frost:1", "title:1", "title:0", "frost:0"]);
		expect(onScroll).toHaveBeenCalledTimes(8);
	});

	it("frosts the clear bar and its tail, behind the bar's items", () => {
		const result = renderHook();
		const edge = mount(<>{result.current.edge}</>);
		const frost = edge.root.findByType("Frost" as never);
		expect(frost.props).toMatchObject({ from: "top", stops: [[0, 80]] });
		const layer = edge.root.findByType("AnimatedView" as never);
		expect(layer.props.pointerEvents).toBe("none");
		expect(Object.assign({}, ...layer.props.style).height).toBe(80 + 32);
	});
});
