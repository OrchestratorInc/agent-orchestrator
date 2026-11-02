import { expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "./SessionImportPanel";

it("offers Kilo Code terminal histories for import", () => {
	expect(IMPORTABLE_AGENTS.has("kilocode")).toBe(true);
});
