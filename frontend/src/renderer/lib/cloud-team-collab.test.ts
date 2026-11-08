import { afterEach, describe, expect, it } from "vitest";
import { CloudCpError } from "./cloud-cp/errors";
import type { CloudCpFeedItem, CloudCpSuggestion } from "./cloud-cp/types";
import {
	applySuggestionEvents,
	asidePayload,
	awayHasNews,
	composerLockedError,
	groupFeed,
	composerMentionsOf,
	mergeFeedHead,
	mergeSuggestionRead,
	sentAsides,
	suggestionMentions,
	type FeedPages,
	isPathQuery,
	MAX_MENTIONS,
	mentionAskSteps,
	mentionNotAllowedError,
	namedMentions,
	parseMentions,
	splitByMentions,
	tooManyMentionedError,
	mentionSections,
	mentionToken,
	peopleWithoutAccess,
	probeTeamCollab,
	resetTeamCollabSupport,
	suggestionFromEvent,
	suggestionResolvedError,
	teamCollabSupport,
	teamsToConfirm,
	uniqueMentions,
	withSuggestion,
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
	it("reads an aside's note, author and mentions with the names they had", () => {
		expect(asidePayload({
			type: "chat.aside",
			payload: { asideId: "a1", text: "Looks right", authorUserId: "u-chen", authorName: " Chen Wei ", mentions: [{ kind: "person", userId: "u-ben", name: "Ben Ortiz" }, { kind: "team", teamId: "t-1" }, { kind: "x" }] },
		})).toEqual({
			asideId: "a1", text: "Looks right", authorUserId: "u-chen", authorName: "Chen Wei",
			mentions: [{ kind: "person", userId: "u-ben", name: "Ben Ortiz" }, { kind: "team", teamId: "t-1" }],
		});
	});

	it("gives an aside from before names were recorded an empty author name", () => {
		expect(asidePayload({ type: "chat.aside", payload: { asideId: "a1", text: "Old", authorUserId: "u" } })).toEqual({
			asideId: "a1", text: "Old", authorUserId: "u", authorName: "", mentions: [],
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
	const teams = [{ teamId: "t1", name: "Checkout", memberCount: 3, membersWithoutAccess: 0, namesWithoutAccess: [] }];

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
		expect(awayHasNews({ ...nothing, prsMerged: [{ number: 1, url: "", sessionId: "s", orgId: "o", projectId: "p" }] })).toBe(true);
		expect(awayHasNews({ ...nothing, failed: [{ sessionId: "s", orgId: "o", projectId: "p", title: "t" }] })).toBe(true);
		expect(awayHasNews({ ...nothing, started: { count: 2, names: ["Ben"] } })).toBe(true);
	});
});

describe("suggestion reads and events", () => {
	const made = (id: string, status: CloudCpSuggestion["status"] = "pending", asideId = `aside-${id}`): CloudCpSuggestion => ({
		id, orgId: "o", sessionId: "s", kind: "aside", asideId, text: id, mentions: [], status, createdAt: `2026-11-07T10:0${id.length}:00Z`, updatedAt: "2026-11-07T10:00:00Z",
		suggestedBy: { userId: "u", name: "U" }, author: { userId: "u", name: "U" },
	});

	it("never lets a read asked before an event undo it", () => {
		const news = new Map([
			["sg-1", { heardAt: 20, resolved: true }],
			["sg-3", { heardAt: 20, resolved: false }],
		]);
		// Asked at 10: sg-1 was resolved and sg-3 made after the read was asked.
		expect(mergeSuggestionRead([made("sg-2"), made("sg-3")], [made("sg-1"), made("sg-2")], news, 10).map((item) => item.id)).toEqual(["sg-2", "sg-3"]);
	});

	it("drops one the window knew before asking that the read no longer lists", () => {
		const news = new Map([["sg-2", { heardAt: 5, resolved: false }]]);
		expect(mergeSuggestionRead([made("sg-2")], [], news, 10)).toEqual([]);
		expect(mergeSuggestionRead([made("sg-2")], [], undefined, 10)).toEqual([]);
	});

	it("says which asides were last sent to the agent", () => {
		const event = (status: CloudCpSuggestion["status"], asideId: string) => ({ type: "chat.suggestion_resolved", payload: made(`sg-${asideId}-${status}`, status, asideId) });
		expect([...sentAsides([event("sent", "a1"), event("dismissed", "a2"), event("sent", "a3"), { type: "chat.suggestion_created", payload: made("sg-again", "pending", "a3") }])]).toEqual(["a1"]);
	});

	it("reads the mentions a suggestion carries, and says when a control plane sent none", () => {
		const ben = { kind: "person" as const, userId: "u-ben", name: "Ben Ortiz" };
		expect(suggestionMentions({ ...made("sg-1"), mentions: [ben] })).toEqual([ben]);
		expect(suggestionMentions({ ...made("sg-2"), mentions: [] })).toEqual([]);
		const { mentions: _none, ...older } = made("sg-3");
		expect(suggestionMentions(older as CloudCpSuggestion)).toBeUndefined();
	});

	it("draws recorded mention tokens as chips, skipping any without a name", () => {
		expect(composerMentionsOf([
			{ kind: "person", userId: "u-1", name: "Chen Wei" },
			{ kind: "team", teamId: "t-1", name: "Platform" },
			{ kind: "person", userId: "u-2" },
		])).toEqual([{ kind: "person", id: "u-1", name: "Chen Wei" }, { kind: "team", id: "t-1", name: "Platform" }]);
	});
});

describe("mergeFeedHead", () => {
	const row = (sessionId: string) => ({ orgId: "o", sessionId }) as CloudCpFeedItem;
	const ids = (data: { pages: Array<{ items: CloudCpFeedItem[] }> }) => data.pages.map((page) => page.items.map((item) => item.sessionId));
	const fresh = (sessionIds: string[], away?: unknown) => ({ items: sessionIds.map(row), page: { hasMore: true, nextCursor: "fresh" }, ...(away ? { away } : {}) }) as never;

	it("takes a refreshed first page as it is when nothing more was loaded", () => {
		const data: FeedPages = { pages: [{ items: [row("a")], page: { hasMore: true, nextCursor: "c1" } }], pageParams: [undefined] };
		const merged = mergeFeedHead(data, fresh(["n", "a"], { needsYou: 1 }));
		expect(ids(merged)).toEqual([["n", "a"]]);
		expect(merged.pages[0]!.page.nextCursor).toBe("fresh");
		expect(merged.pages[0]!.away).toEqual({ needsYou: 1 });
	});

	it("lays it over the pages Show more loaded, losing no row and showing none twice", () => {
		const data: FeedPages = {
			pages: [
				{ items: [row("a"), row("b")], page: { hasMore: true, nextCursor: "c1" } },
				{ items: [row("c"), row("d")], page: { hasMore: false } },
			],
			pageParams: [undefined, "c1"],
		};
		// n is new, d moved up; b fell off the refreshed page but sits under it.
		const merged = mergeFeedHead(data, fresh(["n", "d", "a"]));
		expect(ids(merged)).toEqual([["n", "d", "a", "b"], ["c"]]);
		expect(merged.pageParams).toEqual([undefined, "c1"]);
		expect(merged.pages[0]!.page.nextCursor).toBe("c1");
	});
});

describe("suggestions", () => {
	const suggestion = (id: string, status: CloudCpSuggestion["status"], createdAt: string): CloudCpSuggestion => ({
		id, orgId: "o", sessionId: "s", kind: "aside", asideId: `aside-${id}`, text: id, mentions: [], status, createdAt, updatedAt: createdAt,
		suggestedBy: { userId: "u-chen", name: "Chen Wei" }, author: { userId: "u-chen", name: "Chen Wei" },
	});
	const event = (type: string, payload: unknown) => ({ type, payload });

	it("reads a suggestion from its two event types, and nothing else", () => {
		const made = suggestion("a", "pending", "2026-11-07T10:00:00Z");
		expect(suggestionFromEvent(event("chat.suggestion_created", made))).toBe(made);
		expect(suggestionFromEvent(event("chat.suggestion_resolved", { ...made, status: "sent" }))?.status).toBe("sent");
		expect(suggestionFromEvent(event("chat.aside", made))).toBeUndefined();
		expect(suggestionFromEvent(event("chat.suggestion_created", { id: "x" }))).toBeUndefined();
	});

	it("keeps the pending list oldest first as suggestions are made, sent and dismissed", () => {
		const first = suggestion("a", "pending", "2026-11-07T10:00:00Z");
		const second = suggestion("b", "pending", "2026-11-07T10:05:00Z");
		const list = applySuggestionEvents([], [
			event("chat.suggestion_created", second),
			event("chat.user_message", { text: "hi" }),
			event("chat.suggestion_created", first),
		]);
		expect(list.map((item) => item.id)).toEqual(["a", "b"]);
		expect(applySuggestionEvents(list, [event("chat.suggestion_resolved", { ...first, status: "sent" })]).map((item) => item.id)).toEqual(["b"]);
		expect(withSuggestion(list, { ...second, status: "dismissed" }).map((item) => item.id)).toEqual(["a"]);
		// Made twice (its 200 answer), it shows once.
		expect(withSuggestion(list, first).map((item) => item.id)).toEqual(["a", "b"]);
	});

	it("tells a resolved suggestion and a locked composer from other failures", () => {
		expect(suggestionResolvedError(new CloudCpError("x", { status: 409, code: "suggestion_resolved" }))).toBe(true);
		expect(composerLockedError(new CloudCpError("x", { status: 409, code: "COMPOSER_LOCKED" }))).toBe(true);
		expect(suggestionResolvedError(new CloudCpError("x", { status: 409, code: "COMPOSER_LOCKED" }))).toBe(false);
		expect(composerLockedError(new CloudCpError("x", { status: 500, code: "COMPOSER_LOCKED" }))).toBe(false);
		expect(suggestionResolvedError(new Error("offline"))).toBe(false);
	});
});

describe("mentions in posted text", () => {
	const chen = { kind: "person" as const, name: "Chen Wei" };
	const chenShort = { kind: "person" as const, name: "Chen" };
	const platform = { kind: "team" as const, name: "Platform" };

	it("picks out each mention where it starts the text or follows whitespace, longest name first", () => {
		expect(splitByMentions("@Chen Wei and @Platform, look", [chenShort, chen, platform])).toEqual([chen, " and ", platform, ", look"]);
		expect(splitByMentions("hi @Chen there", [chen, chenShort])).toEqual(["hi ", chenShort, " there"]);
	});

	it("leaves emails, longer words and unknown names as text", () => {
		expect(splitByMentions("mail ana@Platform.com", [platform])).toEqual(["mail ana@Platform.com"]);
		expect(splitByMentions("@Platforms team", [platform])).toEqual(["@Platforms team"]);
		expect(splitByMentions("@Dana hi", [platform])).toEqual(["@Dana hi"]);
		expect(splitByMentions("no mentions", [])).toEqual(["no mentions"]);
	});

	it("reads recorded tokens and keeps the named ones for drawing", () => {
		const tokens = parseMentions([{ kind: "person", userId: "u", name: "Chen Wei" }, { kind: "team", teamId: "t" }, "junk"]);
		expect(tokens).toEqual([{ kind: "person", userId: "u", name: "Chen Wei" }, { kind: "team", teamId: "t" }]);
		expect(namedMentions(tokens)).toEqual([{ kind: "person", name: "Chen Wei" }]);
		expect(parseMentions(undefined)).toEqual([]);
	});

	it("caps one send at 20 people and teams, and knows the two mention refusals", () => {
		expect(MAX_MENTIONS).toBe(20);
		expect(mentionNotAllowedError(new CloudCpError("x", { status: 422, code: "mention_not_allowed" }))).toBe(true);
		expect(tooManyMentionedError(new CloudCpError("x", { status: 422, code: "too_many_mentioned" }))).toBe(true);
		expect(tooManyMentionedError(new CloudCpError("x", { status: 422, code: "mention_not_allowed" }))).toBe(false);
	});
});
