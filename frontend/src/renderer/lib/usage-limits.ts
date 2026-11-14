import type { components } from "../../api/schema";
import { withoutSameLogins, type AgentAccount, type AgentAccountLimitWindow, type AgentAccountProvider } from "../hooks/useAgentAccounts";

export type UsageLimitWindows = components["schemas"]["UsageLimitWindowsResponse"];
export type UsageLimitLogin = components["schemas"]["UsageLimitLogin"];
export type UsageLimitSpan = components["schemas"]["UsageLimitSpan"];

export const DAY_MINUTES = 24 * 60;

export type ProviderAccounts = {
	provider: AgentAccountProvider;
	accounts: AgentAccount[];
	checkedAt: string | null;
};

const PROVIDER_ORDER: AgentAccountProvider[] = ["codex", "claude"];

export const PROVIDER_HARNESS: Record<AgentAccountProvider, string> = { codex: "codex", claude: "claude-code" };

export function groupAccounts(accounts: readonly AgentAccount[]): ProviderAccounts[] {
	return PROVIDER_ORDER.flatMap((provider) => {
		const sorted = accounts
			.filter((account) => account.provider === provider)
			.sort((a, b) => Number(b.default) - Number(a.default) || a.id.localeCompare(b.id));
		const providerAccounts = withoutSameLogins(sorted, sorted.find((account) => account.default)?.id);
		if (providerAccounts.length === 0) return [];
		const checked = providerAccounts.map((account) => account.limits?.fetchedAt).filter((value): value is string => Boolean(value)).sort();
		return [{ provider, accounts: providerAccounts, checkedAt: checked.at(-1) ?? null }];
	});
}

/** Consumed share of a limit window, clamped to 0-100. Providers report it as used, so it is drawn as-is. */
export function usedPercent(value: number): number {
	return Math.max(0, Math.min(100, value));
}

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
	atReset: number;
	runsOutAt: Date | null;
};

const HOUR_MS = 3_600_000;

export function paceToReset(startMs: number, resetMs: number, atMs: number, used: number): LimitPace | null {
	const elapsed = atMs - startMs;
	if (!(elapsed >= Math.min(HOUR_MS, (resetMs - startMs) / 10)) || !(resetMs > atMs) || !(used > 0) || used >= 100) return null;
	const perMs = used / elapsed;
	const atReset = Math.min(100, used + perMs * (resetMs - atMs));
	const runsOut = atMs + (100 - used) / perMs;
	return { atReset, runsOutAt: runsOut < resetMs ? new Date(runsOut) : null };
}

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

export function limitWindowPace(window: AgentAccountLimitWindow, fetchedAt: string): LimitPace | null {
	if (!window.resetsAt || !window.durationMinutes) return null;
	const reset = Date.parse(window.resetsAt);
	return paceToReset(reset - window.durationMinutes * 60_000, reset, Date.parse(fetchedAt), window.usedPercent);
}
