import { formatTimeCompact as formatPortableTimeCompact } from "@aoagents/product-ui";
import { appI18n, type MessageKey } from "../i18n";

const DAY_MS = 86_400_000;

/**
 * `longUnits` counts spans past a month in months and years ("3mo ago",
 * "14y ago") instead of days, for dates that can be years old, such as a
 * branch's last commit.
 */
export function formatTimeCompact(
	isoDate: string | null | undefined,
	{ longUnits = false, now = Date.now() }: { longUnits?: boolean; now?: number | Date } = {},
): string {
	if (longUnits && isoDate) {
		const nowMs = now instanceof Date ? now.getTime() : now;
		const days = Math.floor((nowMs - new Date(isoDate).getTime()) / DAY_MS);
		if (days >= 365) return appI18n.t("time.yearsAgo", { n: Math.floor(days / 365) });
		if (days >= 30) return appI18n.t("time.monthsAgo", { n: Math.floor(days / 30) });
	}
	return formatPortableTimeCompact(isoDate, {
		now,
		translate: (key, values) => appI18n.t(key as MessageKey, values),
	});
}

/** Extra-terse relative time for space-constrained navigation rows. */
export function formatTimeTerse(
	isoDate: string | null | undefined,
	now: number | Date = Date.now(),
): string {
	if (!isoDate) return "now";
	const timestamp = new Date(isoDate).getTime();
	if (!Number.isFinite(timestamp)) return "now";
	const nowMs = now instanceof Date ? now.getTime() : now;
	const diffMinutes = Math.floor((nowMs - timestamp) / 60_000);
	if (diffMinutes < 1) return "now";
	if (diffMinutes < 60) return `${diffMinutes}m`;
	const diffHours = Math.floor(diffMinutes / 60);
	if (diffHours < 24) return `${diffHours}h`;
	return `${Math.floor(diffHours / 24)}d`;
}
