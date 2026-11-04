// Typed failures for the cloud control-plane client. Callers can branch on
// `instanceof CloudCpAuthError` for sign-in-required handling and read
// `status`/`code`/`requestId` (from the control plane's error envelope) off
// any `CloudCpError` for everything else. `origin` says whether AO Cloud
// answered with the error ("cloud") or was never reached ("network").

import { CLOUD_UNREACHABLE, isOpaqueRequestId, safeErrorCode, safeNetworkCause } from "../../../shared/cloud-cp-errors";

export interface CloudCpErrorOptions {
	/** HTTP status of the failed response; 0 when the request never produced one (network failure). */
	status: number;
	/** Machine-readable code from the control plane's error envelope (e.g. "validation_error"). */
	code?: string;
	/**
	 * Correlation id: the control plane's (envelope, else its X-Request-ID
	 * header), or the one the desktop sent when the control plane never answered.
	 */
	requestId?: string;
	/** How long the control plane asked the caller to wait (its Retry-After), in milliseconds. */
	retryAfterMs?: number;
	/** Why the control plane was not reached (status 0), as a bounded token such as ENOTFOUND; never a message. */
	networkCause?: string;
	cause?: unknown;
}

export class CloudCpError extends Error {
	readonly status: number;
	readonly code?: string;
	readonly requestId?: string;
	readonly retryAfterMs?: number;
	readonly networkCause?: string;

	constructor(message: string, options: CloudCpErrorOptions) {
		super(message, options.cause === undefined ? undefined : { cause: options.cause });
		this.name = "CloudCpError";
		this.status = options.status;
		this.code = options.code;
		this.requestId = options.requestId;
		this.retryAfterMs = options.retryAfterMs;
		this.networkCause = options.networkCause;
	}

	/** network: AO Cloud was not reached (no HTTP status). cloud: it answered with this error. */
	get origin(): "cloud" | "network" {
		return this.status === 0 ? "network" : "cloud";
	}
}

/**
 * The control plane rejected the bearer token (HTTP 401), or no token was
 * available to attach. The renderer should treat this as "sign in again",
 * never as a retryable request failure.
 */
export class CloudCpAuthError extends CloudCpError {
	constructor(message: string, options: CloudCpErrorOptions) {
		super(message, options);
		this.name = "CloudCpAuthError";
	}
}

/** A failure the main-process proxy reports as fields: an HTTP status (0 when unreachable), message, code and request id. */
export interface CloudCpFailureFields {
	status: number;
	message: string;
	code?: string;
	requestId?: string;
	cause?: string;
}

/** The typed error for a failure main reported as fields (a stream that did not open, or the CP not reached). */
export function cloudCpErrorFromFailure(failure: CloudCpFailureFields, cause?: unknown): CloudCpError {
	const status = Number.isInteger(failure.status) && failure.status >= 0 ? failure.status : 0;
	const options: CloudCpErrorOptions = {
		status,
		code: safeErrorCode(failure.code) ?? (status === 0 ? CLOUD_UNREACHABLE : undefined),
		requestId: typeof failure.requestId === "string" && failure.requestId !== "" ? failure.requestId : undefined,
		networkCause: status === 0 ? safeNetworkCause(failure.cause) : undefined,
		...(cause === undefined ? {} : { cause }),
	};
	const message = typeof failure.message === "string" && failure.message !== ""
		? failure.message
		: status === 0 ? "AO Cloud could not be reached." : `Cloud control plane request failed with status ${status}.`;
	return status === 401 ? new CloudCpAuthError(message, options) : new CloudCpError(message, options);
}

/**
 * The proxy answers status 0 when the control plane was not reached, with its
 * error envelope in the body. A Response cannot carry status 0, so the bridged
 * fetch throws this instead.
 */
export function cloudCpUnreachableError(result: { headers: Record<string, string>; body: string }): CloudCpError {
	const envelope = parseEnvelope(result.body);
	const header = result.headers["x-request-id"];
	return cloudCpErrorFromFailure({
		status: 0,
		message: typeof envelope?.message === "string" ? envelope.message : "",
		code: typeof envelope?.code === "string" ? envelope.code : CLOUD_UNREACHABLE,
		requestId: isOpaqueRequestId(envelope?.requestId) ? envelope.requestId : isOpaqueRequestId(header) ? header : undefined,
		cause: typeof envelope?.cause === "string" ? envelope.cause : undefined,
	});
}

function parseEnvelope(body: string): { message?: unknown; code?: unknown; requestId?: unknown; cause?: unknown } | null {
	try {
		const parsed: unknown = JSON.parse(body);
		return parsed !== null && typeof parsed === "object" ? parsed : null;
	} catch {
		// The fields fall back to the header and defaults.
		return null;
	}
}
