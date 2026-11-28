import { sanitizeSensitiveString } from "./telemetry-text";

// The first-day exit feedback prompt: one question shown when a new install
// quits without starting any work. Main decides and holds the quit; the shell
// renderer draws the dialog and records the answer. See docs/telemetry.md,
// "Exit feedback prompt".

export const EXIT_FEEDBACK_REQUEST_CHANNEL = "exit-feedback:request";
export const EXIT_FEEDBACK_SECOND_CLOSE_CHANNEL = "exit-feedback:second-close";
export const EXIT_FEEDBACK_STATE_CHANNEL = "exit-feedback:state";
export const EXIT_FEEDBACK_EVENT_CHANNEL = "exit-feedback:event";

export type ExitFeedbackVersion = "no_project" | "project_no_work";

export type ExitFeedbackRequest = { version: ExitFeedbackVersion };

/** What the renderer reports back about the dialog. */
export type ExitFeedbackState = "shown" | "typing" | "done" | "declined";

export const EXIT_FEEDBACK_EVENTS = ["ao.exit_feedback.shown", "ao.exit_feedback.answered", "ao.exit_feedback.dismissed"] as const;

export const EXIT_FEEDBACK_ANSWERS: Record<ExitFeedbackVersion, readonly string[]> = {
	no_project: ["just_looking", "unsure_where_to_start", "no_repo_or_agent", "something_broke", "not_a_fit"],
	project_no_work: ["no_task_in_mind", "unsure_what_to_ask", "agent_not_ready", "tried_nothing_started", "not_comfortable"],
};

export const EXIT_FEEDBACK_DISMISSALS = ["skip", "escape", "second_close", "timeout"] as const;

export const EXIT_FEEDBACK_TEXT_LIMIT = 500;
export const EXIT_FEEDBACK_TIMEOUT_MS = 15_000;

export function isExitFeedbackVersion(value: unknown): value is ExitFeedbackVersion {
	return value === "no_project" || value === "project_no_work";
}

const EXIT_FEEDBACK_DISMISSAL_SET = new Set<string>(EXIT_FEEDBACK_DISMISSALS);

/**
 * The exit feedback events: a fixed version, answer and dismissal vocabulary,
 * whole seconds, and the typed text only with an answer. The text is trimmed,
 * stripped of local paths and URLs and capped at 500 characters.
 */
export function sanitizeExitFeedbackProperties(event: string, properties?: Record<string, unknown>): Record<string, unknown> {
	const safe: Record<string, unknown> = {};
	const version = properties?.version;
	if (!isExitFeedbackVersion(version)) return safe;
	safe.version = version;
	if (event === "ao.exit_feedback.shown") return safe;
	const seconds = properties?.seconds_open;
	if (typeof seconds === "number" && Number.isFinite(seconds) && seconds >= 0) safe.seconds_open = Math.min(Math.floor(seconds), 86_400);
	if (event === "ao.exit_feedback.dismissed") {
		if (typeof properties?.how === "string" && EXIT_FEEDBACK_DISMISSAL_SET.has(properties.how)) safe.how = properties.how;
		return safe;
	}
	// Typed text rides only with an accepted answer.
	if (typeof properties?.answer !== "string" || !EXIT_FEEDBACK_ANSWERS[version].includes(properties.answer)) return safe;
	safe.answer = properties.answer;
	const raw = typeof properties?.text === "string" ? properties.text.trim().slice(0, EXIT_FEEDBACK_TEXT_LIMIT) : "";
	const text = sanitizeSensitiveString(raw).slice(0, EXIT_FEEDBACK_TEXT_LIMIT);
	safe.has_text = text !== "";
	if (text) safe.text = text;
	return safe;
}
