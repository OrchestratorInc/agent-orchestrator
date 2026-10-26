import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = readFileSync(new URL("./elicitation-native-controls.ios.tsx", import.meta.url), "utf8");

describe("iOS elicitation controls", () => {
	it("keeps choices visually subordinate to the question, at the reader's text size", () => {
		expect(source).toContain('font({ textStyle: "subheadline", weight: "semibold" })');
		expect(source).toContain('font({ textStyle: "footnote" })');
		expect(source).not.toMatch(/font\(\{ size:/);
		expect(source).toContain("size={iconSize.md}");
		expect(source).toContain("minHeight: 56");
	});

	it("announces the chosen answer as selected rather than by its glyph", () => {
		expect(source).toContain('...(checked ? [accessibilityAddTraits(["isSelected"])] : [])');
		expect(source).toContain("accessibilityHidden(true)");
	});

	it("uses the same native glass material as the worker controls", () => {
		// The recipe module is the single place the glass material, shape and
		// interactivity are decided, so this asserts the shared call and not a
		// literal that every control would otherwise have to repeat.
		expect(source).toContain("glassPanel()");
		expect(source).not.toContain("glassEffect(");
		expect(source).not.toContain("background(t.bgSubtle)");
	});

	it("grows the answer field with its text instead of clipping it", () => {
		expect(source).toContain('<Host matchContents={{ vertical: true }} style={{ width: "100%" }} colorScheme={scheme}>\n\t\t<TextField');
		expect(source).toContain('frame({ maxWidth: 1000, minHeight: touchTarget, alignment: "leading" })');
		expect(source).toContain('font({ textStyle: "callout" })');
	});

	// A width guessed from the label's length cut long decisions short. The
	// button sizes itself, and a caller's width only reserves a slot.
	it("sizes each action to its label", () => {
		expect(source).toContain("<Host matchContents colorScheme={scheme}>\n\t\t<Button");
		expect(source).toContain('controlSize("regular")');
		expect(source).toContain("...(width ? [frame({ minWidth: width })] : [])");
		expect(source).not.toContain("resolvedWidth");
	});
});
