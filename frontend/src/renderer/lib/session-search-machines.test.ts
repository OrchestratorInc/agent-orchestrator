import { describe, expect, it } from "vitest";
import { LOCAL_HOST } from "./hosts";
import {
	boundMachineResult,
	classifyMachineSearchFailure,
	isQueryRefinement,
	MACHINE_SNIPPET_MAX_CHARS,
	machinesNotSearched,
	mergeNewestFirst,
	orderMachineSearchResults,
	pickMachineSearchRows,
	savedMachineOutcome,
	type MachineSearchAnswer,
	type MachineSearchResult,
} from "./session-search-machines";

function result(sessionId: string, createdAt: string, namedForQuery = false) {
	return { sessionId, projectId: "proj", harness: "codex", kind: "user" as const, seq: 1, snippet: sessionId, rank: 0, createdAt, ...(namedForQuery ? { namedForQuery } : {}) };
}

function answer(hostId: string, label: string, results: ReturnType<typeof result>[], total = results.length): MachineSearchAnswer {
	return { hostId, label, outcome: "searched", results, total };
}

const ids = (rows: readonly MachineSearchResult[]) => rows.map((row) => row.sessionId);

/** n results named `${prefix}1`..`${prefix}n`. */
function results(prefix: string, n: number) {
	return Array.from({ length: n }, (_, index) => result(`${prefix}${index + 1}`, "2026-10-01T00:00:00Z"));
}

describe("orderMachineSearchResults", () => {
	it("takes turns across machines, this machine first, and stamps each remote row", () => {
		const ordered = orderMachineSearchResults([
			answer(LOCAL_HOST, "this mac", [result("a1", "2026-10-01T00:00:00Z"), result("a2", "2026-10-02T00:00:00Z")]),
			answer("h_air", "MacBook Air", [result("b1", "2026-10-03T00:00:00Z")]),
			answer("h_box", "Build box", [result("c1", "2026-10-04T00:00:00Z"), result("c2", "2026-10-05T00:00:00Z")]),
		], false);
		expect(ids(ordered)).toEqual(["a1", "b1", "c1", "a2", "c2"]);
		// This machine's rows keep no host, so they open on the local routes.
		expect(ordered[0]).not.toHaveProperty("hostId");
		expect(ordered[1]).toMatchObject({ hostId: "h_air", machineLabel: "MacBook Air" });
	});

	// Plain turn-taking put a passing mention on one machine above the sessions
	// named for the query on another (the MacBook Pro / Air test, "morning standup").
	it("puts sessions named for the query first, still taking turns", () => {
		const ordered = orderMachineSearchResults([
			answer(LOCAL_HOST, "this mac", [result("orchestrator", "2026-10-01T00:00:00Z"), result("review", "2026-10-02T00:00:00Z")]),
			answer("h_air", "MacBook Air", [
				result("standup-1", "2026-10-03T00:00:00Z", true),
				result("standup-2", "2026-10-04T00:00:00Z", true),
				result("notes", "2026-10-05T00:00:00Z"),
			]),
		], false);
		expect(ids(ordered)).toEqual(["standup-1", "standup-2", "orchestrator", "notes", "review"]);
	});

	// QA: this machine had 87 matching sessions and each of eight others had 8;
	// plain turns gave it one row in eight.
	it("shares the rows after every machine's best by how many sessions matched there", () => {
		const ordered = orderMachineSearchResults([
			answer(LOCAL_HOST, "this mac", results("a", 8), 80),
			answer("h_b", "B", results("b", 8), 10),
			answer("h_c", "C", results("c", 8), 10),
		], false);
		// Every machine's best first, then this machine takes 8 of every 10 turns.
		expect(ids(ordered).slice(0, 8)).toEqual(["a1", "b1", "c1", "a2", "a3", "a4", "b2", "a5"]);
	});

	it("merges by time when sorted newest first", () => {
		const ordered = orderMachineSearchResults([
			answer(LOCAL_HOST, "this mac", [result("a-new", "2026-10-09T00:00:00Z"), result("a-old", "2026-10-01T00:00:00Z")]),
			answer("h_air", "MacBook Air", [result("b-mid", "2026-10-05T00:00:00Z", true)]),
		], true);
		expect(ids(ordered)).toEqual(["a-new", "b-mid", "a-old"]);
	});

	// QA: one result without a time made the merge return a1, a2, b1-nodate, b2, b3.
	it("sorts a row with no readable time last when newest first, keeping the rest in time order", () => {
		const ordered = orderMachineSearchResults([
			answer(LOCAL_HOST, "this mac", [result("a1", "2026-10-09T00:00:00Z"), result("a2", "2026-10-07T00:00:00Z")]),
			answer("h_b", "B", [
				result("b-nodate", ""),
				result("b2", "2026-10-08T00:00:00Z"),
				result("b3", "not a date"),
				result("b4", "2026-10-06T00:00:00Z"),
			]),
		], true);
		expect(ids(ordered)).toEqual(["a1", "b2", "a2", "b4", "b-nodate", "b3"]);
	});

	it("leaves out machines that did not answer without losing the rest", () => {
		const offline: MachineSearchAnswer = { hostId: "h_off", label: "Laptop", outcome: "offline", results: [], total: 0 };
		const ordered = orderMachineSearchResults([answer(LOCAL_HOST, "this mac", [result("a1", "2026-10-01T00:00:00Z")]), offline], false);
		expect(ids(ordered)).toEqual(["a1"]);
	});
});

describe("pickMachineSearchRows", () => {
	it("keeps to the limit when few machines matched", () => {
		const ordered = orderMachineSearchResults([answer(LOCAL_HOST, "this mac", results("a", 8)), answer("h_b", "B", results("b", 8))], false);
		expect(pickMachineSearchRows(ordered, 8)).toHaveLength(8);
	});

	// QA: ten machines with matches and eight rows left the last two out entirely.
	it("gives every machine with a match a row, growing past the limit when it must", () => {
		const answers = [answer(LOCAL_HOST, "this mac", results("local", 8), 87)];
		for (let machine = 1; machine <= 9; machine++) answers.push(answer(`h_${machine}`, `Fast ${machine}`, results(`f${machine}-`, 8)));
		const picked = pickMachineSearchRows(orderMachineSearchResults(answers, false), 8);
		expect(picked).toHaveLength(10);
		expect(new Set(picked.map((row) => row.hostId ?? LOCAL_HOST)).size).toBe(10);
	});

	it("still represents every machine when the named rows of one fill the list", () => {
		const named = Array.from({ length: 8 }, (_, index) => result(`n${index}`, "2026-10-01T00:00:00Z", true));
		const ordered = orderMachineSearchResults([
			answer(LOCAL_HOST, "this mac", named),
			answer("h_b", "B", [result("b1", "2026-10-01T00:00:00Z")]),
		], false);
		const picked = pickMachineSearchRows(ordered, 8);
		expect(picked).toHaveLength(8);
		expect(ids(picked)).toContain("b1");
	});

	// A machine that answers after the list is on screen adds its rows below:
	// the row under the cursor never moves.
	it("keeps rows already shown in place and adds a late machine's rows below them", () => {
		const first = pickMachineSearchRows(orderMachineSearchResults([answer(LOCAL_HOST, "this mac", results("a", 8))], false), 8);
		expect(ids(first)).toEqual(["a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8"]);
		const late = orderMachineSearchResults([
			answer(LOCAL_HOST, "this mac", results("a", 8)),
			answer("h_slow", "Slow box", [result("named", "2026-10-01T00:00:00Z", true), result("s2", "2026-10-01T00:00:00Z")]),
		], false);
		const next = pickMachineSearchRows(late, 8, first);
		expect(ids(next).slice(0, 8)).toEqual(ids(first));
		expect(ids(next).slice(8)).toEqual(["named"]);
	});

	it("drops a shown row the latest answers no longer hold, and shows the rest as they are now", () => {
		const before = pickMachineSearchRows(orderMachineSearchResults([answer(LOCAL_HOST, "this mac", results("a", 3))], false), 8);
		const renamed = { ...result("a1", "2026-10-01T00:00:00Z"), sessionName: "renamed" };
		const after = pickMachineSearchRows(orderMachineSearchResults([answer(LOCAL_HOST, "this mac", [renamed, result("a3", "2026-10-01T00:00:00Z")])], false), 8, before);
		expect(ids(after)).toEqual(["a1", "a3"]);
		expect(after[0]?.sessionName).toBe("renamed");
	});

	it("tells the same session id on two machines apart", () => {
		const ordered = orderMachineSearchResults([
			answer(LOCAL_HOST, "this mac", [result("same", "2026-10-01T00:00:00Z")]),
			answer("h_b", "B", [result("same", "2026-10-01T00:00:00Z")]),
		], false);
		expect(pickMachineSearchRows(ordered, 8)).toHaveLength(2);
	});
});

describe("isQueryRefinement", () => {
	it.each([
		["orb", "orbit", true],
		["orbit", "orb", true],
		["Orbit", "orbit status", true],
		["orbit", "deploy", false],
		["", "orbit", false],
		// Review: an operator's value is a different filter, not a narrower one.
		["project:alpha", "project:alpha2", false],
		["orbit", "orbit harness:codex", false],
		["!test", "!testing", false],
		['"flak', '"flaky', false],
		['"flaky test"', '"flaky test" login', true],
		["orbit harness:codex", "orbit harness:codex sta", true],
		["project:alpha", "project:alpha", true],
		["orbit", "", false],
	] as const)("%j then %j is %s", (previous, next, refines) => {
		expect(isQueryRefinement(previous, next)).toBe(refines);
	});
});

describe("boundMachineResult", () => {
	it("cuts an oversized snippet around its first match and keeps the spans aligned", () => {
		const big = `${"lorem ".repeat(2_000)}needle ${"ipsum ".repeat(6_000)}`;
		const at = big.indexOf("needle");
		const bounded = boundMachineResult({ ...result("big", "2026-10-01T00:00:00Z"), snippet: big, highlights: [{ start: at, end: at + 6 }] });
		expect(bounded.snippet.length).toBeLessThanOrEqual(MACHINE_SNIPPET_MAX_CHARS + 2);
		expect(bounded.snippet.startsWith("…")).toBe(true);
		expect(bounded.snippet.endsWith("…")).toBe(true);
		const [span] = bounded.highlights ?? [];
		expect(span && bounded.snippet.slice(span.start, span.end)).toBe("needle");
	});

	it("drops spans that do not fit the snippet, overlap, or are not whole numbers", () => {
		const bounded = boundMachineResult({
			...result("odd", "2026-10-01T00:00:00Z"),
			snippet: "find the needle here",
			highlights: [{ start: 9999, end: 10010 }, { start: -5, end: 3 }, { start: 9, end: 15 }, { start: 10, end: 12 }, { start: 1.5, end: 3 }],
		});
		expect(bounded.highlights).toEqual([{ start: 9, end: 15 }]);
	});

	it("removes the spans entirely when none fit", () => {
		const bounded = boundMachineResult({ ...result("odd", "2026-10-01T00:00:00Z"), snippet: "short", highlights: [{ start: 50, end: 60 }] });
		expect(bounded).not.toHaveProperty("highlights");
	});

	it("cuts an oversized session name", () => {
		const bounded = boundMachineResult({ ...result("named", "2026-10-01T00:00:00Z"), sessionName: "x".repeat(5_000) });
		expect(bounded.sessionName?.length).toBeLessThanOrEqual(301);
	});

	it("leaves an ordinary result as it was", () => {
		const ordinary = { ...result("plain", "2026-10-01T00:00:00Z"), snippet: "a needle", highlights: [{ start: 2, end: 8 }], sessionName: "Plain" };
		expect(boundMachineResult(ordinary)).toEqual(ordinary);
	});
});

// The daemon reports the sort it applied, a typed `sort:` included, and the
// merge follows it: Cmd+K's `retry sort:recent` once merged by relevance.
describe("mergeNewestFirst", () => {
	it("follows the sort this machine's daemon applied", () => {
		expect(mergeNewestFirst({ sort: "recent" })).toBe(true);
		expect(mergeNewestFirst({ sort: "relevance" })).toBe(false);
		expect(mergeNewestFirst({})).toBe(false);
		expect(mergeNewestFirst(undefined)).toBe(false);
	});
});

describe("classifyMachineSearchFailure", () => {
	it.each([
		[401, "unauthorized"],
		[403, "unauthorized"],
		[404, "needs_update"],
		[405, "needs_update"],
		[501, "needs_update"],
		[502, "offline"],
		[504, "offline"],
		[521, "offline"],
		// A quick tunnel whose machine went away answers 530.
		[530, "offline"],
		[500, "failed"],
		[503, "failed"],
	] as const)("HTTP %i is %s", (status, outcome) => {
		expect(classifyMachineSearchFailure(status)).toBe(outcome);
	});

	it("names a machine whose session search is off", () => {
		expect(classifyMachineSearchFailure(503, undefined, "SESSION_SEARCH_UNAVAILABLE")).toBe("search_off");
	});

	it("tells a timeout, an unreachable machine and anything else apart", () => {
		expect(classifyMachineSearchFailure(undefined, new DOMException("timed out", "TimeoutError"))).toBe("timed_out");
		expect(classifyMachineSearchFailure(undefined, new TypeError("Failed to fetch"))).toBe("offline");
		expect(classifyMachineSearchFailure(undefined, new Error("bad json"))).toBe("failed");
	});
});

describe("savedMachineOutcome", () => {
	it("says why a saved machine is not connected when the connection said", () => {
		expect(savedMachineOutcome({ status: "connecting" })).toBe("connecting");
		expect(savedMachineOutcome({ status: "offline" })).toBe("offline");
		expect(savedMachineOutcome({ status: "offline", failureReason: "unauthorized" })).toBe("unauthorized");
		expect(savedMachineOutcome({ status: "offline", failureReason: "incompatible" })).toBe("needs_update");
	});
});

describe("machinesNotSearched", () => {
	it("names every machine that was not searched, with the reason", () => {
		expect(machinesNotSearched([
			{ label: "this mac", outcome: "searched" },
			{ label: "Laptop", outcome: "offline" },
			{ label: "Old desk", outcome: "needs_update" },
			{ label: "Slow box", outcome: "timed_out" },
			{ label: "Quiet box", outcome: "search_off" },
		])).toEqual([
			"Laptop (offline)",
			"Old desk (update AO there to search it)",
			"Slow box (no answer within 6 s)",
			"Quiet box (search is turned off there)",
		]);
	});
});
