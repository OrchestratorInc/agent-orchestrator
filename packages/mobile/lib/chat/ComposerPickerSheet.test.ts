import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { sheetInset } from "../tokens";
import { composerSheetContentStyle } from "./chatLayout";

const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

describe("composer picker native sheet layout", () => {
	it("uses the same safe top inset as the project picker", () => {
		// Compare against the shared sheet inset rather than a literal, so the two
		// sheets cannot drift apart the next time the ladder moves.
		expect(composerSheetContentStyle).toMatchObject({
			paddingHorizontal: sheetInset.horizontal,
			paddingTop: sheetInset.top,
			paddingBottom: sheetInset.bottom,
		});
	});

	// iOS titles the sheet and searches in its native bar, which the route shows
	// from the start; Android keeps the title in the body.
	it("titles the sheet in the native bar on iOS", () => {
		const sheet = source("./ComposerPickerSheet.tsx");
		expect(sheet).toContain("<Stack.Screen options={{ title }} />");
		expect(sheet).toContain("title={ios ? undefined : title}");
		const layout = source("../../app/_layout.tsx");
		expect(layout).toMatch(/name="sheets\/composer-picker"[\s\S]*?headerShown: Platform\.OS === "ios"/);
	});

	it("answers a press the platform's way and divides rows that always draw", () => {
		const sheet = source("./ComposerPickerSheet.tsx");
		expect(sheet).toContain('const feedback = usePressFeedback("row");');
		expect(sheet).toContain("height: rowDividerWidth");
		expect(sheet).not.toContain("hairlineWidth");
		expect(sheet).not.toContain("../icons");
	});
});
