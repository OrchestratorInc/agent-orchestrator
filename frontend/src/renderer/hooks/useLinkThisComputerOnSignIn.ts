import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { fetchMobileStatus, tunnelUrl } from "../components/settings/ConnectMobileContent";
import { AccountHostSyncError } from "../lib/account-remote-hosts";
import { apiClient } from "../lib/api-client";
import { useCloudSession } from "../lib/cloud-session";
import { linkThisComputer } from "../lib/link-this-computer";
import { mobileStatusQueryKey } from "../lib/mobile-status-key";
import { useSettings } from "./useSettings";

/**
 * Links this computer to the account once the desktop is signed in, when
 * Connect Mobile already serves it remotely and no account holds it yet:
 * Connect Mobile turned on while signed in links it the same way, so signing
 * in afterwards needs no second step. The computer then moves to its fixed
 * address by itself. A computer an account holds is left alone, so signing in
 * to another account does not take it from the first, and one its account
 * removed on another device is not listed again; Allow access moves it.
 */
export function useLinkThisComputerOnSignIn(): void {
	const queryClient = useQueryClient();
	const { session } = useCloudSession();
	const { settings } = useSettings();
	const cloudBaseUrl = settings?.cloudControlPlaneUrl ?? "";
	const accountId = session?.user.id ?? "";
	const done = useRef("");
	useEffect(() => {
		const key = cloudBaseUrl && accountId ? `${cloudBaseUrl}#${accountId}` : "";
		if (!key || done.current === key) return;
		let cancelled = false;
		void (async () => {
			const status = await fetchMobileStatus();
			if (cancelled) return;
			done.current = key; // once per sign-in, after the daemon answered
			if (!status.enabled || status.noPublicTunnel || status.accountLinked || !status.hostId || !status.password) return;
			const link = (url?: string) => linkThisComputer(cloudBaseUrl, accountId, status.hostId ?? "", status.password, { ifUnlisted: true, url });
			// An AO Cloud without fixed addresses lists it at the tunnel's address instead, as Allow access does.
			const url = tunnelUrl(status);
			// An account that uses all of its fixed addresses lists it at the tunnel's address too.
			let { hostId, added } = await link().catch((cause: unknown) => {
				if (cause instanceof AccountHostSyncError && cause.status === 409) return { hostId: "", added: false };
				throw cause;
			});
			if (!hostId && url) ({ hostId, added } = await link(url));
			// The connector asked AO Cloud when the link began, before the listing
			// landed; asked again now, it moves at once instead of at its next try.
			if (added) await apiClient.POST("/api/v1/mobile/remote-access");
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: mobileStatusQueryKey }),
				queryClient.invalidateQueries({ queryKey: ["account-remote-hosts", cloudBaseUrl] }),
			]);
		})().catch(() => {
			// Not fatal: the computer keeps its quick tunnel, and Allow access links it.
		});
		return () => {
			cancelled = true;
		};
	}, [accountId, cloudBaseUrl, queryClient]);
}
