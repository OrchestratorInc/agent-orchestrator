/** The swipe rail's width: two 64pt slots, pin and archive. */
export const WORKER_ACTION_REVEAL_WIDTH = 128;

/**
 * How much of the row's width a drag has to uncover before letting go archives
 * it, as a full swipe does in Mail: well past the rail, so a drag that only
 * opens it never archives by accident.
 */
const FULL_SWIPE_SHARE = 0.6;

/**
 * Whether a row dragged to `offset` (negative is to the left) has gone far
 * enough to archive when let go. Runs on the UI thread with the drag.
 */
export function isFullSwipe(offset: number, rowWidth: number): boolean {
	"worklet";
	if (rowWidth <= 0) return false;
	return -offset >= Math.max(rowWidth * FULL_SWIPE_SHARE, WORKER_ACTION_REVEAL_WIDTH * 1.5);
}
