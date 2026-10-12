/**
 * Shared sidebar/inspector resize hit-strip.
 *
 * - On hover only, a thin line is drawn over the panel edge, fading out at the top and
 *   bottom, with a short opacity fade in/out; not painted on click/drag, not always-visible.
 * - The strip is centered on the sidebar/inspector boundary, and the line starts at that
 *   center and runs toward the center pane, so it covers the center surface's 1px border
 *   on both sides (the inspector's outer half is clipped by its `overflow-hidden`).
 * - useResizable is the ONLY width integrator; this strip just forwards pointer events.
 */
import { cn } from "@/lib/utils";

type ResizeHandleProps = React.HTMLAttributes<HTMLDivElement> & {
	side: "left" | "right";
};

export function ResizeHandle({ className, side, ...props }: ResizeHandleProps) {
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
			{...props}
		>
			<span
				aria-hidden="true"
				data-resize-line=""
				className="pointer-events-none absolute inset-y-0 left-1/2 w-0.5 bg-gradient-to-b from-transparent via-foreground/30 to-transparent opacity-0 transition-opacity duration-fast group-hover/resize:opacity-100 motion-reduce:transition-none"
			/>
		</div>
	);
}
