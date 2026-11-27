// Anchors beyond a page element: lines of a text page, a point or rectangle
// of an image, a moment (and optionally a point or rectangle) of a video.
// Text lines are picked by the comment bridge inside the frame; images and
// videos are shown by the viewer itself (web page or app), which picks on
// them with these helpers. Imports only sibling files so ao-landing can
// vendor the folder.

import {
	ARTIFACT_COMMENT_MAX_TIME_SECONDS,
	isArtifactCommentRegion,
	type ArtifactCommentAnchor,
	type ArtifactCommentAnchorKind,
	type ArtifactCommentRegion,
} from "./types";

/** textHash of empty text: what image and video anchors carry (they have no text). */
export const ARTIFACT_COMMENT_EMPTY_TEXT_HASH = "e3b0c44298fc1c14";
/** The path an image anchor carries: the viewer's own picture, not a page element. */
export const ARTIFACT_COMMENT_IMAGE_PATH = "body>img:1";
/** The path a video anchor carries. */
export const ARTIFACT_COMMENT_VIDEO_PATH = "body>video:1";
/** A drag smaller than this (fraction of width or height) is a point, not a rectangle. */
export const ARTIFACT_COMMENT_MIN_REGION = 0.01;

/** The anchor's kind, or null for a kind this client does not know (skip the thread). */
export function artifactCommentAnchorKind(anchor: ArtifactCommentAnchor): ArtifactCommentAnchorKind | null {
	const kind: unknown = anchor.kind;
	if (kind === undefined) return "element";
	return kind === "element" || kind === "lines" || kind === "image" || kind === "video" ? kind : null;
}

/** Whether this client can show the anchor: its kind is known. */
export function isKnownArtifactCommentAnchor(anchor: ArtifactCommentAnchor): boolean {
	return artifactCommentAnchorKind(anchor) !== null;
}

/** Whether the bridge in the frame places this anchor (elements and lines), rather than the viewer. */
export function isBridgeArtifactCommentAnchor(anchor: ArtifactCommentAnchor): boolean {
	const kind = artifactCommentAnchorKind(anchor);
	return kind === "element" || kind === "lines";
}

const round4 = (value: number) => Math.round(value * 10_000) / 10_000;
const clamp01 = (value: number) => Math.min(1, Math.max(0, Number.isFinite(value) ? value : 0));

function regionKey(region: ArtifactCommentRegion): string {
	return [region.x, region.y, region.w, region.h].map((value) => round4(value).toString()).join(",");
}

/**
 * The key threads are grouped by: the element path for elements and lines
 * (so a range starting on line 12 groups with line 12), and the place for
 * images and videos. Unique per place, never a page path for media.
 */
export function artifactCommentAnchorKey(anchor: ArtifactCommentAnchor): string {
	switch (artifactCommentAnchorKind(anchor)) {
		case "image":
			return anchor.region ? `image:${regionKey(anchor.region)}` : "image:";
		case "video": {
			const ms = Math.round((anchor.time ?? 0) * 1000);
			return anchor.region ? `video:${ms}:${regionKey(anchor.region)}` : `video:${ms}`;
		}
		default:
			return anchor.path;
	}
}

/**
 * Where a media group sorts, with no bridge to ask: an image top to bottom,
 * then left to right; a video by time. undefined for elements and lines.
 */
export function artifactCommentMediaOrder(anchor: ArtifactCommentAnchor): number | undefined {
	switch (artifactCommentAnchorKind(anchor)) {
		case "image":
			return anchor.region ? round4(anchor.region.y) * 10_000 + round4(anchor.region.x) : 0;
		case "video":
			return anchor.time ?? 0;
		default:
			return undefined;
	}
}

/** A point on the picture, as fractions of its width and height. */
export type ArtifactCommentPoint = { x: number; y: number };

/**
 * The region from a press at `from` to a release at `to` (fractions, clamped
 * to the picture): a point when the drag is under ARTIFACT_COMMENT_MIN_REGION
 * both ways, else the rectangle between them. Rounded to 4 decimals.
 */
export function artifactCommentRegionFromDrag(from: ArtifactCommentPoint, to: ArtifactCommentPoint = from): ArtifactCommentRegion {
	const x1 = clamp01(from.x), y1 = clamp01(from.y), x2 = clamp01(to.x), y2 = clamp01(to.y);
	if (Math.abs(x2 - x1) < ARTIFACT_COMMENT_MIN_REGION && Math.abs(y2 - y1) < ARTIFACT_COMMENT_MIN_REGION) {
		return { x: round4(x1), y: round4(y1), w: 0, h: 0 };
	}
	const x = round4(Math.min(x1, x2));
	const y = round4(Math.min(y1, y2));
	// From the rounded edges, so x + w never passes 1.
	return { x, y, w: round4(round4(Math.max(x1, x2)) - x), h: round4(round4(Math.max(y1, y2)) - y) };
}

export function isPointRegion(region: ArtifactCommentRegion): boolean {
	return region.w === 0 && region.h === 0;
}

/** A new image anchor on a point or rectangle. */
export function imageCommentAnchor(region: ArtifactCommentRegion): ArtifactCommentAnchor {
	return { path: ARTIFACT_COMMENT_IMAGE_PATH, tag: "img", textHash: ARTIFACT_COMMENT_EMPTY_TEXT_HASH, excerpt: "", kind: "image", region };
}

/** A new video anchor at `time` seconds (ms precision), optionally on a point or rectangle of that frame. */
export function videoCommentAnchor(time: number, region?: ArtifactCommentRegion): ArtifactCommentAnchor {
	const seconds = Math.min(ARTIFACT_COMMENT_MAX_TIME_SECONDS, Math.max(0, Number.isFinite(time) ? Math.round(time * 1000) / 1000 : 0));
	const anchor: ArtifactCommentAnchor = {
		path: ARTIFACT_COMMENT_VIDEO_PATH,
		tag: "video",
		textHash: ARTIFACT_COMMENT_EMPTY_TEXT_HASH,
		excerpt: "",
		kind: "video",
		time: seconds,
	};
	if (region && isArtifactCommentRegion(region)) anchor.region = region;
	return anchor;
}

/** A box in CSS px, relative to its container. */
export type ArtifactCommentBox = { left: number; top: number; width: number; height: number };

/**
 * Where a picture of `natural` size is drawn inside a `box`-sized element
 * with object-fit: contain (centred, letterboxed), so a region maps to the
 * pixels on screen and a pointer to the picture's fractions.
 */
export function artifactCommentContainedBox(
	natural: { width: number; height: number },
	box: { width: number; height: number },
): ArtifactCommentBox {
	if (!(natural.width > 0 && natural.height > 0 && box.width > 0 && box.height > 0)) {
		return { left: 0, top: 0, width: Math.max(0, box.width), height: Math.max(0, box.height) };
	}
	const scale = Math.min(box.width / natural.width, box.height / natural.height);
	const width = natural.width * scale;
	const height = natural.height * scale;
	return { left: (box.width - width) / 2, top: (box.height - height) / 2, width, height };
}

/** A pointer at (x, y) px in the element, as fractions of the drawn picture (clamped). */
export function artifactCommentPointIn(box: ArtifactCommentBox, x: number, y: number): ArtifactCommentPoint {
	return {
		x: box.width > 0 ? clamp01((x - box.left) / box.width) : 0,
		y: box.height > 0 ? clamp01((y - box.top) / box.height) : 0,
	};
}

/** A region drawn on the picture, in px relative to the element. */
export function artifactCommentRegionBox(box: ArtifactCommentBox, region: ArtifactCommentRegion): ArtifactCommentBox {
	return {
		left: box.left + region.x * box.width,
		top: box.top + region.y * box.height,
		width: region.w * box.width,
		height: region.h * box.height,
	};
}

/** "01:23", or "1:02:03" from an hour on. */
export function formatArtifactCommentTime(seconds: number): string {
	const total = Math.max(0, Math.floor(Number.isFinite(seconds) ? seconds : 0));
	const hours = Math.floor(total / 3600);
	const minutes = Math.floor((total % 3600) / 60);
	const secs = total % 60;
	const two = (value: number) => value.toString().padStart(2, "0");
	return hours > 0 ? `${hours}:${two(minutes)}:${two(secs)}` : `${two(minutes)}:${two(secs)}`;
}

export type ArtifactCommentRegionPlace =
	| "top-left"
	| "top"
	| "top-right"
	| "left"
	| "center"
	| "right"
	| "bottom-left"
	| "bottom"
	| "bottom-right";

/** Which ninth of the picture the region's centre falls in. */
export function artifactCommentRegionPlace(region: ArtifactCommentRegion): ArtifactCommentRegionPlace {
	const cx = region.x + region.w / 2;
	const cy = region.y + region.h / 2;
	const column = cx < 1 / 3 ? 0 : cx < 2 / 3 ? 1 : 2;
	const row = cy < 1 / 3 ? 0 : cy < 2 / 3 ? 1 : 2;
	const places: ArtifactCommentRegionPlace[][] = [
		["top-left", "top", "top-right"],
		["left", "center", "right"],
		["bottom-left", "bottom", "bottom-right"],
	];
	return places[row][column];
}

/**
 * The anchor in plain words, for viewers that are not translated and for the
 * agent message: "Line 12", "Lines 12–14", "Rows 3–5", "Point near top-left",
 * "Region near center", "At 01:23", "At 01:23, region near top-left".
 * null for an element (and an unknown kind): the page shows where it is.
 */
export function artifactCommentAnchorLabel(anchor: ArtifactCommentAnchor): string | null {
	switch (artifactCommentAnchorKind(anchor)) {
		case "lines": {
			if (!anchor.lines) return null;
			const { start, end } = anchor.lines;
			const rows = anchor.tag === "tr";
			if (start === end) return `${rows ? "Row" : "Line"} ${start}`;
			return `${rows ? "Rows" : "Lines"} ${start}–${end}`;
		}
		case "image":
			return anchor.region ? regionWords(anchor.region, true) : null;
		case "video": {
			const at = `At ${formatArtifactCommentTime(anchor.time ?? 0)}`;
			return anchor.region ? `${at}, ${regionWords(anchor.region, false)}` : at;
		}
		default:
			return null;
	}
}

function regionWords(region: ArtifactCommentRegion, capital: boolean): string {
	const noun = isPointRegion(region) ? "point" : "region";
	const word = capital ? noun[0].toUpperCase() + noun.slice(1) : noun;
	return `${word} near ${artifactCommentRegionPlace(region)}`;
}
