/**
 * The arithmetic behind the image viewer's pinch, pan and double tap, kept
 * apart from the gestures so it can be tested without a UI thread. Each
 * function is a worklet: the gestures call them on the UI thread.
 *
 * Offsets are from the viewport's centre, where the image sits at rest and
 * scales about.
 */

export const MIN_ZOOM = 1;
export const MAX_ZOOM = 4;
/** A double tap's zoom: close enough to read, far enough to tell it happened. */
export const DOUBLE_TAP_ZOOM = 2.5;
/** How far a downward drag of the unzoomed image goes before letting go closes it. */
export const DISMISS_DISTANCE = 120;
/** A flick this fast closes it from any distance. */
export const DISMISS_VELOCITY = 1000;

export function clamp(value: number, min: number, max: number): number {
	"worklet";
	return Math.min(max, Math.max(min, value));
}

/**
 * Where the image has to move so the point under the fingers stays under them
 * while it scales by `factor`: a point drawn at `focal` sits at
 * `(focal - translate) / scale` in the image, and has to land on `focal` again.
 */
export function zoomTranslation(translate: number, focal: number, factor: number): number {
	"worklet";
	return translate + (focal - translate) * (1 - factor);
}

/** How far the image can slide at `scale` before an edge comes away from the viewport's. */
export function maxTranslation(viewport: number, scale: number): number {
	"worklet";
	return Math.max(0, (viewport * scale - viewport) / 2);
}

export function boundTranslation(translate: number, viewport: number, scale: number): number {
	"worklet";
	const limit = maxTranslation(viewport, scale);
	return clamp(translate, -limit, limit);
}

/** Whether a vertical drag of the unzoomed image, let go here, closes the viewer. */
export function shouldDismiss(translationY: number, velocityY: number): boolean {
	"worklet";
	return Math.abs(translationY) > DISMISS_DISTANCE || Math.abs(velocityY) > DISMISS_VELOCITY;
}

/** The backdrop's opacity as the image is dragged away: it thins out as the image leaves. */
export function backdropOpacity(translationY: number, viewport: number): number {
	"worklet";
	return clamp(1 - Math.abs(translationY) / Math.max(1, viewport * 0.6), 0.2, 1);
}
