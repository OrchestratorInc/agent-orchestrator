import { describe, expect, it } from "vitest";
import type { ConversationActivity, ConversationMessage, ConversationSnapshot, ConversationTurn } from "../types/conversation";
import { sessionRailWork } from "./session-rail-work";

const turn = (id: string, over: Partial<ConversationTurn> = {}): ConversationTurn => ({ id, state: "completed", requestedAt: "2026-10-20T09:00:00Z", startedAt: "2026-10-20T09:00:00Z", completedAt: "2026-10-20T09:08:00Z", ...over });
const message = (id: string, turnId: string, over: Partial<ConversationMessage> = {}): ConversationMessage => ({ kind: "message", id, turnId, sequence: 1, revision: 0, role: "user", origin: "human", text: "Fix the chart", streaming: false, createdAt: "2026-10-20T09:00:00Z", ...over });
const activity = (id: string, over: Partial<ConversationActivity> = {}): ConversationActivity => ({ kind: "activity", id, sequence: 2, revision: 0, activityKind: "approval", status: "pending", summary: "Run npm test", detail: {}, createdAt: "2026-10-20T09:03:00Z", ...over }) as ConversationActivity;
const snapshot = (turns: ConversationTurn[], items: ConversationSnapshot["items"]) => ({ turns, items }) as ConversationSnapshot;

describe("sessionRailWork", () => {
	it("has nothing to say without a history", () => {
		expect(sessionRailWork(undefined)).toBeUndefined();
	});

	it("gives one entry per turn: who asked, what, and how it went", () => {
		const work = sessionRailWork(snapshot(
			[turn("t1", { diff: { files: [{ path: "a.ts", additions: 1, deletions: 0, status: "modified" }, { path: "b.ts", additions: 2, deletions: 1, status: "added" }] } })],
			[message("m1", "t1"), message("m2", "t1", { role: "assistant", origin: "provider", text: "Done" }), message("m3", "t1", { text: "And the legend" })],
		));
		expect(work?.turns).toEqual([{ id: "t1", at: "2026-10-20T09:00:00Z", sender: "you", text: "Fix the chart", turn: { state: "completed", ms: 480_000, files: 2, paths: ["a.ts", "b.ts"], error: undefined } }]);
	});

	it("says who sent a request the person did not write", () => {
		const work = sessionRailWork(snapshot(
			[turn("t1"), turn("t2")],
			[message("m1", "t1", { origin: "daemon", text: "The lint check failed" }), message("m2", "t2", { origin: "automation", text: "Nightly tidy" })],
		));
		expect(work?.turns.map((entry) => entry.sender)).toEqual(["orchestrator", "automation"]);
	});

	it("reports a turn that failed, was stopped or is still running as that", () => {
		const work = sessionRailWork(snapshot(
			[turn("t1", { state: "failed", errorMessage: "Rate limited" }), turn("t2", { state: "interrupted" }), turn("t3", { state: "running", completedAt: undefined }), turn("t4", { state: "recovered" })],
			[message("m1", "t1"), message("m2", "t2"), message("m3", "t3"), message("m4", "t4")],
		));
		expect(work?.turns.map((entry) => entry.turn.state)).toEqual(["failed", "stopped", "running", "completed"]);
		expect(work?.turns[0]?.turn.error).toBe("Rate limited");
		expect(work?.turns[2]?.turn.ms).toBeUndefined();
	});

	it("does not count files when the agent reports no diff or the list was cut", () => {
		const work = sessionRailWork(snapshot(
			[turn("t1"), turn("t2", { diff: { files: [{ path: "a.ts", additions: 1, deletions: 0, status: "modified" }], truncated: true } })],
			[message("m1", "t1"), message("m2", "t2")],
		));
		expect(work?.turns.map((entry) => entry.turn.files)).toEqual([undefined, undefined]);
	});

	it("leaves out a turn an undo took back, one that never ran, a bare continue and a request with no loaded turn", () => {
		const work = sessionRailWork(snapshot(
			[turn("t1", { rolledBack: true }), turn("t2", { notRun: true }), turn("t3")],
			[message("m1", "t1"), message("m2", "t2"), message("m3", "t3", { continuation: true, text: "" }), message("m4", "gone")],
		));
		expect(work?.turns).toEqual([]);
	});

	it("lists where the agent stopped to ask, and whether it is still waiting", () => {
		const work = sessionRailWork(snapshot([turn("t1")], [
			activity("a1"),
			activity("a5", { turnId: "t1", status: "resolved" }),
			activity("a2", { activityKind: "user_input", status: "resolved", summary: "Which branch?" }),
			activity("a3", { status: "cancelled" }),
			activity("a4", { activityKind: "command", status: "completed", summary: "npm test" }),
		]));
		expect(work?.asks.find((ask) => ask.id === "a5")?.turnId).toBe("t1");
		expect(work?.asks.filter((ask) => ask.id !== "a5")).toEqual([
			{ id: "a1", at: "2026-10-20T09:03:00Z", kind: "approval", summary: "Run npm test", waiting: true, turnId: undefined },
			{ id: "a2", at: "2026-10-20T09:03:00Z", kind: "question", summary: "Which branch?", waiting: false, turnId: undefined },
		]);
	});
});
