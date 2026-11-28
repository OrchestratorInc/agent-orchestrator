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

const ANCHOR_FIRST_VISIBLE = { minIndexForVisible: 0 } as const;

/**
 * What the list holds in place when content off screen resizes (its
 * `maintainVisibleContentPosition`): the first row in view, except on iOS while
 * it follows the newest message, where nothing needs holding because every
 * size change scrolls it to the end anyway.
 *
 * React Native's iOS anchor is the first view in sight, and that can be the
 * list's spacer for rows not drawn yet. A long chat opening scrolls to its end
 * before the newest rows are drawn, so it landed in the spacer below the tall
 * replies. Drawing those rows recycled the spacer as another view, and the
 * anchor read the move as the content shifting by the spacer's whole offset and
 * scrolled back by as much: up into the tall replies, where the spacer was drawn
 * again, so each follow to the end bounced back to the same place.
 */
export function visibleContentAnchor(platform: string, following: boolean): typeof ANCHOR_FIRST_VISIBLE | undefined {
	return platform === "ios" && following ? undefined : ANCHOR_FIRST_VISIBLE;
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
