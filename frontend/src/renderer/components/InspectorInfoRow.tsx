import { ArrowUpRight, type LucideIcon } from "lucide-react";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "@aoagents/product-ui";
import type { ComponentProps, ReactNode } from "react";
import { cn } from "../lib/utils";
import { ProductExternalLink } from "./ProductExternalLink";
import { Pressable } from "./ui/pressable";

const rowBaseClass = "flex min-h-8 min-w-0 items-center gap-2 rounded-md px-1.5 text-left text-sm text-foreground";
// Interactive controls hover like sidebar rows: the shared growing pill owns the fill.
const interactiveRowClass = cn(NAV_ROW_HIGHLIGHT_HOST_CLASS, "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60");

/**
 * An inspector row whose label is the button, read like a link: it underlines
 * on hover rather than filling the row, so the controls in `action` beside it
 * stay separate targets.
 */
export function InfoRowLabelButton({ action, icon: Icon, iconClassName, label, ...props }: Omit<ComponentProps<typeof Pressable>, "children" | "className"> & {
	action?: ReactNode;
	icon: LucideIcon;
	iconClassName?: string;
	label: ReactNode;
}) {
	return (
		<div className="-mx-1.5 flex min-w-0 items-center gap-0.5">
			<Pressable className={cn(rowBaseClass, "group flex-1 focus-visible:ring-2 focus-visible:ring-ring/60")} {...props}>
				<Icon aria-hidden="true" className={cn("size-icon-sm shrink-0", iconClassName ?? "text-muted-foreground")} />
				<span className="min-w-0 flex-1 truncate decoration-muted-foreground underline-offset-4 group-hover:underline">{label}</span>
			</Pressable>
			{action}
		</div>
	);
}

/** A compact external link with the row hover, for a row's `action` slot: a square, like an icon button. */
export function InfoRowLink({ ariaLabel, href, icon }: { ariaLabel: string; href: string; icon?: ReactNode }) {
	return (
		<ProductExternalLink ariaLabel={ariaLabel} className={cn("flex size-8 shrink-0 items-center justify-center rounded-md", interactiveRowClass)} href={href}>
			<NavRowHighlight />
			<span className="relative z-[1] flex items-center">
				{icon ?? <ArrowUpRight aria-hidden="true" className="size-icon-sm shrink-0 text-passive" />}
			</span>
		</ProductExternalLink>
	);
}
