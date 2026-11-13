// The width a custom header title can take between the back button and the
// header's actions. A custom title view is laid out at its own size: UIKit
// neither shrinks nor truncates it, so one wider than the gap is drawn under
// the buttons on either side.

/** The back button and its margin, from the screen edge. */
const SIDE = 60;
/** Each header action after the first. */
const ACTION = 60;
/** Clearance between the title and the buttons. */
const GAP = 8;
const MAX_WIDTH = 240;
const MIN_WIDTH = 120;

/**
 * The widest a centred header title can be with `actions` buttons on its right.
 * The title is centred, so the wider side sets the room on both.
 */
export function headerTitleWidth(windowWidth: number, actions: number): number {
	const side = SIDE + ACTION * Math.max(0, actions - 1);
	return Math.max(MIN_WIDTH, Math.min(MAX_WIDTH, windowWidth - 2 * side - GAP));
}
