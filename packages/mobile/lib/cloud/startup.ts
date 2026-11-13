import { useRef } from "react";
import type { SessionProvisionStep } from "../api";
import type { ConversationSnapshot } from "../chat/types";
import { startupAgentName, type SessionStartupView } from "../session/sessionStartup";
import { cloudLifecycleStage, cloudStartupProblem } from "./lifecycle";
import type { CloudSession } from "./types";

// A starting cloud session shows the setup checklist a local start shows
// (SessionStartupStatus), with the cloud's own three stages: the desktop's
// startup steps for a chat (cloudStartupStage in
// frontend/src/renderer/components/SessionView.tsx), less its terminal step.
//
// The checklist's step ids are the local ones, so it draws and announces them
// the same way; `labels` names what each stage is in the cloud.

const ORDER = ["worktree", "setup", "agent"] as const satisfies readonly SessionProvisionStep["id"][];

export const CLOUD_STARTUP_LABELS: SessionStartupView["labels"] = {
	worktree: "Create the workspace",
	setup: "Connect to the worker",
};

/** Startup errors the worker reports once it is in: its checkout, setup or agent. */
const AGENT_STEP_ERRORS = new Set([
	"repository_checkout_failed",
	"working_directory_unusable",
	"working_directory_occupied",
	"project_setup_failed",
	"agent_start_failed",
]);

/**
 * Which stage is under way: 0 the sandbox is being created, 1 the worker inside
 * it is connecting, 2 the worker is preparing the repository and starting the
 * agent, 3 the agent is up.
 */
export function cloudStartupIndex(session: CloudSession): number {
	const observed = session.observedState;
	if (!observed || observed === "requested" || observed === "provisioning") return 0;
	if (!session.runtimeConnected) return 1;
	return observed === "bootstrapping" ? 2 : 3;
}

type Turns = Pick<ConversationSnapshot, "turns"> & Partial<Pick<ConversationSnapshot, "hasMoreBefore">>;

/**
 * The checklist for a session that is starting for the first time, or whose
 * start failed; undefined otherwise. A session that has already worked (any
 * turn past queued) waking or reconnecting keeps its conversation and the
 * paused/reconnecting banner instead, as on the desktop. Before the
 * conversation loads, only the sandbox and worker stages count as a first start.
 */
export function cloudStartupView(session: CloudSession, snapshot: Turns | null | undefined): SessionStartupView | undefined {
	if (session.isTerminated) return undefined;
	const stage = cloudLifecycleStage(session);
	if (stage === "paused" || stage === "resuming") return undefined;
	const problem = cloudStartupProblem(session);
	const agentName = startupAgentName(session.harness);
	const turns = snapshot?.turns ?? [];
	const opening = turns[0]?.state === "queued" || turns[0]?.state === "running" ? turns[0] : undefined;
	const reached = cloudStartupIndex(session);
	const base = { agentName, labels: CLOUD_STARTUP_LABELS, namingBranch: false, openingTurnId: opening?.id };

	if (problem?.phase === "failed") {
		const failedAt = session.startupError && AGENT_STEP_ERRORS.has(session.startupError.code) ? 2 : Math.min(reached, 2);
		return {
			...base,
			failed: true,
			steps: steps(failedAt),
			error: [problem.message, problem.detail].filter(Boolean).join("\n"),
			branch: failedAt > 0 ? session.branch || undefined : undefined,
		};
	}

	// History older than the loaded page means the session has worked before.
	const worked = Boolean(snapshot?.hasMoreBefore) || turns.some((turn) => turn.state !== "queued");
	if (worked || (!snapshot && reached >= 2)) return undefined;
	// Up, but the opening brief has not been picked up yet: the agent is still
	// starting as far as the person can tell.
	const running = reached === 3 ? (opening?.state === "queued" ? 2 : undefined) : reached;
	if (running === undefined) return undefined;
	return { ...base, failed: false, steps: steps(running), branch: running > 0 ? session.branch || undefined : undefined };
}

function steps(running: number): SessionProvisionStep[] {
	return ORDER.map((id, index) => ({ id, status: index < running ? "done" : index === running ? "running" : "pending" }));
}

type StepTimes = { startedAt?: string; endedAt?: string };

/**
 * Step timings as this phone saw them, so the checklist ticks like a local
 * one. The control plane reports no per-stage times: a step's clock starts
 * when it was first seen running (the sandbox's at the session's creation) and
 * stops when it was first seen done. A step already done on arrival shows none.
 */
export function useCloudStartup(session: CloudSession | null | undefined, snapshot: Turns | null | undefined): SessionStartupView | undefined {
	const times = useRef<{ sessionId?: string; steps: Map<string, StepTimes> }>({ steps: new Map() });
	if (!session) return undefined;
	const view = cloudStartupView(session, snapshot);
	if (!view) return undefined;
	if (times.current.sessionId !== session.id) times.current = { sessionId: session.id, steps: new Map() };
	const seen = times.current.steps;
	const now = new Date().toISOString();
	return {
		...view,
		steps: view.steps.map((step) => {
			const mark = seen.get(step.id) ?? {};
			if (step.status === "running" && !mark.startedAt) mark.startedAt = step.id === "worktree" ? session.createdAt : now;
			if (step.status === "done" && mark.startedAt && !mark.endedAt) mark.endedAt = now;
			seen.set(step.id, mark);
			return { ...step, ...mark };
		}),
	};
}
