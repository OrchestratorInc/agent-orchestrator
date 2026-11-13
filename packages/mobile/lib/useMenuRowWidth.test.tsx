import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { createElement } from "react";
import { afterEach, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({ View: "View" }));

import { useMenuRowWidth } from "./useMenuRowWidth";

let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
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

const rowStyle = (tree: ReactTestRenderer) => tree.root.findByType("Row" as never).props.style;

it("gives the row the slot's width on mount, before any onLayout", () => {
	expect(rowStyle(mount(402.5))).toEqual({ width: 402.5 });
});

it("leaves the row unsized while the slot has no width yet, and takes it from onLayout", () => {
	const tree = mount(0);
	expect(rowStyle(tree)).toBeUndefined();
	act(() => tree.root.findByProps({ testID: "slot" }).props.onLayout({ nativeEvent: { layout: { width: 390 } } }));
	expect(rowStyle(tree)).toEqual({ width: 390 });
});

it("follows the slot when it is resized later", () => {
	const tree = mount(402);
	act(() => tree.root.findByProps({ testID: "slot" }).props.onLayout({ nativeEvent: { layout: { width: 874 } } }));
	expect(rowStyle(tree)).toEqual({ width: 874 });
});
