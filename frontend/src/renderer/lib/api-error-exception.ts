import { apiErrorMessage } from "./api-client";
import { isApiError, markApiError } from "./reported-errors";

// Keep the original UI message while preserving API ownership across wrapping.
// Missing data with no API error is still an unexpected client failure.
export function apiErrorException(error: unknown, fallback?: string): Error {
	const exception = new Error(apiErrorMessage(error, fallback), { cause: error });
	Error.captureStackTrace?.(exception, apiErrorException);
	if (isApiError(error)) markApiError(exception);
	return exception;
}
