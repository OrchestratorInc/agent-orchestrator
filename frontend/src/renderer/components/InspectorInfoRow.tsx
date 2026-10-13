import { ArrowUpRight, ChevronRight, type LucideIcon } from "lucide-react";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "@aoagents/product-ui";
import type { ReactNode } from "react";
import { cn } from "../lib/utils";
import { ProductExternalLink } from "./ProductExternalLink";

const rowBaseClass = "flex min-h-8 min-w-0 items-center gap-2 rounded-md px-1.5 text-left text-sm text-foreground";
const rowClass = cn("-mx-1.5", rowBaseClass);
// Interactive rows hover like sidebar rows: the shared growing pill owns the fill.
const interactiveRowClass = cn(NAV_ROW_HIGHLIGHT_HOST_CLASS, "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60");

/**
 * One inspector row: icon, label, optional note and trailing content. A row
 * with `onClick` adds hover and a chevron (a disclosure when `expanded` is
 * set), one with `href` opens externally with an arrow; static rows keep the
 * same geometry so every row lines up. `action` is a separate control placed
 * after the row's own button, so it never nests inside it.
 */
export function InfoRow({
	action,
	ariaControls,
	ariaLabel,
	expanded,
	href,
	icon: Icon,
	iconClassName,
	label,
	note,
	noteClassName,
	noteTone,
	onClick,
	title,
	trailing,
}: {
	action?: ReactNode;
	/** The id of the region a disclosure row expands. */
	ariaControls?: string;
	ariaLabel?: string;
	expanded?: boolean;
	href?: string;
	icon: LucideIcon;
	/** Overrides the icon's muted colour, e.g. for a status tone. */
	iconClassName?: string;
	label: ReactNode;
	note?: ReactNode;
	/** Replaces the note's tone colour, e.g. `text-muted-foreground` where passive would fail contrast. */
	noteClassName?: string;
	noteTone?: "warn" | "muted";
	onClick?: () => void;
	title?: string;
	trailing?: ReactNode;
}) {
	const content = (
		<>
			<Icon aria-hidden="true" className={cn("size-icon-sm shrink-0", iconClassName ?? "text-muted-foreground")} />
			<span className="flex min-w-0 flex-1 items-baseline gap-1.5">
				<span className="truncate" title={title}>{label}</span>
				{note ? (
					<span className={cn("min-w-0 shrink-0 truncate text-caption", noteClassName ?? (noteTone === "warn" ? "text-status-in-review" : "text-passive"))}>{note}</span>
				) : null}
			</span>
			{trailing}
			{onClick ? (
				<ChevronRight
					aria-hidden="true"
					className={cn(
						"size-icon-sm shrink-0 text-passive",
						expanded !== undefined && "transition-transform motion-reduce:transition-none",
						expanded && "rotate-90",
					)}
				/>
			) : null}
			{href ? <ArrowUpRight aria-hidden="true" className="size-icon-sm shrink-0 text-passive" /> : null}
		</>
	);
	const highlighted = (
		<>
			<NavRowHighlight />
			<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2">{content}</span>
		</>
	);
	if (href) {
		return (
			<ProductExternalLink ariaLabel={ariaLabel} className={cn(rowClass, interactiveRowClass)} href={href}>
				{highlighted}
			</ProductExternalLink>
		);
	}
	if (onClick) {
		const button = (
			<button
				aria-controls={ariaControls}
				aria-expanded={expanded}
				aria-label={ariaLabel}
				className={cn(action ? cn(rowBaseClass, "flex-1") : rowClass, interactiveRowClass)}
				onClick={onClick}
				type="button"
			>
				{highlighted}
			</button>
		);
		if (!action) return button;
		return (
			<div className="-mx-1.5 flex min-w-0 items-center gap-0.5">
				{button}
				{action}
			</div>
		);
	}
	if (!action) return <div className={rowClass}>{content}</div>;
	return (
		<div className="-mx-1.5 flex min-w-0 items-center gap-0.5">
			<div className={cn(rowBaseClass, "flex-1")}>{content}</div>
			{action}
		</div>
	);
}

/** A compact external link with the row hover, for a row's `action` slot. */
export function InfoRowLink({ ariaLabel, children, href }: { ariaLabel: string; children?: ReactNode; href: string }) {
	return (
		<ProductExternalLink ariaLabel={ariaLabel} className={cn("flex min-h-8 shrink-0 items-center rounded-md px-1.5", interactiveRowClass)} href={href}>
			<NavRowHighlight />
			<span className="relative z-[1] flex items-center gap-1.5">
				{children}
				<ArrowUpRight aria-hidden="true" className="size-icon-sm shrink-0 text-passive" />
			</span>
		</ProductExternalLink>
	);
}

export function DiffCounts({ additions, deletions }: { additions: number; deletions: number }) {
	return (
		<span className="inline-flex shrink-0 gap-1.5 font-mono text-caption tabular-nums">
			<span className="text-success">+{additions}</span>
			<span className="text-error">−{deletions}</span>
		</span>
	);
}
