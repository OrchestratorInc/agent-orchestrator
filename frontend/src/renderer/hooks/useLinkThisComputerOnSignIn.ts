import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { fetchMobileStatus, tunnelUrl } from "../components/settings/ConnectMobileContent";
import { AccountHostSyncError } from "../lib/account-remote-hosts";
import { apiClient } from "../lib/api-client";
import { isDevelopmentBuild } from "../lib/build-channel";
import { useCloudSession } from "../lib/cloud-session";
import { linkThisComputer } from "../lib/link-this-computer";
import { mobileStatusQueryKey } from "../lib/mobile-status-key";
import { useSettings } from "./useSettings";

// A development build's daemon and credentials live under ~/.ao/dev, beside
// the installed app's. Publishing it would add a second machine entry for the
// same laptop: an address that stops answering as soon as the dev daemon does,
// which the phone cannot tell apart from the app the user actually runs.
const LINK_RETRY_MS = 30_000;

/**
 * Links this computer to the account once the desktop is signed in, when
 * Connect Mobile already serves it remotely and no account holds it yet:
 * Connect Mobile turned on while signed in links it the same way, so signing
 * in afterwards needs no second step. The computer then moves to its fixed
 * address by itself. A computer an account holds is left alone, so signing in
 * to another account does not take it from the first, and one its account
 * removed on another device is not listed again; Allow access moves it.
 *
 * Development builds never link themselves: their identity is temporary, and
 * an account-directed listing would outlive the checkout that made it. Allow
 * access still links one explicitly, for testing.
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
		if (isDevelopmentBuild()) {
			done.current = key;
			return;
		}
		let cancelled = false;
		let timer: ReturnType<typeof setTimeout> | undefined;
		// Set once this sign-in starts linking. A token issued by a failed attempt
		// makes accountLinked true before the directory entry exists.
		let linkBegun = false;
		const finish = () => {
			done.current = key;
		};
		const retry = () => {
			if (!cancelled) timer = setTimeout(() => void run(), LINK_RETRY_MS);
		};
		const run = async () => {
			let status: Awaited<ReturnType<typeof fetchMobileStatus>>;
			try {
				status = await fetchMobileStatus();
			} catch {
				// The daemon may still be starting; try again while this sign-in lasts.
				retry();
				return;
			}
			if (cancelled) return;
			// An account holds this computer already, or removed it: neither is
			// ours to undo here. Allow access links or relists it.
			if (status.accountLinked && !linkBegun) {
				finish();
				return;
			}
			// Remote access may not have been turned on yet, or its tunnel may be
			// starting. Check again so the computer becomes discoverable without
			// the user reopening Connect Mobile.
			if (!status.enabled || status.noPublicTunnel || !status.hostId || !status.password) {
				retry();
				return;
			}
			const link = (url?: string) => linkThisComputer(cloudBaseUrl, accountId, status.hostId ?? "", status.password, { ifUnlisted: true, url });
			// An AO Cloud without fixed addresses lists it at the tunnel's address instead, as Allow access does.
			const url = tunnelUrl(status);
			try {
				linkBegun = true;
				// An account that uses all of its fixed addresses lists it at the tunnel's address too.
				let { hostId, added } = await link().catch((cause: unknown) => {
					if (cause instanceof AccountHostSyncError && cause.status === 409) return { hostId: "", added: false };
					throw cause;
				});
				if (!hostId && url) ({ hostId, added } = await link(url));
				if (cancelled) return;
				if (!hostId) {
					retry();
					return;
				}
				// The connector asked AO Cloud when the link began, before the listing
				// landed; asked again now, it moves at once instead of at its next try.
				if (added) await apiClient.POST("/api/v1/mobile/remote-access");
				await Promise.all([
					queryClient.invalidateQueries({ queryKey: mobileStatusQueryKey }),
					queryClient.invalidateQueries({ queryKey: ["account-remote-hosts", cloudBaseUrl] }),
				]);
				finish();
			} catch {
				// Not fatal: the computer keeps its quick tunnel, and Allow access links
				// it. Retry in case the link landed but the answer was lost.
				retry();
			}
		};
		void run();
		return () => {
			cancelled = true;
			if (timer) clearTimeout(timer);
		};
	}, [accountId, cloudBaseUrl, queryClient]);
}
