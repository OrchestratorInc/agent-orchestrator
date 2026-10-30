import type { components } from "../../api/schema";
import type { AgentAccount, AgentAccountLimitWindow, AgentAccountProvider } from "../hooks/useAgentAccounts";

export type UsageLimitWindows = components["schemas"]["UsageLimitWindowsResponse"];
export type UsageLimitLogin = components["schemas"]["UsageLimitLogin"];
export type UsageLimitSpan = components["schemas"]["UsageLimitSpan"];

/** Windows a day or longer are weekly limits; shorter ones are sessions. */
export const DAY_MINUTES = 24 * 60;

export type ProviderAccounts = {
	provider: AgentAccountProvider;
	/** The default account first, then by id, so the order holds across refreshes. */
	accounts: AgentAccount[];
	/** The newest reading among the provider's accounts. */
	checkedAt: string | null;
};

const PROVIDER_ORDER: AgentAccountProvider[] = ["codex", "claude"];

/** Groups signed-in accounts per provider. Each account keeps its own limit windows. */
export function groupAccounts(accounts: readonly AgentAccount[]): ProviderAccounts[] {
	return PROVIDER_ORDER.flatMap((provider) => {
		const providerAccounts = accounts
			.filter((account) => account.provider === provider)
			.sort((a, b) => Number(b.default) - Number(a.default) || a.id.localeCompare(b.id));
		if (providerAccounts.length === 0) return [];
		const checked = providerAccounts.map((account) => account.limits?.fetchedAt).filter((value): value is string => Boolean(value)).sort();
		return [{ provider, accounts: providerAccounts, checkedAt: checked.at(-1) ?? null }];
	});
}

/** Percent of a window still available, 0–100. */
export function remainingPercent(usedPercent: number): number {
	return Math.max(0, Math.min(100, 100 - usedPercent));
}

/** "3h 45m", "6d 0h" or "12m" until a reset; null once it has passed. */
export function formatCountdown(resetsAt: string | null | undefined, now: Date): string | null {
	if (!resetsAt) return null;
	const ms = new Date(resetsAt).getTime() - now.getTime();
	if (!Number.isFinite(ms) || ms <= 0) return null;
	const minutes = Math.ceil(ms / 60_000);
	const days = Math.floor(minutes / 1440);
	const hours = Math.floor((minutes % 1440) / 60);
	if (days > 0) return `${days}d ${hours}h`;
	if (hours > 0) return `${hours}h ${minutes % 60}m`;
	return `${minutes}m`;
}

export type LimitPace = {
	/** Percent used at the reset if the pace so far holds, capped at 100. */
	atReset: number;
	/** When it reaches 100% at that pace, if before the reset. */
	runsOutAt: Date | null;
};

const HOUR_MS = 3_600_000;

/**
 * Where a window lands at its reset if it keeps the pace it has had since it
 * opened. null while nothing is used, once it is already at the limit, or
 * until a tenth of the window (at most an hour) has passed, when one reading
 * would say little.
 */
export function paceToReset(startMs: number, resetMs: number, atMs: number, used: number): LimitPace | null {
	const elapsed = atMs - startMs;
	if (!(elapsed >= Math.min(HOUR_MS, (resetMs - startMs) / 10)) || !(resetMs > atMs) || !(used > 0) || used >= 100) return null;
	const perMs = used / elapsed;
	const atReset = Math.min(100, used + perMs * (resetMs - atMs));
	const runsOut = atMs + (100 - used) / perMs;
	return { atReset, runsOutAt: runsOut < resetMs ? new Date(runsOut) : null };
}

/** The pace of a window from AO's reading history, as of its last reading. */
export function spanPace(span: UsageLimitSpan): LimitPace | null {
	if (!span.startsAt) return null;
	const last = span.points?.at(-1);
	return paceToReset(
		Date.parse(span.startsAt),
		Date.parse(span.resetsAt),
		last ? Date.parse(last.at) : Date.parse(span.lastReadAt),
		last ? last.usedPercent : span.peakUsedPercent,
	);
}

/** The pace of a window from one live reading: its length gives when it opened. */
export function limitWindowPace(window: AgentAccountLimitWindow, fetchedAt: string): LimitPace | null {
	if (!window.resetsAt || !window.durationMinutes) return null;
	const reset = Date.parse(window.resetsAt);
	return paceToReset(reset - window.durationMinutes * 60_000, reset, Date.parse(fetchedAt), window.usedPercent);
}
