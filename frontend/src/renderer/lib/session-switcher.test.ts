import { describe, expect, it } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import {
	buildSessionSwitcherEntries,
	initialSessionSwitcherIndex,
	sessionSwitcherCommitId,
	sessionSwitcherEligibleIds,
	stepSessionSwitcherIndex,
} from "./session-switcher";

function session(overrides: Partial<WorkspaceSession> & Pick<WorkspaceSession, "id">): WorkspaceSession {
	return {
		workspaceId: "proj",
		workspaceName: "proj",
		title: overrides.id,
		provider: "codex",
		branch: "main",
		status: "working",
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		kind: "worker",
		...overrides,
	};
}

const workers = ["A", "B", "C", "D", "E", "F", "G", "H", "I"].map((id, index) =>
	session({
		id,
		lastInteractionAt: new Date(Date.UTC(2026, 0, 10 - index)).toISOString(),
	}),
);

describe("buildSessionSwitcherEntries", () => {
	it("puts the live orchestrator first and keeps the latest seven workers", () => {
		const orchestrator = session({
			id: "orch",
			kind: "orchestrator",
			createdAt: "2026-02-01T00:00:00Z",
		});
		const dead = session({
			id: "orch-old",
			kind: "orchestrator",
			status: "terminated",
			isTerminated: true,
			createdAt: "2026-01-01T00:00:00Z",
		});
		const entries = buildSessionSwitcherEntries([dead, orchestrator, ...workers], "A");
		expect(entries.map((entry) => entry.id)).toEqual(["orch", "A", "B", "C", "D", "E", "F", "G"]);
		expect(entries[0]?.role).toBe("orchestrator");
	});

	it("keeps the current worker plus the latest six when the current worker is past the cap", () => {
		const entries = buildSessionSwitcherEntries(workers, "I");
		expect(entries.map((entry) => entry.id)).toEqual(["A", "B", "C", "D", "E", "F", "I"]);
	});

	it("keeps activity order for a current worker just outside the latest seven", () => {
		const entries = buildSessionSwitcherEntries(workers, "H");
		expect(entries.map((entry) => entry.id)).toEqual(["A", "B", "C", "D", "E", "F", "H"]);
	});

	it("shows the latest seven from the kanban when no worker is current", () => {
		const orchestrator = session({ id: "orch", kind: "orchestrator", createdAt: "2026-02-01T00:00:00Z" });
		const entries = buildSessionSwitcherEntries([orchestrator, ...workers]);
		expect(entries.map((entry) => entry.id)).toEqual(["orch", "A", "B", "C", "D", "E", "F", "G"]);
		expect(initialSessionSwitcherIndex(entries, undefined, true)).toBe(0);
	});

	it("drops terminated workers and a terminated-only orchestrator", () => {
		const gone = session({ id: "gone", isTerminated: true, lastInteractionAt: "2026-03-01T00:00:00Z" });
		const entries = buildSessionSwitcherEntries([gone, workers[0]!]);
		expect(entries.map((entry) => entry.id)).toEqual(["A"]);
		expect(sessionSwitcherEligibleIds([gone, workers[0]!]).has("gone")).toBe(false);
	});

	it("uses only the newest live orchestrator when several are active", () => {
		const older = session({ id: "orch-old", kind: "orchestrator", createdAt: "2026-01-01T00:00:00Z" });
		const newer = session({ id: "orch-new", kind: "orchestrator", createdAt: "2026-03-01T00:00:00Z" });
		const entries = buildSessionSwitcherEntries([older, newer, workers[0]!], "A");
		expect(entries.map((entry) => entry.id)).toEqual(["orch-new", "A"]);
	});
});

describe("session switcher interaction", () => {
	const entries = buildSessionSwitcherEntries(workers, "C");

	it("highlights the current session before any advance, then cycles", () => {
		const start = initialSessionSwitcherIndex(entries, "C", false);
		expect(entries[start]?.id).toBe("C");
		const forward = stepSessionSwitcherIndex(start, entries.length, 1);
		expect(entries[forward]?.id).toBe("D");
		const backward = stepSessionSwitcherIndex(start, entries.length, -1);
		expect(entries[backward]?.id).toBe("B");
		expect(stepSessionSwitcherIndex(0, entries.length, -1)).toBe(entries.length - 1);
	});

	it("does not commit after cancel, when the row vanished, or when the selection is already current", () => {
		const eligible = sessionSwitcherEligibleIds(workers);
		expect(sessionSwitcherCommitId("C", eligible, "A", true)).toBeUndefined();
		expect(sessionSwitcherCommitId("C", eligible, "C", false)).toBeUndefined();
		expect(sessionSwitcherCommitId("I", new Set(["A"]), "A", false)).toBeUndefined();
		expect(sessionSwitcherCommitId("B", eligible, "A", false)).toBe("B");
	});
});
