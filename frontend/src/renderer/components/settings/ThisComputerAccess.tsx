import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useSettings } from "../../hooks/useSettings";
import { deleteAccountRemoteHost, listAccountRemoteHosts } from "../../lib/account-remote-hosts";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { useCloudSession } from "../../lib/cloud-session";
import { linkThisComputer } from "../../lib/link-this-computer";
import { Switch } from "../ui/switch";
import { fetchMobileStatus, mobileStatusQueryKey, mobileStatusRefetchInterval, tunnelUrl, waitForTunnel } from "./ConnectMobileContent";
import { InstallCloudflared } from "./InstallCloudflared";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

type MobileStatus = Awaited<ReturnType<typeof fetchMobileStatus>>;

async function disableRemoteAccess() {
	const stopped = await apiClient.POST("/api/v1/mobile/disable");
	// Disable can report an error after the listener has already stopped.
	if (stopped.error && (await fetchMobileStatus()).enabled) throw new Error(apiErrorMessage(stopped.error));
}

/** Puts remote access back the way it was before a turn-on that did not link this computer. */
async function restoreRemoteAccess(before: MobileStatus) {
	if (!before.enabled) return disableRemoteAccess();
	// Remote access that was already on stays on. A LAN-only bridge loses the
	// tunnel again but keeps its password, so a paired phone stays paired.
	if (!before.noPublicTunnel) return;
	const restored = await apiClient.POST("/api/v1/mobile/enable-lan-only");
	if (restored.error) throw new Error(apiErrorMessage(restored.error));
}

/**
 * Lets the account's other devices open this computer: turns on the
 * authenticated remote listener with its tunnel and links this machine to the
 * signed-in AO account, so it appears on every device without a password.
 * On means both facts hold; neither is stored here.
 */
export function ThisComputerAccess() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const { session } = useCloudSession();
	const accountId = session?.user.id ?? "";
	const { settings } = useSettings();
	const cloudBaseUrl = settings?.cloudControlPlaneUrl ?? "";
	const accountHostsKey = ["account-remote-hosts", cloudBaseUrl] as const;
	const [pending, setPending] = useState<boolean | null>(null);
	const [needsConnector, setNeedsConnector] = useState(false);
	const [error, setError] = useState("");

	const usable = Boolean(cloudBaseUrl && accountId);
	const status = useQuery({
		queryKey: mobileStatusQueryKey,
		queryFn: fetchMobileStatus,
		enabled: usable,
		refetchInterval: (query) => mobileStatusRefetchInterval(query.state.data),
	});
	const accountHosts = useQuery({
		queryKey: accountHostsKey,
		// Only what this row shows: the cache never holds other hosts' tokens.
		queryFn: async () => (await listAccountRemoteHosts(cloudBaseUrl)).map(({ hostId, label }) => ({ hostId, label })),
		enabled: usable,
	});

	if (!usable) return null;

	const current = status.data;
	const record = current ? accountHosts.data?.find((host) => host.hostId === current.hostId) : undefined;
	const on = Boolean(current?.enabled && !current.noPublicTunnel && record);
	const loaded = current !== undefined && accountHosts.data !== undefined;
	const loadError = [status.error, accountHosts.error].find((cause) => cause instanceof Error)?.message ?? "";

	const turnOn = async () => {
		const before = await fetchMobileStatus();
		// Only a listing this turn-on added is taken back when it fails: one Connect Mobile or an earlier turn-on made stays.
		let hostId = "";
		let added = false;
		let shared = false;
		try {
			// A running bridge keeps its password, so a phone paired over Connect Mobile stays paired.
			let password = before.password ?? "";
			// Started with its tunnel, as before fixed addresses: an app that quits
			// while the link runs leaves remote access on, not a LAN-only computer.
			if (!before.enabled) {
				const started = await apiClient.POST("/api/v1/mobile/enable");
				if (started.error || !started.data) throw new Error(apiErrorMessage(started.error));
				password = started.data.password ?? "";
			}
			// remote-access then moves the tunnel to the fixed address the link gave this computer. When
			// AO Cloud cannot give it one, it is listed at the quick tunnel's address below, as before.
			({ hostId, added } = await linkThisComputer(cloudBaseUrl, accountId, before.hostId ?? "", password).catch(
				() => ({ hostId: "", added: false }),
			));
			const access = await apiClient.POST("/api/v1/mobile/remote-access");
			if (access.error) throw new Error(apiErrorMessage(access.error));
			const ready = await waitForTunnel(t("remote.shareThisComputerTimeout"));
			if (!ready) {
				setNeedsConnector(true);
				return;
			}
			// No fixed address: listed at the quick tunnel's, as before fixed addresses.
			if (!hostId) ({ hostId, added } = await linkThisComputer(cloudBaseUrl, accountId, before.hostId ?? "", password, { url: ready.url }));
			shared = true;
		} finally {
			// Not shared, the switch shows off; leaving access on, or the account listing this computer, would contradict it.
			if (!shared) {
				if (hostId && added) await deleteAccountRemoteHost(cloudBaseUrl, hostId).catch(() => undefined);
				await restoreRemoteAccess(before);
			}
		}
	};

	const turnOff = async (hostId: string) => {
		// Cut access first: the account record only tells devices where to look.
		await disableRemoteAccess();
		await deleteAccountRemoteHost(cloudBaseUrl, hostId);
	};

	const change = (next: boolean) => {
		if (!current) return;
		setPending(next);
		setNeedsConnector(false);
		setError("");
		void (next ? turnOn() : turnOff(current.hostId ?? ""))
			.catch((cause: unknown) => setError(cause instanceof Error && cause.message ? cause.message : t("remote.shareThisComputerFailed")))
			.finally(async () => {
				// Hold the requested position until the refreshed facts land, so the switch does not flicker back.
				await Promise.all([
					queryClient.invalidateQueries({ queryKey: mobileStatusQueryKey }),
					queryClient.invalidateQueries({ queryKey: accountHostsKey }),
				]);
				setPending(null);
			});
	};

	// Linked but not reachable: still starting, or the connector has failed.
	const unreachable = on && current !== undefined && !tunnelUrl(current);
	const tunnelError = unreachable ? current?.tunnel?.lastError ?? "" : "";
	let description = t("remote.shareThisComputerDescription");
	if (pending === true || (unreachable && !tunnelError)) description = t("remote.shareThisComputerStarting");
	else if (on && record) description = t("remote.shareThisComputerOn", { name: record.label });
	const shownError = error || tunnelError || loadError;

	return (
		<SettingsSection title={t("remote.thisComputer")} grouped>
			<SettingsRow
				label={t("remote.shareThisComputer")}
				description={
					<>
						<span className="block" role="status">{description}</span>
						{needsConnector ? (
							<>
								<span className="mt-1 block">{t("remote.shareThisComputerNeedsCloudflared")}</span>
								<InstallCloudflared onInstalled={() => change(true)} />
							</>
						) : null}
						{shownError ? <span className="mt-1 block text-error" role="alert">{shownError}</span> : null}
					</>
				}
			>
				<Switch
					aria-label={t("remote.shareThisComputer")}
					checked={pending ?? on}
					disabled={!loaded || pending !== null}
					onCheckedChange={change}
				/>
			</SettingsRow>
		</SettingsSection>
	);
}
