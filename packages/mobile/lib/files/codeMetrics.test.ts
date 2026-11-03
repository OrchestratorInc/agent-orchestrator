import { describe, expect, it } from "vitest";
import { CODE_MAX_SCALE, codeContentWidth, codeScale, gutterWidth } from "./codeMetrics";
import type { DisplayRow } from "./model";

const line = (text: string): DisplayRow => ({ type: "line", key: text, row: { kind: "context", text, oldNo: 1, newNo: 1 } } as DisplayRow);

describe("code metrics", () => {
	it("follows the reader's text size up to the code's cap", () => {
		expect(codeScale(0.85)).toBe(0.85);
		expect(codeScale(1.2)).toBe(1.2);
		expect(codeScale(2.5)).toBe(CODE_MAX_SCALE);
		expect(codeScale(0)).toBe(1);
	});

	// Measured unscaled, a long line at a large text size ran out of room and
	// ended in an ellipsis instead of scrolling sideways.
	it("measures an unwrapped listing at the size it is drawn", () => {
		const rows = [line("short"), line("x".repeat(120))];
		const plain = codeContentWidth(rows, 3, 1);
		const scaled = codeContentWidth(rows, 3, 1.4);
		expect(scaled).toBeGreaterThan(plain * 1.3);
		expect(gutterWidth(3, 1.4)).toBeGreaterThan(gutterWidth(3, 1));
	});
});
