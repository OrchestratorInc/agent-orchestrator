/**
 * Taps outside a floating control, for one that closes on them. Touch-start
 * events bubble from the touched view up to the app's root: the control marks
 * a touch inside it on the way, and the root, which hears every touch last,
 * tells the listeners about any touch nobody marked. Nothing is consumed, so
 * the tap still lands wherever it was aimed.
 */

let inside = false;
const listeners = new Set<() => void>();

/** Call from a floating control's `onTouchStart`: this touch is its own. */
export function markTouchInside(): void {
	inside = true;
}

/** Hears every touch that no floating control marked as its own. */
export function onTouchOutside(listener: () => void): () => void {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
}

/** The app root's `onTouchStart`, after every view under the finger has had its say. */
export function rootTouchStart(): void {
	const marked = inside;
	inside = false;
	if (!marked) for (const listener of [...listeners]) listener();
}
