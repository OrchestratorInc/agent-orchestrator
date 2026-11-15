import { useCallback, useLayoutEffect, useRef, useState } from "react";

/**
 * A page's refresh, run two ways: from the list's pull-to-refresh, and from
 * its Retry buttons, each with a spinner of its own.
 *
 * `refreshing` (the RefreshControl's) only ever turns on for a pull. When it
 * turns on without one, React Native's iOS refresh control scrolls the list
 * down by the spinner's height to show it, and never scrolls it back once the
 * refresh ends, so every Retry tap left the page ~60 pt lower until the empty
 * state's Retry slid under the tab bar. Buttons call `retry` instead, and can
 * show `retrying` as their own loading state.
 */
export function useRefreshActions(refresh: () => Promise<unknown>) {
	const [refreshing, setRefreshing] = useState(false);
	const [retrying, setRetrying] = useState(false);
	// The latest refresh behind stable handlers, so a memoised list or banner
	// is not rebuilt each time the page's refresh closes over new state. Taken
	// at commit, not during render: a machine chosen inside a transition renders
	// before it commits, and a Retry on the board still shown must refresh the
	// machine that board is for.
	const latest = useRef(refresh);
	useLayoutEffect(() => {
		latest.current = refresh;
	});
	const onRefresh = useCallback(async () => {
		setRefreshing(true);
		try {
			await latest.current();
		} finally {
			setRefreshing(false);
		}
	}, []);
	// The stale banner's Retry stays tappable while one runs, so retries can
	// overlap: `retrying` holds until the last of them answers.
	const pendingRetries = useRef(0);
	const retry = useCallback(async () => {
		pendingRetries.current += 1;
		setRetrying(true);
		try {
			await latest.current();
		} finally {
			pendingRetries.current -= 1;
			if (pendingRetries.current === 0) setRetrying(false);
		}
	}, []);
	return { refreshing, onRefresh, retrying, retry };
}
