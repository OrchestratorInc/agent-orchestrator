/** The agent switch that runs before a send failed, so the message never left. */
export const AGENT_SWITCH_FAILED = "AGENT_SWITCH_FAILED";

// These responses precede AppendUserMessage. Provider/transport errors can
// follow durable acceptance and must keep the original delivery ID for recovery.
/** API codes that refuse a send before anything was accepted, so the draft goes back into the box. */
export const SEND_REJECTION_CODES = [
	AGENT_SWITCH_FAILED,
	// The new chat page could not create the session this brief would start.
	"TASK_CREATE_FAILED",
	// An archived chat could not be restored, so its message was never sent.
	"SESSION_RESTORE_FAILED",
	"CHAT_FILE_REFERENCE_INVALID", "CHAT_ANNOTATION_LIMIT", "CHAT_FILE_REFERENCES_SIDE_UNSUPPORTED",
	// Cloud validation failures reject the request before durable acceptance.
	"validation_error",
	"INVALID_BODY",
	"CHAT_MESSAGE_EMPTY",
	"INVALID_RESOURCE",
	"UNSUPPORTED_ATTACHMENT_TYPE",
	"INVALID_ATTACHMENT_DATA",
	"ATTACHMENT_TOO_LARGE",
	"TOO_MANY_ATTACHMENTS",
	"ATTACHMENTS_TOO_LARGE",
	"SESSION_NOT_FOUND",
	"SESSION_MODE_MISMATCH",
	"CHAT_CONTROLLER_NOT_READY",
	"CHAT_RESUME_FAILED",
	"CHAT_USAGE_LIMITED",
	"CHAT_INTERFACE_TRANSITION",
	"CHAT_OPEN_ELSEWHERE",
	// A shared session refused the send because someone else holds its
	// composer; nothing was accepted, so the text goes back to the draft.
	"COMPOSER_LOCKED",
	"COMPOSER_LEASE_LOST",
	// Excerpts are verified before AppendUserMessage, so retrying the same
	// references can only fail again; return them to the draft for removal.
	"CHAT_EXCERPT_INVALID",
	"CHAT_EXCERPT_STALE",
	// A /btw request the daemon refused never opened a side chat, so a
	// retry cannot succeed; return the draft instead of locking the composer.
	"CHAT_SIDE_UNAVAILABLE",
	"CHAT_SIDE_UNSUPPORTED",
	"CHAT_SIDE_ANCHOR_UNAVAILABLE",
	"CHAT_SIDE_INVALID_REQUEST",
	"CHAT_SIDE_NOT_READY",
	"CHAT_SIDE_RECREATE_REQUIRED",
	"CHAT_SIDE_CLOSED",
	"CHAT_SIDE_IDEMPOTENCY_CONFLICT",
] as const;
type SendRejectionCode = (typeof SEND_REJECTION_CODES)[number];

/**
 * Why the composer showed a send error, for `error_code` on
 * `ao.ui.error_shown`. The first group stops a send before it leaves the app;
 * the rest are a request that went out and failed. A refused send reports the
 * API's own code from `SEND_REJECTION_CODES` instead.
 */
export const COMPOSER_FAILURE_CODES = [
	"ANNOTATION_LIMIT",
	"EXCERPT_TOO_LONG",
	"MESSAGE_TOO_LONG",
	"FILE_REFERENCES_UNSUPPORTED",
	"ATTACHMENTS_UNAVAILABLE",
	"ATTACHMENT_UNREADABLE",
	"COMPACT_UNAVAILABLE",
	"COMPACT_FAILED",
	"CONTINUE_FAILED",
	"QUEUE_SAVE_FAILED",
	"SEND_FAILED",
	"SEND_REJECTED",
	"UNSENT_RETRY_FAILED",
] as const;
export type ComposerFailureCode = (typeof COMPOSER_FAILURE_CODES)[number];

export type ComposerErrorCode = ComposerFailureCode | Uppercase<SendRejectionCode>;

/** Every code a composer send error can report: its own, then the API refusals in upper case. */
export const COMPOSER_ERROR_CODES: readonly ComposerErrorCode[] = [
	...COMPOSER_FAILURE_CODES,
	...SEND_REJECTION_CODES.map((code) => code.toUpperCase() as Uppercase<SendRejectionCode>),
];

const REJECTIONS = new Set<string>(SEND_REJECTION_CODES);

/** The code for a send the API refused: its own code when known, else `SEND_REJECTED`. */
export function sendRejectionCode(apiCode: string | undefined): ComposerErrorCode {
	return apiCode && REJECTIONS.has(apiCode) ? apiCode.toUpperCase() as Uppercase<SendRejectionCode> : "SEND_REJECTED";
}
