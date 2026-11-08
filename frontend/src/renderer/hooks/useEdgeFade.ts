import { useCallback, useEffect, useRef, type RefCallback, type RefObject } from "react";

/**
 * Scroll edge fades, shared by every scroll container that fades its content
 * out at an edge instead of cutting it off.
 *
 * The fade is a CSS mask on the scroller itself (`.edge-fade` in styles.css),
 * not a gradient painted in a background color, so it works over any surface:
 * the translucent window tint, every theme, light and dark. These helpers only
 * toggle `data-fade-start` / `data-fade-end` on the element while content sits
 * past that edge, so scrolling never re-renders React.
 */
export type EdgeFadeAxis = "x" | "y";

/** Marks which edges of a scroller still have content past them. `suppressEnd` holds the end fade off (a strip gliding to its end). */
export function syncEdgeFade(element: HTMLElement, axis: EdgeFadeAxis = "y", suppressEnd = false): void {
	const offset = axis === "x" ? element.scrollLeft : element.scrollTop;
	const overflow = axis === "x"
		? element.scrollWidth - element.clientWidth
		: element.scrollHeight - element.clientHeight;
	element.toggleAttribute("data-fade-start", overflow > 1 && offset > 1);
	element.toggleAttribute("data-fade-end", !suppressEnd && overflow > 1 && offset < overflow - 1);
}

/**
 * Keeps a scroller's fade attributes current: on every scroll (passive), and a
 * frame after the scroller or its content resizes or rows come and go. Resizes
 * are read in the next frame rather than inside the observer, where another
 * observer has usually just dirtied layout. Returns the cleanup.
 */
export function observeEdgeFade(element: HTMLElement, axis: EdgeFadeAxis = "y"): () => void {
	const sync = () => syncEdgeFade(element, axis);
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
	// The scroller, plus its first and last child: content that grows inside a
	// wrapper resizes one of those, and rows added or removed are a mutation.
	// Not every child: a model menu holds hundreds of rows.
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
	schedule();
	return () => {
		element.removeEventListener("scroll", sync);
		resizeObserver?.disconnect();
		mutationObserver?.disconnect();
		if (frame !== null) window.cancelAnimationFrame(frame);
	};
}

/**
 * Drives the fade attributes of the scroller in `ref`. The element needs the
 * `edge-fade` class (plus `edge-fade-x` for a horizontal strip). Pass the
 * values that mount or remount the scroller (a menu opening) as `dependencies`.
 */
export function useEdgeFade(
	ref: RefObject<HTMLElement | null>,
	axis: EdgeFadeAxis = "y",
	dependencies: readonly unknown[] = [],
): void {
	useEffect(() => {
		const element = ref.current;
		if (!element) return;
		return observeEdgeFade(element, axis);
	}, [ref, axis, ...dependencies]);
}

/** The same as `useEdgeFade`, as a callback ref, for a scroller that mounts on its own (a page body behind a loading state). */
export function useEdgeFadeRef<T extends HTMLElement>(axis: EdgeFadeAxis = "y"): RefCallback<T> {
	// Cleaned up on the null call rather than through a returned cleanup, which
	// ref composers (Radix) do not all pass on.
	const cleanup = useRef<(() => void) | undefined>(undefined);
	return useCallback((node: T | null) => {
		cleanup.current?.();
		cleanup.current = node ? observeEdgeFade(node, axis) : undefined;
	}, [axis]);
}
