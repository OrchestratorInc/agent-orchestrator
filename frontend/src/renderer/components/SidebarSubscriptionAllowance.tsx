import { LoaderCircle } from "lucide-react";
import { useTranslation } from "react-i18next";
import { agentAccountProviderNames, useAgentAccounts, type AgentAccountLimitWindow } from "../hooks/useAgentAccounts";
import { formatCountdown, groupAccounts, usedPercent } from "../lib/usage-limits";
import { formatRelativeTime } from "../lib/usage-format";
import { useUiStore } from "../stores/ui-store";

/** A compact view of the provider-reported allowance for each default subscription. */
export function SidebarSubscriptionAllowance({ tabIndex = 0 }: { tabIndex?: number }) {
	const { t, i18n } = useTranslation();
	const accounts = useAgentAccounts();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);

	if (accounts.isLoading && !accounts.data) {
		return (
			<div className="flex h-7 items-center gap-2 px-2.5 text-xs text-muted-foreground" role="status" aria-live="polite">
				<LoaderCircle aria-hidden="true" className="size-icon-sm animate-spin motion-reduce:animate-none" />
				<span>{t("common.loading")}</span>
			</div>
		);
	}

	const grouped = groupAccounts(accounts.data ?? []);
	const summaries: Array<{ provider: string; text: string; reset?: string; checkedAt?: string; stale?: boolean }> = grouped.flatMap(({ provider, accounts: providerAccounts }) => {
		const defaultAccount = providerAccounts.find((candidate) => candidate.default);
		if (!defaultAccount) return [];
		if (!defaultAccount.signedIn) return [{ provider, text: t("sidebar.subscriptionAllowanceSignIn", { provider: agentAccountProviderNames[provider] }) }];
		const account = defaultAccount;
		const window = account.limits?.windows.reduce<AgentAccountLimitWindow | undefined>((lowest, candidate) => {
			if (!lowest || usedPercent(candidate.usedPercent) > usedPercent(lowest.usedPercent)) return candidate;
			return lowest;
		}, undefined);
		if (!window) return [{
			provider,
			text: t("sidebar.providerSubscriptionAllowanceUnavailable", { provider: agentAccountProviderNames[provider] }),
			checkedAt: account.limits?.fetchedAt,
			stale: Boolean(account.problem || account.problemKind),
		}];
		const remaining = 100 - usedPercent(window.usedPercent);
		const percent = new Intl.NumberFormat(i18n.language, { style: "percent", maximumFractionDigits: 0 }).format(remaining / 100);
		const reset = formatCountdown(window.resetsAt, new Date(), i18n.language);
		return [{
			provider,
			text: t("sidebar.subscriptionAllowanceRemaining", {
				provider: agentAccountProviderNames[provider],
				window: window.label,
				percent,
			}),
			reset: reset ? t("sidebar.subscriptionAllowanceReset", { value: reset }) : undefined,
			checkedAt: account.limits?.fetchedAt,
			stale: Boolean(account.problem || account.problemKind || accounts.error),
		}];
	});

	const visibleSummaries = summaries.length > 0
		? summaries
		: [{
			provider: "unavailable",
			text: accounts.error ? t("sidebar.subscriptionAllowanceUnavailable") : t("sidebar.subscriptionAllowanceNoAccounts"),
		}];

	return (
		<div className="flex flex-col">
			{visibleSummaries.map((summary) => {
				const label = [summary.text, summary.reset].filter(Boolean).join(" · ");
				const checkedAt = summary.checkedAt
					? summary.stale
						? t("usage.limits.staleFrom", { value: formatRelativeTime(summary.checkedAt, new Date(), i18n.language) })
						: t("usage.limits.checked", { value: formatRelativeTime(summary.checkedAt, new Date(), i18n.language) })
					: undefined;
				return (
					<button
						key={summary.provider}
						aria-description={t("usage.limits.openSettings")}
						aria-label={label}
						className="flex min-h-7 w-full min-w-0 items-center gap-2 rounded-md px-2.5 text-left text-xs text-muted-foreground hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
						onClick={() => openGlobalSettings("harness")}
						tabIndex={tabIndex}
						title={[label, checkedAt].filter(Boolean).join(" · ")}
						type="button"
					>
						<span aria-hidden="true" className="size-1.5 shrink-0 rounded-full bg-current opacity-70" />
						<span className="min-w-0 truncate" data-testid="sidebar-subscription-allowance">{label}</span>
					</button>
				);
			})}
		</div>
	);
}
