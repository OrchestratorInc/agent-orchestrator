import { expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "./SessionImportPanel";

it("offers local FX history for Terminal import", () => {
	expect(IMPORTABLE_AGENTS.has("fx")).toBe(true);
});
