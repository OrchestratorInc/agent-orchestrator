import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");
const page = source("../app/onboarding.tsx");

describe("onboarding screen", () => {
	it("pins the primary action to the bottom safe area, outside the scrolling copy", () => {
		const scrollEnd = page.indexOf("</ScrollView>");
		const cta = page.indexOf('title="Pair a machine"');
		expect(scrollEnd).toBeGreaterThan(0);
		expect(cta).toBeGreaterThan(scrollEnd);
		expect(page).toContain("{ paddingBottom: insets.bottom + space.lg }");
	});

	// The shared Button gives Skip a touch target and the platform's pressed
	// state; the bare Pressable it replaced had neither.
	it("skips with a text button", () => {
		expect(page).toContain('<Button variant="ghost" title="Skip" onPress={() => void skip()} style={styles.skip} />');
		expect(page).not.toMatch(/<Pressable\b|haptics\./);
	});

	it("labels the steps in sentence case, as a header", () => {
		expect(page).toContain('<Text accessibilityRole="header" style={styles.howLabel}>How it works</Text>');
		expect(page).not.toMatch(/HOW IT WORKS|letterSpacing: 1\.3/);
	});

	// NumberedStep owns where its title starts, so the dividers follow its badge
	// and gap instead of a copy of them that could drift.
	it("starts the step dividers under the step titles, on the shared divider width", () => {
		expect(page).toContain('import { Button, MASCOT_ASPECT, NumberedStep, STEP_TITLE_INSET } from "../lib/ui";');
		expect(page).toContain("divider: { height: rowDividerWidth, backgroundColor: t.borderSubtle, marginLeft: STEP_TITLE_INSET }");
		expect(page).not.toMatch(/STEP_TEXT_INSET|STEP_BADGE|fontScaleCap|height: 1\b|marginLeft: 43/);
		const ui = source("./ui.tsx");
		expect(ui).toContain("export const STEP_TITLE_INSET = STEP_BADGE + STEP_GAP;");
		expect(ui).toMatch(/step: \{\s*flexDirection: "row",\s*alignItems: "flex-start",\s*gap: STEP_GAP,/);
		expect(ui).toContain("minWidth: STEP_BADGE,");
	});

	it("describes where paired work shows up in the tabbed app", () => {
		expect(page).toContain("Your workers appear on the Workers tab");
		expect(page).not.toMatch(/drawer|sidebar|menu/i);
	});
});
