import { describe, expect, it } from "vitest";
import { IMPORTABLE_AGENTS } from "./SessionImportPanel";

describe("Cline session import", () => {
	it("offers the Terminal harness", () => {
		expect(IMPORTABLE_AGENTS.has("cline")).toBe(true);
	});
});
