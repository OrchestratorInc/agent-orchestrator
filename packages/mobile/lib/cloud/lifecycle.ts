import type { ControllerState } from "../chat/types";

/** Ported verbatim from frontend/src/renderer/lib/cloud-lifecycle.ts. */
export type CloudLifecycleStage =
	| "paused_by_coder"
	| "resuming_workspace"
	| "waiting_for_coder_agent"
	| "starting_ao_worker"
	| "restoring_agent"
	| "failed"
	| "connected";

export type CloudLifecycleInput = {
	cloud?: { sandboxProvider?: string; desiredState?: string; observedState?: string };
	runtimeConnected?: boolean;
};

/**
 * The control plane stays authoritative for intent and observation; this only
 * translates those provider-neutral facts into something the phone can say.
 */
export function cloudLifecycleStage(session: CloudLifecycleInput): CloudLifecycleStage | undefined {
	const lifecycle = session.cloud;
	if (!lifecycle) return undefined;
	const { desiredState: desired, observedState: observed } = lifecycle;

	if (observed === "failed") return "failed";
	if (lifecycle.sandboxProvider === "coder" && desired === "paused" && observed === "stopped") {
		return "paused_by_coder";
	}
	if (desired === "running" && (observed === "stopped" || observed === "restoring")) {
		return "resuming_workspace";
	}
	if (observed === "requested" || observed === "provisioning") {
		return "waiting_for_coder_agent";
	}
	if (observed === "bootstrapping") {
		return "starting_ao_worker";
	}
	if (observed === "running") {
		return session.runtimeConnected ? "connected" : "restoring_agent";
	}
	return undefined;
}

/** Only a paused sandbox offers a resume action; everything else is in motion. */
export function isResumable(stage: CloudLifecycleStage | undefined): boolean {
	return stage === "paused_by_coder";
}

export function stageLabel(stage: CloudLifecycleStage): string {
	switch (stage) {
		case "paused_by_coder": return "Paused by Coder";
		case "resuming_workspace": return "Resuming workspace…";
		case "waiting_for_coder_agent": return "Starting sandbox…";
		case "starting_ao_worker": return "Starting worker…";
		case "restoring_agent": return "Restoring agent…";
		case "failed": return "Sandbox unavailable";
		case "connected": return "Connected";
	}
}

export function cloudHeaderControllerState(
	stage: CloudLifecycleStage | undefined,
	fallback: ControllerState,
): ControllerState {
	if (!stage || stage === "connected") return fallback;
	if (stage === "failed" || stage === "paused_by_coder") return "stopped";
	return "connecting";
}
