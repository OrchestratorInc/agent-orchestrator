import { describe, expect, it } from "vitest";
import { projectHue, projectInitials } from "./projectAvatar";

describe("project avatar", () => {
	it("takes the first letter of each of the first two words", () => {
		expect(projectInitials("agent-orchestrator")).toBe("AO");
		expect(projectInitials("ao_landing site")).toBe("AL");
		expect(projectInitials("landing")).toBe("L");
		expect(projectInitials("  ")).toBe("?");
	});

	it("gives a name the same hue every time, and spreads names across hues", () => {
		expect(projectHue("landing")).toBe(projectHue("landing"));
		const hues = new Set(["ao-landing", "landing", "agent-orchestrator", "meetyou", "demo-app", "second-app"].map(projectHue));
		expect(hues.size).toBeGreaterThan(2);
	});
});
