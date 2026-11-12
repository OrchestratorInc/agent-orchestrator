import { afterEach, describe, expect, it, vi } from "vitest";
import { boundedGridTarget, gridNavigationTarget, isGridNavigationKey, measureGridColumns, measureVisibleRows, mountLimitForTarget } from "./grid-navigation";

describe("gridNavigationTarget", () => {
	// 10 items, 4 columns:
	//   0 1 2 3
	//   4 5 6 7
	//   8 9
	const move = (key: Parameters<typeof gridNavigationTarget>[0], index: number, pageRows = 1) =>
		gridNavigationTarget(key, index, 10, 4, pageRows);

	it("steps one item left and right within the row, never wrapping to the next or previous row", () => {
		expect(move("ArrowRight", 1)).toBe(2);
		expect(move("ArrowLeft", 2)).toBe(1);
		expect(move("ArrowRight", 3)).toBe(3);
		expect(move("ArrowLeft", 4)).toBe(4);
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

describe("boundedGridTarget", () => {
	it("lets a move mount at most a bounded number of items past those mounted", () => {
		// End in 5,000 items with 24 mounted stops on the 120th, not the 5,000th.
		expect(boundedGridTarget(4999, 24, 96)).toBe(119);
		// Pressing End again goes on from the larger window.
		expect(boundedGridTarget(4999, 144, 96)).toBe(239);
		// Within the bound, the move is unchanged.
		expect(boundedGridTarget(30, 24, 96)).toBe(30);
		expect(boundedGridTarget(5, 24, 96)).toBe(5);
		expect(boundedGridTarget(-1, 24, 96)).toBe(-1);
	});
});

describe("mountLimitForTarget", () => {
	it("keeps the page past the target inside the per-press budget", () => {
		// End from 24 mounted: target 119 fills the 96-card budget exactly; no page past it.
		const end = mountLimitForTarget(boundedGridTarget(4999, 24, 96), 24, 24, 96);
		expect(end).toBe(120);
		expect(end - 24).toBeLessThanOrEqual(96);
		// Room left in the budget: the target and a page past it.
		expect(mountLimitForTarget(30, 24, 24, 96)).toBe(55);
		// Part of a page fits.
		expect(mountLimitForTarget(100, 24, 24, 96)).toBe(120);
		// The target itself is always mounted.
		expect(mountLimitForTarget(200, 24, 24, 96)).toBe(201);
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

	it("counts the whole rows that fit from the current row, by their positions", () => {
		const viewport = document.createElement("div");
		Object.defineProperty(viewport, "clientHeight", { value: 250 });
		// Two columns; rows 190, 190, 30 and 30 px tall with no gap.
		const heights = [190, 190, 30, 30];
		const grid = document.createElement("div");
		let top = 0;
		for (const height of heights) {
			for (let column = 0; column < 2; column += 1) {
				const item = document.createElement("div");
				const rowTop = top;
				vi.spyOn(item, "getBoundingClientRect").mockReturnValue({ top: rowTop, bottom: rowTop + height } as DOMRect);
				grid.append(item);
			}
			top += height;
		}
		// From the first row only it fits; the average height would say two.
		expect(measureVisibleRows(viewport, grid, 0, 2)).toBe(1);
		// From the second row: 190 + 30 + 30 fill the 250 px exactly.
		expect(measureVisibleRows(viewport, grid, 3, 2)).toBe(3);
		expect(measureVisibleRows(viewport, grid, 5, 2)).toBe(2);
	});

	it("counts the rows above for PageUp, from the last row", () => {
		const viewport = document.createElement("div");
		Object.defineProperty(viewport, "clientHeight", { value: 100 });
		// Two columns, four rows of 30 px.
		const grid = document.createElement("div");
		for (let index = 0; index < 8; index += 1) {
			const item = document.createElement("div");
			const top = Math.floor(index / 2) * 30;
			vi.spyOn(item, "getBoundingClientRect").mockReturnValue({ top, bottom: top + 30 } as DOMRect);
			grid.append(item);
		}
		// From the last row, three rows (90 px) fit going up; nothing is below it.
		expect(measureVisibleRows(viewport, grid, 7, 2, -1)).toBe(3);
		expect(measureVisibleRows(viewport, grid, 7, 2, 1)).toBe(1);
		// PageUp from the last row then moves those three rows, in the same column.
		expect(gridNavigationTarget("PageUp", 7, 8, 2, measureVisibleRows(viewport, grid, 7, 2, -1))).toBe(1);
		expect(measureVisibleRows(viewport, grid, 0, 2, 1)).toBe(3);
	});

	it("measures one row without a viewport, items or layout", () => {
		const viewport = document.createElement("div");
		const grid = document.createElement("div");
		expect(measureVisibleRows(null, grid, 0, 3)).toBe(1);
		expect(measureVisibleRows(viewport, grid, 0, 3)).toBe(1);
		grid.append(document.createElement("div"), document.createElement("div"));
		// jsdom lays nothing out: every rect is zero.
		expect(measureVisibleRows(viewport, grid, 0, 1)).toBe(1);
	});
});
