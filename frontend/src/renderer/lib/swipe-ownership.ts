// Two-finger horizontal swipes over the center pane go back and forward (see
// SwipeNavigation). Content that takes horizontal swipes or scrolls for itself
// keeps them: a swipe that starts over it, or that a handler claims, never
// navigates, however much of it later lands on the pane.

/** Put on an element whose horizontal swipes are its own, such as a swipe-to-dismiss
 * card or a code block that scrolls sideways. */
export const NO_SWIPE_NAV_ATTRIBUTE = "data-no-swipe-nav";

// Opted out without the marker: strips that scroll sideways and fade at their
// edges (tab strips, attachment rows), the Browser panel, terminals, and the
// code and table scrollers of a streaming chat reply.
export const NO_SWIPE_NAV_SELECTOR = `[${NO_SWIPE_NAV_ATTRIBUTE}], .edge-fade-x, .browser-panel, .xterm, .chat-md-stream :is(pre, [data-markdown="table"])`;

let claimed = false;

/** For a handler that has taken the current swipe, such as swipe-to-dismiss:
 * the rest of that scroll never navigates, wherever its events land. */
export function claimSwipe(): void {
	claimed = true;
}

/** Whether a handler has claimed the swipe since `resetSwipeClaim`. */
export function swipeClaimed(): boolean {
	return claimed;
}

/** SwipeNavigation calls this when a new swipe starts. */
export function resetSwipeClaim(): void {
	claimed = false;
}
