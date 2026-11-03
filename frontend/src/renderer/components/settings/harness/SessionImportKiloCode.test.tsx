import { expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "./SessionImportPanel";

it("keeps Kilo import hidden until native export preserves the source", () => {
  expect(IMPORTABLE_AGENTS.has("kilocode")).toBe(false);
});
