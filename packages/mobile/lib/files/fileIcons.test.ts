import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
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
	// Material Symbol's drawable under its own name, required by that name.
	it.each(names)("bundles %s as its Material Symbol drawable", (name) => {
		const android = fileIconSymbols[name].android;
		const file = fileURLToPath(new URL(`../../assets/icons/${android}.xml`, import.meta.url));
		expect(existsSync(file)).toBe(true);
		expect(readFileSync(file, "utf8")).toContain('android:viewportWidth="960"');
		expect(source).toContain(`${name}: require("../../assets/icons/${android}.xml")`);
	});
});
