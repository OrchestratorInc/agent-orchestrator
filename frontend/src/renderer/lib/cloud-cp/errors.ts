import { markApiError } from "../reported-errors";

export interface CloudCpErrorOptions {
	status: number;
	code?: string;
	requestId?: string;
	retryAfterMs?: number;
	cause?: unknown;
}

export class CloudCpError extends Error {
	readonly status: number;
	readonly code?: string;
	readonly requestId?: string;
	readonly retryAfterMs?: number;

	constructor(message: string, options: CloudCpErrorOptions) {
		super(message, options.cause === undefined ? undefined : { cause: options.cause });
		this.name = "CloudCpError";
		markApiError(this);
		this.status = options.status;
		this.code = options.code;
		this.requestId = options.requestId;
		this.retryAfterMs = options.retryAfterMs;
	}
}

export class CloudCpAuthError extends CloudCpError {
	constructor(message: string, options: CloudCpErrorOptions) {
		super(message, options);
		this.name = "CloudCpAuthError";
	}
}
