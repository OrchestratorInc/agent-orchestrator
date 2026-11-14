import type { ConversationRateLimits, ConversationUsage, McpServer } from "./types";

export type Severity = "normal" | "warn" | "critical";

export function contextReadout(usage?: ConversationUsage): {
	percent?: number;
	fillPercent?: number;
	severity: Severity;
} | undefined {
	if (!usage) return undefined;
	if (usage.contextWindow <= 0 || usage.contextUsed <= 0) return { severity: "normal" };
	const fraction = Math.min(1, Math.max(0, usage.contextUsed / usage.contextWindow));
	return {
		percent: Math.round(fraction * 100),
		fillPercent: Math.max(2, fraction * 100),
		severity: fraction >= 0.9 ? "critical" : fraction >= 0.7 ? "warn" : "normal",
	};
}

export function contextUsageLabel(usage?: ConversationUsage): string {
	return usage && usage.contextWindow > 0 && usage.contextUsed > 0
		? `${usage.contextUsed.toLocaleString()} / ${usage.contextWindow.toLocaleString()} context tokens`
		: "Context unavailable";
}

export function compactContextUsageLabel(usage?: ConversationUsage): string {
	if (!usage || usage.contextWindow <= 0 || usage.contextUsed <= 0) return "Context unavailable";
	return `${compactTokenCount(usage.contextUsed)} / ${compactTokenCount(usage.contextWindow)} context tokens`;
}

export function compactTokenCount(tokens: number): string {
	if (tokens < 1_000) return String(tokens);
	let unit = tokens >= 1_000_000_000 ? 1_000_000_000 : tokens >= 1_000_000 ? 1_000_000 : 1_000;
	let rounded = Number((tokens / unit).toFixed(1));
	if (rounded >= 1_000 && unit < 1_000_000_000) {
		unit *= 1_000;
		rounded = Number((tokens / unit).toFixed(1));
	}
	return `${rounded}${unit === 1_000_000_000 ? "B" : unit === 1_000_000 ? "M" : "K"}`;
}

/** Tighter than the context thresholds: exhausted quota does not degrade, it stops. */
export const QUOTA_WARN_PERCENT = 75;
export const QUOTA_CRITICAL_PERCENT = 90;

export type QuotaWindow = {
	key: "primary" | "secondary";
	label: string;
	/** Whole percent used, clamped to 0..100, for display. */
	percent: number;
	/** The same reading unrounded: compare windows on this, not on `percent`. */
	exactPercent: number;
	/** Bar width — floored so a thin sliver stays visible. */
	fillPercent: number;
	severity: Severity;
	resetsInSeconds?: number;
};

/**
 * Each window the provider reported. Negative means "not reported", which is not
 * the same as empty, so those are left out rather than drawn as 0%.
 *
 * The phone does not learn a window's duration, so it cannot say "weekly". With
 * two windows they are the provider's short and long one; with one, `planLabel`
 * names it when the provider gave one (Claude sends the window type there).
 */
export function quotaWindows(limits?: ConversationRateLimits): QuotaWindow[] {
	if (!limits) return [];
	const reported = ([
		{ key: "primary", percent: limits.primaryUsedPercent, resetsInSeconds: limits.primaryResetsInSeconds },
		{ key: "secondary", percent: limits.secondaryUsedPercent, resetsInSeconds: limits.secondaryResetsInSeconds },
	] as const).filter((window) => Number.isFinite(window.percent) && window.percent >= 0);
	const single = reported.length === 1 ? singleWindowLabel(limits.planLabel) : undefined;
	return reported.map((window) => {
		const percent = Math.min(100, Math.max(0, window.percent));
		return {
			key: window.key,
			label: single ?? (window.key === "primary" ? "Short-term limit" : "Long-term limit"),
			percent: Math.round(percent),
			exactPercent: percent,
			fillPercent: Math.max(2, percent),
			severity: percent >= QUOTA_CRITICAL_PERCENT ? "critical" : percent >= QUOTA_WARN_PERCENT ? "warn" : "normal",
			resetsInSeconds: window.resetsInSeconds,
		};
	});
}

function singleWindowLabel(planLabel?: string): string {
	const name = planLabel?.trim().replace(/_/g, " ");
	if (!name) return "Usage limit";
	const sentence = `${name.charAt(0).toUpperCase()}${name.slice(1).toLowerCase()}`;
	return /\blimit$/i.test(sentence) ? sentence : `${sentence} limit`;
}

/** The fullest reported window, once it is worth a warning. */
export function quotaWarning(limits?: ConversationRateLimits): {
	percent: number;
	severity: Exclude<Severity, "normal">;
	label: string;
	resetsInSeconds?: number;
} | undefined {
	const windows = quotaWindows(limits);
	if (!windows.length) return undefined;
	// Unrounded: 74.6 and 75.1 both round to 75, and the tie would keep the
	// window that is still below the threshold.
	const worst = windows.reduce((current, candidate) => candidate.exactPercent > current.exactPercent ? candidate : current);
	if (worst.severity === "normal") return undefined;
	return { percent: worst.percent, severity: worst.severity, label: worst.label, resetsInSeconds: worst.resetsInSeconds };
}

export function elapsedLabel(startedAt: string | undefined, nowMs: number): string | undefined {
	if (!startedAt) return undefined;
	const elapsed = Math.max(0, nowMs - Date.parse(startedAt));
	if (!Number.isFinite(elapsed)) return undefined;
	const seconds = Math.floor(elapsed / 1000);
	if (seconds < 60) return `${seconds}s`;
	const minutes = Math.floor(seconds / 60);
	if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
	return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

export function workingElapsedLabel(startedAt: string | undefined, nowMs: number): string | undefined {
	const label = elapsedLabel(startedAt, nowMs);
	return label === "0s" ? "1s" : label;
}

export function resetLabel(seconds?: number): string | undefined {
	if (seconds === undefined || seconds < 0) return undefined;
	if (seconds < 60) return `${Math.ceil(seconds)}s`;
	if (seconds < 3600) return `${Math.ceil(seconds / 60)}m`;
	if (seconds < 86_400) return `${Math.ceil(seconds / 3600)}h`;
	return `${Math.ceil(seconds / 86_400)}d`;
}

export function mcpServerFailureLabel(server: McpServer): string {
	const details = [server.failureReason, server.error].filter((value): value is string => Boolean(value?.trim()));
	return `${server.name}${details.length ? ` (${details.join(": ")})` : ""}`;
}
