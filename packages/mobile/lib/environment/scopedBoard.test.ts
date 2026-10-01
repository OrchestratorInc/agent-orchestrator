import { describe, expect, it } from "vitest";

import type { DashboardSession, ProjectInfo } from "../api";
import type { SessionSourceBoard } from "./board";
import {
	composeBoards,
	filterScopedWorkers,
	resolveUnscopedId,
	resourceKey,
	sourceSlice,
	sourceKey,
	type Scoped,
	type SourceRef,
	type SourceStatus,
} from "./scopedBoard";

const local: SourceRef = { kind: "local", id: "mac-1" };
const cloud: SourceRef = { kind: "cloud", id: "org-1" };
const ready: SourceStatus = { resolved: true, available: true, loading: false, stale: false, error: null };
const unresolved: SourceStatus = { resolved: false, available: false, loading: true, stale: false, error: null };

const session = (id: string, projectId: string) => ({ id, projectId }) as DashboardSession;
const board = (projectId: string, sessionId: string): SessionSourceBoard => ({
	projects: [{ id: projectId, name: "Same name" }] as ProjectInfo[],
	sessions: [session(sessionId, projectId)],
	orchestrators: [],
});

describe("source-qualified board", () => {
	it("keeps identical project and session IDs distinct across sources", () => {
		expect(sourceKey(local)).not.toBe(sourceKey(cloud));
		expect(resourceKey(local, "same")).not.toBe(resourceKey(cloud, "same"));
		expect(resourceKey({ kind: "local", id: "a,b" }, "c")).not.toBe(resourceKey({ kind: "local", id: "a" }, "b,c"));
	});

	it("composes tagged rows and preserves per-source loading and errors", () => {
		const cloudError: SourceStatus = { ...ready, stale: true, error: "Cloud unreachable" };
		const result = composeBoards({
			local: { status: ready, snapshot: { source: local, board: board("same", "same") } },
			cloud: { status: cloudError, snapshot: { source: cloud, board: board("same", "same") } },
		});
		expect(result.projects.map((entry) => resourceKey(entry.source, entry.value.id))).toEqual([
			resourceKey(local, "same"), resourceKey(cloud, "same"),
		]);
		expect(result.sessions).toHaveLength(2);
		expect(result.sources).toEqual({ local: ready, cloud: cloudError });
	});

	it("keeps a successful peer while the other source has not resolved", () => {
		const result = composeBoards({
			local: { status: unresolved },
			cloud: { status: ready, snapshot: { source: cloud, board: board("p", "s") } },
		});
		expect(result.sessions.map((entry) => entry.source)).toEqual([cloud]);
		expect(result.sources.local).toEqual(unresolved);
	});

	it("selects only one source for a project detail even when IDs collide", () => {
		const combined = composeBoards({
			local: { status: ready, snapshot: { source: local, board: board("same", "same") } },
			cloud: { status: ready, snapshot: { source: cloud, board: board("same", "same") } },
		});
		const selected = sourceSlice(combined, cloud);
		expect(selected.projects).toHaveLength(1);
		expect(selected.sessions).toHaveLength(1);
		expect(selected.projects[0]).toBe(combined.projects[1].value);
	});

	it("filters workers by environment and source-qualified project", () => {
		const workers: Scoped<DashboardSession>[] = [
			{ source: local, value: session("same", "p") },
			{ source: cloud, value: session("same", "p") },
		];
		expect(filterScopedWorkers(workers, "all", "all")).toHaveLength(2);
		expect(filterScopedWorkers(workers, "cloud", "all")).toEqual([workers[1]]);
		expect(filterScopedWorkers(workers, "all", resourceKey(local, "p"))).toEqual([workers[0]]);
	});

	it("only resolves an unscoped ID when exactly one source contains it", () => {
		const localRow = { source: local, value: session("same", "p") };
		const cloudRow = { source: cloud, value: session("same", "p") };
		expect(resolveUnscopedId("missing", [localRow])).toEqual({ kind: "missing" });
		expect(resolveUnscopedId("same", [localRow])).toEqual({ kind: "found", entry: localRow });
		expect(resolveUnscopedId("same", [localRow, cloudRow])).toEqual({ kind: "ambiguous" });
	});
});
