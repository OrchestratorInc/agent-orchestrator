import { afterEach, describe, expect, it, vi } from "vitest";
import { gridNavigationTarget, isGridNavigationKey, measureGridColumns, measureVisibleRows } from "./grid-navigation";

describe("gridNavigationTarget", () => {
	// 10 items, 4 columns:
	//   0 1 2 3
	//   4 5 6 7
	//   8 9
	const move = (key: Parameters<typeof gridNavigationTarget>[0], index: number, pageRows = 1) =>
		gridNavigationTarget(key, index, 10, 4, pageRows);

	it("steps one item left and right, across rows, stopping at the ends", () => {
		expect(move("ArrowRight", 3)).toBe(4);
		expect(move("ArrowLeft", 4)).toBe(3);
		expect(move("ArrowLeft", 0)).toBe(0);
		expect(move("ArrowRight", 9)).toBe(9);
	});

	it("moves up and down one row in the same column, clamped to the first and last rows", () => {
		expect(move("ArrowDown", 1)).toBe(5);
		expect(move("ArrowUp", 5)).toBe(1);
		expect(move("ArrowUp", 2)).toBe(2);
		expect(move("ArrowDown", 9)).toBe(9);
		expect(move("ArrowDown", 8)).toBe(8);
	});

	it("lands on the last item when the last row is short", () => {
		expect(move("ArrowDown", 6)).toBe(9);
		expect(move("ArrowDown", 7)).toBe(9);
	});

	it("goes to the first and last items with Home and End", () => {
		expect(move("Home", 6)).toBe(0);
		expect(move("End", 1)).toBe(9);
	});

	it("pages by whole rows, then to the first or last item", () => {
		expect(move("PageDown", 1, 2)).toBe(9);
		expect(move("PageDown", 0, 1)).toBe(4);
		expect(move("PageDown", 3, 5)).toBe(9);
		expect(move("PageDown", 9, 2)).toBe(9);
		expect(move("PageUp", 9, 1)).toBe(5);
		expect(move("PageUp", 6, 5)).toBe(2);
		expect(move("PageUp", 2, 1)).toBe(0);
	});

	it("treats a bad column count as one column and an empty grid as nothing", () => {
		expect(gridNavigationTarget("ArrowDown", 0, 3, 0, 1)).toBe(1);
		expect(gridNavigationTarget("ArrowDown", 0, 0, 4, 1)).toBe(-1);
		expect(gridNavigationTarget("ArrowUp", 7, 3, 1, 1)).toBe(1);
	});

	it("knows its keys", () => {
		expect(isGridNavigationKey("PageDown")).toBe(true);
		expect(isGridNavigationKey("Enter")).toBe(false);
	});
});

describe("grid measurement", () => {
	afterEach(() => vi.restoreAllMocks());

	const withTemplate = (template: string) => {
		const grid = document.createElement("div");
		vi.spyOn(window, "getComputedStyle").mockReturnValue({
			getPropertyValue: (name: string) => (name === "grid-template-columns" ? template : ""),
		} as CSSStyleDeclaration);
		return grid;
	};

	it("counts the resolved column tracks of a responsive grid", () => {
		expect(measureGridColumns(withTemplate("282.5px 282.5px 282.5px 282.5px"))).toBe(4);
		expect(measureGridColumns(withTemplate("300px 300px"))).toBe(2);
	});

	it("falls back to one column when the layout cannot be read", () => {
		expect(measureGridColumns(null)).toBe(1);
		expect(measureGridColumns(withTemplate(""))).toBe(1);
		expect(measureGridColumns(withTemplate("none"))).toBe(1);
		expect(measureGridColumns(withTemplate("repeat(auto-fill, minmax(17rem, 1fr))"))).toBe(1);
	});

	it("fits whole rows of the grid in the viewport", () => {
		const viewport = document.createElement("div");
		Object.defineProperty(viewport, "clientHeight", { value: 500 });
		const grid = document.createElement("div");
		vi.spyOn(grid, "getBoundingClientRect").mockReturnValue({ height: 600 } as DOMRect);
		// 12 cards in 3 columns: 4 rows of 150px; 3 fit in 500px.
		expect(measureVisibleRows(viewport, grid, 12, 3)).toBe(3);
		expect(measureVisibleRows(viewport, grid, 0, 3)).toBe(1);
		expect(measureVisibleRows(null, grid, 12, 3)).toBe(1);
	});
});
