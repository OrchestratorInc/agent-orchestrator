import { describe, expect, it } from "vitest";
import { AUTOMATION_TEMPLATES } from "./automation-templates";
import { buildRRuleFromSchedule, scheduleFieldsFromRRule } from "./automation-schedule";

describe("bundled automation starters", () => {
	it.each(AUTOMATION_TEMPLATES)("keeps $id editable, bounded and schedulable", (template) => {
		expect(new TextEncoder().encode(template.prompt).length).toBeLessThanOrEqual(4096);
		expect(buildRRuleFromSchedule(scheduleFieldsFromRRule(template.rrule))).toBe(template.rrule);
		expect(template.prompt).toContain("Read only:");
		expect(template.prompt).toContain("If GitHub access");
	});
});
