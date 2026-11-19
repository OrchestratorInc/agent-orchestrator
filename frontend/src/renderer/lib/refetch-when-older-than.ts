/**
 * For a query kept fresh by a poll and events with staleTime Infinity, so that
 * none of its subscribers arms a stale timer: refetch on a new subscriber's
 * mount, or when the window comes back into view, once the data is older than
 * ms, as a finite staleTime of ms would on mount. The parameter is typed
 * structurally so it takes no part in inferring the query's data type.
 */
export const refetchWhenOlderThan = (ms: number) => (query: { state: { dataUpdatedAt: number } }) =>
	Date.now() - query.state.dataUpdatedAt >= ms ? ("always" as const) : false;
