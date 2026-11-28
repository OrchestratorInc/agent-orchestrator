import { appI18n } from "../i18n";
import { CloudCpError } from "./cloud-cp";

/**
 * AO Cloud's answers about a session's move state, with the text the app
 * shows for each: SESSION_MOVING while a move's export holds the session
 * (messages, interface switches), from when its export starts until the
 * move ends, MOVE_WORKER_TOO_OLD before a move its worker cannot run,
 * MOVE_AGENT_STILL_RUNNING when an export could not confirm the agent stopped
 * (nothing committed or pushed; Try again is safe), MOVE_TURNS_QUEUED when
 * messages are still queued for the session (let them run or remove them,
 * then move), and MOVE_IN_PROGRESS when agent/restart cannot abandon a move
 * whose export still runs.
 */
const MOVE_STATE_TEXT = {
	SESSION_MOVING: "move.sessionMoving",
	MOVE_WORKER_TOO_OLD: "move.workerTooOld",
	MOVE_AGENT_STILL_RUNNING: "move.agentStillRunning",
	MOVE_TURNS_QUEUED: "move.turnsQueued",
	MOVE_IN_PROGRESS: "move.inProgress",
} as const;

/**
 * Refusals of a move's export the move dialog words itself. An interface or
 * agent switch in progress refuses other calls too, where their own text
 * fits, so it is not a move-state code.
 */
const MOVE_REFUSAL_TEXT = {
	INTERFACE_TRANSITION_IN_PROGRESS: "move.switchInProgress",
} as const;

/** The translated text for a move-state code, or undefined for any other. */
export function moveStateText(code: string | undefined): string | undefined {
	return code && code in MOVE_STATE_TEXT ? appI18n.t(MOVE_STATE_TEXT[code as keyof typeof MOVE_STATE_TEXT]) : undefined;
}

/** The move dialog's text for a failed move's code: a move-state code or a refusal of its export. */
export function moveErrorText(code: string | undefined): string | undefined {
	if (code && code in MOVE_REFUSAL_TEXT) return appI18n.t(MOVE_REFUSAL_TEXT[code as keyof typeof MOVE_REFUSAL_TEXT]);
	return moveStateText(code);
}

/**
 * error with a move-state answer's message translated, still a CloudCpError
 * with its code and status so callers that branch on them keep working; any
 * other error is returned as it is.
 */
export function withMoveStateText(error: unknown): unknown {
	if (!(error instanceof CloudCpError)) return error;
	const text = moveStateText(error.code);
	if (!text) return error;
	return new CloudCpError(text, { status: error.status, code: error.code, requestId: error.requestId, retryAfterMs: error.retryAfterMs, details: error.details, cause: error });
}
