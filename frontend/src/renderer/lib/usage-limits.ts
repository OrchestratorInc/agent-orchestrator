import type { AgentAccount, AgentAccountProvider } from "../hooks/useAgentAccounts";

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
