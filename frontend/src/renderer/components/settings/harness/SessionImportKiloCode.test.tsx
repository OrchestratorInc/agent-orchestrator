import { expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "../../../lib/session-import-agents";

it("offers the approved Kilo native import route", () => {
  expect(IMPORTABLE_AGENTS.has("kilocode")).toBe(true);
});
