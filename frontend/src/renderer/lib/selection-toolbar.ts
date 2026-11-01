export const selectionToolbarClass =
	"z-overlay flex w-max flex-wrap gap-0.5 rounded-lg border border-(--color-border-overlay) bg-overlay p-0.5 font-sans shadow-(--shadow-popover)";
export const selectionActionClass =
	"min-h-7 min-w-0 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-interactive-hover hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50";

type Rect = Pick<DOMRect, "left" | "right" | "top" | "bottom">;

export function selectionRangesEqual(
	a: Range | undefined,
	b: Range | undefined,
): boolean {
	return Boolean(
		a &&
			b &&
			a.startContainer === b.startContainer &&
			a.startOffset === b.startOffset &&
			a.endContainer === b.endContainer &&
			a.endOffset === b.endOffset,
	);
}

/** Clip to the window and each enclosing scroll/clip pane, never the whole page. */
export function selectionViewport(element?: Element): Rect {
	const visual = window.visualViewport;
	let bounds: Rect = {
		left: visual?.offsetLeft ?? 0,
		top: visual?.offsetTop ?? 0,
		right: (visual?.offsetLeft ?? 0) + (visual?.width ?? window.innerWidth),
		bottom: (visual?.offsetTop ?? 0) + (visual?.height ?? window.innerHeight),
	};
	for (let node = element; node; node = node.parentElement ?? undefined) {
		const style = getComputedStyle(node);
		const clipX =
			node === element || /(auto|scroll|hidden|clip)/.test(style.overflowX);
		const clipY =
			node === element || /(auto|scroll|hidden|clip)/.test(style.overflowY);
		if (!clipX && !clipY) continue;
		const rect = node.getBoundingClientRect();
		bounds = {
			left: clipX ? Math.max(bounds.left, rect.left) : bounds.left,
			right: clipX ? Math.min(bounds.right, rect.right) : bounds.right,
			top: clipY ? Math.max(bounds.top, rect.top) : bounds.top,
			bottom: clipY ? Math.min(bounds.bottom, rect.bottom) : bounds.bottom,
		};
	}
	return bounds;
}

/** First-line anchor, with the full extent used only to decide visibility. */
export function selectionToolbarPosition(
	rect: Rect,
	bounds: Rect,
	size: { width: number; height: number },
	extent: Rect = rect,
) {
	const visible =
		bounds.right > bounds.left &&
		bounds.bottom > bounds.top &&
		extent.bottom > bounds.top &&
		extent.top < bounds.bottom &&
		extent.right > bounds.left &&
		extent.left < bounds.right;
	const clamp = (value: number, low: number, high: number) =>
		Math.max(low, Math.min(value, Math.max(low, high)));
	const left = clamp(
		(rect.left + rect.right - size.width) / 2,
		bounds.left + 8,
		bounds.right - size.width - 8,
	);
	const above = rect.top - size.height - 6;
	const preferred =
		rect.top >= bounds.top && above < bounds.top + 8 ? rect.bottom + 6 : above;
	return {
		left,
		top: clamp(preferred, bounds.top + 8, bounds.bottom - size.height - 8),
		visible,
	};
}
