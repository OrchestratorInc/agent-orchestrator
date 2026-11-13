/**
 * A failed Orchestrator Cloud request. `status` is 0 when the control plane was never
 * reached (offline, DNS, timeout); otherwise it is the HTTP status, and `code`
 * and `requestId` come from the control plane's error envelope.
 */
export class CloudError extends Error {
	readonly status: number;
	readonly code?: string;
	readonly requestId?: string;
	readonly details?: Record<string, unknown>;
	/** When a plan limit lifts, for a plan refusal that says (WINDOW_/WEEKLY_LIMIT_REACHED). */
	readonly resetsAt?: string;

	constructor(message: string, options: { status: number; code?: string; requestId?: string; details?: Record<string, unknown>; resetsAt?: string; cause?: unknown }) {
		super(message);
		this.name = "CloudError";
		this.status = options.status;
		this.code = options.code;
		this.requestId = options.requestId;
		this.details = options.details;
		this.resetsAt = options.resetsAt;
		if (options.cause !== undefined) (this as { cause?: unknown }).cause = options.cause;
	}
}

/** The account token is missing, expired or refused: the person has to sign in again. */
export class CloudAuthError extends CloudError {
	constructor(message: string, options: { status: number; code?: string; requestId?: string }) {
		super(message, options);
		this.name = "CloudAuthError";
	}
}

/** Codes the control plane uses when the organization's plan does not allow a request. */
export const PLAN_REFUSAL_CODES: ReadonlySet<string> = new Set([
	"PLAN_REQUIRED",
	"ORCHESTRATOR_SLOTS_FULL",
	"WORKER_SLOTS_FULL",
	"SESSION_LIMIT_REACHED",
	"WINDOW_LIMIT_REACHED",
	"WEEKLY_LIMIT_REACHED",
]);

/** The organization's plan refused the request: a usage window, weekly limit or slot cap. */
export function isPlanRefusal(error: unknown): error is CloudError {
	return error instanceof CloudError && Boolean(error.code && PLAN_REFUSAL_CODES.has(error.code));
}

/** "Resets Mon, 9 Nov, 10:00" for a plan limit's reset time, or nothing when it has none. */
export function planResetCopy(resetsAt: string | undefined, now = Date.now()): string | undefined {
	const at = resetsAt ? Date.parse(resetsAt) : NaN;
	if (!Number.isFinite(at)) return undefined;
	if (at <= now) return "It should reset any moment now.";
	const when = new Date(at).toLocaleString(undefined, { weekday: "short", day: "numeric", month: "short", hour: "numeric", minute: "2-digit" });
	return `Resets ${when}.`;
}

export function isCloudError(error: unknown, code?: string): error is CloudError {
	return error instanceof CloudError && (code === undefined || error.code === code);
}

/** The worker is not connected yet; the request is worth asking again shortly. */
export function isWorkerUnavailable(error: unknown): boolean {
	return error instanceof CloudError && (error.code === "WORKER_UNAVAILABLE" || error.code === "TRANSPORT_TIMEOUT" || error.code === "WORKER_TIMEOUT");
}

/**
 * What to show a person for a failed cloud request. The control plane's own
 * message is already written for people; a few conditions get advice instead,
 * because the person can act on them from the phone.
 */
export function cloudErrorMessage(error: unknown): string {
	if (error instanceof CloudAuthError) return "Your Orchestrator Cloud sign-in expired. Sign in again in Settings.";
	if (error instanceof CloudError) {
		if (error.code === "insecure_address") return error.message;
		if (error.status === 0) return "Couldn't reach Orchestrator Cloud. Check your connection and try again.";
		if (error.code === "agent_provider_required") return "Connect this coding agent to Orchestrator Cloud first (Settings → Coding agents).";
		if (error.code === "invalid_credential") return error.message || "The provider rejected that credential.";
		if (error.code && PLAN_REFUSAL_CODES.has(error.code)) {
			const reset = planResetCopy(error.resetsAt);
			return [error.message || "Your Orchestrator Cloud plan doesn't allow this right now.", reset].filter(Boolean).join(" ");
		}
		if (error.code === "SESSION_TERMINATED") return "This session was archived.";
		if (error.code === "COMPOSER_LOCKED") return "Someone else is typing in this session. Try again in a moment.";
		if (error.status === 403) return error.message || "You don't have access to that in this workspace.";
		if (error.status >= 500) return error.message || "Orchestrator Cloud had a problem. Try again.";
		return error.message || "Orchestrator Cloud refused the request.";
	}
	if (error instanceof Error && error.message) return error.message;
	return "Something went wrong. Try again.";
}
