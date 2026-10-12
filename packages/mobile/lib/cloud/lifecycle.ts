import type { ControllerState } from "../chat/types";

/** Ported verbatim from frontend/src/renderer/lib/cloud-lifecycle.ts. */
export type CloudLifecycleStage =
	| "paused_by_coder"
	| "paused"
	| "resuming_workspace"
	| "waiting_for_coder_agent"
	| "starting_ao_worker"
	| "restoring_agent"
	| "failed"
	| "connected";

export type CloudLifecycleInput = {
	cloud?: import("../api").DashboardSession["cloud"];
	runtimeConnected?: boolean;
	isTerminated?: boolean;
	isTerminal?: boolean;
};

/**
 * The control plane stays authoritative for intent and observation; this only
 * translates those provider-neutral facts into something the phone can say.
 */
export function cloudLifecycleStage(session: CloudLifecycleInput): CloudLifecycleStage | undefined {
	const lifecycle = session.cloud;
	if (!lifecycle || session.isTerminated || session.isTerminal) return undefined;
	const { desiredState: desired, observedState: observed } = lifecycle;

	if (desired === "paused" && observed === "stopped") {
		return lifecycle.sandboxProvider === "coder" ? "paused_by_coder" : "paused";
	}
	if (cloudStartupProblem(session)?.phase === "failed" || cloudStartupProblem(session)?.phase === "unavailable") return "failed";
	if (desired === "running" && (observed === "stopped" || observed === "restoring")) {
		return "resuming_workspace";
	}
	if (observed === "requested" || observed === "provisioning") {
		return "waiting_for_coder_agent";
	}
	if (observed === "bootstrapping" || (desired === "running" && observed === "failed")) {
		return session.runtimeConnected ? "restoring_agent" : "starting_ao_worker";
	}
	if (observed === "running") {
		return session.runtimeConnected ? "connected" : "restoring_agent";
	}
	return undefined;
}

/** Only a paused sandbox offers a resume action; everything else is in motion. */
export function isResumable(stage: CloudLifecycleStage | undefined): boolean {
	return stage === "paused_by_coder" || stage === "paused";
}

export function stageLabel(stage: CloudLifecycleStage): string {
	switch (stage) {
		case "paused_by_coder": return "Paused by Coder";
		case "paused": return "Paused";
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
	if (stage === "failed" || isResumable(stage)) return "stopped";
	return "connecting";
}

export type CloudStartupProblem =
	| { phase: "failed"; message: string; code?: string; detail?: string }
	| { phase: "retrying"; message: string }
	| { phase: "unavailable"; message?: string };

export function cloudStartupProblem(session: CloudLifecycleInput): CloudStartupProblem | undefined {
	if (!session.cloud || session.runtimeConnected || session.isTerminated || session.isTerminal) return undefined;
	const lifecycle = session.cloud;
	const final = (lifecycle.runtimeState || lifecycle.observedState) === "terminated";
	const message = lifecycle.startupError?.message.trim();
	if (message) return final ? { phase: "failed", message, code: lifecycle.startupError?.code, detail: lifecycle.startupError?.detail?.trim() || undefined } : { phase: "retrying", message };
	if (final) return { phase: "unavailable", message: lifecycle.runtimeError?.trim() || undefined };
	return undefined;
}

export function cloudStartupErrorCopy(problem: CloudStartupProblem, managed: boolean): { message: string; detail?: string } {
	const message = problem.message || "The Cloud runtime is unavailable. Your session history is preserved.";
	return { message: managed ? message.replace(/https?:\/\/\S+/gi, "an internal service") : message,
		...(!managed && problem.phase === "failed" && problem.detail ? { detail: problem.detail } : {}) };
}

export function shouldShowCloudStartup(input: { spawnIntent: boolean; stage?: CloudLifecycleStage; startupProblem?: CloudStartupProblem; terminalReady: boolean }): boolean {
	return input.spawnIntent && !input.terminalReady && !!input.stage && input.stage !== "failed" && !isResumable(input.stage)
		&& (!input.startupProblem || input.startupProblem.phase === "retrying");
}
