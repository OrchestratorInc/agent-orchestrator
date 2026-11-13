import { describe, expect, it } from "vitest";
import { requestDockModel } from "../chat/requestDockModel";
import { groupConversationByTurn } from "../chat/timelineModel";
import { appendCloudEvents, cloudConversationSnapshot, cloudTurnDiff, runningCloudTurnId } from "./conversation";
import type { CloudClientEvent, CloudSession } from "./types";

const session = (overrides: Partial<CloudSession> = {}): CloudSession => ({
	id: "s1", orgId: "o", projectId: "p", kind: "worker", harness: "claude-code", displayName: "Task", branch: "ao/task",
	mode: "trusted", interfaceMode: "chat", activityState: "active", status: "working", runtimeConnected: true,
	isTerminated: false, createdAt: "2026-11-01T00:00:00Z", updatedAt: "2026-11-01T00:00:00Z", ...overrides,
});

let sequence = 0;
const event = (type: string, payload: Record<string, unknown>): CloudClientEvent => ({
	sessionId: "s1", sequence: ++sequence, type, payload, createdAt: `2026-11-01T00:00:${String(sequence).padStart(2, "0")}Z`,
});

describe("cloud conversation projection", () => {
	it("joins assistant deltas per turn and item and settles them when the turn ends", () => {
		sequence = 0;
		const events = [
			event("chat.user_message", { turnId: "t1", text: "Fix the bug" }),
			event("chat.turn_started", { turnId: "t1" }),
			event("chat.assistant_delta", { turnId: "t1", itemId: "a", text: "Looking" }),
			event("chat.assistant_delta", { turnId: "t1", itemId: "a", text: " now." }),
		];
		const live = cloudConversationSnapshot(session(), events);
		expect(live.controller.state).toBe("busy");
		expect(live.items.map((item) => item.kind === "message" ? [item.role, item.text, item.streaming] : null)).toEqual([
			["user", "Fix the bug", false],
			["assistant", "Looking now.", true],
		]);
		const done = cloudConversationSnapshot(session(), [...events, event("chat.turn_completed", { turnId: "t1" })]);
		expect(done.controller.state).toBe("ready");
		expect(done.turns[0].state).toBe("completed");
		expect(done.items[1]).toMatchObject({ streaming: false });
	});

	it("shows a just-sent turn as running before a worker claims it", () => {
		sequence = 0;
		const snapshot = cloudConversationSnapshot(session(), [event("chat.user_message", { turnId: "t1", text: "Go" })]);
		expect(runningCloudTurnId(snapshot)).toBe("t1");
	});

	it("tracks approvals until they are decided, and cancels them with their turn", () => {
		sequence = 0;
		const requested = [
			event("chat.user_message", { turnId: "t1", text: "Deploy" }),
			event("chat.turn_started", { turnId: "t1" }),
			event("chat.approval_requested", { turnId: "t1", requestId: "r1", summary: "Run deploy.sh", decisions: [{ id: "allow", label: "Allow" }, { id: "deny", label: "Deny" }, { bogus: true }] }),
		];
		const pending = cloudConversationSnapshot(session(), requested);
		expect(pending.items[1]).toMatchObject({ activityKind: "approval", status: "pending", requestId: "r1", decisions: [{ id: "allow", label: "Allow" }, { id: "deny", label: "Deny" }] });
		const decided = cloudConversationSnapshot(session(), [...requested, event("chat.approval_decided", { requestId: "r1", decision: "allow" })]);
		expect(decided.items[1]).toMatchObject({ status: "resolved" });
		const ended = cloudConversationSnapshot(session(), [...requested, event("chat.turn_interrupted", { turnId: "t1" })]);
		expect(ended.items[1]).toMatchObject({ status: "cancelled" });
	});

	it("merges streamed activity output and attaches the turn diff to file changes", () => {
		sequence = 0;
		const diff = "diff --git a/a.ts b/a.ts\n--- a/a.ts\n+++ b/a.ts\n@@ -1 +1,2 @@\n-old\n+new\n+more\n";
		const snapshot = cloudConversationSnapshot(session(), [
			event("chat.turn_started", { turnId: "t1" }),
			event("chat.activity", { turnId: "t1", activity: { id: "c1", kind: "command", status: "running", summary: "npm test", detail: { command: "npm test", outputDelta: "ok 1\n" } } }),
			event("chat.activity", { turnId: "t1", activity: { id: "c1", kind: "command", status: "completed", detail: { outputDelta: "ok 2\n" } } }),
			event("chat.activity", { turnId: "t1", activity: { id: "f1", kind: "file_change", status: "completed", summary: "Edited a.ts", detail: { files: [{ path: "a.ts", status: "modified", additions: 2, deletions: 1 }] } } }),
			event("chat.activity", { turnId: "t1", activity: { id: "d", kind: "turn_diff", detail: { diff } } }),
			event("chat.activity", { turnId: "t1", activity: { id: "x", kind: "unknown_kind" } }),
		]);
		expect(snapshot.items).toHaveLength(2);
		expect(snapshot.items[0]).toMatchObject({ activityKind: "command", status: "completed", summary: "npm test", detail: { output: "ok 1\nok 2\n", command: "npm test" } });
		expect(snapshot.items[1]).toMatchObject({ activityKind: "file_change", detail: { files: [{ path: "a.ts", patch: expect.stringContaining("+new") }] } });
		expect(snapshot.turns[0].diff?.files).toEqual([expect.objectContaining({ path: "a.ts", additions: 2, deletions: 1 })]);
	});

	it("reads the worker's plan, context use, subagent parents, exit codes and approval subject", () => {
		sequence = 0;
		const plan = (status: string) => ({ explanation: "Ship it", steps: [{ text: "Write", status: "completed" }, { text: "Test", status }, { bogus: 1 }] });
		const snapshot = cloudConversationSnapshot(session(), [
			event("chat.turn_started", { turnId: "t1" }),
			event("chat.activity", { turnId: "t1", activity: { id: "p", kind: "turn_plan", detail: { plan: plan("in_progress") } } }),
			event("chat.activity", { turnId: "t1", activity: { id: "p", kind: "turn_plan", detail: { plan: plan("completed") } } }),
			event("chat.activity", { turnId: "t1", activity: { id: "u", kind: "usage", detail: { usage: { contextUsed: 50_000, contextWindow: 200_000, inputTokens: 900, outputTokens: 80, cachedTokens: -1 } } } }),
			event("chat.activity", { turnId: "t1", activity: { id: "u2", kind: "usage", detail: { usage: { bogus: true } } } }),
			event("chat.activity", { turnId: "t1", activity: { id: "task-1", kind: "mcp_tool", status: "running", summary: "Explore" } }),
			event("chat.activity", { turnId: "t1", activity: { id: "c1", kind: "command", status: "failed", summary: "npm test", detail: { command: "npm test", exitCode: 1, parentProviderItemId: "task-1" } } }),
			event("chat.approval_requested", { turnId: "t1", requestId: "r1", subjectKind: "file_change", decisions: [{ id: "a", label: "Allow" }] }),
			event("chat.approval_requested", { turnId: "t1", requestId: "r2", subjectKind: "nonsense", decisions: [] }),
		]);
		expect(snapshot.turns[0].plan).toEqual({ explanation: "Ship it", steps: [{ text: "Write", status: "completed" }, { text: "Test", status: "completed" }] });
		expect(snapshot.usage).toMatchObject({ contextUsed: 50_000, contextWindow: 200_000, inputTokens: 900, cachedTokens: 0 });
		expect(snapshot.items.find((item) => item.id.endsWith(":task-1"))).toMatchObject({ providerItemId: "task-1" });
		expect(snapshot.items.find((item) => item.id.endsWith(":c1"))).toMatchObject({ providerItemId: "c1", detail: { exitCode: 1, parentProviderItemId: "task-1" } });
		expect(snapshot.items.find((item) => item.id === "cloud-approval-r1")).toMatchObject({ detail: { subjectKind: "file_change" } });
		expect((snapshot.items.find((item) => item.id === "cloud-approval-r2") as { detail?: { subjectKind?: string } }).detail?.subjectKind).toBeUndefined();
		expect(requestDockModel(snapshot, { approval: false, input: false })?.pages[0].question).toBe("Do you want to allow these file changes?");
	});

	it("has no context use until the worker reports one", () => {
		sequence = 0;
		expect(cloudConversationSnapshot(session(), [event("chat.turn_started", { turnId: "t1" })]).usage).toBeUndefined();
	});

	it("marks a running turn steerable only when its worker says so", () => {
		sequence = 0;
		const base = [event("chat.user_message", { turnId: "t1", text: "Go" }), event("chat.turn_started", { turnId: "t1" })];
		expect(cloudConversationSnapshot(session(), base).capabilities).toEqual([]);
		expect(cloudConversationSnapshot(session(), [...base, event("chat.turn_capabilities", { turnId: "t1", steering: true })]).capabilities).toEqual(["steer"]);
	});

	it("shows automation prompts by their display text and drops Codex's stdin notice", () => {
		sequence = 0;
		const snapshot = cloudConversationSnapshot(session({ harness: "codex" }), [
			event("chat.user_message", { turnId: "t1", text: "raw", displayText: "CI failed on main", origin: "automation", senderLabel: "Orchestrator" }),
			event("chat.turn_started", { turnId: "t1" }),
			event("chat.assistant_delta", { turnId: "t1", text: "Reading additional input from stdin..." }),
		]);
		expect(snapshot.items).toEqual([expect.objectContaining({ origin: "automation", text: "CI failed on main", senderLabel: "Orchestrator" })]);
	});

	it("folds setup step events into one row", () => {
		sequence = 0;
		const snapshot = cloudConversationSnapshot(session(), [
			event("workspace.script.step", { runId: "r", phase: "setup", step: 1, total: 2, status: "succeeded", command: "npm ci" }),
			event("workspace.script.step", { runId: "r", phase: "setup", step: 2, total: 2, status: "succeeded", command: "npm run build" }),
			event("workspace.setup", { status: "succeeded" }),
		]);
		expect(snapshot.items).toEqual([expect.objectContaining({ summary: "Setup passed", status: "completed" })]);
	});

	it("reads as hibernated, not working, while its sandbox is paused", () => {
		sequence = 0;
		const paused = session({ desiredState: "paused", observedState: "stopped", runtimeConnected: false });
		const snapshot = cloudConversationSnapshot(paused, [event("chat.user_message", { turnId: "t1", text: "Go" })]);
		expect(snapshot.controller.state).toBe("hibernated");
		expect(runningCloudTurnId(snapshot)).toBeUndefined();
		expect(cloudConversationSnapshot(session({ observedState: "provisioning", runtimeConnected: false }), []).controller.state).toBe("connecting");
	});

	it("is stopped once the session is archived", () => {
		expect(cloudConversationSnapshot(session({ isTerminated: true }), []).controller.state).toBe("stopped");
	});

	it("appends only newer events, in order", () => {
		sequence = 0;
		const a = event("chat.user_message", { text: "a" });
		const b = event("chat.user_message", { text: "b" });
		const c = event("chat.user_message", { text: "c" });
		const held = appendCloudEvents([a, b], [b, c]);
		expect(held.map((item) => item.sequence)).toEqual([1, 2, 3]);
		const same = [a, b];
		expect(appendCloudEvents(same, [a])).toBe(same);
	});

	it("counts additions and deletions per file and follows renames", () => {
		const files = cloudTurnDiff("diff --git a/old.ts b/new.ts\nrename from old.ts\nrename to new.ts\n@@ -1 +1 @@\n-x\n+y\ndiff --git a/n.ts b/n.ts\nnew file mode 100644\n+++ b/n.ts\n@@ -0,0 +1 @@\n+z\n");
		expect(files.map(({ path, status, additions, deletions, oldPath }) => ({ path, status, additions, deletions, oldPath }))).toEqual([
			{ path: "new.ts", status: "renamed", additions: 1, deletions: 1, oldPath: "old.ts" },
			{ path: "n.ts", status: "added", additions: 1, deletions: 0, oldPath: undefined },
		]);
	});

	// The chat opens on its newest page, which can begin inside an earlier turn:
	// a prompt queued while a long turn ran sits inside that turn's events.
	it("reads a page that starts inside a turn as the newest part of a longer history", () => {
		sequence = 40;
		const events = [
			event("chat.user_message", { turnId: "t2", text: "Then add tests" }),
			event("chat.assistant_delta", { turnId: "t1", itemId: "a", text: "Still working" }),
			event("chat.turn_completed", { turnId: "t1" }),
			event("chat.turn_started", { turnId: "t2" }),
		];
		const partial = cloudConversationSnapshot(session(), events, { partial: true });
		expect(partial.hasMoreBefore).toBe(true);
		expect(partial.turns.find((turn) => turn.id === "t1")).toMatchObject({ state: "completed", startedAt: events[1].createdAt });
		// t1 began above the page, so it reads before the prompt queued inside it.
		expect(groupConversationByTurn(partial).map((group) => group.turnId)).toEqual(["t1", "t2"]);

		// Read as a whole history, the same events would put t1 after t2's prompt.
		const whole = cloudConversationSnapshot(session(), events);
		expect(whole.hasMoreBefore).toBe(false);
		expect(groupConversationByTurn(whole).map((group) => group.turnId)).toEqual(["t2", "t1"]);
	});
});
