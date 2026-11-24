import { describe, expect, it } from "vitest";
import {
	isRegionLargeEnough,
	normalizeRegionRect,
	regionCaptureRect,
	regionSelectFileName,
} from "./browser-region-select";

const viewport = { width: 800, height: 600 };

describe("normalizeRegionRect", () => {
	it("orders the corners whichever way the drag went", () => {
		expect(normalizeRegionRect({ x: 300, y: 200 }, { x: 100, y: 50 }, viewport)).toEqual({
			x: 100,
			y: 50,
			width: 200,
			height: 150,
		});
	});

	it("keeps a drag that leaves the page inside the viewport", () => {
		expect(normalizeRegionRect({ x: 700, y: 500 }, { x: 950, y: -40 }, viewport)).toEqual({
			x: 700,
			y: 0,
			width: 100,
			height: 500,
		});
	});
});

describe("isRegionLargeEnough", () => {
	it("treats a click or a sliver as no selection", () => {
		expect(isRegionLargeEnough({ x: 0, y: 0, width: 0, height: 0 })).toBe(false);
		expect(isRegionLargeEnough({ x: 0, y: 0, width: 200, height: 3 })).toBe(false);
		expect(isRegionLargeEnough({ x: 0, y: 0, width: 6, height: 6 })).toBe(true);
	});
});

describe("regionCaptureRect", () => {
	it("scales CSS pixels by the page zoom into whole DIPs", () => {
		expect(regionCaptureRect({ x: 10.4, y: 20.6, width: 100, height: 50 }, 1.5, viewport)).toEqual({
			x: 15,
			y: 30,
			width: 151,
			height: 76,
		});
	});

	it("clips to the view", () => {
		expect(regionCaptureRect({ x: 700, y: 500, width: 300, height: 300 }, 1, viewport)).toEqual({
			x: 700,
			y: 500,
			width: 100,
			height: 100,
		});
	});

	it("rejects malformed or empty input", () => {
		expect(regionCaptureRect(undefined, 1, viewport)).toBeUndefined();
		expect(regionCaptureRect({ x: "1", y: 0, width: 10, height: 10 }, 1, viewport)).toBeUndefined();
		expect(regionCaptureRect({ x: Number.NaN, y: 0, width: 10, height: 10 }, 1, viewport)).toBeUndefined();
		expect(regionCaptureRect({ x: 900, y: 0, width: 10, height: 10 }, 1, viewport)).toBeUndefined();
	});

	it("falls back to no zoom for a nonsense zoom factor", () => {
		expect(regionCaptureRect({ x: 1, y: 2, width: 3, height: 4 }, 0, viewport)).toEqual({ x: 1, y: 2, width: 3, height: 4 });
	});
});

describe("regionSelectFileName", () => {
	it("names the capture after the local time it was taken", () => {
		expect(regionSelectFileName(new Date(2026, 10, 20, 9, 5, 3))).toBe("browser-selection-2026-11-20-09-05-03.png");
	});
});
