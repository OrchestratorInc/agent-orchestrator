/**
 * Region snip: the person drags a rectangle over the Browser panel's page and
 * the cropped capture lands in the session's Chat composer as an attachment.
 *
 * The page preload draws the drag overlay and reports the rectangle in CSS
 * pixels; main converts it to the view's DIP space, captures that area, and
 * hands the PNG to the shell, which puts it in the composer.
 */

export type BrowserRegionPoint = { x: number; y: number };
export type BrowserRegionRect = { x: number; y: number; width: number; height: number };
export type BrowserRegionSize = { width: number; height: number };

/** Why a snip ended without an image. */
export type BrowserRegionSelectCancelReason = "cancel" | "navigation" | "tab" | "failed";

/** Shell → main: turn the snip overlay on or off for the panel's active tab. */
export type BrowserRegionSelectModeInput = { viewId: string; enabled: boolean };
/** Main → page: show or remove the overlay. */
export type BrowserRegionSelectPageMode = { enabled: boolean };
/** Page → main: the rectangle the person dragged, in CSS pixels of the viewport. */
export type BrowserRegionSelectPagePayload = { rect: BrowserRegionRect };
/** Main → shell: the cropped capture. */
export type BrowserRegionSelectCapturedPayload = {
	viewId: string;
	image: { mimeType: "image/png"; data: string };
};
/** Main → shell: the snip ended without an image. */
export type BrowserRegionSelectCanceledPayload = { viewId: string; reason: BrowserRegionSelectCancelReason };

/** A drag shorter than this on either side is a click, and cancels. */
export const MIN_REGION_SELECT_SIZE = 6;
/** Longest edge of the image handed to chat; larger captures are scaled down. */
export const REGION_SELECT_MAX_DIMENSION = 2560;

function clamp(value: number, min: number, max: number): number {
	return Math.min(max, Math.max(min, value));
}

/** The rectangle between two drag points, kept inside the viewport. */
export function normalizeRegionRect(
	start: BrowserRegionPoint,
	end: BrowserRegionPoint,
	viewport: BrowserRegionSize,
): BrowserRegionRect {
	const left = clamp(Math.min(start.x, end.x), 0, viewport.width);
	const top = clamp(Math.min(start.y, end.y), 0, viewport.height);
	const right = clamp(Math.max(start.x, end.x), 0, viewport.width);
	const bottom = clamp(Math.max(start.y, end.y), 0, viewport.height);
	return { x: left, y: top, width: right - left, height: bottom - top };
}

export function isRegionLargeEnough(rect: BrowserRegionRect): boolean {
	return rect.width >= MIN_REGION_SELECT_SIZE && rect.height >= MIN_REGION_SELECT_SIZE;
}

/**
 * Converts the page's CSS-pixel rectangle to the whole-DIP rectangle
 * `webContents.capturePage` expects, scaled by the page zoom and clipped to
 * the view. Undefined when the input is malformed or nothing is left.
 */
export function regionCaptureRect(
	rect: unknown,
	zoomFactor: number,
	view: BrowserRegionSize,
): BrowserRegionRect | undefined {
	if (!rect || typeof rect !== "object") return undefined;
	const { x, y, width, height } = rect as Record<string, unknown>;
	const values = [x, y, width, height];
	if (!values.every((value) => typeof value === "number" && Number.isFinite(value))) return undefined;
	const zoom = Number.isFinite(zoomFactor) && zoomFactor > 0 ? zoomFactor : 1;
	const left = clamp(Math.floor((x as number) * zoom), 0, view.width);
	const top = clamp(Math.floor((y as number) * zoom), 0, view.height);
	const right = clamp(Math.ceil(((x as number) + (width as number)) * zoom), 0, view.width);
	const bottom = clamp(Math.ceil(((y as number) + (height as number)) * zoom), 0, view.height);
	if (right - left < 1 || bottom - top < 1) return undefined;
	return { x: left, y: top, width: right - left, height: bottom - top };
}

/** File name for the attachment, e.g. browser-selection-2026-11-20-14-03-07.png. */
export function regionSelectFileName(now: Date): string {
	const pad = (value: number) => String(value).padStart(2, "0");
	const stamp = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}-${pad(now.getHours())}-${pad(now.getMinutes())}-${pad(now.getSeconds())}`;
	return `browser-selection-${stamp}.png`;
}
