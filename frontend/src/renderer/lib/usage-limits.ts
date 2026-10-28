import type { components } from "../../api/schema";
import type { AgentAccount, AgentAccountProvider } from "../hooks/useAgentAccounts";

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

export type WeeklyPace = {
	/** Percent used at the reset if the pace so far holds, capped at 100. */
	atReset: number;
	/** When it reaches 100% at that pace, if before the reset. */
	runsOutAt: Date | null;
};

/**
 * Where a weekly window lands if it keeps the pace it has had since it opened.
 * null until it has been open an hour, when one reading would say little.
 */
export function weeklyPace(span: UsageLimitSpan): WeeklyPace | null {
	if (!span.startsAt) return null;
	const start = Date.parse(span.startsAt);
	const reset = Date.parse(span.resetsAt);
	const last = span.points?.at(-1);
	const lastAt = last ? Date.parse(last.at) : Date.parse(span.lastReadAt);
	const used = last ? last.usedPercent : span.peakUsedPercent;
	const elapsed = lastAt - start;
	if (!(elapsed >= 3_600_000) || !(reset > lastAt)) return null;
	const perMs = used / elapsed;
	const atReset = Math.min(100, used + perMs * (reset - lastAt));
	const runsOut = perMs > 0 ? lastAt + (100 - used) / perMs : Number.POSITIVE_INFINITY;
	return { atReset, runsOutAt: runsOut < reset ? new Date(runsOut) : null };
}
