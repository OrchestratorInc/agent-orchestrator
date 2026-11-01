import type { NativeScrollEvent } from "react-native";

type ScrollMetrics = Pick<NativeScrollEvent, "contentOffset" | "contentSize" | "layoutMeasurement">;

/** How close to the end, in points, still counts as reading the newest message. */
export const TAIL_SLOP = 120;

/** Whether the list is scrolled to (or within a thumb's slop of) its newest message. */
export function atTail({ contentOffset, contentSize, layoutMeasurement }: ScrollMetrics): boolean {
	return contentSize.height - layoutMeasurement.height - contentOffset.y < TAIL_SLOP;
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
