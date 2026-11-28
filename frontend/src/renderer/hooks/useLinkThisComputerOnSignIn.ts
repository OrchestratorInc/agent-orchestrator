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

const LINK_RETRY_INIT_MS = 30_000;
const LINK_RETRY_MAX_MS = 300_000;
const LINK_MAX_RETRIES = 5;

function extractErrorInfo(cause: unknown): {
	status?: number;
	error?: string;
} {
	let current: unknown = cause;
	let status: number | undefined;
	let error: string | undefined;

	for (let depth = 0; depth < 8 && current && typeof current === "object"; depth++) {
		if ("status" in current && typeof (current as { status: unknown }).status === "number") {
			status ??= (current as { status: number }).status;
		}
		if ("error" in current && typeof (current as { error: unknown }).error === "string") {
			error ??= (current as { error: string }).error;
		}
		if ("cause" in current && current.cause) {
			current = current.cause;
		} else {
			break;
		}
	}

	return { status, error };
}

export function isRetryableLinkError(cause: unknown): boolean {
	const { status, error } = extractErrorInfo(cause);
	if (status !== undefined) {
		return status === 409 || status === 429 || status >= 500;
	}
	if (
		error === "forbidden" ||
		error === "unauthorized" ||
		error === "bad_request" ||
		error === "not_found" ||
		error === "not_implemented" ||
		error === "method_not_allowed"
	) {
		return false;
	}
	return true;
}

/**
 * Links this computer to the account once the desktop is signed in, when
 * Connect Mobile already serves it remotely and no account holds it yet.
 * Connect Mobile turned on while signed in links it the same way, so signing
 * in afterwards needs no second step. The computer then moves to its fixed
 * address by itself. A computer an account holds is left alone, so signing in
 * to another account does not take it from the first, and one its account
 * removed on another device is not listed again; Allow access moves it.
 *
 * Retries with exponential backoff (30s→300s, 5 retries) while waiting for the
 * daemon, remote access, or the public tunnel to become ready, stopping early
 * on permanent errors and exhausting retries until the next sign-in or app
 * restart. Remote access enabled outside Connect Mobile (such as via CLI)
 * after retries run out is not linked automatically.
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
		if (!key) {
			done.current = "";
			return;
		}
		if (done.current === key) return;
		if (isDevelopmentBuild()) {
			done.current = key;
			return;
		}
		let cancelled = false;
		let timer: ReturnType<typeof setTimeout> | undefined;
		let retryDelayMs = LINK_RETRY_INIT_MS;
		let attempts = 0;
		// Set once this sign-in starts linking. A token issued by a failed attempt
		// makes accountLinked true before the directory entry exists.
		let linkBegun = false;
		const finish = () => {
			if (cancelled) return;
			done.current = key;
		};
		const retry = () => {
			if (cancelled) return;
			if (attempts >= LINK_MAX_RETRIES) {
				finish();
				return;
			}
			attempts++;
			const delay = retryDelayMs;
			retryDelayMs = Math.min(retryDelayMs * 2, LINK_RETRY_MAX_MS);
			timer = setTimeout(() => void run(), delay);
		};
		const run = async () => {
			let status: Awaited<ReturnType<typeof fetchMobileStatus>>;
			try {
				status = await fetchMobileStatus();
			} catch (cause: unknown) {
				if (cancelled) return;
				if (!isRetryableLinkError(cause)) {
					finish();
					return;
				}
				// The daemon may still be starting; try again with backoff.
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
			} catch (cause: unknown) {
				if (cancelled) return;
				// Not fatal: the computer keeps its quick tunnel, and Allow access links
				// it. Retry transient network or 5xx errors; stop on permanent failures.
				if (isRetryableLinkError(cause)) {
					retry();
				} else {
					finish();
				}
			}
		};
		void run();
		return () => {
			cancelled = true;
			if (timer) clearTimeout(timer);
		};
	}, [accountId, cloudBaseUrl, queryClient]);
}
