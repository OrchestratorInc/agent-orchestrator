import { CloudApiError } from "@aoagents/cloud-client";

/** Cloud envelopes retain billing/retry facts without using daemon pairing copy. */
export function cloudRequestErrorCopy(cause: unknown): string {
	if (!(cause instanceof CloudApiError)) return cause instanceof Error && !(cause instanceof TypeError || cause instanceof ReferenceError || cause instanceof SyntaxError || cause instanceof RangeError)
		? cause.message : "Cloud could not complete this request. Check your connection and try again.";
	const envelope = cause.envelope;
	const message = cause.message.replace(/https?:\/\/\S+/gi, "an internal service");
	const facts = [
		typeof envelope.used === "number" && typeof envelope.limit === "number" ? `${envelope.used} / ${envelope.limit} used.` : undefined,
		envelope.resetsAt ? `Resets at ${envelope.resetsAt}.` : undefined,
		cause.retryAfterSeconds !== undefined ? `Retry after ${cause.retryAfterSeconds} seconds.` : undefined,
		cause.requestId ? `Reference: ${cause.requestId}.` : undefined,
	];
	return [message, ...facts].filter(Boolean).join(" ");
}
