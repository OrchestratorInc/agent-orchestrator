import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { GridSpacer, gridWindow, useVirtualGridRows } from "./useVirtualGridRows";

const layout = { columns: 3, pitch: 216, gap: 16 };

describe("gridWindow", () => {
	it("builds the rows asked for and keeps the height of the rest", () => {
		// 100 cards in 3 columns: 34 rows, each 200 tall with a 16 gap below.
		const range = gridWindow(100, layout, 10, 14);
		expect(range).toEqual({ start: 30, end: 45, before: 10 * 216 - 16, after: (33 - 14) * 216 - 16 });
		// The spacers, the gaps and the built rows add up to the whole list.
		const built = 5 * 200 + 4 * 16;
		expect(range.before + 16 + built + 16 + range.after).toBe(34 * 200 + 33 * 16);
	});

	it("needs no spacer at either end of the list", () => {
		expect(gridWindow(100, layout, -2, 3)).toMatchObject({ start: 0, before: 0 });
		expect(gridWindow(100, layout, 30, 40)).toMatchObject({ end: 100, after: 0 });
	});

	it("builds nothing for an empty list", () => {
		expect(gridWindow(0, layout, 0, 5)).toEqual({ start: 0, end: 0, before: 0, after: 0 });
	});
});

function Grid({ count }: { count: number }) {
	const grid = useVirtualGridRows(count);
	return (
		<ul ref={grid.listRef} style={{ display: "grid" }}>
			<GridSpacer height={grid.before} />
			{Array.from({ length: grid.end - grid.start }, (_, index) => <li key={grid.start + index}>card {grid.start + index}</li>)}
			<GridSpacer height={grid.after} />
		</ul>
	);
}

describe("useVirtualGridRows", () => {
	it("builds every card where there is no layout to measure", () => {
		render(<Grid count={60} />);
		expect(screen.getAllByRole("listitem")).toHaveLength(60);
	});
});
