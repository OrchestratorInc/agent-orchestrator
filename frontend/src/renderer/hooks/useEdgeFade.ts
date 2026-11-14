import { useCallback, useLayoutEffect, useRef, type RefCallback, type RefObject } from "react";

/**
 * Scroll edge fades, shared by every scroll container that fades its content
 * out at an edge instead of cutting it off.
 *
 * The fade is a CSS mask on the scroller itself (`.edge-fade` in styles.css),
 * not a gradient painted in a background color, so it works over any surface:
 * the translucent window tint, every theme, light and dark. These helpers only
 * toggle `data-fade-start` / `data-fade-end` on the element while content sits
 * past that edge, so scrolling never re-renders React.
 *
 * The fades ease in and out as the reader scrolls, but where a scroller already
 * sits when it mounts (a chat opened at its end, a menu opened over a long list)
 * is not a change: that first state applies at once, under `data-fade-instant`.
 */
export type EdgeFadeAxis = "x" | "y";

/**
 * Marks which edges of a scroller still have content past them, and returns
 * whether it overflows at all. `suppressEnd` holds the end fade off (a strip
 * gliding to its end).
 */
export function syncEdgeFade(element: HTMLElement, axis: EdgeFadeAxis = "y", suppressEnd = false): boolean {
	const offset = axis === "x" ? element.scrollLeft : element.scrollTop;
	const overflow = axis === "x"
		? element.scrollWidth - element.clientWidth
		: element.scrollHeight - element.clientHeight;
	element.toggleAttribute("data-fade-start", overflow > 1 && offset > 1);
	element.toggleAttribute("data-fade-end", !suppressEnd && overflow > 1 && offset < overflow - 1);
	return overflow > 1;
}

/**
 * How long a scroller that has not overflowed yet keeps its fades instant.
 * Content loading in that window (a chat's transcript) lands where it opens;
 * content that first overflows later (a reply streaming into a fresh chat) is
 * a change, and fades.
 */
const INSTANT_FADE_LIMIT_MS = 1000;

/**
 * Keeps a scroller's fade attributes current: on every scroll (passive), and a
 * frame after the scroller or its content resizes or rows come and go. Resizes
 * are read in the next frame rather than inside the observer, where another
 * observer has usually just dirtied layout. Returns the cleanup.
 */
export function observeEdgeFade(element: HTMLElement, axis: EdgeFadeAxis = "y"): () => void {
	// Instant until the content is in and has held for two frames: the scroll
	// position a chat restores, or the rows a list measures, land a frame or two
	// after the content itself.
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
	// Now, so the first paint already has its fades; and again next frame, once
	// whatever mounted alongside has laid out.
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

/**
 * Drives the fade attributes of the scroller in `ref`. The element needs the
 * `edge-fade` class (plus `edge-fade-x` for a horizontal strip). Pass the
 * values that mount or remount the scroller (a menu opening) as `dependencies`.
 * Attached before paint, so a scroller never paints its first frame unfaded.
 */
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
