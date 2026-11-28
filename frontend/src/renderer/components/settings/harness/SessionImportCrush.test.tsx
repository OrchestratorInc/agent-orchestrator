import { expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "../../../lib/session-import-agents";

it("offers Crush terminal histories for import", () => {
	expect(IMPORTABLE_AGENTS.has("crush")).toBe(true);
});
