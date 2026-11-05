/**
 * The swipe rail's width: one slot, the pin. Archive is the full swipe past it,
 * not a second button. The slot is the control's 44pt with the gaps the rail
 * kept around two: 12pt before it and the row's 16pt gutter after.
 */
export const WORKER_ACTION_REVEAL_WIDTH = 72;

/** The rail's width when it held pin and archive, which the full swipe was tuned against. */
const TWO_BUTTON_RAIL_WIDTH = 128;

/**
 * How much of the row's width a drag has to uncover before letting go archives
 * it, as a full swipe does in Mail. The pin and archive pair armed at 60% of
 * the row; that ratio is scaled to the one-button rail, so the red comes in a
 * short drag after the pin is showing and says a swipe can archive, rather
 * than after a long dark stretch that reads as the row just sliding about.
 */
const FULL_SWIPE_SHARE = 0.6 * (WORKER_ACTION_REVEAL_WIDTH / TWO_BUTTON_RAIL_WIDTH);

/**
 * The least a full swipe travels on any row, one and a half of the rail as it
 * was for the pair, so a narrow row never arms while the pin is still opening.
 */
const MIN_FULL_SWIPE = WORKER_ACTION_REVEAL_WIDTH * 1.5;

/**
 * Whether a row dragged to `offset` (negative is to the left) has gone far
 * enough to archive when let go. Runs on the UI thread with the drag.
 */
export function isFullSwipe(offset: number, rowWidth: number): boolean {
	"worklet";
	if (rowWidth <= 0) return false;
	return -offset >= Math.max(rowWidth * FULL_SWIPE_SHARE, MIN_FULL_SWIPE);
}
