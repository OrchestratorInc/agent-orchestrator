// React Query pauses retries and refetchInterval while the document is hidden.
// AO's window is often hidden while agents work, and a read that failed there
// (on a reload, or while the control plane was briefly away) then waited for
// the window to be shown again. These options let a read that must work in a
// hidden window recover there without making every read poll while hidden.

/** How often a failed read tries again, hidden window or not. */
export const HIDDEN_RECOVERY_MS = 15_000;

const documentHidden = () => typeof document !== "undefined" && document.visibilityState === "hidden";

/**
 * Retries once while the window is visible. A retry started while it is
 * hidden would pause until it is shown, so a hidden window lets the read fail
 * and recoverInterval tries again instead.
 */
export function retryUnlessHidden(failureCount: number): boolean {
	return failureCount < 1 && !documentHidden();
}

/**
 * The read's own interval while the window is visible; while the read is
 * failing, every HIDDEN_RECOVERY_MS, hidden or not. Pair it with
 * refetchIntervalInBackground: true, which only the failing case uses.
 */
export function recoverInterval(visibleInterval: number | false = false) {
	// Structural, not Query<…>: a generic Query here would pin the read's data
	// type and break inference at every useQuery that spreads these options.
	return (query: { state: { status: string } }): number | false => {
		if (query.state.status === "error") return HIDDEN_RECOVERY_MS;
		return documentHidden() ? false : visibleInterval;
	};
}

/** Options for a read the app needs even while its window is hidden. */
export function recoverWhileHidden(visibleInterval: number | false = false) {
	return {
		retry: retryUnlessHidden,
		refetchInterval: recoverInterval(visibleInterval),
		refetchIntervalInBackground: true,
	} as const;
}
