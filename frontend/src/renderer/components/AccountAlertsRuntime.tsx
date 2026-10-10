import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { percentLeft, useProviderAccounts } from "../hooks/useProviderAccounts";

// said tells the user each thing once: the stamp names the limit window, or the move, it was about.
function said(key: string, stamp: string): boolean {
	try {
		if (localStorage.getItem(key) === stamp) return true;
		localStorage.setItem(key, stamp);
		return false;
	} catch {
		return true;
	}
}
function tell(title: string, body: string) {
	try {
		new Notification(title, { body });
	} catch {
		// Nowhere to show it.
	}
}
// Watches the accounts that asked for it: one notice when little is left, one when a reached limit moved the sessions on.
export function AccountAlertsRuntime() {
	const { t } = useTranslation();
	const catalogue = useProviderAccounts(true, false).data?.accounts;
	const watched = Boolean(catalogue?.some((account) => account.warnAt || account.onLimit));
	const accounts = useProviderAccounts(watched, true).data?.accounts;
	useEffect(() => {
		for (const account of watched ? accounts ?? [] : []) {
			const general = (account.usage?.status === "available" ? account.usage.windows ?? [] : []).filter((window) => !window.scope);
			const lowest = general.reduce<typeof general[number] | undefined>((least, window) => (!least || window.remainingFraction < least.remainingFraction ? window : least), undefined);
			const left = lowest ? percentLeft(lowest.remainingFraction) : null;
			if (account.warnAt && left !== null && left <= account.warnAt && !said(`ao.account-low.${account.id}`, lowest?.resetTime ?? "now")) {
				tell(t("providerAccounts.lowTitle", { name: account.displayName }), t("providerAccounts.lowBody", { percent: left }));
			}
			if (account.moved && !said(`ao.account-moved.${account.id}`, account.moved.at)) {
				const to = accounts?.find((other) => other.id === account.moved?.to)?.displayName ?? "";
				tell(t("providerAccounts.movedTitle", { name: account.displayName }), t("providerAccounts.movedBody", { count: account.moved.sessions, to }));
			}
		}
	}, [accounts, watched, t]);
	return null;
}
