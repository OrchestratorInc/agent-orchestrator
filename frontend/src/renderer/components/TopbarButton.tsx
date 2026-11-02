import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "./NavRowHighlight";

// Utility controls rest muted and brighten on hover or while their menu or toggle is on.
const topbarActionClass = "border-0 bg-transparent text-muted-foreground hover:text-foreground aria-expanded:text-foreground aria-pressed:text-foreground";

const topbarButtonVariants = cva(
	"topbar-control topbar-control--disabled-affordance inline-flex items-center transition-[transform,filter,background-color,color,border-color] duration-fast ease-out active:scale-[0.96] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
	{
		variants: {
			variant: {
				action:
					`topbar-control--action h-control-lg gap-1.5 rounded-md px-3.5 text-sm font-semibold leading-none ${topbarActionClass}`,
				secondary:
					"topbar-control--secondary h-control-lg gap-1.5 rounded-md bg-transparent px-3.5 text-sm font-semibold leading-none text-muted-foreground hover:text-foreground",
				primary:
					"topbar-control--primary h-control-lg gap-1.5 rounded-md bg-accent-strong px-3.5 text-sm leading-none text-accent-foreground hover:brightness-110 active:brightness-95",
				accent:
					"topbar-control--accent h-control-lg gap-1.5 rounded-md border border-border px-3.5 text-sm font-semibold leading-none bg-raised text-muted-foreground hover:bg-surface hover:text-foreground",
				icon:
					`topbar-control--icon grid size-control-md place-items-center rounded-md ${topbarActionClass}`,
			},
		},
		defaultVariants: { variant: "action" },
	},
);

export function TopbarButton({
	children,
	className,
	highlight = true,
	variant = "action",
	type = "button",
	...props
}: React.ComponentProps<"button"> & VariantProps<typeof topbarButtonVariants> & {
	/** Joined controls can let their surrounding group own the highlight. */
	highlight?: boolean;
}) {
	// Accent keeps its bordered raised surface; every other utility control
	// shares the sidebar's hover highlight.
	const useHighlight = highlight && (variant === "action" || variant === "icon" || variant === "secondary");
	const active = props["aria-pressed"] === true || props["aria-pressed"] === "true" ||
		props["aria-expanded"] === true || props["aria-expanded"] === "true";
	return (
		<button
			className={cn(topbarButtonVariants({ variant }), useHighlight && NAV_ROW_HIGHLIGHT_HOST_CLASS, useHighlight && "isolate", className)}
			type={type}
			{...props}
		>
			{useHighlight ? <NavRowHighlight active={active} disabled={props.disabled} /> : null}
			{children}
		</button>
	);
}

export function TopbarActionError({ className, ...props }: React.HTMLAttributes<HTMLSpanElement>) {
	return <span className={cn("text-caption text-destructive", className)} role="alert" {...props} />;
}
