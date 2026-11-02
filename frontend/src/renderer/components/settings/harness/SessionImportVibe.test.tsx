import { expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "./SessionImportPanel";

it("offers Vibe terminal histories for import", () => {
	expect(IMPORTABLE_AGENTS.has("vibe")).toBe(true);
});
