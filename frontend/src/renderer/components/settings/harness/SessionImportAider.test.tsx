import { describe, expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "./SessionImportPanel";

describe("Aider native session import", () => {
  it("offers Aider in the import gate", () => {
    expect(IMPORTABLE_AGENTS.has("aider")).toBe(true);
  });
});
