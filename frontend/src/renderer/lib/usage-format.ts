const NANOS_PER_DOLLAR = 1_000_000_000;

/** "$3,787.14", or "$0.0042" for an amount under a cent. */
export function formatUsd(nanos: number, locale: string): string {
	const dollars = nanos / NANOS_PER_DOLLAR;
	const small = dollars > 0 && dollars < 0.01;
	return new Intl.NumberFormat(locale, {
		style: "currency",
		currency: "USD",
		minimumFractionDigits: 2,
		maximumFractionDigits: small ? 4 : 2,
	}).format(dollars);
}

/** "$400", "$1.5K": axis ticks and other places where cents are noise. */
export function formatUsdCompact(nanos: number, locale: string): string {
	const dollars = nanos / NANOS_PER_DOLLAR;
	return new Intl.NumberFormat(locale, {
		style: "currency",
		currency: "USD",
		notation: dollars >= 10_000 ? "compact" : "standard",
		maximumFractionDigits: dollars < 10 ? 2 : 0,
	}).format(dollars);
}

/** "12.1B", "366K", "27.5M". */
export function formatTokens(value: number, locale: string): string {
	return new Intl.NumberFormat(locale, { notation: "compact", maximumFractionDigits: 1 }).format(value);
}

/** "53.0%" from 0.53. */
export function formatShare(fraction: number, locale: string): string {
	return new Intl.NumberFormat(locale, { style: "percent", minimumFractionDigits: 1, maximumFractionDigits: 1 }).format(
		Number.isFinite(fraction) ? fraction : 0,
	);
}

/**
 * A report period's label. Day keys are "2006-01-02" and hour keys
 * "2006-01-02T15:00", both already in the reader's zone, so they are parsed as
 * local wall-clock times rather than converted.
 */
export function formatPeriod(period: string, locale: string, style: "short" | "long"): string {
	const [day, hour] = period.split("T");
	const [year, month, date] = day.split("-").map(Number);
	const at = new Date(year, (month ?? 1) - 1, date ?? 1, hour ? Number(hour.slice(0, 2)) : 12);
	if (Number.isNaN(at.getTime())) return period;
	if (hour) {
		return style === "short"
			? new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit" }).format(at)
			: new Intl.DateTimeFormat(locale, { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" }).format(at);
	}
	return style === "short"
		? new Intl.DateTimeFormat(locale, { day: "numeric", month: "short" }).format(at)
		: new Intl.DateTimeFormat(locale, { weekday: "short", day: "numeric", month: "short", year: "numeric" }).format(at);
}

/** "5 minutes ago", "now": how old a reading is, in the reader's language. */
export function formatRelativeTime(iso: string, now: Date, locale: string): string {
	const seconds = (new Date(iso).getTime() - now.getTime()) / 1000;
	if (!Number.isFinite(seconds)) return "";
	const format = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
	const abs = Math.abs(seconds);
	if (abs < 60) return format.format(Math.round(seconds), "second");
	if (abs < 3600) return format.format(Math.round(seconds / 60), "minute");
	if (abs < 86_400) return format.format(Math.round(seconds / 3600), "hour");
	return format.format(Math.round(seconds / 86_400), "day");
}
