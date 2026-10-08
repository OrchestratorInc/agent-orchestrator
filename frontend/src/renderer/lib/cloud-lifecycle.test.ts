import { describe, expect, it } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { cloudLifecycleStage, cloudStartupProblem } from "./cloud-lifecycle";

function session(
	desiredState: string,
	observedState: string,
	runtimeConnected = false,
): WorkspaceSession {
	return {
		id: "session-1",
		workspaceId: "project-1",
		workspaceName: "cloud-project",
		title: "Cloud worker",
		provider: "claude-code",
		status: "working",
		updatedAt: "2026-09-01T00:00:00Z",
		prs: [],
		runtimeConnected,
		cloud: { orgId: "org-1", sandboxProvider: "coder", desiredState, observedState },
	};
}

describe("cloudLifecycleStage", () => {
	it.each([
		["paused", "stopped", false, "paused_by_coder"],
		["running", "stopped", false, "resuming_workspace"],
		["running", "restoring", false, "resuming_workspace"],
		["running", "provisioning", false, "waiting_for_coder_agent"],
		["running", "bootstrapping", false, "starting_ao_worker"],
		["running", "bootstrapping", true, "restoring_agent"],
		["running", "running", false, "restoring_agent"],
		["running", "running", true, "connected"],
		["running", "failed", false, "starting_ao_worker"],
	] as const)(
		"maps %s/%s (connected=%s) to %s",
		(desired, observed, connected, expected) => {
			expect(cloudLifecycleStage(session(desired, observed, connected))).toBe(expected);
		},
	);

	it("does not invent lifecycle state for local sessions", () => {
		const local = session("running", "running", true);
		delete local.cloud;
		expect(cloudLifecycleStage(local)).toBeUndefined();
	});
});

describe("cloudStartupProblem", () => {
	const startupError = { code: "workspace_not_ready", message: "Your Coder workspace wasn't ready after 20 minutes.", at: "2026-10-08T00:00:00Z" };
	function withRuntime(runtimeState: string | undefined, extra: Partial<NonNullable<WorkspaceSession["cloud"]>> = {}, connected = false): WorkspaceSession {
		const value = session("running", runtimeState ?? "bootstrapping", connected);
		value.cloud = { ...value.cloud!, runtimeState, ...extra };
		return value;
	}

	it("reports a final startup failure once AO gives up", () => {
		expect(cloudStartupProblem(withRuntime("terminated", { startupError }))).toEqual({ phase: "failed", message: startupError.message });
	});

	it.each(["bootstrapping", "provisioning", "failed"])("keeps a startup error as a retrying note while AO is still trying (%s)", (runtimeState) => {
		expect(cloudStartupProblem(withRuntime(runtimeState, { startupError }))).toEqual({ phase: "retrying", message: startupError.message });
	});

	it("falls back to the runtime error when the runtime was parked without a startup reason", () => {
		expect(cloudStartupProblem(withRuntime("terminated", { runtimeError: "sandbox deleted" }))).toEqual({ phase: "unavailable", message: "sandbox deleted" });
		expect(cloudStartupProblem(withRuntime("failed"))).toBeUndefined();
	});

	it("uses the observed state when the runtime state is absent", () => {
		const value = session("running", "terminated");
		expect(cloudStartupProblem(value)).toEqual({ phase: "unavailable", message: undefined });
	});

	it("reports nothing for a connected, healthy, or local session", () => {
		expect(cloudStartupProblem(withRuntime("terminated", { startupError }, true))).toBeUndefined();
		expect(cloudStartupProblem(withRuntime("running"))).toBeUndefined();
		const local = session("running", "terminated");
		delete local.cloud;
		expect(cloudStartupProblem(local)).toBeUndefined();
	});
});
