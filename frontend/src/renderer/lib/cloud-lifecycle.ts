import type { WorkspaceSession } from "../types/workspace";

export type CloudLifecycleStage =
	| "paused_by_coder"
	| "paused"
	| "resuming_workspace"
	| "waiting_for_coder_agent"
	| "starting_ao_worker"
	| "restoring_agent"
	| "connected";

/** A stage where the sandbox is paused on purpose (no startup loader). */
export function isCloudPausedStage(stage: CloudLifecycleStage | undefined): stage is "paused" | "paused_by_coder" {
	return stage === "paused" || stage === "paused_by_coder";
}

/**
 * Derives the desktop's cloud lifecycle stage from the control-plane contract.
 * The backend remains authoritative for intent and observation; this function
 * only translates those provider-neutral facts into concise presentation.
 */
export function cloudLifecycleStage(session?: WorkspaceSession): CloudLifecycleStage | undefined {
	const lifecycle = session?.cloud;
	if (!lifecycle) return undefined;
	const desired = lifecycle.desiredState;
	const observed = lifecycle.observedState;

	// A paused sandbox reads as paused whatever runs it; only a Coder one names
	// Coder (B2C users never see a Coder session, and do not know the provider).
	if (desired === "paused" && observed === "stopped") {
		return lifecycle.sandboxProvider === "coder" ? "paused_by_coder" : "paused";
	}
	if (desired === "running" && (observed === "stopped" || observed === "restoring")) {
		return "resuming_workspace";
	}
	if (observed === "requested" || observed === "provisioning") {
		return "waiting_for_coder_agent";
	}
	if (observed === "bootstrapping") {
		return session.runtimeConnected ? "restoring_agent" : "starting_ao_worker";
	}
	// "failed" is transient: the reconciler backs off and retries the worker
	// install, so keep presenting startup progress rather than a blank pane.
	if (desired === "running" && observed === "failed") {
		return session.runtimeConnected ? "restoring_agent" : "starting_ao_worker";
	}
	if (observed === "running") {
		return session.runtimeConnected ? "connected" : "restoring_agent";
	}
	return undefined;
}

/**
 * A cloud session's startup problem, as far as the session pane presents it:
 * - "failed": AO gave up starting the worker (runtime "terminated"); the server
 *   explains why and the user can retry startup.
 * - "retrying": AO hit a startup problem but is still retrying in the
 *   background (any other runtime state, including the transient "failed");
 *   the lifecycle loader stays up with the reason as a note.
 * - "unavailable": the runtime was parked without a recorded startup reason;
 *   the pane explains that instead of rendering blank.
 */
export type CloudStartupProblem =
	| { phase: "failed"; message: string; code?: string; detail?: string }
	| { phase: "retrying"; message: string }
	| { phase: "unavailable"; message?: string };

// Only "terminated" means AO stopped trying; "failed" is retried with backoff.
const FINAL_RUNTIME_STATES = new Set(["terminated"]);

export function cloudStartupProblem(session?: WorkspaceSession): CloudStartupProblem | undefined {
	const lifecycle = session?.cloud;
	if (!lifecycle || session.runtimeConnected) return undefined;
	const runtimeState = lifecycle.runtimeState || lifecycle.observedState;
	const final = runtimeState !== undefined && FINAL_RUNTIME_STATES.has(runtimeState);
	const message = lifecycle.startupError?.message?.trim();
	if (message) {
		if (!final) return { phase: "retrying", message };
		const detail = lifecycle.startupError?.detail?.trim() || undefined;
		return { phase: "failed", message, code: lifecycle.startupError?.code, detail };
	}
	if (final) return { phase: "unavailable", message: lifecycle.runtimeError?.trim() || undefined };
	return undefined;
}
