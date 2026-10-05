// Legacy per-machine provider choice. Read only during one-time migration to
// the account-scoped Cloud preference; no new session creation consumes it.
const storageKey = "ao.cloud.sandboxProvider";

function getLocalStorage(): Storage | null {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

/**
 * Reads the persisted provider without a React subscription, for hook-free
 * migration. Returns null when unset or unreadable.
 */
export function readSelectedSandboxProvider(): string | null {
	try {
		const value = getLocalStorage()?.getItem(storageKey);
		return value && value !== "" ? value : null;
	} catch {
		return null;
	}
}

export function clearLegacySandboxProvider(): void {
	try {
		getLocalStorage()?.removeItem(storageKey);
	} catch {
		// A blocked localStorage must not prevent using the Cloud preference.
	}
}
