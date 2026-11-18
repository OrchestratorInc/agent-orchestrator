import { describe, expect, it } from "vitest";
import type { DashboardSession } from "../api";

import { latestEventAt, readPastPinned } from "./archiveOrder";

function session(id: string, at: Partial<Pick<DashboardSession, "lastEventAt" | "lastActivityAt" | "createdAt" | "isPinned">>): DashboardSession {
	return {
		id, projectId: "p", status: "terminated", mode: "chat", branch: null, issueId: null, issueTitle: null,
		userPrompt: null, displayName: null, summary: null, createdAt: "", lastActivityAt: "", ...at,
	};
}

describe("latestEventAt", () => {
	// The archive lists pinned workers first, so its first row can be an old one.
	it("takes the newest event, not the first row", () => {
		expect(latestEventAt([
			session("pinned-old", { lastEventAt: "2026-10-01T00:00:00Z" }),
			session("newest", { lastEventAt: "2026-10-30T15:00:00Z" }),
			session("older", { lastActivityAt: "2026-10-29T00:00:00Z" }),
		])).toBe("2026-10-30T15:00:00Z");
	});

	it("is undefined when no session has a usable time", () => {
		expect(latestEventAt([])).toBeUndefined();
		expect(latestEventAt([session("blank", {})])).toBeUndefined();
	});
});

describe("readPastPinned", () => {
	const pinned = (id: string) => session(id, { isPinned: true, lastEventAt: "2026-10-01T00:00:00Z" });
	const pages: Record<string, { sessions: DashboardSession[]; nextCursor: string | null }> = {
		first: { sessions: [pinned("p1"), pinned("p2")], nextCursor: "c2" },
		c2: { sessions: [pinned("p3"), session("newest", { lastEventAt: "2026-10-30T15:00:00Z" })], nextCursor: "c3" },
		c3: { sessions: [session("older", { lastEventAt: "2026-10-29T00:00:00Z" })], nextCursor: null },
	};
	const read = (cursor: string | null) => Promise.resolve(pages[cursor ?? "first"]);

	// A machine with a page of pinned archived workers: the first page alone
	// said the last worker finished weeks ago.
	it("reads on until the first unpinned row, and no further", async () => {
		const rows = await readPastPinned(read, 4);
		expect(rows.map((row) => row.id)).toEqual(["p1", "p2", "p3", "newest"]);
		expect(latestEventAt(rows)).toBe("2026-10-30T15:00:00Z");
	});

	it("stops at the page cap or the end of the archive", async () => {
		expect((await readPastPinned(read, 1)).map((row) => row.id)).toEqual(["p1", "p2"]);
		const short = (_cursor: string | null) => Promise.resolve({ sessions: [pinned("only")], nextCursor: null });
		expect((await readPastPinned(short, 4)).map((row) => row.id)).toEqual(["only"]);
	});
});
