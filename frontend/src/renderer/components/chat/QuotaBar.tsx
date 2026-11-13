/**
 * The account's usage limit, drawn in the model picker once it is close.
 *
 * Below the warning line the picker names the agent and model as usual. From
 * there the same control shows how much of the provider's usage window is gone,
 * because that has become the thing most likely to stop the next turn. The
 * picker still opens on click and names the model in its hover title.
 *
 * The fill is the real reported percentage. It grows in once when the bar first
 * appears and eases to each new reading; nothing loops. Once the window is full
 * the bar gives way to a clock and the time it resets.
 */

import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { Clock } from "@/components/app-icons";
import { cn } from "../../lib/utils";
import type { ConversationRateLimits } from "../../types/conversation";

/** Where the bar replaces the model name, and where it turns red. */
export const QUOTA_WARN = 75;
export const QUOTA_CRITICAL = 90;

export type QuotaWindow = { percent: number; resetsIn?: number; kind: "session" | "weekly" };

/**
 * The tighter of the provider's two windows, or undefined when it reported
 * neither. Negative is the daemon's "not reported" signal and is never drawn.
 */
export function tightestQuotaWindow(limits: ConversationRateLimits | undefined): QuotaWindow | undefined {
	if (!limits) return undefined;
	const windows: QuotaWindow[] = [
		{ percent: limits.primaryUsedPercent, resetsIn: limits.primaryResetsInSeconds, kind: "session" as const },
		{ percent: limits.secondaryUsedPercent, resetsIn: limits.secondaryResetsInSeconds, kind: "weekly" as const },
	].filter((w) => typeof w.percent === "number" && Number.isFinite(w.percent) && w.percent >= 0);
	if (windows.length === 0) return undefined;
	return windows.reduce((worst, w) => (w.percent > worst.percent ? w : worst));
}

/** The window to show in the picker, once it is close enough to matter. */
export function nearQuotaWindow(limits: ConversationRateLimits | undefined): QuotaWindow | undefined {
	const window = tightestQuotaWindow(limits);
	return window && window.percent >= QUOTA_WARN ? window : undefined;
}

/** "2h 15m", "40m", "4d": precise enough to plan around, never down to seconds. */
export function formatQuotaReset(seconds: number): string {
	const minutes = Math.max(1, Math.round(seconds / 60));
	if (minutes < 60) return `${minutes}m`;
	const hours = Math.floor(minutes / 60);
	if (hours < 24) return minutes % 60 ? `${hours}h ${minutes % 60}m` : `${hours}h`;
	return `${Math.floor(hours / 24)}d`;
}

export function QuotaBar({ quota }: { quota: QuotaWindow }) {
	const { t } = useTranslation();
	const percent = Math.min(100, Math.max(0, Math.round(quota.percent)));
	const critical = percent >= QUOTA_CRITICAL;
	const full = percent >= 100;
	const resets = quota.resetsIn && quota.resetsIn > 0 ? formatQuotaReset(quota.resetsIn) : undefined;
	const label = t(quota.kind === "session" ? "chat.quota.session" : "chat.quota.weekly");

	// Full: there is nothing left to fill, so only when it comes back matters.
	if (full) {
		return (
			<span
				data-quota-bar=""
				data-severity="full"
				role="status"
				className="flex min-w-0 items-center gap-1.5 text-status-exited animate-[quota-in_150ms_var(--ease-out)] motion-reduce:animate-none"
			>
				<Clock aria-hidden="true" className="size-3.5 shrink-0" />
				<span className="truncate tabular-nums">
					{resets ? t("chat.quota.resetsIn", { time: resets }) : t("chat.usageLimit.title")}
				</span>
			</span>
		);
	}

	return (
		<span
			data-quota-bar=""
			data-severity={critical ? "critical" : "warn"}
			className="flex min-w-0 items-center gap-2 animate-[quota-in_150ms_var(--ease-out)] motion-reduce:animate-none"
		>
			<span className="shrink-0 text-muted-foreground">{label}</span>
			<span
				role="meter"
				aria-label={label}
				aria-valuemin={0}
				aria-valuemax={100}
				aria-valuenow={percent}
				className="relative h-1.5 w-16 shrink-0 overflow-hidden rounded-full bg-border"
			>
				<span
					aria-hidden="true"
					style={{ width: `${percent}%` }}
					className={cn(
						"absolute inset-y-0 left-0 origin-left rounded-full",
						"transition-[width,background-color] duration-500 ease-out motion-reduce:transition-none",
						"animate-[quota-fill_700ms_var(--ease-out)] motion-reduce:animate-none",
						critical ? "bg-status-exited" : "bg-status-in-review",
					)}
				/>
			</span>
			<span className={cn("shrink-0 tabular-nums", critical ? "text-status-exited" : "text-status-in-review")}>
				{percent}%
			</span>
		</span>
	);
}

/** One line for the picker's hover title: what the bar means, and when it resets. */
export function quotaTitle(quota: QuotaWindow, t: TFunction): string {
	const percent = Math.min(100, Math.round(quota.percent));
	const used = t(quota.kind === "session" ? "chat.quota.usedSession" : "chat.quota.usedWeekly", { percent });
	return quota.resetsIn && quota.resetsIn > 0
		? `${used} ${t("chat.quota.resetsIn", { time: formatQuotaReset(quota.resetsIn) })}.`
		: used;
}
