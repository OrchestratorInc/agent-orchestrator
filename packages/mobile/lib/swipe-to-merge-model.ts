export const SWIPE_TO_MERGE_THRESHOLD_RATIO = 0.85;

/** How far the knob can travel: the track width less the knob and its inset on both ends. */
export function swipeToMergeTravel(trackWidth: number, knobSize: number, inset: number): number {
	"worklet";
	return Math.max(0, trackWidth - knobSize - inset * 2);
}

/** The knob only moves right, and never past the end of the track. */
export function boundSwipeToMergeTranslation(translationX: number, travel: number): number {
	"worklet";
	return Math.max(0, Math.min(Math.max(0, travel), translationX));
}

/**
 * Merging is distance-based, not velocity-based: a flick while scrolling must
 * not merge a pull request. The knob has to be carried nearly to the end.
 */
export function shouldCommitSwipeToMerge(translationX: number, travel: number): boolean {
	"worklet";
	return travel > 0 && translationX >= travel * SWIPE_TO_MERGE_THRESHOLD_RATIO;
}

/** How many haptic detents the knob passes over the full track. */
export const SWIPE_TO_MERGE_DETENTS = 10;

/** Which detent the knob sits in; a change between frames is one haptic tick. */
export function swipeToMergeDetent(translationX: number, travel: number): number {
	"worklet";
	return travel > 0 ? Math.floor((translationX / travel) * SWIPE_TO_MERGE_DETENTS) : 0;
}
