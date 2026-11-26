import { describe, expect, it } from "vitest";
import { mergeChatSkills } from "./useAgentSkillsQuery";

const skill = (name: string, source = "agent") => ({ name, displayName: name, source });

describe("mergeChatSkills", () => {
	it("uses the scanned list when no session has reported one", () => {
		const scanned = [skill("review"), skill("ship")];
		expect(mergeChatSkills([], scanned)).toBe(scanned);
	});

	it("keeps the reported list first and appends skills found on disk since", () => {
		const reported = [skill("compact"), skill("review")];
		expect(mergeChatSkills(reported, [skill("review"), skill("ship")]).map((entry) => entry.name)).toEqual(["compact", "review", "ship"]);
	});

	it("returns the reported list itself when the scan adds nothing", () => {
		const reported = [skill("review")];
		expect(mergeChatSkills(reported, [skill("review")])).toBe(reported);
		expect(mergeChatSkills(reported, [])).toBe(reported);
	});
});
