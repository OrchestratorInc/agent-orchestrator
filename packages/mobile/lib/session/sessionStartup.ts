import { agentLabel } from "../../../product-ui/src/agents";
import type { DashboardSession, SessionProvisionStep } from "../api";
import type { ConversationSnapshot } from "../chat/types";

/**
 * A starting Chat session's setup checklist, matching the desktop's
 * SessionStartup (frontend/src/renderer/components/chat/SessionStartup.tsx).
 * Every fact here is the daemon's: the stages, their timestamps, and whether
 * provisioning failed. Nothing is inferred from elapsed time.
 */
export type StepStatus = SessionProvisionStep["status"] | "failed";

export type SessionStartupView = {
	failed: boolean;
	steps: readonly SessionProvisionStep[];
	error?: string;
	agentName: string;
	/** Shown beside the worktree step once the daemon has committed to it. */
	branch?: string;
	/** The worktree step is still choosing the branch name. */
	namingBranch: boolean;
	/** The turn carrying the opening brief, which reads as sent while setup runs. */
	openingTurnId?: string;
};

type StartupSession = Partial<Pick<DashboardSession, "provisionState" | "provisionSteps" | "provisionError" | "branch" | "isTerminated">>;

export const STEP_LABELS: Record<SessionProvisionStep["id"], string> = {
	fetch: "Fetch base branch",
	worktree: "Create worktree",
	setup: "Run setup script",
	agent: "Start the agent",
};

export const BRANCH_NAMING_LABELS = [
	"Generating branch name",
	"Naming branch",
	"Preparing branch",
	"Initializing branch",
	"Resolving branch name",
] as const;

export function stepLabel(step: SessionProvisionStep["id"], agentName: string): string {
	return step === "agent" ? `Start ${agentName}` : STEP_LABELS[step];
}

export function startupAgentName(harness: string | undefined | null): string {
	return harness ? agentLabel(harness) : "the agent";
}

/**
 * The startup view for a session, or undefined once there is nothing to show.
 * A provisioning or failed session always has one. A ready session keeps it
 * while its opening turn is queued or running, because ready can land before
 * the first turn does, and the checklist must not blink out in that gap. A
 * promptless start has no turn to wait on, so it keeps the checklist until the
 * conversation itself reports the agent connected.
 */
export function sessionStartupView(session: StartupSession, snapshot: Pick<ConversationSnapshot, "harness" | "turns" | "hasMoreBefore" | "controller"> | undefined): SessionStartupView | undefined {
	const state = session.provisionState === "provisioning" || session.provisionState === "failed" ? session.provisionState : undefined;
	const steps = session.provisionSteps ?? [];
	// Mobile pages history in, so the first loaded turn is only the opening one
	// when nothing older exists.
	const opening = snapshot && !snapshot.hasMoreBefore ? snapshot.turns[0] : undefined;
	const openingLive = opening?.state === "queued" || opening?.state === "running";
	const promptlessConnecting = Boolean(snapshot && !snapshot.hasMoreBefore && snapshot.turns.length === 0 && snapshot.controller.state === "connecting");
	if (!state && !(steps.length && (openingLive || promptlessConnecting))) return undefined;
	const worktreeUnfinished = steps.length
		? steps.some((step) => step.id === "worktree" && step.status !== "done")
		: Boolean(state);
	return {
		failed: state === "failed",
		steps,
		error: session.provisionError,
		agentName: startupAgentName(snapshot?.harness),
		// Keep the provisional branch private until the worktree step lands, as the desktop does.
		branch: worktreeUnfinished ? undefined : session.branch ?? undefined,
		namingBranch: worktreeUnfinished && state === "provisioning" && !session.isTerminated,
		// While a start is in progress or failed, the first turn hosts the
		// checklist whatever its state, as on the desktop: a provider rejection
		// fails that turn too, and the failed start's Retry must still render.
		openingTurnId: state || openingLive ? opening?.id : undefined,
	};
}

/**
 * Whether the checklist holds the working slot. It hands over to the turn's
 * own "Working" row once the agent is up and the opening turn is live, so a
 * clean start leaves no trace. A failed start keeps it, with Retry.
 */
export function startupVisible(startup: SessionStartupView | undefined, snapshot: Pick<ConversationSnapshot, "turns"> | undefined): boolean {
	if (!startup) return false;
	if (startup.failed) return true;
	const opening = snapshot?.turns.find((turn) => turn.id === startup.openingTurnId);
	const handedOff = opening?.state === "running" && startup.steps.length > 0 && startup.steps.every((step) => step.status === "done");
	return !handedOff;
}

/** The daemon leaves the stage it stopped in "running"; a failed start makes that stage the failed one. */
export function stepStatus(step: SessionProvisionStep, failed: boolean): StepStatus {
	return failed && step.status === "running" ? "failed" : step.status;
}

export function stepElapsedMs(step: SessionProvisionStep, now: number): number | undefined {
	if (!step.startedAt) return undefined;
	const start = Date.parse(step.startedAt);
	const end = step.endedAt ? Date.parse(step.endedAt) : now;
	if (!Number.isFinite(start) || !Number.isFinite(end)) return undefined;
	return Math.max(0, end - start);
}

/** A finished step keeps a tenth of a second; a running one ticks whole seconds. */
export function formatStepDuration(ms: number, running = false): string {
	const seconds = ms / 1000;
	if (seconds < 10 && !running) return `${seconds.toFixed(1)}s`;
	if (seconds < 60) return `${Math.floor(seconds)}s`;
	return `${Math.floor(seconds / 60)}m ${Math.floor(seconds % 60)}s`;
}

/** Steps that finished between two daemon reads, for the per-step haptic. */
export function newlyFinishedSteps(previous: readonly SessionProvisionStep[] | undefined, next: readonly SessionProvisionStep[]): SessionProvisionStep["id"][] {
	if (!previous) return [];
	const before = new Map(previous.map((step) => [step.id, step.status]));
	return next.filter((step) => step.status === "done" && before.has(step.id) && before.get(step.id) !== "done").map((step) => step.id);
}
