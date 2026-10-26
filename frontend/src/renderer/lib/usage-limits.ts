import type { AgentAccount, AgentAccountProvider } from "../hooks/useAgentAccounts";

export type LimitSegment = {
	accountId: string;
	/** Percent of this account's window still available, 0–100; null when it does not report the window. */
	remainingPercent: number | null;
	resetsAt: string | null;
};

export type PooledWindow = {
	id: string;
	label: string;
	/** Average of what is left across the accounts that report this window. */
	remainingPercent: number;
	/** One segment per account, in the provider's account order, so columns line up across windows. */
	segments: LimitSegment[];
	/** The soonest reset among the reporting accounts. */
	nextResetAt: string | null;
};

export type ProviderLimits = {
	provider: AgentAccountProvider;
	accounts: AgentAccount[];
	windows: PooledWindow[];
	resetCredits: number;
	checkedAt: string | null;
};

const PROVIDER_ORDER: AgentAccountProvider[] = ["codex", "claude"];

function clampPercent(value: number): number {
	return Math.max(0, Math.min(100, value));
}

/**
 * Pools every account's limit windows per provider, so a provider with several
 * accounts reads as one number per window rather than a list. Accounts keep one
 * column order across windows: the default account first, then by id.
 */
export function poolLimits(accounts: readonly AgentAccount[]): ProviderLimits[] {
	const out: ProviderLimits[] = [];
	for (const provider of PROVIDER_ORDER) {
		const providerAccounts = accounts
			.filter((account) => account.provider === provider)
			.sort((a, b) => Number(b.default) - Number(a.default) || a.id.localeCompare(b.id));
		if (providerAccounts.length === 0) continue;

		const windowOrder: { id: string; label: string }[] = [];
		for (const account of providerAccounts) {
			for (const window of account.limits?.windows ?? []) {
				if (!windowOrder.some((known) => known.id === window.id)) windowOrder.push({ id: window.id, label: window.label });
			}
		}
		const windows = windowOrder.map(({ id, label }): PooledWindow => {
			const segments = providerAccounts.map((account): LimitSegment => {
				const window = account.limits?.windows.find((candidate) => candidate.id === id);
				return {
					accountId: account.id,
					remainingPercent: window ? clampPercent(100 - window.usedPercent) : null,
					resetsAt: window?.resetsAt ?? null,
				};
			});
			const reported = segments.filter((segment) => segment.remainingPercent !== null);
			const remainingPercent = reported.length
				? reported.reduce((sum, segment) => sum + (segment.remainingPercent ?? 0), 0) / reported.length
				: 0;
			const resets = reported.map((segment) => segment.resetsAt).filter((value): value is string => Boolean(value)).sort();
			return { id, label, remainingPercent, segments, nextResetAt: resets[0] ?? null };
		});
		const checked = providerAccounts.map((account) => account.limits?.fetchedAt).filter((value): value is string => Boolean(value)).sort();
		out.push({
			provider,
			accounts: providerAccounts,
			windows,
			resetCredits: providerAccounts.reduce((sum, account) => sum + (account.limits?.resetCredits ?? 0), 0),
			checkedAt: checked.at(-1) ?? null,
		});
	}
	return out;
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
