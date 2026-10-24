import { describe, expect, it } from "vitest";
import type { DashboardSession } from "../api";
import { compareArchived, mergeArchiveStreams } from "./archiveOrder";

const row = (id: string, updatedAt: string, isPinned = false): DashboardSession => ({
	id, projectId: "ao", status: "terminated", mode: "chat", branch: null, issueId: null, issueTitle: null,
	userPrompt: null, displayName: id, summary: null, createdAt: updatedAt, lastActivityAt: updatedAt,
	isTerminated: true, isPinned, updatedAt,
});
const ids = (rows: readonly DashboardSession[]) => rows.map((item) => item.id);

describe("compareArchived", () => {
	// The daemon pages in this order, so a client sort must agree with it.
	it("puts pinned rows first, then the most recently updated, then the higher id", () => {
		const rows = [
			row("a", "2026-10-01T10:00:00Z"),
			row("b", "2026-10-01T12:00:00Z"),
			row("c", "2026-10-01T10:00:00Z"),
			row("pinned", "2026-09-01T00:00:00Z", true),
		];
		expect(ids([...rows].sort(compareArchived))).toEqual(["pinned", "b", "c", "a"]);
	});

	// The daemon orders by nanoseconds. Within one millisecond, a comparator
	// that only saw milliseconds fell back to the id and disagreed with it.
	it("orders rows inside one millisecond by their full timestamp", () => {
		const rows = [
			row("z", "2026-10-01T00:00:00.000800Z"),
			row("a", "2026-10-01T00:00:00.000900Z"),
			row("m", "2026-10-01T00:00:00.0008001Z"),
		];
		expect(ids([...rows].sort(compareArchived))).toEqual(["a", "m", "z"]);
	});

	it("compares instants, not strings, across offsets and fraction lengths", () => {
		const rows = [
			row("utc", "2026-10-01T00:00:00.5Z"),
			row("ist", "2026-10-01T05:30:00.4999999+05:30"),
			row("whole", "2026-10-01T00:00:00Z"),
		];
		expect(ids([...rows].sort(compareArchived))).toEqual(["utc", "ist", "whole"]);
	});
});

describe("mergeArchiveStreams", () => {
	// A non-final page from the daemon, in its order: both rows are above the
	// boundary, so both show.
	it("shows every row of a page whose rows fall inside one millisecond", () => {
		const merged = mergeArchiveStreams([
			{ rows: [row("a", "2026-10-01T00:00:00.000900Z"), row("z", "2026-10-01T00:00:00.000800Z")], done: false },
		]);
		expect(ids(merged.rows)).toEqual(["a", "z"]);
	});

	it("shows a single finished machine's archive in full", () => {
		const merged = mergeArchiveStreams([{ rows: [row("a", "2026-10-02T00:00:00Z"), row("b", "2026-10-01T00:00:00Z")], done: true }]);
		expect(ids(merged.rows)).toEqual(["a", "b"]);
		expect(merged.limiting).toBeNull();
	});

	// Machine B has only loaded down to the 3rd; its next page may hold rows
	// that sort above A's 2nd and 1st, so those wait.
	it("holds back rows that another machine's next page could still precede", () => {
		const merged = mergeArchiveStreams([
			{ rows: [row("a5", "2026-10-05T00:00:00Z"), row("a2", "2026-10-02T00:00:00Z"), row("a1", "2026-10-01T00:00:00Z")], done: false },
			{ rows: [row("b4", "2026-10-04T00:00:00Z"), row("b3", "2026-10-03T00:00:00Z")], done: false },
		]);
		expect(ids(merged.rows)).toEqual(["a5", "b4", "b3"]);
		expect(merged.limiting).toBe(1);
	});

	it("waits for a machine that has not answered yet", () => {
		const merged = mergeArchiveStreams([
			{ rows: [row("a1", "2026-10-01T00:00:00Z")], done: true },
			{ rows: [], done: false },
		]);
		expect(merged.rows).toEqual([]);
		expect(merged.limiting).toBe(1);
	});

	it("shows everything once every machine is done, finished streams included", () => {
		const merged = mergeArchiveStreams([
			{ rows: [row("a2", "2026-10-02T00:00:00Z")], done: true },
			{ rows: [row("b3", "2026-10-03T00:00:00Z"), row("b1", "2026-10-01T00:00:00Z")], done: true },
		]);
		expect(ids(merged.rows)).toEqual(["b3", "a2", "b1"]);
	});
});
