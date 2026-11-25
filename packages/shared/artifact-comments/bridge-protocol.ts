// Protocol between a comments panel (web viewer or desktop app, trusted) and
// the comment bridge the share host serves into the sandboxed artifact frame
// at /_ao/comments-bridge.js (untrusted page). Only positions travel through
// it, never comment text.
//
// Handshake: the panel posts ARTIFACT_COMMENTS_CONNECT to the frame's window
// with targetOrigin "*" (the frame has an opaque origin) and transfers one
// end of a MessageChannel. The bridge accepts it only when event.source is
// window.parent, keeps the port, and answers `ready` on it. Every later
// message, both ways, goes through that port; nothing else is listened to.
// If no `ready` arrives within ARTIFACT_COMMENTS_BRIDGE_READY_TIMEOUT_MS the
// panel shows threads read-only with a notice.
//
// Every message from the bridge may be forged by the page, so the panel parses
// it with parseBridgeToViewerMessage and drops anything that does not fit.
// This file has no imports so ao-landing and the bridge can vendor it.

export const ARTIFACT_COMMENTS_BRIDGE_PATH = "/_ao/comments-bridge.js";
export const ARTIFACT_COMMENTS_PROTOCOL_VERSION = 1;
export const ARTIFACT_COMMENTS_BRIDGE_READY_TIMEOUT_MS = 3000;
/** The `type` of the one window.postMessage that hands the bridge its port. */
export const ARTIFACT_COMMENTS_CONNECT = "ao-comments:connect";

export type ArtifactCommentsConnectMessage = {
	type: typeof ARTIFACT_COMMENTS_CONNECT;
	protocol: number;
};

/** markdown: whole blocks are pickable; html: the smallest visible element ≥ 8×8 px. */
export type ArtifactCommentsPickKind = "markdown" | "html";

export type BridgePickedAnchor = { path: string; tag: string; textHash: string; excerpt: string };

/**
 * Elements to mark. `count` is the element group's open threads; the bridge
 * draws no bubble at 0 but still reports the element's position, so resolved
 * groups can be ordered too.
 */
export type BridgeAnchorMarker = { path: string; textHash: string; count: number };

/** `top` is in CSS px from the top of the document (not the viewport). */
export type BridgeAnchorPosition = { path: string; top: number; found: boolean };

/**
 * Where the selected element sits in the frame's viewport, so the panel can
 * place a popover beside it: its getBoundingClientRect() top and bottom in
 * viewport CSS px, always finite. `path` is null after `clear`, or when the
 * selected element isn't found; top and bottom then carry no position.
 * Additive to protocol 1: a panel that doesn't know it drops it.
 */
export type BridgeSelection = { path: string | null; top: number; bottom: number };

// Panel → bridge.
export type ViewerToBridgeMessage =
	| { type: "mode"; on: boolean; kind: ArtifactCommentsPickKind }
	| { type: "anchors"; anchors: BridgeAnchorMarker[] }
	| { type: "select"; path: string }
	| { type: "clear" }
	| { type: "theme"; accent: string; tint: string };

// Bridge → panel.
export type BridgeToViewerMessage =
	| { type: "ready"; protocol: number }
	| { type: "pick"; anchor: BridgePickedAnchor }
	| { type: "marker"; path: string }
	| { type: "positions"; positions: BridgeAnchorPosition[] }
	| { type: "escape" }
	| ({ type: "selection" } & BridgeSelection);

const MAX_PATH_LENGTH = 2048;
const MAX_ANCHORS = 2000;
const PATH_PATTERN = /^body(>[A-Za-z][A-Za-z0-9-]*:[1-9][0-9]*)*$/;
const TAG_PATTERN = /^[A-Za-z][A-Za-z0-9-]{0,63}$/;
const TEXT_HASH_PATTERN = /^[0-9a-f]{16}$/;
// Colours are applied by the bridge with style properties, never as markup.
const COLOR_PATTERN = /^(#[0-9a-fA-F]{3,8}|(rgb|rgba|hsl|hsla|oklch|oklab|color)\([0-9a-zA-Z .,%/+-]{1,120}\))$/;

function record(value: unknown): Record<string, unknown> | null {
	return typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Record<string, unknown>) : null;
}

function isPath(value: unknown): value is string {
	return typeof value === "string" && value.length <= MAX_PATH_LENGTH && PATH_PATTERN.test(value);
}

function codePoints(value: string): number {
	let count = 0;
	for (const _ of value) count += 1;
	return count;
}

function pickedAnchor(value: unknown): BridgePickedAnchor | null {
	const anchor = record(value);
	if (anchor === null) return null;
	const { path, tag, textHash, excerpt } = anchor;
	if (!isPath(path)) return null;
	if (typeof tag !== "string" || !TAG_PATTERN.test(tag)) return null;
	if (typeof textHash !== "string" || !TEXT_HASH_PATTERN.test(textHash)) return null;
	if (typeof excerpt !== "string" || codePoints(excerpt) > 120) return null;
	return { path, tag, textHash, excerpt };
}

/** Returns a well-formed bridge message, or null for anything else (drop it). */
export function parseBridgeToViewerMessage(data: unknown): BridgeToViewerMessage | null {
	const message = record(data);
	if (message === null) return null;
	switch (message.type) {
		case "ready":
			return typeof message.protocol === "number" && Number.isInteger(message.protocol)
				? { type: "ready", protocol: message.protocol }
				: null;
		case "pick": {
			const anchor = pickedAnchor(message.anchor);
			return anchor === null ? null : { type: "pick", anchor };
		}
		case "marker":
			return isPath(message.path) ? { type: "marker", path: message.path } : null;
		case "positions": {
			if (!Array.isArray(message.positions) || message.positions.length > MAX_ANCHORS) return null;
			const positions: BridgeAnchorPosition[] = [];
			for (const item of message.positions) {
				const position = record(item);
				if (position === null || !isPath(position.path) || typeof position.found !== "boolean") return null;
				if (typeof position.top !== "number" || !Number.isFinite(position.top)) return null;
				positions.push({ path: position.path, top: position.top, found: position.found });
			}
			return { type: "positions", positions };
		}
		case "escape":
			return { type: "escape" };
		case "selection": {
			const { path, top, bottom } = message;
			if (path !== null && !isPath(path)) return null;
			if (typeof top !== "number" || !Number.isFinite(top)) return null;
			if (typeof bottom !== "number" || !Number.isFinite(bottom)) return null;
			return { type: "selection", path, top, bottom };
		}
		// Unknown types (from a newer bridge) are dropped, so adding a message
		// never breaks an older panel.
		default:
			return null;
	}
}

/** The bridge's side: a well-formed panel message, or null. */
export function parseViewerToBridgeMessage(data: unknown): ViewerToBridgeMessage | null {
	const message = record(data);
	if (message === null) return null;
	switch (message.type) {
		case "mode":
			return typeof message.on === "boolean" && (message.kind === "markdown" || message.kind === "html")
				? { type: "mode", on: message.on, kind: message.kind }
				: null;
		case "anchors": {
			if (!Array.isArray(message.anchors) || message.anchors.length > MAX_ANCHORS) return null;
			const anchors: BridgeAnchorMarker[] = [];
			for (const item of message.anchors) {
				const anchor = record(item);
				if (anchor === null || !isPath(anchor.path)) return null;
				if (typeof anchor.textHash !== "string" || !TEXT_HASH_PATTERN.test(anchor.textHash)) return null;
				if (typeof anchor.count !== "number" || !Number.isInteger(anchor.count) || anchor.count < 0) return null;
				anchors.push({ path: anchor.path, textHash: anchor.textHash, count: anchor.count });
			}
			return { type: "anchors", anchors };
		}
		case "select":
			return isPath(message.path) ? { type: "select", path: message.path } : null;
		case "clear":
			return { type: "clear" };
		case "theme":
			return typeof message.accent === "string" &&
				typeof message.tint === "string" &&
				COLOR_PATTERN.test(message.accent) &&
				COLOR_PATTERN.test(message.tint)
				? { type: "theme", accent: message.accent, tint: message.tint }
				: null;
		default:
			return null;
	}
}

/** The bridge's side: whether a window message is the panel's connect handshake. */
export function isArtifactCommentsConnectMessage(data: unknown): data is ArtifactCommentsConnectMessage {
	const message = record(data);
	return message !== null && message.type === ARTIFACT_COMMENTS_CONNECT && typeof message.protocol === "number";
}
