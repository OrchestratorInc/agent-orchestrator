import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MACHINE_SEARCH_GATHER_MS, type MachineSearchAnswer } from "../lib/session-search-machines";
import { useMachineSearchRows, type MachineSearchRowsInput } from "./useMachineSearchRows";
import type { SessionSearchResponse } from "./usePaletteSessionSearch";
import type { RemoteMachineSearch } from "./useRemoteMachineSearch";

function hit(sessionId: string, namedForQuery = false) {
	return { sessionId, projectId: "p", harness: "codex", kind: "user" as const, seq: 1, snippet: sessionId, rank: 0, createdAt: "2026-10-01T00:00:00Z", ...(namedForQuery ? { namedForQuery } : {}) };
}

function local(query: string, ids: string[]): SessionSearchResponse {
	return {
		query, results: ids.map((id) => hit(id)), total: ids.length, limit: 8, offset: 0, sort: "relevance", group: "session",
		coverage: { state: "ready", indexedDocs: 1, indexedSources: 1, pendingSources: 0, unavailableSources: 0, unsupportedSources: 0 },
	};
}

function remoteAnswer(hostId: string, results: ReturnType<typeof hit>[]): MachineSearchAnswer {
	return { hostId, label: hostId, outcome: "searched", results, total: results.length };
}

function remote(query: string, answers: MachineSearchAnswer[], settled: boolean): RemoteMachineSearch {
	return { query, answers, settled };
}

const NO_REMOTE = (query: string) => remote(query, [], true);

function render(initial: MachineSearchRowsInput) {
	return renderHook((props: MachineSearchRowsInput) => useMachineSearchRows(props), { initialProps: initial });
}

const ids = (rows: { sessionId: string }[]) => rows.map((row) => row.sessionId);

describe("useMachineSearchRows", () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});
	afterEach(() => {
		vi.useRealTimers();
	});

	it("shows this machine's rows as soon as they land when no other machine is asked", () => {
		const { result } = render({ query: "orbit", local: local("orbit", ["a1", "a2"]), localFailed: false, remote: NO_REMOTE("orbit"), limit: 8 });
		expect(result.current).toMatchObject({ current: true });
		expect(ids(result.current.rows)).toEqual(["a1", "a2"]);
	});

	it("waits briefly for the other machines, so the ones that answer in time are ranked in", () => {
		const { result, rerender } = render({ query: "standup", local: local("standup", ["a1", "a2"]), localFailed: false, remote: remote("standup", [], false), limit: 8 });
		expect(result.current).toEqual({ rows: [], current: false });
		rerender({ query: "standup", local: local("standup", ["a1", "a2"]), localFailed: false, remote: remote("standup", [remoteAnswer("h_air", [hit("named", true)])], true), limit: 8 });
		// The remote session named for the query leads, though it answered second.
		expect(ids(result.current.rows)).toEqual(["named", "a1", "a2"]);
		expect(result.current.current).toBe(true);
	});

	// QA: one slow machine held every other machine's rows back for six seconds.
	it("shows what has answered once the wait is over, and adds a slow machine's rows below later", () => {
		const fast = remoteAnswer("h_air", [hit("air-1")]);
		const { result, rerender } = render({ query: "deploy", local: local("deploy", ["a1", "a2"]), localFailed: false, remote: remote("deploy", [fast], false), limit: 8 });
		expect(result.current.rows).toEqual([]);
		act(() => {
			vi.advanceTimersByTime(MACHINE_SEARCH_GATHER_MS);
		});
		expect(ids(result.current.rows)).toEqual(["a1", "air-1", "a2"]);
		expect(result.current.current).toBe(true);
		const slow = remoteAnswer("h_slow", [hit("slow-named", true)]);
		rerender({ query: "deploy", local: local("deploy", ["a1", "a2"]), localFailed: false, remote: remote("deploy", [fast, slow], true), limit: 8 });
		// Named for the query, but late: it goes below, so nothing moves under the cursor.
		expect(ids(result.current.rows)).toEqual(["a1", "air-1", "a2", "slow-named"]);
	});

	// QA: "orbit" rows stayed under "deploy" for four seconds, and Enter opened one.
	it("does not keep an unrelated earlier query's rows while the new one loads", () => {
		const { result, rerender } = render({ query: "orbit", local: local("orbit", ["orbit-1"]), localFailed: false, remote: NO_REMOTE("orbit"), limit: 8 });
		expect(ids(result.current.rows)).toEqual(["orbit-1"]);
		rerender({ query: "deploy", local: undefined, localFailed: false, remote: NO_REMOTE("deploy"), limit: 8 });
		expect(result.current).toEqual({ rows: [], current: false });
	});

	it("keeps the previous rows while a query that refines it loads", () => {
		const { result, rerender } = render({ query: "orb", local: local("orb", ["orbit-1"]), localFailed: false, remote: NO_REMOTE("orb"), limit: 8 });
		rerender({ query: "orbit", local: undefined, localFailed: false, remote: NO_REMOTE("orbit"), limit: 8 });
		expect(result.current.current).toBe(false);
		expect(ids(result.current.rows)).toEqual(["orbit-1"]);
		rerender({ query: "orbit", local: local("orbit", ["orbit-2"]), localFailed: false, remote: NO_REMOTE("orbit"), limit: 8 });
		expect(ids(result.current.rows)).toEqual(["orbit-2"]);
	});

	it("waits again when the query comes back to one it already waited for", () => {
		const pending = (query: string) => ({ query, local: local(query, ["a1"]), localFailed: false, remote: remote(query, [], false), limit: 8 });
		const { result, rerender } = render(pending("orbit"));
		act(() => {
			vi.advanceTimersByTime(MACHINE_SEARCH_GATHER_MS);
		});
		expect(result.current.current).toBe(true);
		rerender(pending("orbix"));
		rerender(pending("orbit"));
		// Back to "orbit": its rows stay on screen (the query is the same), but
		// the remote machines get their wait again before anything is added.
		expect(result.current.current).toBe(false);
		act(() => {
			vi.advanceTimersByTime(MACHINE_SEARCH_GATHER_MS);
		});
		expect(result.current.current).toBe(true);
	});

	it("shows the other machines' rows when this machine's search failed", () => {
		const { result } = render({ query: "flaky", local: undefined, localFailed: true, remote: remote("flaky", [remoteAnswer("h_air", [hit("air-1")])], true), limit: 8 });
		expect(ids(result.current.rows)).toEqual(["air-1"]);
	});

	it("ignores answers that belong to another query", () => {
		const { result } = render({ query: "flaky", local: local("flaky", ["a1"]), localFailed: false, remote: remote("flak", [remoteAnswer("h_air", [hit("old")])], true), limit: 8 });
		expect(result.current.rows).toEqual([]);
		act(() => {
			vi.advanceTimersByTime(MACHINE_SEARCH_GATHER_MS);
		});
		expect(ids(result.current.rows)).toEqual(["a1"]);
	});
});
