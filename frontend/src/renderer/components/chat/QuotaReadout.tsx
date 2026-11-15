/**
 * The account's usage limit beside the model picker. Close to the limit a small
 * warning sign sits next to the model (a clock once the window is full); hovering
 * or focusing it shows the usage and when it resets, and the open model menu
 * repeats the warning under its Agent row.
 */

import { useTranslation } from "react-i18next";
import { AlertTriangle, Clock } from "@/components/app-icons";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "../ui/tooltip";
import type { ConversationRateLimits } from "../../types/conversation";

/** Where the warning sign appears. */
export const QUOTA_WARN = 82;

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

/** The sign beside the model: a warning, or a clock once full. Its tooltip wears the model menu's surface. */
export function QuotaSign({ quota }: { quota: QuotaWindow }) {
	const { t, resets, label, Icon, severity } = useQuotaText(quota);
	const used = t(quota.kind === "session" ? "chat.quota.usedSession" : "chat.quota.usedWeekly", { percent: Math.min(100, Math.round(quota.percent)) });
	const detail = resets ? `${used} ${t("chat.quota.resetsIn", { time: resets })}.` : used;
	return (
		<TooltipProvider><Tooltip>
			<TooltipTrigger asChild>
				<button type="button" data-quota-sign="" data-severity={severity} aria-label={`${label}. ${detail}`} className="quota-sign">
					<Icon aria-hidden="true" />
				</button>
			</TooltipTrigger>
			<TooltipContent hideArrow sideOffset={6}
				className="quota-tip whitespace-nowrap rounded-(--radius-settings-panel) border border-(--color-border-overlay) bg-settings-menu px-3 py-2 text-foreground">
				<span className="quota-tip-title" data-severity={severity}><Icon aria-hidden="true" />{label}</span>
				<span className="quota-tip-meta">{detail}</span>
			</TooltipContent>
		</Tooltip></TooltipProvider>
	);
}

/** Announces quota changes to screen readers, whether or not anything is hovered. */
export function QuotaStatus({ quota }: { quota: QuotaWindow | undefined }) {
	const { t, label, resets } = useQuotaText(quota ?? { percent: 0, kind: "session" });
	return (
		<span role="status" aria-live="polite" aria-atomic="true" className="sr-only">
			{quota ? `${label}${resets ? `. ${t("chat.quota.resetsIn", { time: resets })}` : ""}` : ""}
		</span>
	);
}

/** The line under the Agent row of the open model menu. */
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
