import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { fetchMobileStatus, tunnelUrl } from "../components/settings/ConnectMobileContent";
import { AccountHostSyncError } from "../lib/account-remote-hosts";
import { apiClient } from "../lib/api-client";
import { useCloudSession } from "../lib/cloud-session";
import { linkThisComputer } from "../lib/link-this-computer";
import { mobileStatusQueryKey } from "../lib/mobile-status-key";
import { useSettings } from "./useSettings";

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
			done.current = key;
			if (!status.enabled || status.noPublicTunnel || status.accountLinked || !status.hostId || !status.password) return;
			const link = (url?: string) => linkThisComputer(cloudBaseUrl, accountId, status.hostId ?? "", status.password, { ifUnlisted: true, url });
			const url = tunnelUrl(status);
			let { hostId, added } = await link().catch((cause: unknown) => {
				if (cause instanceof AccountHostSyncError && cause.status === 409) return { hostId: "", added: false };
				throw cause;
			});
			if (!hostId && url) ({ hostId, added } = await link(url));
			if (added) await apiClient.POST("/api/v1/mobile/remote-access");
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: mobileStatusQueryKey }),
				queryClient.invalidateQueries({ queryKey: ["account-remote-hosts", cloudBaseUrl] }),
			]);
		})().catch(() => {
		});
		return () => {
			cancelled = true;
		};
	}, [accountId, cloudBaseUrl, queryClient]);
}
