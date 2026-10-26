import { space } from "../tokens";
import type { DisplayRow } from "./model";

/** Monospace metrics for the code rows: fixed, so unwrapped rows can be measured without layout. */
export const CODE_FONT_SIZE = 13;
export const CODE_LINE_HEIGHT = 20;
/** Geist Mono's advance: every glyph is 0.6em wide. */
const CHAR_WIDTH_EM = 0.6;
/** The +/− column. */
export const MARKER_WIDTH = 16;
/**
 * How far code follows the reader's text size. The gutter, the marker and the
 * code all scale by the same amount, so each line number stays on its line.
 */
export const CODE_MAX_SCALE = 1.4;

/** The factor the code rows are drawn at for the system's font scale. */
export function codeScale(fontScale: number): number {
	return Math.min(fontScale > 0 ? fontScale : 1, CODE_MAX_SCALE);
}

export function gutterWidth(digits: number, scale = 1): number {
	return Math.max(2, digits) * CODE_FONT_SIZE * CHAR_WIDTH_EM * scale + space.md;
}

/**
 * The width an unwrapped listing needs, measured at the size its text is
 * drawn: measured unscaled, a long line at a large text size ran out of room
 * and ended in an ellipsis instead of scrolling.
 */
export function codeContentWidth(rows: DisplayRow[], gutterDigits: number, scale = 1): number {
	let longest = 0;
	for (const row of rows) if (row.type === "line" && row.row.text.length > longest) longest = row.row.text.length;
	return gutterWidth(gutterDigits, scale) + MARKER_WIDTH * scale + space.lg + Math.ceil(longest * CODE_FONT_SIZE * CHAR_WIDTH_EM * scale);
}
