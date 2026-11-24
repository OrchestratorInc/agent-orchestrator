import { describe, expect, it } from "vitest";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { buildSidebarSessionOrder, EMPTY_GROUP_ORDER, orderGroupSessions, pinnedFirst, splitGroupOrder } from "./sidebar-order";

function row(id: string, pinnedAt?: string, kind: WorkspaceSession["kind"] = "worker"): WorkspaceSession {
	return {
		id, workspaceId: "p", workspaceName: "P", title: id, provider: "codex", kind, branch: "", status: "working",
		updatedAt: "2026-06-30T00:00:00Z", prs: [], ...(pinnedAt ? { isPinned: true, pinnedAt } : {}),
	};
}

describe("orderGroupSessions", () => {
	const sessions = [row("a"), row("old-pin", "2026-06-30T01:00:00Z"), row("b"), row("new-pin", "2026-06-30T02:00:00Z")];

	it("puts pins first, newest pin on top, and keeps the rest in incoming order", () => {
		expect(orderGroupSessions(sessions, EMPTY_GROUP_ORDER).map((s) => s.id)).toEqual(["new-pin", "old-pin", "a", "b"]);
	});

	it("applies a drag order within each part but never across the pin boundary", () => {
		const order = ["b", "old-pin", "a", "new-pin"];
		expect(orderGroupSessions(sessions, splitGroupOrder(order, sessions)).map((s) => s.id)).toEqual(["old-pin", "new-pin", "b", "a"]);
	});

	it("puts a row pinned after a drag above the pins the drag ordered", () => {
		// Dragged while "b" was unpinned: its slot belongs to the unpinned part.
		const saved = splitGroupOrder(["old-pin", "new-pin", "b", "a"], sessions);
		const pinnedLater = sessions.map((s) => (s.id === "b" ? row("b", "2026-06-30T03:00:00Z") : s));
		expect(orderGroupSessions(pinnedLater, saved).map((s) => s.id)).toEqual(["b", "old-pin", "new-pin", "a"]);
	});

	it("never treats a pinned orchestrator as a pinned row", () => {
		const orchestrator = row("orc", "2026-06-30T03:00:00Z", "orchestrator");
		expect(orderGroupSessions([orchestrator, ...sessions], EMPTY_GROUP_ORDER).map((s) => s.id)).toEqual(["new-pin", "old-pin", "orc", "a", "b"]);
	});

	it("sorts only the unpinned part with arrangeRest", () => {
		expect(orderGroupSessions(sessions, EMPTY_GROUP_ORDER, (rest) => [...rest].reverse()).map((s) => s.id)).toEqual(["new-pin", "old-pin", "b", "a"]);
	});
});

describe("buildSidebarSessionOrder with pins", () => {
	it("walks each group's pins at the top of that group, not in a global pinned block first", () => {
		const project = (id: string, sessions: WorkspaceSession[]): WorkspaceSummary => ({ id, name: id, path: `/${id}`, sessions });
		const order = buildSidebarSessionOrder({
			workspaces: [
				project("alpha", [row("alpha-1"), row("alpha-pin", "2026-06-30T01:00:00Z")]),
				project("bravo", [row("bravo-1"), row("bravo-pin", "2026-06-30T05:00:00Z")]),
			],
			projectOrder: ["alpha", "bravo"],
		});
		expect(order.map((entry) => entry.sessionId)).toEqual(["alpha-pin", "alpha-1", "bravo-pin", "bravo-1"]);
	});
});

describe("pinnedFirst", () => {
	it("moves pinned items to the front and keeps each group's order", () => {
		expect(pinnedFirst(["a", "b", "c", "d"], (id) => id, ["d", "b"])).toEqual(["b", "d", "a", "c"]);
	});

	it("ignores pins for items that are not listed", () => {
		expect(pinnedFirst(["a", "b"], (id) => id, ["gone"])).toEqual(["a", "b"]);
	});
});
