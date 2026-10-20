export const NOTIFICATION_CLEAR_THRESHOLD_RATIO = 0.72;

/** Notifications only travel left; a rightward drag never exposes an action. */
export function boundNotificationSwipeTranslation(translationX: number, rowWidth: number): number {
	"worklet";
	return Math.max(-Math.max(0, rowWidth), Math.min(0, translationX));
}

/**
 * Clearing is deliberately distance-based instead of velocity-based. A quick
 * horizontal flick can happen while scrolling a feed; the user has to carry
 * the row close to the left edge before the destructive action commits.
 */
export function shouldClearNotificationSwipe(translationX: number, rowWidth: number): boolean {
	"worklet";
	return rowWidth > 0 && translationX <= -(rowWidth * NOTIFICATION_CLEAR_THRESHOLD_RATIO);
}
