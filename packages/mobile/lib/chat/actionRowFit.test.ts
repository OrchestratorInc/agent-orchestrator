import { describe, expect, it } from "vitest";
import { fairWidths } from "./actionRowFit";

describe("fairWidths", () => {
	it("leaves the decisions their own widths while they fit", () => {
		expect(fairWidths([80, 120, 130], 360)).toBeUndefined();
		expect(fairWidths([80, 120, 130], 330)).toBeUndefined();
	});

	// A long first label must not take the width the later ones need, the primary
	// among them: every decision keeps at least an equal share of a short row.
	it("gives every decision at least an equal share once they don't fit", () => {
		const widths = fairWidths([400, 120, 130], 300);
		expect(widths).toEqual([100, 100, 100]);
	});

	// Shares a short label doesn't need go to the longer ones.
	it("lets short decisions keep their own width and splits the rest", () => {
		expect(fairWidths([60, 200, 200], 300)).toEqual([60, 120, 120]);
		expect(fairWidths([60, 90, 300], 300)).toEqual([60, 90, 150]);
	});

	it("never hands out more than the row", () => {
		const widths = fairWidths([101.4, 202.7, 303.3], 250.5) ?? [];
		expect(widths.reduce((sum, width) => sum + width, 0)).toBeLessThanOrEqual(250.5);
		expect(Math.min(...widths)).toBeGreaterThan(80);
	});

	it("waits for every width before deciding", () => {
		expect(fairWidths([80, undefined, 130], 200)).toBeUndefined();
		expect(fairWidths([80, 120], 0)).toBeUndefined();
	});
});
