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
