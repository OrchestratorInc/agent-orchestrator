/**
 * Shared sidebar/inspector resize hit-strip.
 *
 * - On hover only, a thin line is drawn over the panel edge, brightest in the middle and
 *   fading out at the top and bottom, stopping 20px inside the panel's top and bottom;
 *   it fades and grows in quickly and shrinks out the same way; not painted on click/drag, not always-visible.
 * - The line traces the edge it resizes exactly: on hover the strip measures `edge` (the
 *   panel whose 1px border is the visible boundary) and centers the line on the border
 *   nearest to it, so insets, paddings and the hit strip's own offset can never pull the
 *   line off the edge. It is wider than the hairline so it reads over it, and straddles
 *   it evenly. Without an edge it falls back to the strip's center.
 * - useResizable is the ONLY width integrator; this strip just forwards pointer events.
 */
import { useRef } from "react";
import { cn } from "@/lib/utils";

type ResizeHandleProps = React.HTMLAttributes<HTMLDivElement> & {
	side: "left" | "right";
	/** The panel whose border the hover line traces. */
	edge?: () => Element | null;
	/** The panel's visible top and bottom, when they differ from `edge`'s box (a top bar over it). */
	span?: () => Pick<DOMRect, "top" | "bottom"> | null;
};

/** A panel's border is a 1px hairline. */
export const PANEL_BORDER_PX = 1;
/** The hover line, wider than the hairline so it shows over it. */
export const RESIZE_LINE_WIDTH_PX = 2;

/**
 * Where the line goes inside the strip: centered on the edge's border that is
 * nearest the strip, its left side if the strip sits nearer that, else its right.
 */
export function resizeLineLeft(handle: Pick<DOMRect, "left" | "width">, edge: Pick<DOMRect, "left" | "right">): number {
	const center = handle.left + handle.width / 2;
	const nearLeft = Math.abs(edge.left - center) <= Math.abs(edge.right - center);
	const borderStart = nearLeft ? edge.left : edge.right - PANEL_BORDER_PX;
	const borderCenter = borderStart + PANEL_BORDER_PX / 2;
	return borderCenter - RESIZE_LINE_WIDTH_PX / 2 - handle.left;
}

/** How far inside the panel's top and bottom the line stops. */
export const RESIZE_LINE_INSET_PX = 20;

/**
 * The line's top and bottom inside the strip: the panel's own height, less the
 * inset at each end, so it never runs past the panel into the window chrome.
 */
export function resizeLineSpan(handle: Pick<DOMRect, "top" | "bottom">, edge: Pick<DOMRect, "top" | "bottom">) {
	return {
		top: Math.max(edge.top, handle.top) - handle.top + RESIZE_LINE_INSET_PX,
		bottom: handle.bottom - Math.min(edge.bottom, handle.bottom) + RESIZE_LINE_INSET_PX,
	};
}

export function ResizeHandle({ className, side, edge, span: panelSpan, onPointerEnter, ...props }: ResizeHandleProps) {
	const lineRef = useRef<HTMLSpanElement>(null);
	return (
		<div
			data-side={side}
			data-slot="resize-handle"
			data-testid="resize-handle"
			className={cn(
				"group/resize absolute inset-y-0 z-[5] w-[length:var(--size-resize-handle)] cursor-col-resize touch-none",
				side === "right" && "right-[calc(-1*var(--size-resize-handle-offset))]",
				side === "left" && "left-[calc(-1*var(--size-resize-handle-offset))]",
				className,
			)}
			onPointerEnter={(event) => {
				// Measured as the line appears, so it follows the edge wherever layout put it.
				const target = edge?.();
				const line = lineRef.current;
				if (target && line) {
					const handle = event.currentTarget.getBoundingClientRect();
					const panel = target.getBoundingClientRect();
					const span = resizeLineSpan(handle, panelSpan?.() ?? panel);
					line.style.left = `${resizeLineLeft(handle, panel)}px`;
					line.style.top = `${span.top}px`;
					line.style.bottom = `${span.bottom}px`;
				}
				onPointerEnter?.(event);
			}}
			{...props}
		>
			<span
				ref={lineRef}
				aria-hidden="true"
				data-resize-line=""
				className="pointer-events-none absolute inset-y-5 left-[calc(50%-1px)] w-0.5 [--resize-line:color-mix(in_oklab,var(--color-foreground)_28%,transparent)] bg-[linear-gradient(to_bottom,transparent,var(--resize-line),transparent)] origin-center scale-y-90 opacity-0 transition-[opacity,scale] duration-fast ease-out group-hover/resize:scale-y-100 group-hover/resize:opacity-100 motion-reduce:transition-none"
			/>
		</div>
	);
}
