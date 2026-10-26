import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useSettings } from "../../hooks/useSettings";
import { deleteAccountRemoteHost, listAccountRemoteHosts, saveAccountRemoteHost } from "../../lib/account-remote-hosts";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { aoBridge } from "../../lib/bridge";
import { useCloudSession } from "../../lib/cloud-session";
import { Switch } from "../ui/switch";
import { fetchMobileStatus, mobileStatusQueryKey, mobileStatusRefetchInterval } from "./ConnectMobileContent";
import { InstallCloudflared } from "./InstallCloudflared";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

type MobileStatus = Awaited<ReturnType<typeof fetchMobileStatus>>;

const TUNNEL_POLL_MS = 2_000;
// A quick tunnel usually has its address within half a minute.
const TUNNEL_WAIT_MS = 90_000;
// AO Cloud rejects host labels longer than 64 bytes.
const LABEL_MAX_BYTES = 64;

/** The tunnel address in the form the daemon's address publisher sends, so a
 * later publish never rewrites the record and reconnects every device. */
function tunnelUrl(status: MobileStatus): string | null {
	const tunnel = status.endpoints?.find((endpoint) => endpoint.kind === "tunnel" && endpoint.secure);
	return tunnel ? `https://${tunnel.host}:${tunnel.port}` : null;
}

function fitLabel(name: string): string {
	let chars = Array.from(name.trim());
	while (new TextEncoder().encode(chars.join("")).length > LABEL_MAX_BYTES) chars = chars.slice(0, -1);
	return chars.join("").trim();
}

/** Polls until the quick tunnel has a public address; null when no connector is installed. */
async function waitForTunnel(timeoutMessage: string): Promise<{ status: MobileStatus; url: string } | null> {
	const deadline = Date.now() + TUNNEL_WAIT_MS;
	for (;;) {
		const status = await fetchMobileStatus();
		const url = tunnelUrl(status);
		if (url) return { status, url };
		if (status.tunnel?.supported === false) return null;
		if (status.tunnel?.lastError) throw new Error(status.tunnel.lastError);
		if (Date.now() >= deadline) throw new Error(timeoutMessage);
		await new Promise((resolve) => setTimeout(resolve, TUNNEL_POLL_MS));
	}
}

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
		let linked = false;
		try {
			// remote-access keeps the password, so a phone paired over Connect Mobile stays paired.
			const started = before.enabled
				? await apiClient.POST("/api/v1/mobile/remote-access")
				: await apiClient.POST("/api/v1/mobile/enable");
			if (started.error) throw new Error(apiErrorMessage(started.error));
			const ready = await waitForTunnel(t("remote.shareThisComputerTimeout"));
			if (!ready) {
				setNeedsConnector(true);
				return;
			}
			const issued = await apiClient.POST("/api/v1/remote-host/account-token", {
				headers: { Authorization: `Bearer ${ready.status.password}`, "X-AO-Account-ID": accountId },
			});
			if (issued.error || !issued.data) throw new Error(apiErrorMessage(issued.error));
			await saveAccountRemoteHost(cloudBaseUrl, {
				hostId: issued.data.hostId,
				label: fitLabel(await aoBridge.remotes.computerName()),
				url: ready.url,
				token: issued.data.token,
			});
			linked = true;
		} finally {
			// Unlinked, the switch shows off; leaving access on would keep this computer reachable while the switch says it is not.
			if (!linked) await restoreRemoteAccess(before);
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
