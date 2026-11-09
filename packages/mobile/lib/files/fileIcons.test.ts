import { describe, expect, it } from "vitest";
import { materialDrawableNames } from "../toolbarIcon.types";
import { fileIconSymbols, type FileIconName } from "./fileIcons.types";

const names = Object.keys(fileIconSymbols) as FileIconName[];

describe("Files glyphs", () => {
	// Android's native items draw only an image source, so each glyph needs the
	// Material Symbol's drawable in the shared registry (lib/toolbarIcon.test.ts
	// checks its files).
	it.each(names)("draws %s from the shared drawable registry on Android", (name) => {
		expect(materialDrawableNames).toContain(fileIconSymbols[name].android);
	});
});
