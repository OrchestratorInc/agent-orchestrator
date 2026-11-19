import { describe, expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "../../../lib/session-import-agents";

describe("Memcode native session import", () => {
  it("offers Memcode in the import gate", () => {
    expect(IMPORTABLE_AGENTS.has("memcode")).toBe(true);
  });
});
