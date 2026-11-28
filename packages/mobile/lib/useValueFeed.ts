import { useCallback, useEffect, useRef } from "react";

/**
 * A value a sheet can follow while it is up, as the chat's actions and usage
 * sheets follow theirs: `subscribe` calls back at once with the latest value,
 * then on every change, and returns the unsubscribe. Stable across renders.
 */
export function useValueFeed<T>(value: T): (listener: (value: T) => void) => () => void {
	const latest = useRef(value);
	const listeners = useRef(new Set<(value: T) => void>());
	useEffect(() => {
		latest.current = value;
		listeners.current.forEach((listener) => listener(value));
	}, [value]);
	return useCallback((listener: (value: T) => void) => {
		listener(latest.current);
		listeners.current.add(listener);
		return () => { listeners.current.delete(listener); };
	}, []);
}
