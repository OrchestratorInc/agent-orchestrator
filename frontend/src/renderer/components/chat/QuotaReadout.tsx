/**
 * The account's usage limit, shown in the model picker once it is close.
 *
 * Below the warning line the picker names the agent and model as usual. Close
 * to the limit a warning pill takes the model's place for a few seconds, when a
 * chat opens or when usage first crosses the line, then the model comes back;
 * the open model menu keeps the warning under its Agent row. Once the window is full a
 * clock and the time until it resets stay in the picker, since nothing runs
 * until then. The picker still opens on click and names the model on hover.
 */

import type { TFunction } from "i18next";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertTriangle, Clock } from "@/components/app-icons";
import type { ConversationRateLimits } from "../../types/conversation";

/** Where the warning replaces the model name. */
export const QUOTA_WARN = 75;
/** How long the warning pill holds before the model comes back. */
export const QUOTA_FLASH_MS = 5_000;

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
	const kind = quota.kind === "session" ? "chat.quota.approaching" : "chat.quota.approachingWeekly";

	// Full: nothing is left to use, so only when it comes back matters.
	if (quota.percent >= 100) {
		return (
			<span data-quota-readout="" data-severity="full" className="quota-pill">
				<Clock aria-hidden="true" className="size-3 shrink-0" />
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
		<span data-quota-readout="" data-severity="warn" className="quota-pill">
			<AlertTriangle aria-hidden="true" className="size-3 shrink-0" />
			<span className="truncate">{t(kind)}</span>
		</span>
	);
}

/**
 * Whether the picker should show the readout in place of the model right now.
 *
 * A full window always does. A warning shows for QUOTA_FLASH_MS each time it
 * becomes true for a conversation: when the chat opens already close, when
 * usage first crosses the line, or when the tighter window changes. Dropping
 * back below the line re-arms it.
 */
export function useQuotaFlash(quota: QuotaWindow | undefined, scope: string): boolean {
	const full = quota !== undefined && quota.percent >= 100;
	const key = quota && !full ? `${scope}:${quota.kind}` : undefined;
	const [dismissed, setDismissed] = useState<string>();
	useEffect(() => {
		if (!key) {
			setDismissed(undefined);
			return;
		}
		const timer = window.setTimeout(() => setDismissed(key), QUOTA_FLASH_MS);
		return () => window.clearTimeout(timer);
	}, [key]);
	return full || (key !== undefined && key !== dismissed);
}

/** The warning under the Agent row of the open model menu, which outlasts the pill. */
export function QuotaMenuNotice({ quota }: { quota: QuotaWindow }) {
	const { t } = useTranslation();
	const full = quota.percent >= 100;
	const resets = quota.resetsIn && quota.resetsIn > 0 ? formatQuotaReset(quota.resetsIn) : undefined;
	const label = full
		? t("chat.usageLimit.title")
		: t(quota.kind === "session" ? "chat.quota.approaching" : "chat.quota.approachingWeekly");
	return (
		<span role="status" data-severity={full ? "full" : "warn"} className="quota-note">
			{full ? <Clock aria-hidden="true" /> : <AlertTriangle aria-hidden="true" />}
			<span className="quota-note-label">{label}</span>
			{resets ? <span className="quota-note-meta">{t("chat.quota.resetsIn", { time: resets })}</span> : null}
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
