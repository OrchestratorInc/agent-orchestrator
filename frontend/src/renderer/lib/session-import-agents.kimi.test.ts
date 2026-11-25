import { expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "./session-import-agents";

it("offers Kimi as a continuable native import", () => {
 expect(IMPORTABLE_AGENTS.has("kimi")).toBe(true);
});
