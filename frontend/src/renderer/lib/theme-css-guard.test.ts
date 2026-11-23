import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { themeCssLoadsFile } from "./theme-css-guard";

// The daemon's scan is held to the same file (apptheme TestCSSGuardVectors):
// a stylesheet one side refuses must not be one the other applies.
const vectors = JSON.parse(
	readFileSync(resolve(__dirname, "../../../../backend/internal/service/apptheme/testdata/css_guard_vectors.json"), "utf8"),
) as { refuse: string[]; accept: string[] };

describe("themeCssLoadsFile", () => {
	it("has the full set of vectors", () => {
		expect(vectors.refuse.length).toBeGreaterThanOrEqual(30);
		expect(vectors.accept.length).toBeGreaterThanOrEqual(15);
	});

	it.each(vectors.refuse)("refuses %j", (css) => {
		expect(themeCssLoadsFile(css)).not.toBeNull();
	});

	it.each(vectors.accept)("accepts %j", (css) => {
		expect(themeCssLoadsFile(css)).toBeNull();
	});

	it("names what would be loaded", () => {
		expect(themeCssLoadsFile("a{background:u\\72l(https://evil.example/a.png)}")).toBe("url(https://evil.example/a.png)");
		expect(themeCssLoadsFile("@\\69mport 'a.css';")).toBe("@import");
		expect(themeCssLoadsFile('a{background:image-set("a.png" 1x)}')).toBe("image-set()");
	});

	it("ends on broken input", () => {
		for (const css of ["\\", "url(", 'url("', "@", "#", "-", "--", "/*", '"\\', "a{b:url(data:x", "\\\n", "@import", "u\\", "\ud83d(", "\\😀("]) {
			expect(() => themeCssLoadsFile(css)).not.toThrow();
		}
	});
});
