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
 * Connect Mobile already serves it remotely and the machine holds no account
 * token: Connect Mobile turned on while signed in links it the same way, so
 * signing in afterwards needs no second step. The computer then moves to its
 * fixed address by itself.
 *
 * A new connection password drops the token. One set while signed out, from
 * `ao remote-host`, or by an AO that did not link again afterwards leaves the
 * account listing the computer with a token the machine no longer accepts: it
 * stops publishing its address and the account's devices lose it. It is
 * linked again here, under the same listing.
 *
 * A machine that holds a token is left alone, so signing in to another
 * account does not take it from the first, and one its account removed on
 * another device is not listed again; Allow access moves it.
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
			const link = (url?: string) => linkThisComputer(cloudBaseUrl, accountId, status.hostId ?? "", status.password, { url });
			// An AO Cloud without fixed addresses lists it at the tunnel's address instead, as Allow access does.
			const url = tunnelUrl(status);
			// An account that uses all of its fixed addresses lists it at the tunnel's address too.
			let { hostId } = await link().catch((cause: unknown) => {
				if (cause instanceof AccountHostSyncError && cause.status === 409) return { hostId: "" };
				throw cause;
			});
			if (!hostId && url) ({ hostId } = await link(url));
			// The connector asked AO Cloud when the token was issued, before the
			// listing carried it; asked again now, it moves at once instead of at
			// its next try.
			if (hostId) await apiClient.POST("/api/v1/mobile/remote-access");
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
