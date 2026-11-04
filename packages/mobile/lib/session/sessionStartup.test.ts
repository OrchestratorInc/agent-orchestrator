import { describe, expect, it } from "vitest";
import type { SessionProvisionStep } from "../api";
import type { ConversationSnapshot, ConversationTurn } from "../chat/types";
import {
	formatStepDuration,
	newlyFinishedSteps,
	sessionStartupView,
	startupVisible,
	stepElapsedMs,
	stepLabel,
	stepStatus,
} from "./sessionStartup";

const step = (id: SessionProvisionStep["id"], status: SessionProvisionStep["status"], over: Partial<SessionProvisionStep> = {}): SessionProvisionStep => ({ id, status, ...over });
const turn = (id: string, state: ConversationTurn["state"]) => ({ id, state }) as ConversationTurn;
const snapshot = (turns: ConversationTurn[], hasMoreBefore = false, controller: ConversationSnapshot["controller"]["state"] = "connecting") => ({ harness: "claude-code", turns, hasMoreBefore, controller: { state: controller } });

const running = [step("fetch", "done"), step("worktree", "running"), step("setup", "pending"), step("agent", "pending")];
const finished = [step("fetch", "done"), step("worktree", "done"), step("setup", "done"), step("agent", "done")];

describe("sessionStartupView", () => {
	it("shows provisioning sessions with the opening turn read as sent", () => {
		const view = sessionStartupView({ provisionState: "provisioning", provisionSteps: running, branch: "ao/x/root" }, snapshot([turn("t1", "queued")]));
		expect(view).toMatchObject({ failed: false, steps: running, agentName: "Claude Code", openingTurnId: "t1", namingBranch: true });
		// The provisional branch stays private until the worktree step lands.
		expect(view?.branch).toBeUndefined();
	});

	it("reveals the branch once the worktree step is done", () => {
		const steps = [step("fetch", "done"), step("worktree", "done"), step("setup", "running"), step("agent", "pending")];
		const view = sessionStartupView({ provisionState: "provisioning", provisionSteps: steps, branch: "ao/x/root" }, snapshot([turn("t1", "queued")]));
		expect(view).toMatchObject({ branch: "ao/x/root", namingBranch: false });
	});

	it("hides the temporary branch until the generated name lands", () => {
		const view = sessionStartupView({ provisionState: "ready", provisionSteps: finished, branch: "ao/x/root", branchNaming: true }, snapshot([turn("t1", "running")]));
		expect(view).toMatchObject({ steps: finished, branch: undefined, namingBranch: true });
		expect(startupVisible(view, snapshot([turn("t1", "running")]))).toBe(false);
		expect(sessionStartupView({ provisionState: "failed", provisionSteps: finished, branch: "ao/x/root", branchNaming: true }, snapshot([turn("t1", "failed")]))).toMatchObject({ branch: "ao/x/root", namingBranch: false });
	});

	it("keeps a ready session's checklist while the opening turn is still live", () => {
		expect(sessionStartupView({ provisionState: "ready", provisionSteps: finished }, snapshot([turn("t1", "running")]))?.openingTurnId).toBe("t1");
		expect(sessionStartupView({ provisionState: "ready", provisionSteps: finished }, snapshot([turn("t1", "completed")]))).toBeUndefined();
	});

	it("keeps a promptless start's checklist until the agent reports connected", () => {
		const view = sessionStartupView({ provisionState: "ready", provisionSteps: finished }, snapshot([]));
		expect(view).toMatchObject({ failed: false, openingTurnId: undefined });
		expect(startupVisible(view, snapshot([]))).toBe(true);
		expect(sessionStartupView({ provisionState: "ready", provisionSteps: finished }, snapshot([], false, "ready"))).toBeUndefined();
		// A session with history reconnecting after a restart is not a start.
		expect(sessionStartupView({ provisionState: "ready", provisionSteps: finished }, snapshot([], true))).toBeUndefined();
	});

	it("never treats a later page's first turn as the opening one", () => {
		expect(sessionStartupView({ provisionState: "ready", provisionSteps: finished }, snapshot([turn("t9", "running")], true))).toBeUndefined();
	});

	it("reports a failed start with its error and no naming shimmer", () => {
		const view = sessionStartupView({ provisionState: "failed", provisionSteps: running, provisionError: "setup exited 1" }, snapshot([turn("t1", "queued")]));
		expect(view).toMatchObject({ failed: true, error: "setup exited 1", namingBranch: false });
	});

	it("keeps a failed opening turn as the checklist's anchor so Retry renders", () => {
		// A provider rejection fails the opening turn as well as provisioning
		// (backend: TestAsyncChatSpawnRealDrainFailureStaysRetryable).
		const failedTurn = snapshot([turn("t1", "failed")], false, "stopped");
		const view = sessionStartupView({ provisionState: "failed", provisionSteps: running, provisionError: "provider rejected" }, failedTurn);
		expect(view).toMatchObject({ failed: true, openingTurnId: "t1" });
		expect(startupVisible(view, failedTurn)).toBe(true);
		// Once the start succeeds, a finished first turn no longer anchors anything.
		expect(sessionStartupView({ provisionState: "ready", provisionSteps: finished }, snapshot([turn("t1", "failed")], false, "ready"))).toBeUndefined();
	});

	it("shows a provisioning session from a daemon too old to send steps", () => {
		expect(sessionStartupView({ provisionState: "provisioning" }, undefined)).toMatchObject({ steps: [], agentName: "the agent" });
		expect(sessionStartupView({ provisionState: "ready" }, snapshot([turn("t1", "running")]))).toBeUndefined();
	});
});

describe("startupVisible", () => {
	it("hands over to the Working row once every step is done and the opening turn runs", () => {
		const view = sessionStartupView({ provisionState: "ready", provisionSteps: finished }, snapshot([turn("t1", "queued")]));
		expect(startupVisible(view, snapshot([turn("t1", "queued")]))).toBe(true);
		expect(startupVisible(view, snapshot([turn("t1", "running")]))).toBe(false);
	});

	it("keeps a failed start visible", () => {
		const view = sessionStartupView({ provisionState: "failed", provisionSteps: running }, snapshot([turn("t1", "queued")]));
		expect(startupVisible(view, snapshot([turn("t1", "queued")]))).toBe(true);
	});
});

describe("steps", () => {
	it("marks the stage a failed start stopped in as failed", () => {
		expect(stepStatus(step("setup", "running"), true)).toBe("failed");
		expect(stepStatus(step("setup", "running"), false)).toBe("running");
		expect(stepStatus(step("fetch", "done"), true)).toBe("done");
	});

	it("names the agent step after the harness", () => {
		expect(stepLabel("agent", "Codex")).toBe("Start Codex");
		expect(stepLabel("worktree", "Codex")).toBe("Create worktree");
	});

	it("measures elapsed time from daemon timestamps", () => {
		const start = "2026-10-19T00:00:00.000Z";
		expect(stepElapsedMs(step("fetch", "done", { startedAt: start, endedAt: "2026-10-19T00:00:02.300Z" }), 0)).toBe(2300);
		expect(stepElapsedMs(step("fetch", "running", { startedAt: start }), Date.parse(start) + 4000)).toBe(4000);
		expect(stepElapsedMs(step("fetch", "pending"), 0)).toBeUndefined();
	});

	it("formats durations like the desktop", () => {
		expect(formatStepDuration(2300)).toBe("2.3s");
		expect(formatStepDuration(2300, true)).toBe("2s");
		expect(formatStepDuration(14_200)).toBe("14s");
		expect(formatStepDuration(63_000)).toBe("1m 3s");
	});

	it("reports only steps that finished since the last read", () => {
		expect(newlyFinishedSteps(undefined, finished)).toEqual([]);
		expect(newlyFinishedSteps(running, [step("fetch", "done"), step("worktree", "done"), step("setup", "running"), step("agent", "pending")])).toEqual(["worktree"]);
	});
});
