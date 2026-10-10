import { useTranslation } from "react-i18next";
import { accountHeadroom, type ProviderAccount } from "../hooks/useProviderAccounts";
import { cn } from "../lib/utils";

// An account's name wherever it is chosen. An account without a name falls back
// to its email, which stays blurred, as on the Accounts page, until it is
// pointed at or the control holding it is highlighted or focused.
export function AccountName({ account }: { account: Pick<ProviderAccount, "displayName" | "email"> }) {
	if (account.displayName) return <>{account.displayName}</>;
	return <span className="rounded-sm blur-sm transition-[filter] hover:blur-none in-data-highlighted:blur-none in-focus-visible:blur-none">{account.email}</span>;
}

// What every account menu shows for one account: its name, whether it is the
// provider default, and how much of its shortest usage window is left. The
// same row in the new task dialog, the session menu and the Accounts page, so
// they never describe an account differently.
export function AccountChoiceLabel({ account, isDefault }: { account: ProviderAccount; isDefault: boolean }) {
	const { t } = useTranslation();
	const headroom = accountHeadroom(account);
	return (
		<>
			<span className="min-w-0 flex-1 truncate"><AccountName account={account} /></span>
			{isDefault ? <span className="shrink-0 text-2xs text-muted-foreground">{t("providerAccounts.default")}</span> : null}
			{headroom === null ? null : (
				<span className={cn("shrink-0 pl-2 text-xs tabular-nums", headroom === 0 ? "text-status-needs-you" : headroom <= 20 ? "text-warning" : "text-muted-foreground")}>
					{headroom === 0 ? t("providerAccounts.usageReached") : t("providerAccounts.usageRemaining", { percent: headroom })}
				</span>
			)}
		</>
	);
}
