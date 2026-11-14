import { useEffect, type RefObject } from "react";

// Publishes UI state as an attribute that CSS can select, in place of a
// container-level `:has()` such as `body:has(.x) .y`. Chromium re-checks a
// `:has()` anchored on a large container on every DOM change beneath it and
// restyles the container's whole subtree, so a terminal repainting its rows
// restyled the entire app each frame. An attribute toggles once per state
// change instead.

const documentFlagCounts = new Map<string, number>();

/**
 * Sets `name` on <html> while any mounted caller has `active` true. Counted, so
 * two callers (say, two live browser panels) do not clear each other's flag.
 */
export function useDocumentFlag(name: string, active: boolean) {
	useEffect(() => {
		if (!active || typeof document === "undefined") return;
		const root = document.documentElement;
		documentFlagCounts.set(name, (documentFlagCounts.get(name) ?? 0) + 1);
		root.setAttribute(name, "");
		return () => {
			const remaining = (documentFlagCounts.get(name) ?? 1) - 1;
			if (remaining > 0) {
				documentFlagCounts.set(name, remaining);
				return;
			}
			documentFlagCounts.delete(name);
			root.removeAttribute(name);
		};
	}, [name, active]);
}

/**
 * Sets `name` on the closest `ancestorSelector` of `ref`'s element while
 * `active`, for state that must stay scoped to one container.
 */
export function useAncestorFlag(
	ref: RefObject<Element | null>,
	ancestorSelector: string,
	name: string,
	active: boolean,
) {
	useEffect(() => {
		if (!active) return;
		const ancestor = ref.current?.closest(ancestorSelector);
		if (!ancestor) return;
		ancestor.setAttribute(name, "");
		return () => ancestor.removeAttribute(name);
	}, [ref, ancestorSelector, name, active]);
}
