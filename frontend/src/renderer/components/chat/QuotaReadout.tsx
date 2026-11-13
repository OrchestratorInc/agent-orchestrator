/**
 * The account's usage limit, shown in the model picker once it is close.
 *
 * Below the warning line the picker names the agent and model as usual. From
 * there the same control shows how much of the provider's usage window is gone,
 * because that has become the thing most likely to stop the next turn. The
 * picker still opens on click and names the model in its hover title.
 *
 * Close to the limit it is a warning; once the window is full, a clock and the
 * time until it resets. Only the icon carries colour.
 */

import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { AlertTriangle, Clock } from "@/components/app-icons";
import { cn } from "../../lib/utils";
import type { ConversationRateLimits } from "../../types/conversation";

/** Where the warning replaces the model name. */
export const QUOTA_WARN = 75;

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

export function QuotaReadout({ quota }: { quota: QuotaWindow }) {
	const { t } = useTranslation();
	const resets = quota.resetsIn && quota.resetsIn > 0 ? formatQuotaReset(quota.resetsIn) : undefined;
	const appear = "animate-[quota-in_150ms_var(--ease-out)] motion-reduce:animate-none";

	// Full: nothing is left to use, so only when it comes back matters.
	if (quota.percent >= 100) {
		return (
			<span data-quota-readout="" data-severity="full" className={cn("flex min-w-0 items-center gap-1.5 text-foreground", appear)}>
				<Clock aria-hidden="true" className="size-3.5 shrink-0 text-status-exited" />
				{resets ? (
					<span className="tabular-nums" aria-label={t("chat.quota.resetsIn", { time: resets })}>{resets}</span>
				) : (
					<span className="truncate">{t("chat.usageLimit.title")}</span>
				)}
			</span>
		);
	}

	// Close: a warning is enough. The exact share lives in the hover title.
	return (
		<span data-quota-readout="" data-severity="warn" className={cn("flex min-w-0 items-center gap-1.5 text-foreground", appear)}>
			<AlertTriangle aria-hidden="true" className="size-3.5 shrink-0 text-status-in-review" />
			<span className="truncate">{t(quota.kind === "session" ? "chat.quota.approaching" : "chat.quota.approachingWeekly")}</span>
		</span>
	);
}

/** One line for the picker's hover title: what the readout means, and when it resets. */
export function quotaTitle(quota: QuotaWindow, t: TFunction): string {
	const percent = Math.min(100, Math.round(quota.percent));
	const used = t(quota.kind === "session" ? "chat.quota.usedSession" : "chat.quota.usedWeekly", { percent });
	return quota.resetsIn && quota.resetsIn > 0
		? `${used} ${t("chat.quota.resetsIn", { time: formatQuotaReset(quota.resetsIn) })}.`
		: used;
}
