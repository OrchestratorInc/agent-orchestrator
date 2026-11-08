import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "../../lib/utils";

// The app's one action button. Rows, cards and tabs use `Pressable` instead.
// See DESIGN.md → Buttons.
//
// - No press motion: buttons never scale or shift when clicked.
// - Ghost-family variants (ghost, quiet, destructive-ghost) share the sidebar
//   row's hover pill: a fill inset 4px that grows to the button's edge on hover
//   or keyboard focus. Opacity snaps; only the inset animates. While the button
//   is on (aria-pressed, data-state=open, data-active) the pill
//   stays full size with the active fill.

/** The ghost hover pill on its own, for triggers that are not `Button` (menu triggers, composer pickers). */
export const GHOST_PILL_CLASS = [
	"relative isolate hover:bg-transparent data-[state=open]:bg-transparent",
	"before:pointer-events-none before:absolute before:inset-1 before:-z-10 before:rounded-[inherit] before:bg-interactive-hover before:opacity-0",
	"before:transition-[inset] before:duration-normal before:ease-[var(--ease-out)] motion-reduce:before:inset-0 motion-reduce:before:transition-none",
	"hover:before:inset-0 hover:before:opacity-100 focus-visible:before:inset-0 focus-visible:before:opacity-100",
	"aria-pressed:before:inset-0 aria-pressed:before:opacity-100 aria-pressed:before:bg-interactive-active aria-pressed:text-foreground",
	"data-[state=open]:before:inset-0 data-[state=open]:before:opacity-100 data-[state=open]:before:bg-interactive-active data-[state=open]:text-foreground",
	"data-[active=true]:before:inset-0 data-[active=true]:before:opacity-100 data-[active=true]:before:bg-interactive-active data-[active=true]:text-foreground",
].join(" ");

// The default 16px icon size has zero specificity (`:where`) so a caller's
// `[&_svg]:size-*` always wins over it.
const buttonVariants = cva(
	"group/button inline-flex shrink-0 items-center justify-center gap-1.5 whitespace-nowrap rounded-md border border-transparent bg-clip-padding text-sm font-normal transition-[background-color,border-color,color,box-shadow,opacity] duration-[100ms] ease-out outline-none select-none focus-visible:outline-none disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [:where(&_svg:not([class*='size-']))]:size-4",
	{
		variants: {
			variant: {
				/** The one main action in a region. Accent fill. */
				primary: "bg-primary text-primary-foreground hover:bg-primary/80",
				/** Brand-blue fill for a decision the turn is waiting on (approve, open link). */
				accent: "bg-logo-accent text-logo-accent-foreground hover:bg-logo-accent-bright",
				/** Quiet filled surface for secondary actions. */
				secondary:
					"bg-secondary text-secondary-foreground hover:bg-[color-mix(in_oklch,var(--secondary),var(--foreground)_5%)] aria-pressed:bg-interactive-active",
				/** Contrast fallback only — prefer secondary. */
				outline:
					"border-border bg-background text-foreground hover:bg-muted aria-expanded:bg-muted aria-expanded:text-foreground dark:bg-transparent dark:hover:bg-input/30",
				/** Transparent, foreground text, growing hover pill. */
				ghost: `text-foreground ${GHOST_PILL_CLASS}`,
				/** Ghost that rests muted and brightens on hover — toolbar and row actions. */
				quiet: `text-muted-foreground hover:text-foreground focus-visible:text-foreground ${GHOST_PILL_CLASS}`,
				/** Danger fill for irreversible actions. */
				destructive: "bg-danger-strong text-white hover:bg-danger-strong/85",
				/** Quiet action that turns red on hover — remove, delete, kill. */
				"destructive-ghost": `text-muted-foreground hover:text-destructive focus-visible:text-destructive ${GHOST_PILL_CLASS}`,
				/** Inline text action. Inherits color and font size; ignores size. */
				link: "h-auto rounded-sm p-0 text-[length:inherit] text-inherit underline underline-offset-2 hover:text-foreground",
				/** Settings and modal footer actions. Own height and padding; ignore size. */
				footer:
					"h-(--size-settings-action-height) rounded-(--radius-settings-action) border-[var(--color-border-settings-input)] bg-[var(--color-bg-settings-input)] px-3 text-[length:var(--font-size-md)] leading-5 text-[var(--color-text-settings-row)] hover:bg-[var(--color-bg-settings-input)] hover:opacity-90",
				"footer-primary":
					"h-(--size-settings-action-height) rounded-(--radius-settings-action) border-transparent bg-[var(--color-settings-accent)] px-(--size-settings-footer-button-padding-x) text-[length:var(--font-size-md)] leading-5 text-[var(--color-settings-footer-button-primary-fg)] hover:bg-[var(--color-settings-accent)] hover:opacity-90",
				"footer-destructive":
					"h-(--size-settings-action-height) rounded-(--radius-settings-action) border-transparent bg-danger-strong px-(--size-settings-footer-button-padding-x) text-[length:var(--font-size-md)] leading-5 text-white hover:bg-danger-strong hover:opacity-90",
			},
			size: {
				/** 24px */
				xs: "h-control-sm gap-1 px-2 text-xs",
				/** 28px */
				sm: "h-control-md px-2.5 text-xs",
				/** 32px */
				default: "h-control-form px-3",
				"icon-xs": "size-control-sm",
				"icon-sm": "size-control-md",
				icon: "size-control-form",
				// Variants that own their own measurements.
				none: "",
			},
			shape: {
				default: "",
				/** Fully rounded — prompt chips and composer pills only. */
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

const SIZELESS_VARIANTS = new Set(["link", "footer", "footer-primary", "footer-destructive"]);

export interface ButtonProps
	extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {
	asChild?: boolean;
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
	({ asChild = false, className, shape, size, variant, ...props }, ref) => {
		const Comp = asChild ? Slot : "button";
		const resolvedSize = variant && SIZELESS_VARIANTS.has(variant) ? "none" : size;
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
