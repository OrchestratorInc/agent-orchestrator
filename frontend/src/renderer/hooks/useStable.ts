import { useCallback, useLayoutEffect, useRef } from "react";

/**
 * A function with a permanent identity that always calls the latest `fn`.
 * For handlers passed to memoized children whose deps churn on every render.
 */
export function useLatestCallback<A extends unknown[], R>(fn: (...args: A) => R): (...args: A) => R {
	const latest = useRef(fn);
	useLayoutEffect(() => {
		latest.current = fn;
	});
	return useCallback((...args: A) => latest.current(...args), []);
}

/**
 * The same Set object for as long as its members are unchanged, so a memoized
 * child is not re-rendered by a Set that was merely rebuilt.
 */
export function useStableSet<S extends ReadonlySet<unknown>>(next: S): S {
	const kept = useRef(next);
	const previous = kept.current;
	if (previous !== next) {
		let same = previous.size === next.size;
		if (same) for (const value of next) if (!previous.has(value)) { same = false; break; }
		if (!same) kept.current = next;
	}
	return kept.current;
}
