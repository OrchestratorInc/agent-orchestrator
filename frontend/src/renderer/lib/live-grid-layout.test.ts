import { beforeEach, describe, expect, it } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import {
	GRID_MIN_HEIGHT,
	GRID_CHAT_MIN_HEIGHT,
	GRID_MIN_WIDTH,
	gridSessions,
	gridShape,
	gridTracks,
	readGridPreferences,
	resizeGridTracks,
	writeGridPreferences,
} from "./live-grid-layout";

const session = (id: string, extra: Partial<WorkspaceSession> = {}): WorkspaceSession => ({
	id,
	workspaceId: "test",
	workspaceName: "test",
	title: id,
	provider: "codex",
	kind: "worker",
	status: "working",
	updatedAt: "2026-10-10T00:00:00Z",
	prs: [],
	...extra,
});
beforeEach(() => localStorage.clear());

it("reserves a taller readable workspace for Chat transcripts and composers", () => {
	const shape = gridShape(12, 1500, 800, GRID_CHAT_MIN_HEIGHT);
	const rows = gridTracks(shape.rows, 800, GRID_CHAT_MIN_HEIGHT);
	expect(rows.every((height) => height >= GRID_CHAT_MIN_HEIGHT)).toBe(true);
	expect(rows.reduce((sum, height) => sum + height, 0)).toBeGreaterThan(800);
});

describe("Live Grid session selection", () => {
	it("pins the existing orchestrator while workers arrive, finish, and stream updates", () => {
		const preferences = readGridPreferences("test");
		const orchestrator = session("orchestrator", { kind: "orchestrator" });
		let selection = gridSessions([session("b"), orchestrator, session("a")], preferences);
		expect(selection.orchestrator).toBe(orchestrator);
		expect(selection.visible.map((entry) => entry.id)).toEqual(["a", "b"]);
		preferences.order = selection.workers.map((entry) => entry.id);
		preferences.orchestratorId = orchestrator.id;
		selection = gridSessions(
			[
				session("b", { updatedAt: "2026-10-11T00:00:00Z" }),
				session("c"),
				orchestrator,
				session("a", { status: "terminated" }),
			],
			preferences,
		);
		expect(selection.orchestrator).toBe(orchestrator);
		expect(selection.visible.map((entry) => entry.id)).toEqual(["b", "c"]);
		expect(selection.workers.map((entry) => entry.id)).toEqual(["a", "b", "c"]);
	});
	it("retains a disconnected worker and a terminated orchestrator without creating sessions", () => {
		const orchestrator = session("orchestrator", { kind: "orchestrator", isTerminated: true });
		const worker = session("worker", { status: "no_signal", activity: { state: "unknown", lastActivityAt: "2026-10-11T00:00:00Z" } });
		const selection = gridSessions([worker, orchestrator], readGridPreferences("test"));
		expect(selection.orchestrator).toBe(orchestrator);
		expect(selection.visible).toEqual([worker]);
	});
	it("shows completed workers on request, preserving manual order", () => {
		const preferences = { ...readGridPreferences("test"), order: ["c", "a", "b"], showCompleted: true };
		expect(
			gridSessions(
				[session("a"), session("b", { kanbanColumn: "archive" }), session("c", { isTerminated: true })],
				preferences,
			).visible.map((entry) => entry.id),
		).toEqual(["c", "a", "b"]);
	});
});

describe("Live Grid geometry and persistence", () => {
	it.each([1, 2, 4, 6, 9, 12])("fits %i sessions into readable tracks across window shapes", (count) => {
		for (const [width, height] of [
			[1200, 800],
			[800, 1000],
			[1800, 700],
			[320, 500],
		]) {
			const { columns, rows } = gridShape(count, width, height);
			expect(columns * rows).toBeGreaterThanOrEqual(count);
			expect(gridTracks(columns, width, GRID_MIN_WIDTH).every((value) => value >= GRID_MIN_WIDTH)).toBe(true);
			expect(gridTracks(rows, height, GRID_MIN_HEIGHT).every((value) => value >= GRID_MIN_HEIGHT)).toBe(true);
		}
	});
	it("uses one large pane for a single session and adapts to the workspace aspect", () => {
		expect(gridShape(1, 1200, 800)).toEqual({ columns: 1, rows: 1 });
		expect(gridShape(4, 1200, 800)).toEqual({ columns: 2, rows: 2 });
		expect(gridShape(9, 1800, 800).columns).toBeGreaterThan(gridShape(9, 600, 1000).columns);
	});
	it("resizes adjacent panels without crossing the reading minimum or changing total size", () => {
		expect(resizeGridTracks([500, 500, 500], 0, 10000, 360)).toEqual([640, 360, 500]);
		expect(resizeGridTracks([500, 500], 0, -10000, 360)).toEqual([360, 640]);
		expect(gridTracks(3, 1400, 360, [1, 3, 1])).toEqual([360, 680, 360]);
	});
	it("persists sizes, completed toggle and worker order separately per project", () => {
		const preferences = {
			...readGridPreferences("project/a"),
			order: ["worker-2", "worker-1"],
			orchestratorId: "main",
			showCompleted: true,
			splits: { "2x2": { columns: [500, 700], rows: [300, 500] } },
		};
		writeGridPreferences("project/a", preferences);
		expect(readGridPreferences("project/a")).toEqual(preferences);
		expect(readGridPreferences("project/b").order).toEqual([]);
	});
	it("ignores corrupted storage and invalid sizing weights", () => {
		localStorage.setItem(
			"ao.live-grid.v1.test",
			'{"version":1,"order":["a","a",42],"splits":{"2x2":{"columns":[-1,1],"rows":[1,1]}}}',
		);
		expect(readGridPreferences("test").order).toEqual(["a"]);
		expect(readGridPreferences("test").splits).toEqual({});
		expect(gridTracks(2, 1000, 360, [NaN, 1])).toEqual([500, 500]);
	});
});
