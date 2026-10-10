import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, SlidersHorizontal, UserRound } from "lucide-react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { providerAccountsKey, sessionAccountKey, sessionAccountQueryOptions, switchSessionAccount, useProviderAccounts } from "../hooks/useProviderAccounts";
import { useUiStore } from "../stores/ui-store";
import { AccountChoiceLabel, AccountName } from "./AccountChoiceLabel";
import {
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuSub,
	DropdownMenuSubContent,
	DropdownMenuSubTrigger,
} from "./ui/dropdown-menu";

type SessionRoute = components["schemas"]["SessionProviderAccountResponse"];

export function SessionProviderAccountMenuItem({ sessionId }: { sessionId: string }) {
	const { t } = useTranslation();
	// Mounted only while the session menu is open, so usage is read just then:
	// the headroom figure is what tells the user why they would switch.
	const accounts = useProviderAccounts(true, true);
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const cache = useQueryClient();
	const [pending, setPending] = useState(false);
	const [error, setError] = useState("");
	const selecting = useRef(false);
	const route = useQuery({ ...sessionAccountQueryOptions(sessionId), refetchInterval: 5000 });
	if (!route.data?.managed) return null;

	const choices = (accounts.data?.accounts ?? []).filter(
		(account) => account.provider === route.data?.provider && account.signedIn && !account.signInRequired,
	);
	const current = choices.find((account) => account.id === route.data?.accountId);
	async function switchAccount(accountId: string) {
		if (accountId === route.data?.accountId) return;
		setPending(true);
		setError("");
		try {
			cache.setQueryData<SessionRoute>(sessionAccountKey(sessionId), await switchSessionAccount(sessionId, accountId));
			void cache.invalidateQueries({ queryKey: providerAccountsKey });
		} catch (switchError) {
			setError(switchError instanceof Error ? switchError.message : t("providerAccounts.sessionChangeFailed"));
		} finally {
			setPending(false);
			selecting.current = false;
		}
	}
	function selectAccount(accountId: string) {
		if (selecting.current) return;
		selecting.current = true;
		void switchAccount(accountId);
	}

	return (
		<DropdownMenuSub>
			<DropdownMenuSubTrigger disabled={pending}>
				<UserRound aria-hidden="true" className="size-3.5" />
				<span>{t("providerAccounts.switchAccount")}</span>
				{current ? <span className="ml-auto max-w-28 truncate text-2xs text-passive"><AccountName account={current} /></span> : null}
			</DropdownMenuSubTrigger>
			<DropdownMenuSubContent className="min-w-64 text-xs">
				{choices.length ? choices.map((account) => (
					<DropdownMenuItem
						key={account.id}
						disabled={pending || account.id === route.data?.accountId}
						onClick={() => selectAccount(account.id)}
						onSelect={() => selectAccount(account.id)}
					>
						<Check aria-hidden="true" className={account.id === route.data?.accountId ? "size-3.5" : "invisible size-3.5"} />
						<AccountChoiceLabel account={account} isDefault={account.primary} />
					</DropdownMenuItem>
				)) : (
					<DropdownMenuItem disabled>{t("providerAccounts.noAccountsAvailable")}</DropdownMenuItem>
				)}
				{route.data.loginRequired ? <DropdownMenuItem disabled>{t("providerAccounts.loginRequired")}</DropdownMenuItem> : null}
				{error ? <DropdownMenuItem disabled className="text-destructive">{error}</DropdownMenuItem> : null}
				<DropdownMenuSeparator />
				<DropdownMenuItem onSelect={() => openGlobalSettings("accountManager")}>
					<SlidersHorizontal aria-hidden="true" className="size-3.5 text-muted-foreground" />
					<span>{t("providerAccounts.manageAccounts")}</span>
				</DropdownMenuItem>
			</DropdownMenuSubContent>
		</DropdownMenuSub>
	);
}
