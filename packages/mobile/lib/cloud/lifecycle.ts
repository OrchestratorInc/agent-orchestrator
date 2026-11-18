import type { CloudSession } from "./types";

/**
 * Where a cloud session's sandbox is, translated from the control plane's
 * provider-neutral desired/observed states (the desktop's cloudLifecycleStage,
 * frontend/src/renderer/lib/cloud-lifecycle.ts). The backend stays
 * authoritative; this only names the facts for a person.
 */
export type CloudLifecycleStage = "paused" | "resuming" | "provisioning" | "starting" | "restoring" | "connected" | "archived";

export function cloudLifecycleStage(session: CloudSession): CloudLifecycleStage | undefined {
	if (session.isTerminated) return "archived";
	const desired = session.desiredState;
	const observed = session.observedState;
	if (desired === "paused" && observed === "stopped") return "paused";
	if (desired === "running" && (observed === "stopped" || observed === "restoring")) return "resuming";
	if (observed === "requested" || observed === "provisioning") return "provisioning";
	if (observed === "bootstrapping") return session.runtimeConnected ? "restoring" : "starting";
	// "failed" is transient: the reconciler retries the worker install.
	if (desired === "running" && observed === "failed") return session.runtimeConnected ? "restoring" : "starting";
	if (observed === "running") return session.runtimeConnected ? "connected" : "restoring";
	return session.runtimeConnected ? "connected" : undefined;
}

export function cloudStageLabel(stage: CloudLifecycleStage | undefined): string | undefined {
	switch (stage) {
		case "paused": return "Paused";
		case "resuming": return "Waking up";
		case "provisioning": return "Creating sandbox";
		case "starting": return "Starting agent";
		case "restoring": return "Reconnecting";
		case "archived": return "Archived";
		default: return undefined;
	}
}

/**
 * The title of the chat's banner for the sandbox's stage, or none.
 *
 * "restoring" is a running sandbox whose runtime link is not up, which the
 * session row reports a few seconds behind the live chat. As the desktop reads
 * it (SessionView), an agent that is working, or that this view has already
 * seen connected, is not held up by it, so there is no banner. Otherwise a
 * session with earlier turns is reconnecting, and a new one is still starting:
 * the gap between the sandbox running and the agent checking in is part of its
 * first start, not a reconnect.
 */
export function cloudChatStageTitle(
	stage: CloudLifecycleStage | undefined,
	{ agentLive, hasHistory }: { agentLive: boolean; hasHistory: boolean },
): string | undefined {
	if (!stage || stage === "connected") return undefined;
	if (stage === "restoring") return agentLive ? undefined : hasHistory ? "Reconnecting" : "Starting agent";
	return cloudStageLabel(stage);
}

/** Startup errors the worker reports after checking in; the agent won't start until a retry. */
const WORKSPACE_STARTUP_ERRORS = new Set([
	"repository_checkout_failed",
	"working_directory_unusable",
	"working_directory_occupied",
	"project_setup_failed",
	"agent_start_failed",
]);

export type CloudStartupProblem =
	| { phase: "failed"; message: string; detail?: string }
	| { phase: "retrying"; message: string };

/** A start that failed (retry is offered) or is being retried (the note shows). */
export function cloudStartupProblem(session: CloudSession): CloudStartupProblem | undefined {
	if (session.isTerminated) return undefined;
	const error = session.startupError;
	const message = error?.message?.trim();
	if (error && message && WORKSPACE_STARTUP_ERRORS.has(error.code)) return { phase: "failed", message, detail: error.detail?.trim() || undefined };
	if (session.runtimeConnected || !message) return undefined;
	const runtime = session.runtimeState || session.observedState;
	return runtime === "terminated" ? { phase: "failed", message, detail: error?.detail?.trim() || undefined } : { phase: "retrying", message };
}

/** A sandbox paused on purpose: opening it wakes it. */
export function isCloudPaused(session: CloudSession): boolean {
	return cloudLifecycleStage(session) === "paused";
}

export type CloudStatusTone = "working" | "attention" | "done" | "idle" | "failed";

/**
 * The phrase and tone a row shows. Status comes from the control plane's
 * derivation (the public contract.DeriveStatus); the sandbox stage wins while
 * the session is not yet usable, because that is what the person is waiting on.
 */
export function cloudSessionStatus(session: CloudSession): { label: string; tone: CloudStatusTone } {
	if (session.isTerminated) return { label: session.status === "merged" ? "Merged" : "Archived", tone: "done" };
	const problem = cloudStartupProblem(session);
	if (problem?.phase === "failed") return { label: "Failed to start", tone: "failed" };
	const stage = cloudLifecycleStage(session);
	if (stage && stage !== "connected") {
		const label = cloudStageLabel(stage) ?? "Starting";
		return { label, tone: stage === "paused" ? "idle" : "working" };
	}
	switch (session.status) {
		case "working": return { label: "Working", tone: "working" };
		case "needs_input": return { label: "Needs input", tone: "attention" };
		case "ci_failed": return { label: "CI failed", tone: "failed" };
		case "changes_requested": return { label: "Changes requested", tone: "attention" };
		case "review_pending": return { label: "Review pending", tone: "idle" };
		case "approved": return { label: "Approved", tone: "done" };
		case "mergeable": return { label: "Ready to merge", tone: "done" };
		case "merged": return { label: "Merged", tone: "done" };
		case "pr_open": return { label: "PR open", tone: "idle" };
		case "draft": return { label: "Draft PR", tone: "idle" };
		case "exited": return { label: "Agent exited", tone: "idle" };
		case "idle":
		case "no_signal":
		default:
			return { label: "Idle", tone: "idle" };
	}
}

