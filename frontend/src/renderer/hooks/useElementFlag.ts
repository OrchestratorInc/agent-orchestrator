import { useEffect, type RefObject } from "react";

const documentFlagCounts = new Map<string, number>();

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
