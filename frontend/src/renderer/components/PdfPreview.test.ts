import { describe, expect, it } from "vitest";
import { anchoredPdfScrollTop, pdfOutputScale } from "./PdfPreview";

// Pages are 1000px tall at 100% and 2000px at 200%; gaps and padding stay 12px.
const before = Array.from({ length: 60 }, () => 1_000);
const after = Array.from({ length: 60 }, () => 2_000);

describe("anchoredPdfScrollTop", () => {
	it("keeps the point under the pinch on a later page, without scaling the gaps above it", () => {
		// 300px into page 51, under a cursor 400px down the pane.
		const point = 50 * (1_000 + 12) + 300;
		const scrollTop = point + 12 - 400;
		const next = anchoredPdfScrollTop(scrollTop, 400, before, after);
		// The same place, 600px into page 51, is under the cursor again.
		expect(next + 400 - 12).toBe(50 * (2_000 + 12) + 600);
	});

	it("keeps a point in the gap below a page at the gap's own size", () => {
		const next = anchoredPdfScrollTop(1_000 + 6 + 12 - 100, 100, before, after);
		expect(next + 100 - 12).toBe(2_000 + 6);
	});

	it("scales the first page's points by its own growth", () => {
		expect(anchoredPdfScrollTop(0, 512, before, after) + 512 - 12).toBe(1_000);
	});
});

describe("pdfOutputScale", () => {
	it("keeps the display's pixel ratio for an ordinary page", () => {
		expect(pdfOutputScale(600, 800, 2)).toBe(2);
	});

	it("caps a deeply zoomed page's bitmap, which CSS then stretches", () => {
		// A Letter page in a ~1200px pane at 500% on a 2x display.
		const width = 6_000;
		const height = 7_765;
		const scale = pdfOutputScale(width, height, 2);
		expect(scale).toBeLessThan(2);
		expect(width * scale * height * scale).toBeLessThanOrEqual(2 ** 25);
	});

	it("keeps a very tall page within the browser's longest canvas side", () => {
		const scale = pdfOutputScale(1_000, 20_000, 1);
		expect(20_000 * scale).toBeLessThanOrEqual(16_384);
	});
});
