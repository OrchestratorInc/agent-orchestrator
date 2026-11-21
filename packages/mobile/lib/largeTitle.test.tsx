import { createElement, type default as React } from "react";
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
vi.mock("./frost", () => ({ Frost: "Frost", FROST_TAIL: 32, BAR_SEAM: 16, SEAM_STOPS: [[0, 0.9], [1, 0]], frostStops: (height: number, tail?: number) => (tail === undefined ? [[0, height]] : [[0, height], [1, tail]]) }));
vi.mock("react-native", () => ({
	Platform: { get OS() { return fixtures.platform; } },
	StyleSheet: { create: (styles: object) => styles, hairlineWidth: 0.5, absoluteFill: { position: "absolute" } },
	View: "View",
}));
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		default: { Text: host("AnimatedText"), View: host("AnimatedView") },
		// Records each write under the value's place in creation order (the
		// title's collapse first, then the solid bar's seam), so a test can tell a
		// crossing from a repeat.
		useSharedValue: (initial: number) => {
			const id = React.useRef(fixtures.values++).current;
			return React.useRef({
				_value: initial,
				get value() { return this._value; },
				set value(next: number) { fixtures.writes.push(`${id === 0 ? "title" : "seam"}:${next}`); this._value = next; },
			}).current;
		},
		useAnimatedStyle: () => ({}),
		withTiming: (value: number) => value,
	};
});
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({ textPrimary: "#textPrimary", borderDefault: "#borderDefault", bgBase: "#bgBase" }) }));

import { LARGE_TITLE_COLLAPSE_AT, largeTitleCollapsed, seamShown, useLargeTitle } from "./largeTitle";

type Result = ReturnType<typeof useLargeTitle>;

let renderer: ReactTestRenderer | undefined;

function renderHook(onScroll?: Parameters<typeof useLargeTitle>[1], options?: Parameters<typeof useLargeTitle>[2]): { current: Result } {
	const ref = { current: undefined as unknown as Result };
	function Probe() {
		ref.current = useLargeTitle("Workers", onScroll, options);
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
		expect(result.current).toEqual({ header: null, edge: null, onScroll, topInset: 0, accessory: null });
		expect(fixtures.setOptions).not.toHaveBeenCalled();
	});

	it("sets the large title in the bar itself and holds the list clear of the bar on Android", () => {
		const result = renderHook();
		expect(result.current.topInset).toBe(80);
		const header = mount(<>{result.current.header}</>);
		expect(header.root.findAll((node) => (node.type as unknown) === "View" && node.props.style?.height === 80)).toHaveLength(1);
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

describe("the title's accessory", () => {
	function renderWith(accessory: React.ReactNode): { current: Result } {
		const ref = { current: undefined as unknown as Result };
		function Probe() {
			ref.current = useLargeTitle("Workers", undefined, { accessory });
			return null;
		}
		act(() => { renderer = create(<Probe />); });
		return ref;
	}
	// The edge draws the frost and the pinned box side by side, so it is mounted under one host.
	const mountEdge = (edge: React.ReactElement) => mount(createElement("Edge", null, edge));
	const pinnedBox = (edge: React.ReactElement) => mountEdge(edge).root.find((node) => (node.type as unknown) === "View" && node.props.pointerEvents === "box-none");

	// In the page it sat in the frost's fade and read washed out; pinned under
	// the bar it draws over the frost. The frost keeps to the bar: grown to back
	// the line, its fade moved down onto the page's first row.
	it("pins it under the Android bar, over the frost, and grows the inset by its height", () => {
		const hook = renderWith("Can't reach Lab");
		const pinned = pinnedBox(hook.current.edge!);
		expect(pinned.props.style).toContainEqual({ top: 80 });
		expect(pinned.props.pointerEvents).toBe("box-none");
		expect([pinned.props.children].flat()).toContain("Can't reach Lab");
		expect(hook.current.accessory).toBeNull();
		expect(hook.current.topInset).toBe(80);

		// Its twin in the header makes room for it in the same layout pass.
		const header = mount(hook.current.header!);
		const twin = header.root.find((node) => (node.type as unknown) === "View" && typeof node.props.onLayout === "function");
		act(() => twin.props.onLayout({ nativeEvent: { layout: { height: 24.4 } } }));
		expect(hook.current.topInset).toBe(104);

		const edge = mountEdge(hook.current.edge!);
		const frosts = edge.root.findAllByType("Frost" as never);
		const bar = frosts.find((frost) => JSON.stringify(frost.props.stops) === JSON.stringify([[0, 80]]))!;
		expect(bar.props.style).toContainEqual({ height: 80 + 32 });
		// The line's own backing: its height plus a short seam, so rows scrolling
		// under it don't show through its text.
		const backing = frosts.find((frost) => frost !== bar)!;
		expect(backing.props.stops).toEqual([[0, 24], [1, 16]]);
		expect(backing.props.style).toContainEqual({ height: 24 + 16 });

		// Drawing nothing (a live machine) measures 0, and the page closes up.
		const remeasured = mount(hook.current.header!).root.find((node) => (node.type as unknown) === "View" && typeof node.props.onLayout === "function");
		act(() => remeasured.props.onLayout({ nativeEvent: { layout: { height: 0 } } }));
		expect(hook.current.topInset).toBe(80);
	});

	// TalkBack reads overlays after a full-screen list, so the pinned line is a
	// picture; its invisible twin, first in the list's header right under the
	// bar, is what TalkBack reaches, before the rows.
	it("leaves TalkBack to the line's invisible twin in the header", () => {
		const hook = renderWith("Can't reach Lab");
		const pinned = pinnedBox(hook.current.edge!);
		expect(pinned.props).toMatchObject({ collapsable: false, accessibilityElementsHidden: true, importantForAccessibility: "no-hide-descendants" });
		const header = mount(hook.current.header!);
		const [spacer, twin] = header.root.findByType("View" as never).children as ReturnType<typeof header.root.findByType>[];
		expect(spacer.props.style).toEqual({ height: 80 });
		expect(twin.props.style).toEqual({ opacity: 0.01 });
		expect(twin.props.collapsable).toBe(false);
		expect(twin.props.importantForAccessibility).toBeUndefined();
		expect([twin.props.children].flat()).toContain("Can't reach Lab");
	});

	it("leaves the inset alone with no accessory", () => {
		const hook = renderWith(null);
		expect(hook.current.topInset).toBe(80);
		expect(mountEdge(hook.current.edge!).root.findAll((node) => node.props.pointerEvents === "box-none")).toHaveLength(0);
		expect(mount(hook.current.header!).root.findAll((node) => typeof node.props.onLayout === "function")).toHaveLength(0);
	});

	it("hands it back to the page on iOS, where it goes under the large title", () => {
		fixtures.platform = "ios";
		const hook = renderWith("Can't reach Lab");
		expect(hook.current.accessory).toBe("Can't reach Lab");
		expect(hook.current.edge).toBeNull();
		expect(hook.current.topInset).toBe(0);
	});
});

describe("a solid bar", () => {
	const flat = (style: unknown) => Object.assign({}, ...[style].flat(Infinity as 1));
	const mountEdge = (edge: React.ReactElement) => mount(createElement("Edge", null, edge));

	it("shows its seam once the list leaves the top, and not through a pull to refresh", () => {
		expect(seamShown(0)).toBe(false);
		expect(seamShown(-80)).toBe(false);
		expect(seamShown(1)).toBe(true);
	});

	// The board's machine chips sat in the frost's tail and read dimmed at rest.
	it("backs the bar with the page's colour, opaque and no deeper than the bar, with no frost over the page", () => {
		const result = renderHook(undefined, { backdrop: "solid" });
		const edge = mountEdge(result.current.edge!);
		const bar = edge.root.find((node) => (node.type as unknown) === "View" && flat(node.props.style).backgroundColor === "#bgBase");
		expect(flat(bar.props.style)).toMatchObject({ position: "absolute", top: 0, height: 80 });
		expect(bar.props.pointerEvents).toBe("none");
		// The only frost left is the seam's, inside its fading view.
		const frosts = edge.root.findAllByType("Frost" as never);
		expect(frosts).toHaveLength(1);
		expect(frosts[0].props.stops).toEqual([[0, 0.9], [1, 0]]);
		const seam = edge.root.findByType("AnimatedView" as never);
		expect(flat(seam.props.style)).toMatchObject({ top: 80, height: 16 });
		expect(seam.props.pointerEvents).toBe("none");
		// The list's room under the bar is the frosted bar's.
		expect(result.current.topInset).toBe(80);
	});

	it("raises the seam on the list leaving the top alone, and lowers it on coming back", () => {
		const onScroll = vi.fn();
		const result = renderHook(onScroll, { backdrop: "solid" });
		for (const y of [-40, 0, 4, 10, 40, 10, 0]) result.current.onScroll!(scrollTo(y));
		expect(fixtures.writes).toEqual(["seam:1", "title:1", "title:0", "seam:0"]);
		expect(onScroll).toHaveBeenCalledTimes(7);
	});

	it("backs the pinned line with the page's colour and puts the seam under it", () => {
		const ref = { current: undefined as unknown as Result };
		function Probe() {
			ref.current = useLargeTitle("Workers", undefined, { accessory: "Can't reach Lab", backdrop: "solid" });
			return null;
		}
		act(() => { renderer = create(<Probe />); });
		const twin = mount(ref.current.header!).root.find((node) => (node.type as unknown) === "View" && typeof node.props.onLayout === "function");
		act(() => twin.props.onLayout({ nativeEvent: { layout: { height: 24 } } }));
		expect(ref.current.topInset).toBe(104);
		const edge = mountEdge(ref.current.edge!);
		const pinned = edge.root.find((node) => (node.type as unknown) === "View" && node.props.pointerEvents === "box-none");
		const backing = pinned.find((node) => (node.type as unknown) === "View" && flat(node.props.style).backgroundColor === "#bgBase");
		expect(flat(backing.props.style)).toMatchObject({ height: 24 });
		expect(flat(edge.root.findByType("AnimatedView" as never).props.style)).toMatchObject({ top: 104, height: 16 });
		expect(edge.root.findAllByType("Frost" as never)).toHaveLength(1);
	});

	it("draws nothing on iOS", () => {
		fixtures.platform = "ios";
		const result = renderHook(undefined, { backdrop: "solid" });
		expect(result.current.edge).toBeNull();
	});
});

describe("a replaced list", () => {
	function renderKeyed(key: string, backdrop: "frost" | "solid" = "solid"): { current: Result; rekey(next: string): void } {
		const ref = { current: undefined as unknown as Result, rekey: (_next: string) => {} };
		function Probe({ k }: { k: string }) {
			ref.current = useLargeTitle("Workers", undefined, { backdrop, resetKey: k });
			return null;
		}
		act(() => { renderer = create(<Probe k={key} />); });
		ref.rekey = (next) => act(() => renderer!.update(<Probe k={next} />));
		return ref;
	}

	// The board keys its list by filter; the new list starts at its top without a scroll event.
	it("lowers the seam and settles the title when the list's key changes", () => {
		const hook = renderKeyed("all|all");
		hook.current.onScroll!(scrollTo(60));
		expect(fixtures.writes).toEqual(["title:1", "seam:1"]);
		hook.rekey("mac|all");
		expect(fixtures.writes).toEqual(["title:1", "seam:1", "title:0", "seam:0"]);
		// The new list's first move raises the seam again.
		hook.current.onScroll!(scrollTo(4));
		expect(fixtures.writes).toEqual(["title:1", "seam:1", "title:0", "seam:0", "seam:1"]);
	});

	it("writes nothing on mount, on a render with the same key, or when the list was already at rest", () => {
		const hook = renderKeyed("all|all");
		hook.rekey("all|all");
		hook.rekey("mac|all");
		expect(fixtures.writes).toEqual([]);
	});

	it("leaves iOS alone", () => {
		fixtures.platform = "ios";
		const hook = renderKeyed("all|all");
		hook.rekey("mac|all");
		expect(fixtures.writes).toEqual([]);
	});
});
