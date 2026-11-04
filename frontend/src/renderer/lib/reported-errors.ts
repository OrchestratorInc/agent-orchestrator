// Identity markers stay local. They carry no messages, keys, URLs or IDs.
import { wasCapturedBeforeRenderer } from "../../shared/renderer-error-handlers";
const apiErrors = new WeakSet<object>();
const capturedErrors = new WeakSet<object>();

export function markApiError(error: unknown): void {
	if (error !== null && (typeof error === "object" || typeof error === "function")) apiErrors.add(error);
}

export function isApiError(error: unknown): boolean {
	for (let depth = 0; depth < 8 && error !== null && typeof error === "object"; depth++) {
		if (apiErrors.has(error)) return true;
		if (!(error instanceof Error)) break;
		// Electron serializes IPC rejections as plain Errors. Main owns both
		// expected-state filtering and unexpected handler fault reporting.
		if (error.message.startsWith("Error invoking remote method '")) return true;
		try { error = error.cause; } catch { break; }
	}
	return false;
}

export function reserveException(error: unknown): boolean {
	if ((error instanceof Error || error instanceof DOMException) && error.name === "AbortError") return false;
	if (error !== null && (typeof error === "object" || typeof error === "function")) {
		if (isApiError(error) || capturedErrors.has(error) || wasCapturedBeforeRenderer(error)) return false;
		capturedErrors.add(error);
	}
	return true;
}

// Mark the exact error object produced by openapi-fetch after it parses the
// response. Non-object error bodies need an identity to survive rethrowing;
// normalize them to an Error with the same user-visible message.
export function withApiErrorOwnership<T extends object>(client: T): T {
	const wrapped = { ...client };
	for (const key of ["GET", "PUT", "POST", "DELETE", "OPTIONS", "HEAD", "PATCH", "TRACE"]) {
		const method: unknown = Reflect.get(client, key);
		if (typeof method !== "function") continue;
		Reflect.set(wrapped, key, async (...args: unknown[]) => {
			let result: unknown = await Reflect.apply(method, client, args);
			if (result && typeof result === "object" && "error" in result) {
				if (result.error != null && typeof result.error !== "object") result = { ...result, error: new Error(String(result.error)) };
				markApiError((result as { error: unknown }).error);
			}
			return result;
		});
	}
	return wrapped;
}
