import { describe, expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "../../../lib/session-import-agents";

describe("Autohand session import", () => {
	it("offers the Terminal harness", () => {
		expect(IMPORTABLE_AGENTS.has("autohand")).toBe(true);
	});
});
