import { describe, expect, it } from "vitest";
import { parseTeamsFlag } from "./teams-flag";

describe("VITE_AO_TEAMS", () => {
	it("is unset when absent or blank", () => {
		expect(parseTeamsFlag(undefined)).toBe("");
		expect(parseTeamsFlag("")).toBe("");
		expect(parseTeamsFlag("  ")).toBe("");
	});

	it("reads on and off in any case, around spaces", () => {
		expect(parseTeamsFlag("on")).toBe("on");
		expect(parseTeamsFlag(" ON ")).toBe("on");
		expect(parseTeamsFlag("off")).toBe("off");
		expect(parseTeamsFlag("Off")).toBe("off");
	});

	it("stops the build on anything else rather than guess", () => {
		for (const raw of ["true", "1", "yes", "nightly", "of", "on,off"]) {
			expect(() => parseTeamsFlag(raw)).toThrow(/VITE_AO_TEAMS must be "on" or "off"/);
		}
	});
});
