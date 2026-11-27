import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }), select: vi.fn(), scrollToOffset: vi.fn() }));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	const FlatList = React.forwardRef(({ data, renderItem, keyExtractor, ListFooterComponent, ...props }: { data: { key: string }[]; renderItem: (info: { item: unknown; index: number }) => React.ReactNode; keyExtractor: (item: never) => string; ListFooterComponent?: React.ReactNode }, ref) => {
		React.useImperativeHandle(ref, () => ({ scrollToOffset: fixtures.scrollToOffset }));
		return React.createElement("FlatList", { ...props, data }, ...data.map((item, index) => React.createElement(React.Fragment, { key: keyExtractor(item as never) }, renderItem({ item, index }))), ListFooterComponent);
	});
	return { FlatList, processColor: () => 0xff000000, StyleSheet: { create: (styles: object) => styles }, Text: host("Text"), View: host("View") };
});
vi.mock("react-native-gesture-handler", async () => {
	const React = await import("react");
	return { Pressable: ({ children, style, ...props }: { children?: React.ReactNode; style?: unknown }) => React.createElement("Pressable", { ...props, style: typeof style === "function" ? style({ pressed: false }) : style }, children) };
});
vi.mock("lucide-react-native/icons/star", () => ({ default: "Star" }));
vi.mock("./AgentLogo", () => ({ AgentLogo: "AgentLogo" }));
vi.mock("./haptics", () => ({ haptics: { select: fixtures.select, scrollTick: () => {} } }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme) }));

import { LIST_ROW, LIST_ROWS, listOffset, ModelList } from "./model-list";

const items = [{ key: "opus", label: "Opus 5.5", starred: true }, { key: "sonnet", label: "Sonnet 5.5" }, { key: "haiku", label: "Haiku 4.5" }];
let renderer: ReactTestRenderer | undefined;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); fixtures.select.mockClear(); fixtures.scrollToOffset.mockClear(); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });
const hostType = (node: ReactTestInstance): string => (typeof node.type === "string" ? node.type : "");
const rows = () => renderer!.root.findAll((node) => hostType(node) === "Pressable");

describe("model list", () => {
	// A plain list, not a dial: every model a row, the one in force marked.
	it("lists the models, the one in force selected, and picks the one tapped", () => {
		const onPick = vi.fn();
		act(() => { renderer = create(<ModelList items={items} index={1} onPick={onPick} />); });
		expect(rows().map((row) => row.props.accessibilityState)).toEqual([{ checked: false }, { checked: true }, { checked: false }]);
		expect(rows()[0].props.accessibilityLabel).toBe("Opus 5.5, default");
		act(() => rows()[2].props.onPress());
		expect(onPick).toHaveBeenCalledExactlyOnceWith(2);
		expect(fixtures.select).toHaveBeenCalledOnce();
	});

	// It opens where the list starts when the model in force is on show there;
	// further down, with that model in the middle, never pinned to the top.
	it("opens with the model in force on show, centred only when it has to scroll", () => {
		expect(listOffset(2, 20)).toBe(0);
		expect(listOffset(12, 20)).toBe((12 - Math.floor(LIST_ROWS / 2)) * LIST_ROW);
		expect(listOffset(19, 20)).toBe((20 - LIST_ROWS) * LIST_ROW);
		act(() => { renderer = create(<ModelList items={items} index={2} visible={false} onPick={() => {}} />); });
		expect(renderer!.root.findAll((node) => hostType(node) === "FlatList")[0].props.contentOffset).toEqual({ x: 0, y: 0 });
		expect(fixtures.scrollToOffset).not.toHaveBeenCalled();
		act(() => renderer!.update(<ModelList items={items} index={2} visible onPick={() => {}} />));
		expect(fixtures.scrollToOffset).toHaveBeenLastCalledWith({ offset: 0, animated: false });
	});

	// At the list's end the last row is the last model, and reads in full.
	it("fades the foot while there is more below, and not at the list's end", () => {
		const long = Array.from({ length: 9 }, (_, i) => ({ key: `m${i}`, label: `Model ${i}` }));
		const fades = () => renderer!.root.findAll((node) => hostType(node) === "View" && Array.isArray(node.props.style) && node.props.style.some((part: { experimental_backgroundImage?: string }) => part?.experimental_backgroundImage));
		act(() => { renderer = create(<ModelList items={long} index={0} onPick={() => {}} />); });
		expect(fades()).toHaveLength(1);
		const list = () => renderer!.root.findAll((node) => hostType(node) === "FlatList")[0];
		act(() => list().props.onScroll({ nativeEvent: { contentOffset: { y: (long.length - LIST_ROWS) * LIST_ROW } } }));
		// The top one only now: rows above, none below.
		expect(fades()).toHaveLength(1);
		expect(fades()[0].props.style.some((part: { top?: number }) => part?.top === 0)).toBe(true);
	});
});
