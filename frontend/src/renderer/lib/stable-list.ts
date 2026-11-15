import { useMemo, useRef } from "react";

export function useStableList<T>(
	list: T[],
	keyOf: (entry: T) => string,
	equal: (a: T, b: T) => boolean,
): T[] {
	const seen = useRef(new Map<string, T>());
	const previousList = useRef<T[] | undefined>(undefined);
	return useMemo(() => {
		const next = new Map<string, T>();
		const stable = list.map((entry) => {
			const before = seen.current.get(keyOf(entry));
			const kept = before !== undefined && equal(before, entry) ? before : entry;
			next.set(keyOf(kept), kept);
			return kept;
		});
		seen.current = next;
		const previous = previousList.current;
		const result =
			previous &&
			previous.length === stable.length &&
			previous.every((entry, index) => entry === stable[index])
				? previous
				: stable;
		previousList.current = result;
		return result;
	}, [list, keyOf, equal]);
}

export function sameContent(a: unknown, b: unknown): boolean {
	if (a === b) return true;
	if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false;
	if (Array.isArray(a) || Array.isArray(b)) {
		if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
		return a.every((entry, index) => sameContent(entry, b[index]));
	}
	const keys = Object.keys(a);
	if (keys.length !== Object.keys(b).length) return false;
	return keys.every((key) =>
		sameContent((a as Record<string, unknown>)[key], (b as Record<string, unknown>)[key]),
	);
}
