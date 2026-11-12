import { readFileSync } from "node:fs";
import type React from "react";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	platform: "ios" as "android" | "ios",
	glass: true,
	setOptions: vi.fn(),
	android: { header: "android-spacer", edge: "android-frost", onScroll: () => {}, topInset: 80 },
}));

vi.mock("expo-router", () => ({ useNavigation: () => ({ setOptions: fixtures.setOptions }) }));
vi.mock("expo-router/react-navigation", () => ({ useHeaderHeight: () => 100 }));
vi.mock("react-native", () => ({
	Platform: { get OS() { return fixtures.platform; } },
	StyleSheet: { create: (styles: object) => styles },
	View: "View",
	Text: "Text",
	Pressable: "Pressable",
}));
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	return {
		default: { Text: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("AnimatedText", props, children) },
		useSharedValue: (initial: number) => React.useRef({ value: initial }).current,
		useAnimatedStyle: () => ({}),
		withTiming: (value: number) => value,
	};
});
vi.mock("./largeTitle", () => ({ useLargeTitle: () => fixtures.android }));
vi.mock("./stackScreenOptions", () => ({ get SYSTEM_GLASS_HEADER() { return fixtures.glass; } }));
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: undefined, pressed: undefined }) }));
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({}, { get: (_, key) => `#${String(key)}` });
	return { useTheme: () => theme, useThemedStyles: (factory: (t: object) => object) => factory(theme) };
});

import { INLINE_TITLE_HANDOFF, inlineTitleCollapsed, TitleActionButton, useInlineLargeTitle } from "./inlineLargeTitle";

type Result = ReturnType<typeof useInlineLargeTitle>;

let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	fixtures.setOptions.mockReset();
	fixtures.platform = "ios";
	fixtures.glass = true;
});

function renderHook(options: Parameters<typeof useInlineLargeTitle>[1]): { current: Result } {
	const ref = { current: undefined as unknown as Result };
	function Probe() {
		ref.current = useInlineLargeTitle("Projects", options);
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

const texts = (root: ReactTestRenderer) => root.root.findAll((node: ReactTestInstance) => String(node.type) === "Text").map((node) => node.props.children);
const scroll = (y: number) => ({ nativeEvent: { contentOffset: { y } } }) as never;

describe("a board title with an action on its line, iOS", () => {
	it("turns the system's large title off for the page and keeps the clear glass bar", () => {
		renderHook({});
		expect(fixtures.setOptions).toHaveBeenCalledWith(expect.objectContaining({ headerLargeTitleEnabled: false, headerTransparent: true }));
		fixtures.setOptions.mockReset();
		fixtures.glass = false;
		renderHook({});
		expect(fixtures.setOptions.mock.calls[0][0]).not.toHaveProperty("headerTransparent");
	});

	it("draws the title as the list's first row with the action at its end", () => {
		const onPress = vi.fn();
		const hook = renderHook({ action: <TitleActionButton icon="add" accessibilityLabel="New cloud project" onPress={onPress} /> });
		const row = mount(hook.current.header!);
		expect(texts(row)).toEqual(["Projects"]);
		const button = row.root.find((node) => node.props.accessibilityLabel === "New cloud project" && node.props.accessibilityRole === "button");
		act(() => button.props.onPress());
		expect(onPress).toHaveBeenCalled();
		expect(hook.current.edge).toBeNull();
		expect(hook.current.topInset).toBe(0);
	});

	it("hands the title to the bar once the row has scrolled under it, and calls the page's own handler", () => {
		const onScroll = vi.fn();
		const hook = renderHook({ onScroll });
		// Under the clear bar the list rests at minus the bar's height.
		expect(inlineTitleCollapsed(-100, -100)).toBe(false);
		expect(inlineTitleCollapsed(-100 + INLINE_TITLE_HANDOFF + 1, -100)).toBe(true);
		const options = fixtures.setOptions.mock.calls[0][0] as { headerTitle: () => React.ReactElement };
		const bar = mount(options.headerTitle());
		const opacityAt = () => bar.root.findByType("AnimatedText" as never).props;
		expect(opacityAt().accessibilityElementsHidden).toBe(true);
		act(() => hook.current.onScroll!(scroll(-100 + INLINE_TITLE_HANDOFF + 1)));
		expect(onScroll).toHaveBeenCalledTimes(1);
	});
});

describe("a board title with an action on its line, Android", () => {
	it("is the top app bar's large title as before, and leaves the action to the floating button", () => {
		fixtures.platform = "android";
		const hook = renderHook({ action: <TitleActionButton icon="add" accessibilityLabel="New cloud project" onPress={vi.fn()} /> });
		expect(hook.current).toBe(fixtures.android);
		expect(fixtures.setOptions).not.toHaveBeenCalled();
	});
});

describe("the Projects board", () => {
	const page = readFileSync(new URL("../app/(tabs)/(projects)/projects.tsx", import.meta.url), "utf8");

	// Android's large title is the bar's own, on the row its actions sit on, and
	// its board has no floating button any more: there the "+" is the bar's.
	it("adds a cloud project from its title line on iOS, and from the bar on Android", () => {
		expect(page).toContain("useInlineLargeTitle(");
		expect(page).toContain('accessibilityLabel="New cloud project"');
		expect(page).toContain('onAdd={Platform.OS === "android" && canCreateCloudProject ? newCloudProject : undefined} addLabel="New cloud project"');
		expect(page).not.toContain("AddFab");
	});
});
