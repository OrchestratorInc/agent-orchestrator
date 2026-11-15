import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../../lib/utils";

export const GHOST_PILL_CLASS = [
	"relative isolate hover:bg-transparent data-[state=open]:bg-transparent",
	"before:pointer-events-none before:absolute before:inset-1 before:-z-10 before:rounded-[inherit] before:bg-interactive-hover before:opacity-0",
	"before:transition-none hover:before:transition-[inset] focus-visible:before:transition-[inset] active:before:transition-[inset] before:duration-normal before:ease-[var(--ease-out)] motion-reduce:before:inset-0 motion-reduce:before:transition-none!",
	"hover:before:inset-0 hover:before:opacity-100 focus-visible:before:inset-0 focus-visible:before:opacity-100",
	"active:before:inset-0 active:before:opacity-100 active:before:bg-interactive-active",
	"aria-pressed:before:inset-0 aria-pressed:before:opacity-100 aria-pressed:before:bg-interactive-active aria-pressed:text-foreground",
	"data-[state=open]:before:inset-0 data-[state=open]:before:opacity-100 data-[state=open]:before:bg-interactive-active data-[state=open]:text-foreground",
	"data-[active=true]:before:inset-0 data-[active=true]:before:opacity-100 data-[active=true]:before:bg-interactive-active data-[active=true]:text-foreground",
].join(" ");

const buttonVariants = cva(
	"group/button inline-flex shrink-0 items-center justify-center gap-1.5 whitespace-nowrap rounded-md border bg-clip-padding text-sm font-normal transition-[background-color,border-color,color,box-shadow,opacity] duration-[200ms] ease-out outline-none select-none motion-reduce:transition-none disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [:where(&_svg:not([class*='size-']))]:size-4",
	{
		variants: {
			variant: {
				primary: "button-raised button-emphasis [--button-color:var(--primary)] text-primary-foreground",
				accent: "button-raised button-emphasis [--button-color:var(--color-brand-logo)] text-logo-accent-foreground",
				secondary:
					"button-raised [--button-color:var(--secondary)] text-secondary-foreground",
				outline:
					"border-border bg-background text-foreground hover:bg-muted aria-expanded:bg-muted aria-expanded:text-foreground dark:bg-transparent dark:hover:bg-input/30 active:bg-interactive-active dark:active:bg-interactive-active",
				ghost: `border-0 text-foreground ${GHOST_PILL_CLASS}`,
				quiet: `border-0 text-muted-foreground hover:text-foreground focus-visible:text-foreground ${GHOST_PILL_CLASS}`,
				destructive: "button-raised [--button-color:var(--color-danger-strong)] text-white",
				"destructive-ghost": `border-0 text-muted-foreground hover:text-destructive focus-visible:text-destructive ${GHOST_PILL_CLASS} before:bg-destructive/12 active:before:bg-destructive/20 aria-pressed:before:bg-destructive/20 data-[state=open]:before:bg-destructive/20 data-[active=true]:before:bg-destructive/20`,
				link: "h-auto border-transparent rounded-sm p-0 text-[length:inherit] text-inherit underline underline-offset-2 hover:text-foreground",
			},
			size: {
				xs: "h-control-sm gap-1 px-2 text-xs",
				sm: "h-control-md px-2.5 text-xs",
				default: "h-control-form px-3",
				"icon-xs": "size-control-sm",
				"icon-sm": "size-control-md",
				icon: "size-control-form",
				none: "",
			},
			shape: {
				default: "",
				pill: "rounded-full",
			},
		},
		defaultVariants: {
			variant: "primary",
			size: "default",
			shape: "default",
		},
	},
);

export interface ButtonProps
	extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {
	asChild?: boolean;
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
	({ asChild = false, className, shape, size, variant, ...props }, ref) => {
		const Comp = asChild ? Slot : "button";
		const resolvedSize = variant === "link" ? "none" : size;
		return (
			<Comp
				className={cn(buttonVariants({ variant, size: resolvedSize, shape, className }))}
				ref={ref}
				{...props}
			/>
		);
	},
);

Button.displayName = "Button";

export { buttonVariants };
