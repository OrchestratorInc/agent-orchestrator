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

/** One frame toward the measured end, with a short lead after content grows. */
export function tailFollowStep(offset: number, end: number, viewport: number, elapsedMs: number, growth: number, growthAgeMs: number): number {
	const gap = end - offset;
	if (gap <= 0.5 || gap > viewport || elapsedMs >= 1000) return end;
	const lead = Math.min(24, growth * 0.35) * Math.max(0, 1 - Math.max(0, growthAgeMs) / 150);
	const fraction = 1 - Math.exp(-Math.min(elapsedMs, 50) / 85);
	return Math.min(end, offset + (gap + lead) * fraction);
}

/**
 * Whether the list is scrolled to (or within a thumb's slop of) its newest
 * message. `inset` is room the list has made below its content (the keyboard's).
 */
export function atTail({ contentOffset, contentSize, layoutMeasurement }: ScrollMetrics, inset = 0, slop = TAIL_SLOP): boolean {
	return contentSize.height - layoutMeasurement.height + inset - contentOffset.y < slop;
}

/** Where a person's drag let go, and whether that was at the newest message. */
export type Release = { offset: number; atEnd: boolean };

/**
 * Whether a person's scroll that came to rest leaves the list following new
 * output. Android reports momentum after every touch-up, even a still one, and
 * ends it a few frames later. Output that arrives in between moves the end, not
 * the reader, so a list that has not moved since the release keeps the
 * release's verdict. A list that moved, by a fling or by scroll anchoring for
 * content above, is measured where it rests.
 */
export function followsAfterRest(release: Release | undefined, offset: number, atEndNow: boolean): boolean {
	return release !== undefined && Math.abs(offset - release.offset) < 1 ? release.atEnd : atEndNow;
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
