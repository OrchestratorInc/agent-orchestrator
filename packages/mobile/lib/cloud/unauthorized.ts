import { CloudApiError, type CloudClient } from "@aoagents/cloud-client";

/**
 * Wraps every method on a CloudClient so a 401 response — including the one
 * `authorizedFetch` synthesizes when `getAccessToken` yields `null` — calls
 * `onUnauthorized` before the error is rethrown to the caller.
 *
 * This is the single place that watches for an unrecoverable cloud session:
 * a local-auth token has no refresh token (see session.ts's refreshOnce), so
 * once it expires every call through this client 401s forever unless
 * something tells authStore to drop back to signed-out. Wrapping the client
 * catches that everywhere it's used — org resolution, the session source,
 * event polling — rather than requiring every call site to check status
 * itself.
 */
export function wrapUnauthorized(client: CloudClient, onUnauthorized: () => void): CloudClient {
	return new Proxy(client, {
		get(target, prop, receiver) {
			const value = Reflect.get(target, prop, receiver);
			if (typeof value !== "function") return value;
			return (...args: unknown[]) => {
				const result = Reflect.apply(value, target, args);
				if (result instanceof Promise) {
					return result.catch((error: unknown) => {
						if (error instanceof CloudApiError && error.status === 401) onUnauthorized();
						throw error;
					});
				}
				return result;
			};
		},
	});
}
