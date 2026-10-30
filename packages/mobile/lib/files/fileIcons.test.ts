import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { materialDrawableNames } from "../toolbarIcon.types";
import { fileIcon } from "./fileIcons";
import { fileIconSymbols, type FileIconName } from "./fileIcons.types";

const source = readFileSync(fileURLToPath(new URL("./fileIcons.android.ts", import.meta.url)), "utf8");
const names = Object.keys(fileIconSymbols) as FileIconName[];

describe("Files glyphs", () => {
	it("names the SF Symbol on iOS", () => {
		expect(fileIcon("previous")).toBe("chevron.up");
		expect(fileIcon("share")).toBe("square.and.arrow.up");
	});

	// Android's native items draw only an image source, so each glyph needs the
	// Material Symbol's drawable in the shared registry (lib/toolbarIcon.test.ts
	// checks its files).
	it.each(names)("draws %s from the shared drawable registry on Android", (name) => {
		expect(materialDrawableNames).toContain(fileIconSymbols[name].android);
	});

	it("resolves every Android glyph through materialDrawable", () => {
		expect(source).toContain("materialDrawable(fileIconSymbols[name].android)");
		expect(source).not.toContain("require(");
	});
});
