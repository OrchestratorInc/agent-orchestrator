import { describe, expect, it } from "vitest";
import type { ControllerState } from "../chat/types";
import * as lifecycle from "./lifecycle";
import { cloudLifecycleStage, isResumable, stageLabel } from "./lifecycle";

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

	it("keeps a fresh bootstrap in the starting state after the runtime connects", () => {
		expect(cloudLifecycleStage({ cloud: { ...base, observedState: "bootstrapping" }, runtimeConnected: false }))
			.toBe("starting_ao_worker");
		expect(cloudLifecycleStage({ cloud: { ...base, observedState: "bootstrapping" }, runtimeConnected: true }))
			.toBe("starting_ao_worker");
	});

	it("is connected only once the runtime is attached", () => {
		expect(cloudLifecycleStage({ cloud: base, runtimeConnected: true })).toBe("connected");
		expect(cloudLifecycleStage({ cloud: base, runtimeConnected: false })).toBe("restoring_agent");
	});

	it("surfaces a failed sandbox even when the runtime connection flag is stale", () => {
		expect(cloudLifecycleStage({
			cloud: { ...base, observedState: "failed" },
			runtimeConnected: true,
		})).toBe("failed");
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
		expect(stageLabel("paused_by_coder")).toBe("Paused");
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
