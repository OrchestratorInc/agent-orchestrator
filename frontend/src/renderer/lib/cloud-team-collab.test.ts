import { afterEach, describe, expect, it } from "vitest";
import { CloudCpError } from "./cloud-cp/errors";
import type { CloudCpFeedItem } from "./cloud-cp/types";
import {
	asidePayload,
	awayHasNews,
	groupFeed,
	isPathQuery,
	mentionAskSteps,
	mentionSections,
	mentionToken,
	peopleWithoutAccess,
	probeTeamCollab,
	resetTeamCollabSupport,
	teamCollabSupport,
	teamsToConfirm,
	uniqueMentions,
	type ComposerMention,
} from "./cloud-team-collab";

afterEach(() => resetTeamCollabSupport());

describe("learning whether a control plane has asides, mentions and the feed", () => {
	const BASE = "https://cp.example.test/";

	it("records support from any answer", async () => {
		await expect(probeTeamCollab(BASE, async () => "page")).resolves.toBe("page");
		expect(teamCollabSupport("https://cp.example.test")).toBe("supported");
	});

	it.each([
		["a bare 404", new CloudCpError("Not found", { status: 404 })],
		["405", new CloudCpError("Method not allowed", { status: 405, code: "method_not_allowed" })],
		["501", new CloudCpError("Not implemented", { status: 501, code: "not_implemented" })],
	])("treats %s as a control plane without them", async (_name, error) => {
		await expect(probeTeamCollab(BASE, async () => { throw error; })).rejects.toBe(error);
		expect(teamCollabSupport(BASE)).toBe("unsupported");
	});

	it.each([
		["a 404 with a code (a missing session)", new CloudCpError("Not found", { status: 404, code: "SESSION_NOT_FOUND" })],
		["a 500", new CloudCpError("Boom", { status: 500, code: "internal" })],
		["a network failure", new TypeError("Failed to fetch")],
	])("stays undecided after %s", async (_name, error) => {
		await expect(probeTeamCollab(BASE, async () => { throw error; })).rejects.toBe(error);
		expect(teamCollabSupport(BASE)).toBe("unknown");
	});

	it("never records for a control plane without a URL", async () => {
		await probeTeamCollab("", async () => "page");
		expect(teamCollabSupport("")).toBe("unknown");
	});
});

describe("aside events", () => {
	it("reads an aside's note, author and mentions", () => {
		expect(asidePayload({
			type: "chat.aside",
			payload: { asideId: "a1", text: "Looks right", authorUserId: "u-chen", authorName: " Chen Wei ", mentions: [{ kind: "person", userId: "u-ben" }, { kind: "team", teamId: "t-1" }, { kind: "x" }] },
		})).toEqual({
			asideId: "a1", text: "Looks right", authorUserId: "u-chen", authorName: "Chen Wei",
			mentions: [{ kind: "person", userId: "u-ben" }, { kind: "team", teamId: "t-1" }],
		});
	});

	it("ignores other events and malformed asides", () => {
		expect(asidePayload({ type: "chat.user_message", payload: { asideId: "a1", text: "hi" } })).toBeUndefined();
		expect(asidePayload({ type: "chat.aside", payload: { text: "no id" } })).toBeUndefined();
		expect(asidePayload({ type: "chat.aside", payload: { asideId: "a1", text: "   " } })).toBeUndefined();
		expect(asidePayload({ type: "chat.aside", payload: null })).toBeUndefined();
	});
});

describe("mentions", () => {
	const people = [
		{ userId: "u1", name: "Chen Wei", inSession: true, canOpen: true },
		{ userId: "u2", name: "Cara Diaz", inSession: false, canOpen: false },
		{ userId: "me", name: "Asha Rao", inSession: true, canOpen: true },
		...Array.from({ length: 6 }, (_, index) => ({ userId: `s${index}`, name: `Sam ${index}`, inSession: true, canOpen: true })),
	];
	const teams = [{ teamId: "t1", name: "Checkout", memberCount: 3, membersWithoutAccess: 0 }];

	it("treats a query with a slash or a dot as a path", () => {
		expect(isPathQuery("src/auth/")).toBe(true);
		expect(isPathQuery("README.md")).toBe(true);
		expect(isPathQuery("scope/pkg")).toBe(true);
		expect(isPathQuery("chen")).toBe(false);
		expect(isPathQuery("")).toBe(false);
	});

	it("splits people into the session (at most five) and the workspace, without the caller", () => {
		const sections = mentionSections({ people, teams }, "", "me");
		expect(sections.inSession.map((person) => person.userId)).toEqual(["u1", "s0", "s1", "s2", "s3"]);
		expect(sections.workspace.map((person) => person.userId)).toEqual(["u2"]);
		expect(sections.teams.map((team) => team.teamId)).toEqual(["t1"]);
	});

	it("ranks by name and drops what doesn't match", () => {
		const sections = mentionSections({ people, teams }, "di", "me");
		expect(sections.workspace.map((person) => person.name)).toEqual(["Cara Diaz"]);
		expect(sections.inSession).toEqual([]);
		expect(sections.teams).toEqual([]);
		expect(mentionSections({ people, teams }, "ch").teams.map((team) => team.name)).toEqual(["Checkout"]);
	});

	it("offers nobody for a path query or before candidates arrive", () => {
		expect(mentionSections({ people, teams }, "src/")).toEqual({ inSession: [], teams: [], workspace: [] });
		expect(mentionSections(undefined, "ch")).toEqual({ inSession: [], teams: [], workspace: [] });
	});

	it("asks about teams of more than five, then about people who can't open the session", () => {
		const mentions: ComposerMention[] = [
			{ kind: "person", id: "u2", name: "Cara Diaz", canOpen: false },
			{ kind: "team", id: "t-small", name: "Checkout", memberCount: 5 },
			{ kind: "team", id: "t-big", name: "Platform", memberCount: 6, membersWithoutAccess: 2 },
			{ kind: "person", id: "u1", name: "Chen Wei", canOpen: true },
		];
		expect(teamsToConfirm(mentions).map((team) => team.id)).toEqual(["t-big"]);
		expect(peopleWithoutAccess(mentions).map((person) => person.id)).toEqual(["u2"]);
		expect(mentionAskSteps(mentions)).toEqual([
			{ kind: "team", team: mentions[2] },
			{ kind: "noAccess", people: [mentions[0]] },
		]);
		expect(mentionAskSteps([mentions[1]!, mentions[3]!])).toEqual([]);
	});

	it("sends each person or team once, as typed tokens", () => {
		const twice: ComposerMention[] = [
			{ kind: "person", id: "u1", name: "Chen Wei" },
			{ kind: "team", id: "u1", name: "Same id, other kind" },
			{ kind: "person", id: "u1", name: "Chen Wei" },
		];
		expect(uniqueMentions(twice).map(mentionToken)).toEqual([{ kind: "person", userId: "u1" }, { kind: "team", teamId: "u1" }]);
	});
});

describe("the team feed", () => {
	const row = (sessionId: string, status: CloudCpFeedItem["status"]): CloudCpFeedItem => ({
		orgId: "org", orgName: "Northwind", projectId: "p", projectName: "storefront", sessionId, title: sessionId,
		owner: { userId: "u", name: "Asha Rao" }, status, line: "", people: [], following: false, updatedAt: "2026-11-07T10:00:00Z",
	});

	it("groups needs, working, then everything else, each in the server's order", () => {
		const groups = groupFeed([row("a", "review"), row("b", "needs"), row("c", "working"), row("d", "idle"), row("e", "needs"), row("f", "merged"), row("g", "failed"), row("h", "ready")]);
		expect(groups.map((group) => [group.id, group.items.map((item) => item.sessionId)])).toEqual([
			["needs", ["b", "e"]],
			["working", ["c"]],
			["recent", ["a", "d", "f", "g", "h"]],
		]);
	});

	it("leaves out empty groups and shows a session once across pages", () => {
		expect(groupFeed([])).toEqual([]);
		expect(groupFeed([row("a", "idle"), row("a", "working")]).map((group) => group.id)).toEqual(["recent"]);
	});

	it("shows While you were away only when something changed", () => {
		const nothing = { since: "", needsYou: 0, prsMerged: [], failed: [], started: { count: 0, names: [] } };
		expect(awayHasNews(undefined)).toBe(false);
		expect(awayHasNews(nothing)).toBe(false);
		expect(awayHasNews({ ...nothing, needsYou: 1 })).toBe(true);
		expect(awayHasNews({ ...nothing, prsMerged: [{ number: 1, url: "", sessionId: "s" }] })).toBe(true);
		expect(awayHasNews({ ...nothing, failed: [{ sessionId: "s", title: "t" }] })).toBe(true);
		expect(awayHasNews({ ...nothing, started: { count: 2, names: ["Ben"] } })).toBe(true);
	});
});
