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
