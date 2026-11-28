// Where a comments popover sits over the framed page, so the selected element,
// its highlight and the popover can all be seen (the desktop app's popover).
// Imports nothing, so ao-landing can vendor the folder.

/** The selected element's top and bottom in the frame's viewport, CSS px. */
export type CommentsSelectionRect = { top: number; bottom: number };

/** The popover's top, from the top of the frame, and the most it may grow to. */
export type CommentsPopoverPlacement = { top: number; maxHeight: number };

/** Space between the element and the popover. */
export const COMMENTS_POPOVER_GAP = 8;
/** Space kept between the popover and the frame's edges. */
export const COMMENTS_POPOVER_MARGIN = 8;
/** The popover never shrinks below this, even beside a tall element. */
export const COMMENTS_POPOVER_MIN_HEIGHT = 120;

function clamp(value: number, min: number, max: number): number {
	return Math.min(Math.max(value, min), Math.max(min, max));
}

/**
 * Places the popover against the selected element: 8px below it if the
 * popover fits there, else 8px above it, else on the side with more room,
 * with its height capped to that room and kept inside the frame. With no
 * selection, or one scrolled out of the frame, it stays at the top (the app's
 * top-right corner).
 *
 * `popoverHeight` is the popover's natural height, before any cap.
 */
export function placeCommentsPopover(
	selection: CommentsSelectionRect | null,
	frameHeight: number,
	popoverHeight: number,
): CommentsPopoverPlacement {
	const margin = COMMENTS_POPOVER_MARGIN;
	const gap = COMMENTS_POPOVER_GAP;
	const usable = Math.max(0, frameHeight - 2 * margin);
	// No selection, or it is scrolled out of the frame: the top.
	if (!selection || selection.bottom <= 0 || selection.top >= frameHeight) return { top: margin, maxHeight: usable };
	const minHeight = Math.min(COMMENTS_POPOVER_MIN_HEIGHT, usable);
	const below = frameHeight - margin - (selection.bottom + gap);
	const above = selection.top - gap - margin;
	if (popoverHeight <= below) return { top: selection.bottom + gap, maxHeight: below };
	if (popoverHeight <= above) return { top: selection.top - gap - popoverHeight, maxHeight: above };
	// Fits neither side: the roomier one, capped, and never off the frame.
	if (below >= above) {
		const maxHeight = clamp(below, minHeight, usable);
		return { top: clamp(selection.bottom + gap, margin, frameHeight - margin - maxHeight), maxHeight };
	}
	const maxHeight = clamp(above, minHeight, usable);
	const height = Math.min(popoverHeight, maxHeight);
	return { top: clamp(selection.top - gap - height, margin, frameHeight - margin - height), maxHeight };
}
