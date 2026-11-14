const DAY_MS = 86_400_000;

/**
 * When a reply was written, as its turn's action row shows it (the desktop's
 * message timestamp): the time today, "Yesterday · 4:17 PM", and the date for
 * anything older, with the year once it is not this one. Written out rather
 * than through Intl.RelativeTimeFormat, which Hermes does not have.
 */
export function formatReplyTime(iso: string | undefined, now: Date = new Date(), locale?: string): string {
	if (!iso) return "";
	const date = new Date(iso);
	if (Number.isNaN(date.getTime())) return "";
	const time = date.toLocaleTimeString(locale, { hour: "numeric", minute: "2-digit" });
	const dayStart = (value: Date) => new Date(value.getFullYear(), value.getMonth(), value.getDate()).getTime();
	const days = Math.round((dayStart(now) - dayStart(date)) / DAY_MS);
	if (days <= 0) return time;
	if (days === 1) return `Yesterday · ${time}`;
	return date.toLocaleDateString(locale, { day: "numeric", month: "short", ...(date.getFullYear() === now.getFullYear() ? {} : { year: "numeric" }) });
}
