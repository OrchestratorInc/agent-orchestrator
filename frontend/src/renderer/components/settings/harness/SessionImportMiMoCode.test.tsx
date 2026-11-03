import { expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "./SessionImportPanel";

it("keeps MiMo import hidden until native export preserves the source", () => {
  expect(IMPORTABLE_AGENTS.has("mimo-code")).toBe(false);
});
