import { describe, expect, it } from "vitest";
import type { ControllerState } from "../chat/types";
import * as lifecycle from "./lifecycle";
import { cloudLifecycleStage, cloudStartupProblem, cloudStartupErrorCopy, shouldShowCloudStartup, isResumable, stageLabel } from "./lifecycle";

const base = { sandboxProvider: "docker", desiredState: "running", observedState: "running" };

describe("cloudLifecycleStage", () => {
	it("is undefined when the session carries no lifecycle", () => {
		expect(cloudLifecycleStage({})).toBeUndefined();
	});

	it("reports a coder-paused sandbox", () => {
		expect(cloudLifecycleStage({
			cloud: { ...base, sandboxProvider: "coder", desiredState: "paused", observedState: "stopped" },
		})).toBe("paused_by_coder");
	});

	it("reports a sandbox coming back up", () => {
		expect(cloudLifecycleStage({
			cloud: { ...base, desiredState: "running", observedState: "stopped" },
		})).toBe("resuming_workspace");
	});

	it("waits for the agent while provisioning", () => {
		expect(cloudLifecycleStage({ cloud: { ...base, observedState: "provisioning" } }))
			.toBe("waiting_for_coder_agent");
	});

	it("matches desktop's bootstrap-to-agent attachment stage", () => {
		expect(cloudLifecycleStage({ cloud: { ...base, observedState: "bootstrapping" }, runtimeConnected: false }))
			.toBe("starting_ao_worker");
		expect(cloudLifecycleStage({ cloud: { ...base, observedState: "bootstrapping" }, runtimeConnected: true }))
			.toBe("restoring_agent");
	});

	it("is connected only once the runtime is attached", () => {
		expect(cloudLifecycleStage({ cloud: base, runtimeConnected: true })).toBe("connected");
		expect(cloudLifecycleStage({ cloud: base, runtimeConnected: false })).toBe("restoring_agent");
	});

	it("keeps transient failed startup in progress", () => {
		expect(cloudLifecycleStage({
			cloud: { ...base, observedState: "failed" },
			runtimeConnected: true,
		})).toBe("restoring_agent");
		expect(cloudLifecycleStage({ cloud: { ...base, observedState: "failed" }, runtimeConnected: false })).toBe("starting_ao_worker");
	});
	it("reports a provider-neutral pause without naming Coder", () => {
		const stage = cloudLifecycleStage({ cloud: { ...base, desiredState: "paused", observedState: "stopped" } });
		expect(stage).toBe("paused"); expect(isResumable(stage)).toBe(true); expect(stageLabel(stage!)).toBe("Paused");
	});
});

describe("startup problems", () => {
	const startupError = { code: "bootstrap_failed", message: "Worker could not start", at: "2026-10-13T00:00:00Z", detail: "Internal https://private.example/log" };
	it("distinguishes final startup from retries and preserves archive as a separate fact", () => {
		expect(cloudStartupProblem({ cloud: { runtimeState: "failed", startupError } })).toEqual({ phase: "retrying", message: startupError.message });
		expect(cloudStartupProblem({ cloud: { runtimeState: "terminated", startupError } })).toMatchObject({ phase: "failed", code: "bootstrap_failed" });
		expect(cloudStartupProblem({ cloud: { runtimeState: "terminated", runtimeError: "Runtime parked" } })).toEqual({ phase: "unavailable", message: "Runtime parked" });
		expect(cloudStartupProblem({ isTerminated: true, cloud: { runtimeState: "terminated", startupError } })).toBeUndefined();
		expect(cloudStartupProblem({ runtimeConnected: true, cloud: { runtimeState: "terminated", startupError } })).toBeUndefined();
	});
	it("omits internal detail and URLs for managed compute", () => {
		const problem = { phase: "failed" as const, message: "Could not reach https://private.example/start", detail: startupError.detail };
		expect(cloudStartupErrorCopy(problem, true).message).not.toContain("private.example");
		expect(cloudStartupErrorCopy(problem, true).detail).toBeUndefined();
		expect(cloudStartupErrorCopy(problem, false).detail).toBe(startupError.detail);
	});
	it("never replays creation for a revisit or invents it from unknown facts", () => {
		expect(shouldShowCloudStartup({ spawnIntent: false, stage: "connected", terminalReady: false })).toBe(false);
		expect(shouldShowCloudStartup({ spawnIntent: true, terminalReady: false })).toBe(false);
		expect(shouldShowCloudStartup({ spawnIntent: true, stage: "waiting_for_coder_agent", terminalReady: false })).toBe(true);
		expect(shouldShowCloudStartup({ spawnIntent: true, stage: "starting_ao_worker", startupProblem: { phase: "failed", message: "Stopped" }, terminalReady: false })).toBe(false);
	});
});

describe("isResumable", () => {
	// Only a paused session offers a resume button; the rest are already moving.
	it("is true only for a paused sandbox", () => {
		expect(isResumable("paused_by_coder")).toBe(true);
		expect(isResumable("resuming_workspace")).toBe(false);
		expect(isResumable("connected")).toBe(false);
		expect(isResumable(undefined)).toBe(false);
	});
});

describe("stageLabel", () => {
	it("gives every stage a phrase for the chat banner", () => {
		expect(stageLabel("paused_by_coder")).toBe("Paused by Coder");
		expect(stageLabel("resuming_workspace")).toBe("Resuming workspace…");
		expect(stageLabel("waiting_for_coder_agent")).toBe("Starting sandbox…");
		expect(stageLabel("starting_ao_worker")).toBe("Starting worker…");
		expect(stageLabel("restoring_agent")).toBe("Restoring agent…");
		expect(stageLabel("connected")).toBe("Connected");
	});
});

describe("cloud lifecycle header state", () => {
	it("does not show a connected dot for failed or paused sandboxes", () => {
		const headerState = (lifecycle as typeof lifecycle & {
			cloudHeaderControllerState?: (stage: lifecycle.CloudLifecycleStage | undefined, fallback: ControllerState) => ControllerState;
		}).cloudHeaderControllerState;
		expect(headerState?.("failed", "ready")).toBe("stopped");
		expect(headerState?.("paused_by_coder", "ready")).toBe("stopped");
	});

	it("shows transitional sandboxes as connecting and preserves connected state", () => {
		const headerState = (lifecycle as typeof lifecycle & {
			cloudHeaderControllerState?: (stage: lifecycle.CloudLifecycleStage | undefined, fallback: ControllerState) => ControllerState;
		}).cloudHeaderControllerState;
		expect(headerState?.("restoring_agent", "ready")).toBe("connecting");
		expect(headerState?.("connected", "busy")).toBe("busy");
		expect(headerState?.(undefined, "ready")).toBe("ready");
	});
});
