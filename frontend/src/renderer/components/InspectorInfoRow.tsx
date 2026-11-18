import { ArrowUpRight, type LucideIcon } from "lucide-react";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "@aoagents/product-ui";
import type { ReactNode } from "react";
import { cn } from "../lib/utils";
import { ProductExternalLink } from "./ProductExternalLink";

const rowBaseClass = "flex min-h-8 min-w-0 items-center gap-2 rounded-md px-1.5 text-left text-sm text-foreground";
// Interactive controls hover like sidebar rows: the shared growing pill owns the fill.
const interactiveRowClass = cn(NAV_ROW_HIGHLIGHT_HOST_CLASS, "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60");

/**
 * One inspector row: an icon and a label, with an optional `action` control
 * placed after it (beside the row, never inside it).
 */
export function InfoRow({ action, icon: Icon, iconClassName, label, title }: {
	action?: ReactNode;
	icon: LucideIcon;
	/** Overrides the icon's muted colour, e.g. for a status tone. */
	iconClassName?: string;
	label: ReactNode;
	title?: string;
}) {
	const content = (
		<>
			<Icon aria-hidden="true" className={cn("size-icon-sm shrink-0", iconClassName ?? "text-muted-foreground")} />
			<span className="min-w-0 flex-1 truncate" title={title}>{label}</span>
		</>
	);
	if (!action) return <div className={cn("-mx-1.5", rowBaseClass)}>{content}</div>;
	return (
		<div className="-mx-1.5 flex min-w-0 items-center gap-0.5">
			<div className={cn(rowBaseClass, "flex-1")}>{content}</div>
			{action}
		</div>
	);
}

/**
 * An inspector row that opens something in the app, such as a PR's tab.
 * `action` sits beside it.
 */
export function InfoRowButton({ action, icon: Icon, iconClassName, label, onClick, title, trailing }: {
	action?: ReactNode;
	icon: LucideIcon;
	iconClassName?: string;
	label: ReactNode;
	onClick: () => void;
	title?: string;
	/** Says what the row does, inside the same click target. */
	trailing?: ReactNode;
}) {
	const button = (
		<button className={cn(rowBaseClass, interactiveRowClass, "group relative w-full flex-1")} onClick={onClick} title={title} type="button">
			<NavRowHighlight />
			<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2">
				<Icon aria-hidden="true" className={cn("size-icon-sm shrink-0", iconClassName ?? "text-muted-foreground")} />
				<span className="min-w-0 flex-1 truncate">{label}</span>
				{trailing}
			</span>
		</button>
	);
	if (!action) return <div className="-mx-1.5 flex min-w-0">{button}</div>;
	return (
		<div className="-mx-1.5 flex min-w-0 items-center gap-0.5">
			{button}
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
