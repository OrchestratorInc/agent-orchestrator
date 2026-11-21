import { describe, expect, it } from "vitest";
import { CloudCpError } from "./cloud-cp/errors";
import { isPathQuery, mentionRefusal, mentionSections, mentionToken, parseEventMentions, splitByMentions, uniqueMentions } from "./cloud-mentions";

describe("mention tokens", () => {
	it("sends a person by userId and a team by teamId, never a name", () => {
		expect(mentionToken({ kind: "person", id: "u-1" })).toEqual({ kind: "person", userId: "u-1" });
		expect(mentionToken({ kind: "team", id: "t-1" })).toEqual({ kind: "team", teamId: "t-1" });
	});

	it("keeps each person or team once, in the order first named", () => {
		expect(uniqueMentions([
			{ kind: "person", id: "a", name: "A" },
			{ kind: "team", id: "a", name: "Team A" },
			{ kind: "person", id: "a", name: "A" },
		])).toEqual([{ kind: "person", id: "a", name: "A" }, { kind: "team", id: "a", name: "Team A" }]);
	});
});

describe("splitByMentions", () => {
	const chen = { kind: "person" as const, id: "u-chen", name: "Chen Wei" };
	const chenShort = { kind: "person" as const, id: "u-c", name: "Chen" };

	it("picks out each @Name at a word start, longest name first", () => {
		expect(splitByMentions("ask @Chen Wei and @Chen now", [chenShort, chen])).toEqual(["ask ", chen, " and ", chenShort, " now"]);
	});

	it("leaves an email address and a name inside a longer word alone", () => {
		expect(splitByMentions("mail ana@Chen or @Chenna", [chenShort])).toEqual(["mail ana@Chen or @Chenna"]);
	});
});

describe("parseEventMentions", () => {
	it("reads {kind, id, name} as the event records them and drops anything else", () => {
		expect(parseEventMentions([
			{ kind: "person", id: "u-1", name: "Priya Shah" },
			{ kind: "team", id: "t-1", name: "Platform" },
			{ kind: "person", id: "u-2" },
			{ kind: "robot", id: "x", name: "X" },
			"junk",
		])).toEqual([
			{ kind: "person", id: "u-1", name: "Priya Shah" },
			{ kind: "team", id: "t-1", name: "Platform" },
		]);
		expect(parseEventMentions(undefined)).toEqual([]);
	});
});

describe("mentionRefusal", () => {
	it("names the reason of a 422 on the mentions field", () => {
		const refused = new CloudCpError("Mention not allowed.", { status: 422, code: "validation_error", details: { field: "mentions", reason: "no_access" } });
		expect(mentionRefusal(refused)).toBe("no_access");
	});

	it("is undefined for any other validation error or failure", () => {
		expect(mentionRefusal(new CloudCpError("Bad text.", { status: 422, code: "validation_error", details: { field: "text" } }))).toBeUndefined();
		expect(mentionRefusal(new CloudCpError("Down.", { status: 503 }))).toBeUndefined();
	});
});

describe("mentionSections", () => {
	const candidates = {
		people: [
			{ userId: "u-ben", name: "Ben Ortiz", present: false, inSession: true },
			{ userId: "u-chen", name: "Chen Wei", present: true, inSession: true },
			{ userId: "u-me", name: "Me", present: true, inSession: true },
			{ userId: "u-cara", name: "Cara Diaz", present: false, inSession: false },
		],
		teams: [{ teamId: "t-platform", name: "Platform" }],
	};

	it("puts the people here now first, then the rest in the session, teams, and the workspace", () => {
		const sections = mentionSections(candidates, "", "u-me");
		expect(sections.inSession.map((person) => person.userId)).toEqual(["u-chen", "u-ben"]);
		expect(sections.teams.map((team) => team.teamId)).toEqual(["t-platform"]);
		expect(sections.workspace.map((person) => person.userId)).toEqual(["u-cara"]);
	});

	it("offers nothing but files for a path", () => {
		expect(isPathQuery("src/app")).toBe(true);
		expect(mentionSections(candidates, "src/app", "u-me")).toEqual({ inSession: [], teams: [], workspace: [] });
	});
});
