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
 * The same array for as long as its items are unchanged (by identity, in
 * order), so a context or memoized child keyed on it is not invalidated by an
 * array that was merely rebuilt.
 */
export function useStableArray<A extends readonly unknown[]>(next: A): A {
	const kept = useRef(next);
	const previous = kept.current;
	if (previous !== next && (previous.length !== next.length || next.some((item, index) => !Object.is(item, previous[index])))) {
		kept.current = next;
	}
	return kept.current;
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
