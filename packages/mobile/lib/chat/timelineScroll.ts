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

/**
 * The row the timeline opens on: the start of the newest `window` rows. The list
 * draws those first and scrolls to them, rather than drawing the oldest rows and
 * then jumping to an end it has not drawn yet, which showed an empty list while
 * the newest turns caught up.
 */
export function openingRow(count: number, window: number): number {
	return Math.max(0, count - window);
}

/** How quickly a follow glide closes on the end: the gap left decays with this time constant. */
export const GLIDE_TAU_MS = 140;
/** The fastest a glide moves, in points per millisecond, so a long one starts gently. */
export const GLIDE_MAX_SPEED = 3;
/** The slowest, so its last few points do not crawl. */
const GLIDE_MIN_SPEED = 0.2;

/**
 * One frame of a follow glide from `from` toward `to` (see useFollowGlide).
 * Further than `reach` away, it first jumps to within `reach` of `to`: no
 * glide covers more than a screen. Returns `to` once there.
 */
export function glideStep(from: number, to: number, elapsedMs: number, reach = 0): number {
	"worklet";
	let start = from;
	if (reach > 0 && Math.abs(to - start) > reach) start = to - Math.sign(to - start) * reach;
	const gap = to - start;
	if (Math.abs(gap) < 0.5) return to;
	const elapsed = Math.min(Math.max(elapsedMs, 1), 48);
	const eased = Math.abs(gap) * (1 - Math.exp(-elapsed / GLIDE_TAU_MS));
	const distance = Math.min(Math.max(eased, GLIDE_MIN_SPEED * elapsed), GLIDE_MAX_SPEED * elapsed, Math.abs(gap));
	return start + Math.sign(gap) * distance;
}

/** Where a send's new message will be found: after the newest item the conversation had. */
export function newestSequence(items: readonly { sequence: number }[]): number {
	let newest = 0;
	for (const item of items) if (item.sequence > newest) newest = item.sequence;
	return newest;
}

/**
 * The room a just-sent message's turn holds open below it, so that at the end
 * of the list the message sits at the top of what can be seen (under the
 * header and the banners) while the reply grows into the space beneath it.
 * `keyboard` is the room the keyboard took when the message appeared: the
 * keyboard usually stays up through a send, and the turn is laid out for the
 * part of the screen above it.
 */
export function sentTurnRoom({ listHeight, keyboard, topClear, endPad }: { listHeight: number; keyboard: number; topClear: number; endPad: number }): number {
	return Math.max(0, Math.round(listHeight - keyboard - topClear - endPad));
}

/** A send under way: its message is the first human message after `after` in conversation `conversationId`. */
export type SendAnchor = { conversationId: string; after: number; at: number };
