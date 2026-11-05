import type { CSSProperties } from "react";

/**
 * The center pane can split into two panes. The main pane always keeps the
 * session's own tab and the topbar tab strip; the side pane holds the tabs the
 * user dragged out of it and has its own strip. The side pane sits on `side`
 * and takes `size` of the area; it closes when its last tab leaves.
 */
export type SplitSide = "left" | "right" | "top" | "bottom";
export type PaneId = "main" | "side";

export type SplitLayout = {
	side: SplitSide;
	/** Fraction of the area the side pane takes. */
	size: number;
	/** Tab keys in the side pane, in strip order. */
	keys: string[];
	active: string;
};

/** Drag key for the session's own tab (chat or agent terminal). */
export const SESSION_TAB_KEY = "session";
export const SPLIT_SIZE_MIN = 0.2;
export const SPLIT_SIZE_MAX = 0.8;
/** CSS variable on the split area holding the side pane's fraction. */
export const SPLIT_SIZE_VAR = "--pane-split-size";

export function clampSplitSize(size: number): number {
	if (!Number.isFinite(size)) return 0.5;
	return Math.min(SPLIT_SIZE_MAX, Math.max(SPLIT_SIZE_MIN, size));
}

export function oppositeSide(side: SplitSide): SplitSide {
	return side === "left" ? "right" : side === "right" ? "left" : side === "top" ? "bottom" : "top";
}

export function isVerticalSplit(side: SplitSide): boolean {
	return side === "top" || side === "bottom";
}

/**
 * Drops tabs that no longer exist and fixes the active tab. Returns null when
 * nothing is left, which collapses the split.
 */
export function reconcileSplit(layout: SplitLayout | null, available: ReadonlySet<string>): SplitLayout | null {
	if (!layout) return null;
	const keys = layout.keys.filter((key) => available.has(key));
	if (keys.length === 0) return null;
	if (keys.length === layout.keys.length && keys.includes(layout.active)) return layout;
	// A closed active tab hands over to the tab that stood before it.
	const removedIndex = layout.keys.indexOf(layout.active);
	const active = keys.includes(layout.active)
		? layout.active
		: keys[Math.max(0, Math.min(keys.length - 1, removedIndex - 1))]!;
	return { ...layout, keys, active };
}

const size = `var(${SPLIT_SIZE_VAR})`;
const rest = `(1 - var(${SPLIT_SIZE_VAR}))`;

/**
 * The box of a pane inside the split area, as CSS that reads the size variable.
 * A live resize then only rewrites that variable; no pane re-renders. `inset`
 * trims the top edge (the side pane's own tab strip).
 */
export function paneBox(pane: PaneId, side: SplitSide | null, topInset?: string): CSSProperties {
	let box: { left: string; top: string; width: string; height: string };
	if (!side) {
		box = { left: "0px", top: "0px", width: "100%", height: "100%" };
	} else {
		const own = pane === "side" ? size : rest;
		const other = pane === "side" ? rest : size;
		const first = (pane === "side") === (side === "left" || side === "top");
		const offset = first ? "0px" : `calc(${other} * 100%)`;
		box = isVerticalSplit(side)
			? { left: "0px", top: offset, width: "100%", height: `calc(${own} * 100%)` }
			: { left: offset, top: "0px", width: `calc(${own} * 100%)`, height: "100%" };
	}
	if (topInset) {
		box = { ...box, top: `calc(${box.top} + ${topInset})`, height: `calc(${box.height} - ${topInset})` };
	}
	return { position: "absolute", ...box };
}

/** Where the boundary between the panes sits, measured from the area's start. */
export function dividerOffset(side: SplitSide): string {
	return side === "left" || side === "top" ? `calc(${size} * 100%)` : `calc(${rest} * 100%)`;
}

export type ClientRect = { left: number; top: number; width: number; height: number };

/**
 * Which edge a pointer over an unsplit area points at. Left and right halves
 * cover most of the area; top and bottom take a band near those edges only
 * when the pointer is closer to them than to a side.
 */
export function edgeAt(rect: ClientRect, x: number, y: number): SplitSide | null {
	if (rect.width <= 0 || rect.height <= 0) return null;
	const nx = (x - rect.left) / rect.width;
	const ny = (y - rect.top) / rect.height;
	if (nx < 0 || nx > 1 || ny < 0 || ny > 1) return null;
	const dy = Math.min(ny, 1 - ny);
	const dx = Math.min(nx, 1 - nx);
	if (dy < 0.22 && dy < dx) return ny < 0.5 ? "top" : "bottom";
	return nx < 0.5 ? "left" : "right";
}

/** The client box a pane would take with the side pane on `side` at `fraction`. */
export function paneClientRect(rect: ClientRect, pane: PaneId, side: SplitSide, fraction: number): ClientRect {
	const own = pane === "side" ? fraction : 1 - fraction;
	const first = (pane === "side") === (side === "left" || side === "top");
	if (isVerticalSplit(side)) {
		const height = rect.height * own;
		return { left: rect.left, width: rect.width, height, top: first ? rect.top : rect.top + rect.height - height };
	}
	const width = rect.width * own;
	return { top: rect.top, height: rect.height, width, left: first ? rect.left : rect.left + rect.width - width };
}

export function containsPoint(rect: ClientRect, x: number, y: number): boolean {
	return x >= rect.left && x <= rect.left + rect.width && y >= rect.top && y <= rect.top + rect.height;
}
