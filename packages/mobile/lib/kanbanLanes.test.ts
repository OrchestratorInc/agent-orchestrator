import { describe, expect, it } from "vitest";
import type { DashboardSession } from "./api";
import { initialLane, KANBAN_LANES, laneCounts, laneLabel, laneOf, sessionsForLane } from "./kanbanLanes";

const session = (id: string, over: Partial<DashboardSession> = {}): DashboardSession => ({
	id,
	projectId: "app",
	status: "working",
	activity: "idle",
	mode: "chat",
	branch: null,
	issueId: null,
	issueTitle: null,
	userPrompt: null,
	displayName: id,
	summary: null,
	createdAt: "2026-11-21T10:00:00Z",
	lastActivityAt: "2026-11-21T10:00:00Z",
	...over,
} as DashboardSession);

describe("kanban lanes", () => {
	// The desktop board's own order and names (product-ui).
	it("lists the desktop's lanes in delivery order", () => {
		expect(KANBAN_LANES.map(laneLabel)).toEqual(["Building", "Validating", "In review", "Ready"]);
	});

	it("places sessions by the daemon's column and keeps archived ones out of every lane", () => {
		expect(laneOf(session("a", { kanbanColumn: "needs_review" }))).toBe("needs_review");
		expect(laneOf(session("b", { kanbanColumn: "archive" }))).toBeNull();
		expect(laneOf(session("c", { isTerminated: true, status: "terminated" }))).toBeNull();
	});

	it("counts each lane", () => {
		const counts = laneCounts([session("a", { kanbanColumn: "building" }), session("b", { kanbanColumn: "building" }), session("c", { kanbanColumn: "ready" })]);
		expect(counts).toEqual({ building: 2, validating: 0, needs_review: 0, ready: 1 });
	});

	// A board opens where it needs you first, else where anything is.
	it("opens on the lane that needs you, else the first with sessions", () => {
		expect(initialLane([session("a", { kanbanColumn: "building" }), session("b", { kanbanColumn: "ready", status: "needs_input", activity: "waiting_input" })])).toBe("ready");
		expect(initialLane([session("a", { kanbanColumn: "validating" }), session("b", { kanbanColumn: "ready" })])).toBe("validating");
		expect(initialLane([])).toBe("building");
	});

	it("filters a lane and keeps the archive for the board's Archive", () => {
		const sessions = [session("a", { kanbanColumn: "building" }), session("b", { kanbanColumn: "ready" }), session("c", { isTerminated: true, status: "terminated" })];
		expect(sessionsForLane(sessions, "ready").map((item) => item.id)).toEqual(["b", "c"]);
	});
});
