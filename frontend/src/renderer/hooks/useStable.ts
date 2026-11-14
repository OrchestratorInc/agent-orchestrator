import { useCallback, useLayoutEffect, useRef } from "react";

export function useLatestCallback<A extends unknown[], R>(fn: (...args: A) => R): (...args: A) => R {
	const latest = useRef(fn);
	useLayoutEffect(() => {
		latest.current = fn;
	});
	return useCallback((...args: A) => latest.current(...args), []);
}

export function useStableArray<A extends readonly unknown[]>(next: A): A {
	const kept = useRef(next);
	const previous = kept.current;
	if (previous !== next && (previous.length !== next.length || next.some((item, index) => !Object.is(item, previous[index])))) {
		kept.current = next;
	}
	return kept.current;
}

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
