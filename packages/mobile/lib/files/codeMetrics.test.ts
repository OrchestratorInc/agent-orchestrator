import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
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

describe("the diff rows", () => {
	const components = readFileSync(fileURLToPath(new URL("./components.tsx", import.meta.url)), "utf8");

	it("draws the selection as an overlay, its knobs inside the row", () => {
		expect(components).not.toContain("borderLeftWidth");
		expect(components).toContain('{selected ? <View pointerEvents="none" style={styles.selectionBar} /> : null}');
		expect(components).not.toMatch(/knob\w*: \{[^}]*(left|top|bottom): -/);
	});

	it("scales the gutter and the marker with the code, so numbers stay on their lines", () => {
		expect(components.match(/maxFontSizeMultiplier=\{CODE_MAX_SCALE\}/g)).toHaveLength(3);
		expect(components).toContain("gutterWidth(digits, scale)");
	});

	it("gives a fold the touch target and no haptic", () => {
		expect(components).toContain("fold: { minHeight: touchTarget,");
		const fold = components.slice(components.indexOf("export const FoldRow"), components.indexOf("// ---- Selection bar"));
		expect(fold).not.toContain("haptics");
	});

	it("draws the send glyph in the accent fill's own ink", () => {
		expect(components).not.toContain("color={t.bgBase}");
		expect(components).toContain('<AppSymbol name="send" size={iconSize.md} color={t.onAccent}');
	});
});
