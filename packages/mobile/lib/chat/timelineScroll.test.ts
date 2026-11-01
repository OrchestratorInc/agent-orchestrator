import { describe, expect, it } from "vitest";
import { atTail, openingRow, TAIL_SLOP } from "./timelineScroll";

const metrics = (offset: number) => ({ contentOffset: { x: 0, y: offset }, contentSize: { width: 390, height: 3000 }, layoutMeasurement: { width: 390, height: 800 } });

describe("atTail", () => {
	it("counts the end, and a thumb's slop short of it, as the newest message", () => {
		expect(atTail(metrics(2200))).toBe(true);
		expect(atTail(metrics(2200 - TAIL_SLOP + 1))).toBe(true);
		expect(atTail(metrics(2200 - TAIL_SLOP))).toBe(false);
		expect(atTail(metrics(0))).toBe(false);
	});
});

describe("openingRow", () => {
	it("opens on the newest rows, or the first when there are fewer", () => {
		expect(openingRow(30, 4)).toBe(26);
		expect(openingRow(4, 4)).toBe(0);
		expect(openingRow(2, 4)).toBe(0);
	});
});
