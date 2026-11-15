import { apiErrorMessage } from "./api-client";
import { isApiError, markApiError } from "./reported-errors";

export function apiErrorException(error: unknown, fallback?: string): Error {
	const exception = new Error(apiErrorMessage(error, fallback), { cause: error });
	Error.captureStackTrace?.(exception, apiErrorException);
	if (isApiError(error)) markApiError(exception);
	return exception;
}
