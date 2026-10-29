import { describe, expect, it } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { cloudChatStartingInPlace, cloudLifecycleStage, cloudStartupProblem, isCloudPausedStage } from "./cloud-lifecycle";

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

	it("names Coder only for a paused Coder sandbox", () => {
		const freestyle = session("paused", "stopped");
		freestyle.cloud = { ...freestyle.cloud!, sandboxProvider: "freestyle" };
		expect(cloudLifecycleStage(freestyle)).toBe("paused");
		expect(isCloudPausedStage("paused")).toBe(true);
		expect(isCloudPausedStage("paused_by_coder")).toBe(true);
		expect(isCloudPausedStage("connected")).toBe(false);
	});

	it("does not invent lifecycle state for local sessions", () => {
		const local = session("running", "running", true);
		delete local.cloud;
		expect(cloudLifecycleStage(local)).toBeUndefined();
	});
});

describe("cloudChatStartingInPlace", () => {
	const freestyleChat = (desired: string, observed: string, connected = false): WorkspaceSession => {
		const base = session(desired, observed, connected);
		return { ...base, mode: "chat", cloud: { ...base.cloud!, sandboxProvider: "freestyle" } };
	};

	it.each([
		["running", "requested"],
		["running", "provisioning"],
		["running", "bootstrapping"],
		["running", "stopped"],
		["running", "restoring"],
	] as const)("holds a Freestyle Chat session on its conversation while %s/%s", (desired, observed) => {
		expect(cloudChatStartingInPlace(freestyleChat(desired, observed))).toBe(true);
	});

	it("lets go once the worker connects, and while the session is paused", () => {
		expect(cloudChatStartingInPlace(freestyleChat("running", "bootstrapping", true))).toBe(false);
		expect(cloudChatStartingInPlace(freestyleChat("paused", "stopped"))).toBe(false);
		// A session being deleted or archived is going away, not getting ready.
		expect(cloudChatStartingInPlace(freestyleChat("deleted", "stopped"))).toBe(false);
		expect(cloudChatStartingInPlace(freestyleChat("deleted", "running"))).toBe(false);
	});

	it("leaves other providers and terminal sessions to the startup screen", () => {
		expect(cloudChatStartingInPlace({ ...freestyleChat("running", "provisioning"), mode: "tui" })).toBe(false);
		const coder = freestyleChat("running", "provisioning");
		expect(cloudChatStartingInPlace({ ...coder, cloud: { ...coder.cloud!, sandboxProvider: "coder" } })).toBe(false);
	});
});

describe("cloudStartupProblem", () => {
	const startupError = { code: "workspace_not_ready", message: "Your Coder workspace wasn't ready after 20 minutes.", at: "2026-10-08T00:00:00Z" };
	function withRuntime(runtimeState: string | undefined, extra: Partial<NonNullable<WorkspaceSession["cloud"]>> = {}, connected = false): WorkspaceSession {
		const value = session("running", runtimeState ?? "bootstrapping", connected);
		value.cloud = { ...value.cloud!, runtimeState, ...extra };
		return value;
	}

	it("reports a workspace the worker could not prepare even though the worker is connected", () => {
		const setupError = {
			code: "project_setup_failed",
			message: "Setup step 1 failed: exit status 3.",
			at: "2026-10-13T18:31:39Z",
			detail: "missing secret\n",
		};
		expect(cloudStartupProblem(withRuntime("running", { startupError: setupError }, true))).toEqual({
			phase: "failed",
			message: setupError.message,
			code: "project_setup_failed",
			detail: "missing secret",
			at: "2026-10-13T18:31:39Z",
		});
		// A control-plane startup error still disappears once the worker connects.
		expect(cloudStartupProblem(withRuntime("running", { startupError }, true))).toBeUndefined();
	});

	it("reports a working folder the template already created even though the worker is connected", () => {
		const occupied = {
			code: "working_directory_occupied",
			message: "~/work/alice already exists (created by your Coder template). Untick it in the project's Template options, or turn on 'Run the agent as the workspace user' to reuse it. Start a new session after changing it.",
			at: "2026-10-20T10:00:00Z",
		};
		expect(cloudStartupProblem(withRuntime("running", { startupError: occupied }, true))).toEqual({
			phase: "failed", message: occupied.message, code: "working_directory_occupied", detail: undefined, at: occupied.at,
		});
	});

	it("reports a terminal agent that could not start in the ready workspace even though the worker is connected", () => {
		const agentStart = {
			code: "agent_start_failed",
			message: "AO couldn't start the coding agent in the terminal. Select Retry on this session to try again.",
			at: "2026-10-20T10:00:00Z",
			detail: "load coding-agent credential: control plane unavailable",
		};
		expect(cloudStartupProblem(withRuntime("running", { startupError: agentStart }, true))).toEqual({
			phase: "failed", message: agentStart.message, code: "agent_start_failed", detail: agentStart.detail, at: agentStart.at,
		});
	});

	it("reports a final startup failure once AO gives up", () => {
		expect(cloudStartupProblem(withRuntime("terminated", { startupError }))).toEqual({
			phase: "failed",
			message: startupError.message,
			code: startupError.code,
		});
	});

	it("carries bring-your-own diagnostics with a final failure", () => {
		const withDetail = { ...startupError, detail: "coder build log:\nterraform: quota exceeded\n" };
		expect(cloudStartupProblem(withRuntime("terminated", { startupError: withDetail }))).toMatchObject({
			phase: "failed",
			detail: "coder build log:\nterraform: quota exceeded",
		});
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
