import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { createElement, type ReactElement } from "react";
import { afterEach, expect, it, vi } from "vitest";

// The window the rows sit in; a test may set it to 0, as before the window has a size.
const screen = vi.hoisted(() => ({ width: 430 }));
vi.mock("react-native", () => ({ View: "View", Dimensions: { get: () => ({ width: screen.width }) } }));

import { MenuRowWidthScope, useMenuRowWidth, useMenuRowWidths, type MenuRowWidths } from "./useMenuRowWidth";

let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	screen.width = 430;
});

// The slot and the row inside it, as the worker row and PR card build them.
function Slot() {
	const menuWidth = useMenuRowWidth();
	return createElement("View", { ref: menuWidth.ref, onLayout: menuWidth.onLayout, testID: "slot" },
		createElement("Row", { style: menuWidth.style }));
}

function mount(slotWidth: number) {
	act(() => {
		renderer = create(createElement(Slot), {
			createNodeMock: (element) => element.type === "View" ? { getBoundingClientRect: () => ({ width: slotWidth }) } : null,
		});
	});
	return renderer!;
}

const rowStyle = (tree: ReactTestRenderer, id?: string) =>
	(id ? tree.root.findByProps({ testID: id }) : tree.root).findByType("Row" as never).props.style;

it("gives the row the slot's width on mount, before any onLayout", () => {
	expect(rowStyle(mount(402.5))).toEqual({ width: 402.5 });
});

it("starts the row at the window's width while the slot has none yet, and takes the slot's from onLayout", () => {
	const tree = mount(0);
	expect(rowStyle(tree)).toEqual({ width: 430 });
	act(() => tree.root.findByProps({ testID: "slot" }).props.onLayout({ nativeEvent: { layout: { width: 390 } } }));
	expect(rowStyle(tree)).toEqual({ width: 390 });
});

it("follows the slot when it is resized later", () => {
	const tree = mount(402);
	act(() => tree.root.findByProps({ testID: "slot" }).props.onLayout({ nativeEvent: { layout: { width: 874 } } }));
	expect(rowStyle(tree)).toEqual({ width: 874 });
});

// Rows a list mounts in a later render batch, or after it remounts. Their slot
// may not be readable when they first render (a width of 0 here), and nothing
// may be painted before they are sized.
function LoggedSlot({ id, log }: { id: string; log: [string, number | undefined][] }) {
	const menuWidth = useMenuRowWidth();
	log.push([id, menuWidth.width]);
	return createElement("View", { ref: menuWidth.ref, onLayout: menuWidth.onLayout, testID: id },
		createElement("Row", { style: menuWidth.style }));
}

function list(ids: string[], log: [string, number | undefined][], scoped = true) {
	const rows = ids.map((id) => createElement(LoggedSlot, { key: id, id, log }));
	return scoped ? createElement(MenuRowWidthScope, null, ...rows) : createElement("List", null, ...rows);
}

// The first batch reads its slot (402.5 wide); later rows report nothing yet.
const firstBatchOnly = (element: ReactElement) => {
	const id = (element.props as { testID?: string }).testID;
	return element.type === "View" ? { getBoundingClientRect: () => ({ width: id?.startsWith("first") ? 402.5 : 0 }) } : null;
};

const firstRender = (log: [string, number | undefined][], id: string) => log.find(([row]) => row === id)?.[1];

it("renders a row from a later batch at the list's width on its first render", () => {
	const log: [string, number | undefined][] = [];
	act(() => { renderer = create(list(["first-a", "first-b"], log), { createNodeMock: firstBatchOnly }); });
	act(() => renderer!.update(list(["first-a", "first-b", "later-c"], log)));
	expect(firstRender(log, "later-c")).toBe(402.5);
	expect(rowStyle(renderer!, "later-c")).toEqual({ width: 402.5 });
});

it("keeps the width for rows mounted again under the same scope", () => {
	const log: [string, number | undefined][] = [];
	act(() => { renderer = create(list(["first-a"], log), { createNodeMock: firstBatchOnly }); });
	act(() => renderer!.update(list([], log)));
	act(() => renderer!.update(list(["later-d"], log)));
	expect(firstRender(log, "later-d")).toBe(402.5);
});

it("takes the width a row measures later as the seed for the rows after it", () => {
	const log: [string, number | undefined][] = [];
	act(() => { renderer = create(list(["first-a"], log), { createNodeMock: firstBatchOnly }); });
	act(() => renderer!.root.findByProps({ testID: "first-a" }).props.onLayout({ nativeEvent: { layout: { width: 874 } } }));
	act(() => renderer!.update(list(["first-a", "later-e"], log)));
	expect(firstRender(log, "later-e")).toBe(874);
});

it("starts rows outside a scope at the window's width, then measures each slot", () => {
	const log: [string, number | undefined][] = [];
	act(() => { renderer = create(list(["first-a"], log, false), { createNodeMock: firstBatchOnly }); });
	act(() => renderer!.update(list(["first-a", "later-f"], log, false)));
	expect(firstRender(log, "later-f")).toBe(430);
	expect(rowStyle(renderer!, "first-a")).toEqual({ width: 402.5 });
});

// A list's first rows have no sibling to inherit from. Rendered once without a
// width inside an iOS menu, a row's title line keeps its intrinsic width for
// good, running off the screen with the row's time past its edge.
it("renders a list's first rows at the window's width, never unsized, then at their slot's", () => {
	const log: [string, number | undefined][] = [];
	act(() => { renderer = create(list(["first-a", "first-b"], log), { createNodeMock: firstBatchOnly }); });
	expect(firstRender(log, "first-a")).toBe(430);
	expect(firstRender(log, "first-b")).toBe(430);
	expect(log.some(([, width]) => width === undefined)).toBe(false);
	expect(rowStyle(renderer!, "first-a")).toEqual({ width: 402.5 });
	expect(rowStyle(renderer!, "first-b")).toEqual({ width: 402.5 });
});

it("prefers the list's measured width over the window's for later rows", () => {
	const log: [string, number | undefined][] = [];
	act(() => { renderer = create(list(["first-a"], log), { createNodeMock: firstBatchOnly }); });
	act(() => renderer!.update(list(["first-a", "later-j"], log)));
	expect(firstRender(log, "later-j")).toBe(402.5);
});

it("leaves a row unsized when the window has no size yet", () => {
	screen.width = 0;
	const log: [string, number | undefined][] = [];
	act(() => { renderer = create(list(["first-a"], log), { createNodeMock: () => ({ getBoundingClientRect: () => ({ width: 0 }) }) }); });
	expect(firstRender(log, "first-a")).toBeUndefined();
});

it("seeds a remounted list from widths held above it", () => {
	const log: [string, number | undefined][] = [];
	// A screen holding the widths, with its board keyed by the filter.
	function Screen({ filter, ids }: { filter: string; ids: string[] }) {
		const widths: MenuRowWidths = useMenuRowWidths();
		return createElement(MenuRowWidthScope, { key: filter, widths }, ...ids.map((id) => createElement(LoggedSlot, { key: id, id, log })));
	}
	act(() => { renderer = create(createElement(Screen, { filter: "cloud", ids: ["first-a"] }), { createNodeMock: firstBatchOnly }); });
	act(() => renderer!.update(createElement(Screen, { filter: "all", ids: ["later-g", "later-h"] })));
	expect(firstRender(log, "later-g")).toBe(402.5);
	expect(firstRender(log, "later-h")).toBe(402.5);
});

it("keeps the last real width as the seed when a row reports zero", () => {
	const log: [string, number | undefined][] = [];
	act(() => { renderer = create(list(["first-a"], log), { createNodeMock: firstBatchOnly }); });
	// A slot laid out at zero for a moment (hidden, or mid-transition).
	act(() => renderer!.root.findByProps({ testID: "first-a" }).props.onLayout({ nativeEvent: { layout: { width: 0 } } }));
	act(() => renderer!.update(list(["first-a", "later-i"], log)));
	expect(firstRender(log, "later-i")).toBe(402.5);
});
