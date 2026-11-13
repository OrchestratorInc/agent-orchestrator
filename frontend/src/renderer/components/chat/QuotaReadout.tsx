/**
 * The account's usage limit in the model picker. Close to the limit a warning
 * pill holds the model's place for a few seconds (when a chat opens, or when
 * usage first crosses the line) and the open menu keeps it under the Agent row.
 * A full window shows a clock and the time left until it resets.
 */

import type { TFunction } from "i18next";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, Clock } from "@/components/app-icons";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "../ui/tooltip";
import type { ConversationRateLimits } from "../../types/conversation";

/** Where the warning replaces the model name. */
export const QUOTA_WARN = 75;
/** How long the warning pill holds before the model comes back. */
export const QUOTA_FLASH_MS = 5_000;

export type QuotaWindow = { percent: number; resetsIn?: number; kind: "session" | "weekly" };

/**
 * The tighter of the provider's two windows once it reaches QUOTA_WARN. Negative
 * is the daemon's "not reported" signal and is never drawn.
 */
export function nearQuotaWindow(limits: ConversationRateLimits | undefined): QuotaWindow | undefined {
	if (!limits) return undefined;
	const windows: QuotaWindow[] = [
		{ percent: limits.primaryUsedPercent, resetsIn: limits.primaryResetsInSeconds, kind: "session" },
		{ percent: limits.secondaryUsedPercent, resetsIn: limits.secondaryResetsInSeconds, kind: "weekly" },
	];
	const tightest = windows
		.filter((w) => Number.isFinite(w.percent) && w.percent >= 0)
		.reduce<QuotaWindow | undefined>((worst, w) => (!worst || w.percent > worst.percent ||
			(w.percent === worst.percent && (w.resetsIn ?? 0) > (worst.resetsIn ?? 0)) ? w : worst), undefined);
	return tightest && tightest.percent >= QUOTA_WARN ? tightest : undefined;
}

/** "2h 15m", "40m", "4d": precise enough to plan around, never down to seconds. */
export function formatQuotaReset(seconds: number): string {
	const minutes = Math.max(1, Math.round(seconds / 60));
	if (minutes < 60) return `${minutes}m`;
	const hours = Math.floor(minutes / 60);
	if (hours < 24) return minutes % 60 ? `${hours}h ${minutes % 60}m` : `${hours}h`;
	return `${Math.floor(hours / 24)}d`;
}

function useQuotaText(quota: QuotaWindow) {
	const { t } = useTranslation();
	const full = quota.percent >= 100;
	const resets = quota.resetsIn && quota.resetsIn > 0 ? formatQuotaReset(quota.resetsIn) : undefined;
	const label = full
		? t("chat.usageLimit.title")
		: t(quota.kind === "session" ? "chat.quota.approaching" : "chat.quota.approachingWeekly");
	return { t, full, resets, label, Icon: full ? Clock : AlertTriangle, severity: full ? "full" : "warn" };
}

/** The pill in the picker: the warning, or once full, the time left. */
export function QuotaReadout({ quota }: { quota: QuotaWindow }) {
	const { t, full, resets, label, Icon, severity } = useQuotaText(quota);
	return (
		<span data-quota-readout="" data-severity={severity} className="quota-pill">
			<Icon aria-hidden="true" className="size-3 shrink-0" />
			{full && resets
				? <span className="tabular-nums" aria-label={t("chat.quota.resetsIn", { time: resets })}>{resets}</span>
				: <span className="truncate">{label}</span>}
		</span>
	);
}

/** Stays mounted outside the animated model label so only quota changes speak. */
export function QuotaStatus({ quota }: { quota: QuotaWindow | undefined }) {
	const { t, label, resets } = useQuotaText(quota ?? { percent: 0, kind: "session" });
	return (
		<span role="status" aria-live="polite" aria-atomic="true" className="sr-only">
			{quota ? `${label}${resets ? `. ${t("chat.quota.resetsIn", { time: resets })}` : ""}` : ""}
		</span>
	);
}

/** The pill on its own, for composers whose model controls are not the agent picker. */
export function QuotaPill({ quota }: { quota: QuotaWindow }) {
	const { t } = useTranslation();
	return (
		<TooltipProvider><Tooltip>
			<TooltipTrigger asChild><span tabIndex={0} className="inline-flex rounded-full focus-visible:outline-2 focus-visible:outline-ring"><QuotaReadout quota={quota} /></span></TooltipTrigger>
			<TooltipContent>{quotaTitle(quota, t)}</TooltipContent>
		</Tooltip></TooltipProvider>
	);
}

/** The line under the Agent row of the open model menu, which outlasts the pill. */
export function QuotaMenuNotice({ quota }: { quota: QuotaWindow }) {
	const { t, resets, label, Icon, severity } = useQuotaText(quota);
	return (
		<span role="status" data-severity={severity} className="quota-note">
			<Icon aria-hidden="true" />
			<span className="quota-note-label">{label}</span>
			{resets ? <><span aria-hidden="true">·</span><span className="quota-note-meta">{t("chat.quota.resetsIn", { time: resets })}</span></> : null}
		</span>
	);
}

/**
 * Whether the picker shows the readout in place of the model right now. A full
 * window always does; a warning does for QUOTA_FLASH_MS each time it starts for
 * a conversation (chat opened, line crossed, or tighter window changed).
 * Dropping back below the line re-arms it.
 */
export function useQuotaFlash(quota: QuotaWindow | undefined, scope: string): boolean {
	const full = quota !== undefined && quota.percent >= 100;
	const key = quota && !full ? `${scope}:${quota.kind}` : undefined;
	const [dismissed, setDismissed] = useState<string>();
	useEffect(() => {
		setDismissed(undefined);
		if (!key) return;
		const timer = window.setTimeout(() => setDismissed(key), QUOTA_FLASH_MS);
		return () => window.clearTimeout(timer);
	}, [key]);
	return full || (key !== undefined && key !== dismissed);
}

/** The picker's hover title: the exact share used, and when it resets. */
export function quotaTitle(quota: QuotaWindow, t: TFunction): string {
	const used = t(quota.kind === "session" ? "chat.quota.usedSession" : "chat.quota.usedWeekly", { percent: Math.min(100, Math.round(quota.percent)) });
	return quota.resetsIn && quota.resetsIn > 0 ? `${used} ${t("chat.quota.resetsIn", { time: formatQuotaReset(quota.resetsIn) })}.` : used;
}
