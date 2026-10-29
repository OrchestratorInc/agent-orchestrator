import { useLayoutEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../../lib/utils";
import { Tooltip, TooltipContent, TooltipTrigger } from "../../ui/tooltip";

export type HarnessDetailTab = "account" | "models" | "health";
export type HarnessTone = "ok" | "error" | "off";

const SPLIT_LAYOUT_MIN_WIDTH = 720;
export const FOCUS_HIGHLIGHT_MS = 2_000;

/** True once the element is wide enough to show the harness list beside the open harness. */
export function useWideLayout(ref: { current: HTMLElement | null }): boolean {
	const [wide, setWide] = useState(false);
	// Layout effect: the first width is read before paint, so the page never shows
	// the narrow list for a few frames and then jumps to the split layout.
	useLayoutEffect(() => {
		const element = ref.current;
		if (!element) return;
		// The content box, as the observer reports it: clientWidth less the padding.
		const style = getComputedStyle(element);
		const content = element.clientWidth - (parseFloat(style.paddingLeft) || 0) - (parseFloat(style.paddingRight) || 0);
		setWide(content >= SPLIT_LAYOUT_MIN_WIDTH);
		if (typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(([entry]) => setWide((entry?.contentRect.width ?? 0) >= SPLIT_LAYOUT_MIN_WIDTH));
		observer.observe(element);
		return () => observer.disconnect();
	}, [ref]);
	return wide;
}

// Page tabs underline the active section instead of nesting a pill inside a pill.
export const HARNESS_TABS_CLASS = "shrink-0 h-auto w-full justify-start gap-5 rounded-none border-b border-border bg-transparent p-0";
export const HARNESS_TAB_CLASS = "-mb-px h-9 flex-none rounded-none border-0 border-b-2 border-transparent bg-transparent px-0.5 shadow-none data-[state=active]:border-foreground data-[state=active]:bg-transparent dark:data-[state=active]:border-foreground dark:data-[state=active]:bg-transparent";

// Harness page sections: one bordered, tonal card per section, rows divided by hairlines.
export const HARNESS_CARD_CLASS = "flex w-full min-w-0 max-w-full flex-col divide-y divide-border overflow-hidden rounded-lg border border-border bg-(--color-bg-secondary)";
export const HARNESS_CARD_ROW_CLASS = "flex min-h-12 items-center px-3 py-2.5";

export function HarnessStatusDot({ tone }: { tone: HarnessTone }) {
	return <span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", tone === "ok" ? "bg-success" : tone === "error" ? "bg-error" : "bg-settings-muted/50")} />;
}

export function HarnessDetailGroup({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
	return <section className="flex min-w-0 max-w-full flex-col gap-1.5">
		{action ? <div className="flex min-h-7 items-center justify-between gap-3">
			<h3 className="text-xs font-medium leading-4 text-settings-muted">{title}</h3>
			{action}
		</div> : <h3 className="text-xs font-medium leading-4 text-settings-muted">{title}</h3>}
		<div className={HARNESS_CARD_CLASS}>{children}</div>
	</section>;
}

export function HarnessDetailRow({ label, description, children }: { label: string; description?: string; children?: ReactNode }) {
	return <div className={cn(HARNESS_CARD_ROW_CLASS, "min-w-0 flex-wrap gap-x-4 gap-y-1 [overflow-wrap:anywhere]")}>
		<span className="min-w-0 flex-1">
			<span className="block text-sm leading-5 text-settings-label">{label}</span>
			{description ? <span className="mt-0.5 block text-pretty text-xs leading-4 text-settings-muted">{description}</span> : null}
		</span>
		<span className="flex min-w-0 max-w-full flex-wrap items-center justify-end gap-x-3 gap-y-1">{children}</span>
	</div>;
}

export function HarnessStateText({ tone, children }: { tone: HarnessTone; children: ReactNode }) {
	return <span className="inline-flex items-center gap-2 text-xs text-settings-label">
		<HarnessStatusDot tone={tone} />
		{children}
	</span>;
}

/** A short local date and time; the full one shows on hover. Nothing for a missing or invalid value. */
export function HarnessTimestamp({ value }: { value?: string | null }) {
	const { i18n } = useTranslation();
	if (!value || !Number.isFinite(Date.parse(value))) return null;
	const date = new Date(value);
	return <Tooltip>
		<TooltipTrigger asChild>
			<time dateTime={value}>{date.toLocaleString(i18n.resolvedLanguage, { day: "numeric", month: "short", hour: "numeric", minute: "2-digit" })}</time>
		</TooltipTrigger>
		<TooltipContent>{date.toLocaleString(i18n.resolvedLanguage)}</TooltipContent>
	</Tooltip>;
}

export function hasValidTimestamp(value?: string | null): value is string {
	return Boolean(value && Number.isFinite(Date.parse(value)));
}
