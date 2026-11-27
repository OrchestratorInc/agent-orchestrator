import { type RefObject, useEffect } from "react";

// How fast an overflowing title slides while hovered, and the share of each
// pass spent moving (the rest holds at either end so both can be read).
const SPEED_PX_PER_S = 40;
const TRAVEL_SHARE = 0.76;
const MIN_PASS_S = 2;
// The keyframes hold this share of each pass at either end (12% in styles.css).
const HOLD_SHARE = 0.12;
// Hover to first movement, the same for every title: the animation's delay
// absorbs (or, negative, skips into) the start hold, whose length grows with
// the pass. The motion reads about 0.2s after it starts.
const START_AFTER_S = 1.3;

type Watched = { box: HTMLElement; boxWidth: number; textWidth: number; distance: number };

// One observer for every title in the sidebar. Widths come from the observer's
// own entries, so measuring never forces a layout, and a row only writes when
// its distance actually changes. The writes touch paint-only properties (an
// attribute and custom properties feeding a mask and an animation), so they
// never resize anything and can't loop the observer.
const watched = new Map<Element, Watched>();
let observer: ResizeObserver | null = null;

function inlineSize(entry: ResizeObserverEntry, box: "border" | "content"): number {
	const sizes = box === "border" ? entry.borderBoxSize : entry.contentBoxSize;
	return sizes?.[0]?.inlineSize ?? (box === "border" ? (entry.target as HTMLElement).offsetWidth : entry.contentRect.width);
}

function onResize(entries: ResizeObserverEntry[]) {
	const changed = new Set<Watched>();
	for (const entry of entries) {
		const record = watched.get(entry.target);
		if (!record) continue;
		if (entry.target === record.box) record.boxWidth = inlineSize(entry, "content");
		else record.textWidth = inlineSize(entry, "border");
		changed.add(record);
	}
	for (const record of changed) {
		const overflow = Math.ceil(record.textWidth - record.boxWidth);
		const distance = overflow > 1 ? overflow : 0;
		if (distance === record.distance) continue;
		record.distance = distance;
		const { box } = record;
		if (distance) {
			box.dataset.overflow = "";
			box.style.setProperty("--marquee-distance", `${distance}px`);
			const duration = Math.max(MIN_PASS_S, distance / SPEED_PX_PER_S / TRAVEL_SHARE);
			box.style.setProperty("--marquee-duration", `${duration.toFixed(2)}s`);
			box.style.setProperty("--marquee-delay", `${(START_AFTER_S - duration * HOLD_SHARE).toFixed(2)}s`);
		} else {
			delete box.dataset.overflow;
			box.style.removeProperty("--marquee-distance");
			box.style.removeProperty("--marquee-duration");
			box.style.removeProperty("--marquee-delay");
		}
	}
}

/**
 * Marks a one-line title that doesn't fit so CSS can fade its cut edge instead
 * of an ellipsis, and scroll it on hover. Sets `data-overflow` on the box plus
 * `--marquee-distance` (how far the text must move to show its end) and
 * `--marquee-duration` (one pass) and `--marquee-delay`, straight on the
 * element so a resize never re-renders the row. `textRef` is the inline-block holding the text.
 */
export function useTitleMarquee(boxRef: RefObject<HTMLElement | null>, textRef: RefObject<HTMLElement | null>) {
	useEffect(() => {
		const box = boxRef.current;
		const text = textRef.current;
		if (!box || !text || typeof ResizeObserver === "undefined") return;
		observer ??= new ResizeObserver(onResize);
		const record: Watched = { box, boxWidth: 0, textWidth: 0, distance: 0 };
		watched.set(box, record);
		watched.set(text, record);
		observer.observe(box);
		observer.observe(text);
		return () => {
			observer?.unobserve(box);
			observer?.unobserve(text);
			watched.delete(box);
			watched.delete(text);
		};
	}, [boxRef, textRef]);
}
