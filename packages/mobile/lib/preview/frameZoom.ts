/**
 * The live browser's pinch zoom, as plain functions the gesture worklets call
 * on the UI thread. A zoom scales the viewport about its centre and then moves
 * it by an offset, which is how the transform `[translate, scale]` draws it.
 */

export type FramePoint = { x: number; y: number };
export type FrameSize = { width: number; height: number };
export type FrameZoom = { scale: number; x: number; y: number };

/** Close enough to the desktop's size that a dirty pinch lands back on it. */
export const FRAME_ZOOM_SNAP = 1.05;
/** Past this the JPEG frame is too soft to read anything new. */
export const MAX_FRAME_ZOOM = 4;

export const unzoomed: FrameZoom = { scale: 1, x: 0, y: 0 };

/** Where a point on screen lies on the unzoomed viewport, so a tap or a drag reaches the page under the finger. */
export function unzoomPoint(point: FramePoint, zoom: FrameZoom, size: FrameSize): FramePoint {
	"worklet";
	const centreX = size.width / 2;
	const centreY = size.height / 2;
	return {
		x: centreX + (point.x - centreX - zoom.x) / zoom.scale,
		y: centreY + (point.y - centreY - zoom.y) / zoom.scale,
	};
}

/** Keeps a zoomed viewport covering the screen: it can move only as far as its scale has grown it. */
export function clampZoom(zoom: FrameZoom, size: FrameSize): FrameZoom {
	"worklet";
	const reachX = ((zoom.scale - 1) * size.width) / 2;
	const reachY = ((zoom.scale - 1) * size.height) / 2;
	return {
		scale: zoom.scale,
		x: Math.min(reachX, Math.max(-reachX, zoom.x)),
		y: Math.min(reachY, Math.max(-reachY, zoom.y)),
	};
}

/**
 * The zoom a pinch has reached. What was under the fingers when it began stays
 * under them, so spreading them magnifies that spot and moving them together
 * pans across the page.
 */
export function pinchZoom(start: FrameZoom, startFocal: FramePoint, focal: FramePoint, pinchScale: number, size: FrameSize): FrameZoom {
	"worklet";
	const scale = Math.min(MAX_FRAME_ZOOM, Math.max(1, start.scale * pinchScale));
	const anchor = unzoomPoint(startFocal, start, size);
	const centreX = size.width / 2;
	const centreY = size.height / 2;
	return clampZoom({
		scale,
		x: focal.x - centreX - scale * (anchor.x - centreX),
		y: focal.y - centreY - scale * (anchor.y - centreY),
	}, size);
}
