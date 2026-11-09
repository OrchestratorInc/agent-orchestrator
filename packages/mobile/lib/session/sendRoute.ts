// When a message to the agent should be re-sent down the terminal instead.
//
// There are two channels to a session and they are not interchangeable. The
// REST route (POST /sessions/{id}/send) hands text to the harness as a message;
// the mux writes bytes straight to the PTY. Almost always the first is right —
// but the daemon *refuses* it while a session is paused on a permission prompt,
// answering 409 SESSION_AWAITING_DECISION with the advice "answer it in the
// session terminal first".
//
// Rather than making the user carry a mode switch for a case that announces
// itself, we do exactly what the daemon says: on that one code, retry over the
// PTY.
//
// The narrowness is the point. Every other refusal means the send should NOT be
// retried anywhere:
//
//   SESSION_TERMINATED / AGENT_EXITED / SESSION_NOT_FOUND
//       There is no live PTY. Writing to it would swallow the user's text with
//       no agent to read it, and hide a real error behind a fake success.
//   401 / 429 / network failures
//       Nothing to do with session state.

/** The daemon's code for "paused on a permission decision". */
export const AWAITING_DECISION = "SESSION_AWAITING_DECISION";

/** Shape of the mobile ApiError, kept structural so this module stays pure. */
export type SendFailure = { status?: number; code?: string } | null | undefined;
export type SendTarget = "agent" | "terminal";

export function shouldRetryOnTerminal(err: SendFailure): boolean {
	return err?.code === AWAITING_DECISION;
}

export function routeForSend(target: SendTarget, err?: SendFailure): SendTarget {
	if (target === "terminal") return "terminal";
	return shouldRetryOnTerminal(err) ? "terminal" : "agent";
}

/**
 * What the PTY needs to receive for `text`. The trailing carriage return is the
 * Enter the user would otherwise have to press — a prompt answer that is never
 * submitted just sits on the line looking like nothing happened.
 *
 * Interior newlines are collapsed to spaces first. The composer is a multiline
 * field, so a pasted or dictated answer can contain them, and a PTY reads every
 * one as its own Enter: "yes,\nuse the second option" answers the dialog with
 * "yes," and then feeds the remainder to whatever the dialog opened next. One
 * message must submit once. A run of blank lines collapses to a single space
 * rather than a gap, and surrounding whitespace is dropped so a trailing
 * newline does not become a second, empty submission.
 */
export function terminalPayload(text: string): string {
	return `${text.replace(/[\r\n]+/g, " ").trim()}\r`;
}

/** Shown once after an automatic reroute, so the switch is never silent. */
export const REROUTED_NOTICE = "Agent was paused on a prompt — sent straight to the terminal.";
export const TERMINAL_UNAVAILABLE_NOTICE = "Terminal is not connected yet — text was not sent.";
