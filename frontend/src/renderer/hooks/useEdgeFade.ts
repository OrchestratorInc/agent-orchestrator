import { useCallback, useLayoutEffect, useRef, type RefCallback, type RefObject } from "react";

export type EdgeFadeAxis = "x" | "y";

export function syncEdgeFade(element: HTMLElement, axis: EdgeFadeAxis = "y", suppressEnd = false): boolean {
	const offset = axis === "x" ? element.scrollLeft : element.scrollTop;
	const overflow = axis === "x"
		? element.scrollWidth - element.clientWidth
		: element.scrollHeight - element.clientHeight;
	element.toggleAttribute("data-fade-start", overflow > 1 && offset > 1);
	element.toggleAttribute("data-fade-end", !suppressEnd && overflow > 1 && offset < overflow - 1);
	return overflow > 1;
}

const INSTANT_FADE_LIMIT_MS = 1000;

export function observeEdgeFade(element: HTMLElement, axis: EdgeFadeAxis = "y"): () => void {
	element.setAttribute("data-fade-instant", "");
	let settleFrame: number | null = null;
	const endInstant = () => {
		if (settleFrame !== null) window.cancelAnimationFrame(settleFrame);
		settleFrame = null;
		window.clearTimeout(instantLimit);
		element.removeAttribute("data-fade-instant");
	};
	const instantLimit = window.setTimeout(() => {
		if (settleFrame === null) endInstant();
	}, INSTANT_FADE_LIMIT_MS);
	let settling = false;
	const sync = () => {
		const overflows = syncEdgeFade(element, axis);
		if (!overflows || settling || !element.hasAttribute("data-fade-instant")) return;
		settling = true;
		settleFrame = window.requestAnimationFrame(() => {
			settleFrame = window.requestAnimationFrame(endInstant);
		});
	};
	let frame: number | null = null;
	const schedule = () => {
		if (frame !== null) return;
		frame = window.requestAnimationFrame(() => {
			frame = null;
			sync();
		});
	};
	element.addEventListener("scroll", sync, { passive: true });
	const resizeObserver = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(schedule);
	const observeChildren = () => {
		if (!resizeObserver) return;
		resizeObserver.disconnect();
		resizeObserver.observe(element);
		if (element.firstElementChild) resizeObserver.observe(element.firstElementChild);
		if (element.lastElementChild) resizeObserver.observe(element.lastElementChild);
	};
	observeChildren();
	const mutationObserver = typeof MutationObserver === "undefined"
		? undefined
		: new MutationObserver(() => {
			observeChildren();
			schedule();
		});
	mutationObserver?.observe(element, { childList: true });
	sync();
	schedule();
	return () => {
		element.removeEventListener("scroll", sync);
		resizeObserver?.disconnect();
		mutationObserver?.disconnect();
		if (frame !== null) window.cancelAnimationFrame(frame);
		endInstant();
	};
}

export function useEdgeFade(
	ref: RefObject<HTMLElement | null>,
	axis: EdgeFadeAxis = "y",
	dependencies: readonly unknown[] = [],
): void {
	useLayoutEffect(() => {
		const element = ref.current;
		if (!element) return;
		return observeEdgeFade(element, axis);
	}, [ref, axis, ...dependencies]);
}

export function useEdgeFadeRef<T extends HTMLElement>(axis: EdgeFadeAxis = "y"): RefCallback<T> {
	const cleanup = useRef<(() => void) | undefined>(undefined);
	return useCallback((node: T | null) => {
		cleanup.current?.();
		cleanup.current = node ? observeEdgeFade(node, axis) : undefined;
	}, [axis]);
}
