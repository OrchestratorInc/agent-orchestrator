import type { QueryState } from "@tanstack/react-query";

/**
 * Restored data carries dataUpdatedAt 0: it is stale at once, so every observer
 * refetches it, and any fetch or write replaces the marker. Nothing that acts on
 * the daemon's behalf may trust data while this is true.
 */
export function isRestoredQueryData(state: Pick<QueryState, "data" | "dataUpdatedAt"> | undefined): boolean {
	return state?.data !== undefined && state.dataUpdatedAt === 0;
}
