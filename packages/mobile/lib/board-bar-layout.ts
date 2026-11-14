import { space } from "./tokens";

/**
 * Android's board bar (see BoardTabBar.android): the Workers and Projects pill
 * at the start and the round New task button at the end, floated over the page
 * as iOS 26 floats its tab bar and the button it sets apart.
 */

/** The pill's and the button's height. */
export const BOARD_BAR_HEIGHT = 64;

/** How far the bar floats above the system navigation, and in from each side. */
export const BOARD_BAR_MARGIN = space.lg;

/** How much of a tab page's bottom the bar covers, the system navigation under it included. */
export function boardBarFootprint(bottomInset: number): number {
	return BOARD_BAR_HEIGHT + BOARD_BAR_MARGIN + bottomInset;
}
