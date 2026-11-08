import { describe, expect, it } from "vitest";
import { isNewSidebarDesign } from "./new-sidebar-design";

describe("isNewSidebarDesign", () => {
	it("is on by default and for a state that does not know the flag", () => {
		expect(isNewSidebarDesign({})).toBe(true);
		expect(isNewSidebarDesign({ developerMode: true })).toBe(true);
	});

	it("turns off only when switched off in Developer mode", () => {
		expect(isNewSidebarDesign({ developerMode: true, newSidebarDesign: false })).toBe(false);
		expect(isNewSidebarDesign({ developerMode: false, newSidebarDesign: false })).toBe(true);
	});
});
