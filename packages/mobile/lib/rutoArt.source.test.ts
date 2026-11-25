import { readFileSync } from "node:fs";
import { expect, it } from "vitest";
import { RUTO_BATON, RUTO_BODY, RUTO_DEFS, RUTO_OUTLINE } from "./rutoArt";

it("draws Ruto exactly as the brand source does", () => {
	// The brand script writes assets/brand/ruto.svg as <svg>, the shared defs,
	// then the baton and body groups. The mobile copy must match it byte for
	// byte, so a redrawn mark reaches the update screen too.
	const brand = readFileSync(new URL("../../../assets/brand/ruto.svg", import.meta.url), "utf8").trim();
	const open = brand.slice(0, brand.indexOf(">") + 1);
	expect(open).toMatch(/^<svg [^>]*viewBox="0 0 1024 1024"/);
	expect(brand).toBe(`${open}${RUTO_DEFS}${RUTO_BATON}${RUTO_BODY}</svg>`);
});

it("exposes Ruto's outline for clipping", () => {
	expect(RUTO_OUTLINE).toMatch(/^M295 352L/);
	expect(RUTO_DEFS).toContain(`d="${RUTO_OUTLINE}"`);
});
