import { expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "../../../lib/session-import-agents";

it("offers OpenHands terminal histories for import", () => {
	expect(IMPORTABLE_AGENTS.has("openhands")).toBe(true);
});
