import { expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "./SessionImportPanel";

it("offers Crush terminal histories for import", () => {
	expect(IMPORTABLE_AGENTS.has("crush")).toBe(true);
});
