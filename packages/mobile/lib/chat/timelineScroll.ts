import type { NativeScrollEvent } from "react-native";

type ScrollMetrics = Pick<NativeScrollEvent, "contentOffset" | "contentSize" | "layoutMeasurement">;

/** How close to the end, in points, still counts as reading the newest message. */
export const TAIL_SLOP = 120;

/**
 * How close to the end a scroll must come to rest for the list to follow new
 * output again. Tighter than `TAIL_SLOP`: a short flick up to start reading
 * lets go inside a thumb's slop, and reading that as "at the end" pulled the
 * person back down with the next output.
 */
export const RESUME_SLOP = 48;

/**
 * Whether the list is scrolled to (or within a thumb's slop of) its newest
 * message. `inset` is room the list has made below its content (the keyboard's).
 */
export function atTail({ contentOffset, contentSize, layoutMeasurement }: ScrollMetrics, inset = 0, slop = TAIL_SLOP): boolean {
	return contentSize.height - layoutMeasurement.height + inset - contentOffset.y < slop;
}

/**
 * The row the timeline opens on: the start of the newest `window` rows. The list
 * draws those first and scrolls to them, rather than drawing the oldest rows and
 * then jumping to an end it has not drawn yet, which showed an empty list while
 * the newest turns caught up.
 */
export function openingRow(count: number, window: number): number {
	return Math.max(0, count - window);
}
