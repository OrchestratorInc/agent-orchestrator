import { expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "./SessionImportPanel";

it("offers MiMo Code terminal histories for import", () => {
	expect(IMPORTABLE_AGENTS.has("mimo-code")).toBe(true);
});
