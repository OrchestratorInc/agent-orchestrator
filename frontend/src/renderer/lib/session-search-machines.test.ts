import { describe, expect, it } from "vitest";
import { LOCAL_HOST } from "./hosts";
import {
	classifyMachineSearchFailure,
	machinesNotSearched,
	mergeMachineSearchResults,
	type MachineSearchAnswer,
} from "./session-search-machines";

function result(sessionId: string, createdAt: string, namedForQuery = false) {
	return { sessionId, projectId: "proj", harness: "codex", kind: "user" as const, seq: 1, snippet: sessionId, rank: 0, createdAt, ...(namedForQuery ? { namedForQuery } : {}) };
}

function answer(hostId: string, label: string, results: ReturnType<typeof result>[]): MachineSearchAnswer {
	return { hostId, label, outcome: "searched", results, total: results.length };
}

describe("mergeMachineSearchResults", () => {
	it("takes turns across machines, this machine first, and stamps each remote row", () => {
		const merged = mergeMachineSearchResults([
			answer(LOCAL_HOST, "this mac", [result("a1", "2026-10-01T00:00:00Z"), result("a2", "2026-10-02T00:00:00Z")]),
			answer("h_air", "MacBook Air", [result("b1", "2026-10-03T00:00:00Z")]),
			answer("h_box", "Build box", [result("c1", "2026-10-04T00:00:00Z"), result("c2", "2026-10-05T00:00:00Z")]),
		], false);
		expect(merged.map((row) => row.sessionId)).toEqual(["a1", "b1", "c1", "a2", "c2"]);
		// This machine's rows keep no host, so they open on the local routes.
		expect(merged[0]).not.toHaveProperty("hostId");
		expect(merged[1]).toMatchObject({ hostId: "h_air", machineLabel: "MacBook Air" });
	});

	// Plain turn-taking put a passing mention on one machine above the sessions
	// named for the query on another (the MacBook Pro / Air test, "morning standup").
	it("puts sessions named for the query first, still taking turns", () => {
		const merged = mergeMachineSearchResults([
			answer(LOCAL_HOST, "this mac", [result("orchestrator", "2026-10-01T00:00:00Z"), result("review", "2026-10-02T00:00:00Z")]),
			answer("h_air", "MacBook Air", [
				result("standup-1", "2026-10-03T00:00:00Z", true),
				result("standup-2", "2026-10-04T00:00:00Z", true),
				result("notes", "2026-10-05T00:00:00Z"),
			]),
		], false);
		expect(merged.map((row) => row.sessionId)).toEqual(["standup-1", "standup-2", "orchestrator", "notes", "review"]);
	});

	it("merges by time when sorted newest first", () => {
		const merged = mergeMachineSearchResults([
			answer(LOCAL_HOST, "this mac", [result("a-new", "2026-10-09T00:00:00Z"), result("a-old", "2026-10-01T00:00:00Z")]),
			answer("h_air", "MacBook Air", [result("b-mid", "2026-10-05T00:00:00Z", true)]),
		], true);
		expect(merged.map((row) => row.sessionId)).toEqual(["a-new", "b-mid", "a-old"]);
	});

	it("leaves out machines that did not answer without losing the rest", () => {
		const offline: MachineSearchAnswer = { hostId: "h_off", label: "Laptop", outcome: "offline", results: [], total: 0 };
		const merged = mergeMachineSearchResults([answer(LOCAL_HOST, "this mac", [result("a1", "2026-10-01T00:00:00Z")]), offline], false);
		expect(merged.map((row) => row.sessionId)).toEqual(["a1"]);
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

	it("tells a timeout, an unreachable machine and anything else apart", () => {
		expect(classifyMachineSearchFailure(undefined, new DOMException("timed out", "TimeoutError"))).toBe("timed_out");
		expect(classifyMachineSearchFailure(undefined, new TypeError("Failed to fetch"))).toBe("offline");
		expect(classifyMachineSearchFailure(undefined, new Error("bad json"))).toBe("failed");
	});
});

describe("machinesNotSearched", () => {
	it("names every machine that was not searched, with the reason", () => {
		expect(machinesNotSearched([
			{ label: "this mac", outcome: "searched" },
			{ label: "Laptop", outcome: "offline" },
			{ label: "Old desk", outcome: "needs_update" },
			{ label: "Slow box", outcome: "timed_out" },
		])).toEqual(["Laptop (offline)", "Old desk (update AO there to search it)", "Slow box (no answer within 6 s)"]);
	});
});
