import { AoError } from "../../../../packages/shared/telemetry/ao-error";
import { apiErrorCode, apiErrorMessage } from "./api-client";
import { isApiError, markApiError } from "./reported-errors";

// Keep the original UI message while preserving API ownership across wrapping.
// Missing data with no API error is still an unexpected client failure.
export function apiErrorException(error: unknown, fallback?: string): Error {
	const code = apiErrorCode(error);
	const message = apiErrorMessage(error, fallback);
	const exception = code ? new AoError(code, message, { cause: error }) : new Error(message, { cause: error });
	Error.captureStackTrace?.(exception, apiErrorException);
	if (isApiError(error)) markApiError(exception);
	return exception;
}
