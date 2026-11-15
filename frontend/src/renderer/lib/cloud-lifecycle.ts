import type { WorkspaceSession } from "../types/workspace";
import type { CloudCpClient } from "./cloud-cp";
import { FREESTYLE_PROVIDER } from "./cloud-offering";

export type CloudLifecycleStage =
	| "paused_by_coder"
	| "paused"
	| "resuming_workspace"
	| "waiting_for_coder_agent"
	| "starting_ao_worker"
	| "restoring_agent"
	| "connected";

export function isCloudPausedStage(stage: CloudLifecycleStage | undefined): stage is "paused" | "paused_by_coder" {
	return stage === "paused" || stage === "paused_by_coder";
}

const STARTING_SANDBOX_STATES = new Set(["requested", "provisioning", "bootstrapping", "stopped", "restoring"]);

export function cloudChatStartingInPlace(session?: WorkspaceSession): boolean {
	const cloud = session?.cloud;
	if (session?.mode !== "chat" || cloud?.sandboxProvider !== FREESTYLE_PROVIDER || session.runtimeConnected) return false;
	return cloud.desiredState === "running" && STARTING_SANDBOX_STATES.has(cloud.observedState ?? "");
}

export function cloudLifecycleStage(session?: WorkspaceSession): CloudLifecycleStage | undefined {
	const lifecycle = session?.cloud;
	if (!lifecycle) return undefined;
	const desired = lifecycle.desiredState;
	const observed = lifecycle.observedState;

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
	if (desired === "running" && observed === "failed") {
		return session.runtimeConnected ? "restoring_agent" : "starting_ao_worker";
	}
	if (observed === "running") {
		return session.runtimeConnected ? "connected" : "restoring_agent";
	}
	return undefined;
}

export type CloudStartupProblem =
	| { phase: "failed"; message: string; code?: string; detail?: string; at?: string }
	| { phase: "retrying"; message: string }
	| { phase: "unavailable"; message?: string };

const FINAL_RUNTIME_STATES = new Set(["terminated"]);

export const WORKSPACE_STARTUP_ERROR_CODES: ReadonlySet<string> = new Set([
	"repository_checkout_failed",
	"working_directory_unusable",
	"working_directory_occupied",
	"project_setup_failed",
	"agent_start_failed",
]);

export function isWorkspaceStartupError(code?: string): boolean {
	return code !== undefined && WORKSPACE_STARTUP_ERROR_CODES.has(code);
}

export function cloudStartupProblem(session?: WorkspaceSession): CloudStartupProblem | undefined {
	const lifecycle = session?.cloud;
	if (!lifecycle) return undefined;
	const workspaceError = lifecycle.startupError;
	if (workspaceError && isWorkspaceStartupError(workspaceError.code) && workspaceError.message?.trim()) {
		const detail = workspaceError.detail?.trim() || undefined;
		return { phase: "failed", message: workspaceError.message.trim(), code: workspaceError.code, detail, at: workspaceError.at };
	}
	if (session.runtimeConnected) return undefined;
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

const resumesInFlight = new Map<string, Promise<unknown>>();

export function resumeCloudSession(client: Pick<CloudCpClient, "resumeSession">, orgId: string, sessionId: string): Promise<unknown> {
	const key = `${orgId}/${sessionId}`;
	const pending = resumesInFlight.get(key);
	if (pending) return pending;
	const request = client.resumeSession(orgId, sessionId).finally(() => resumesInFlight.delete(key));
	resumesInFlight.set(key, request);
	return request;
}
