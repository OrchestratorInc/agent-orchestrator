import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { useSettings } from "../../hooks/useSettings";
import { apiClient } from "../../lib/api-client";
import { apiErrorException } from "../../lib/api-error-exception";
import { aoBridge } from "../../lib/bridge";
import { computerUseRemoteQueryKey, fetchComputerUseRemote, REMOTE_ACTIVITY_POLL_MS, type ComputerUseRemoteSettings } from "../../lib/computer-use-remote";
import { clientForHost } from "../../lib/host-clients";
import { savedRemoteHosts, subscribeSavedRemoteHosts } from "../../lib/saved-remote-hosts";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

type RemoteSettings = ComputerUseRemoteSettings;

const remoteOfferQueryKey = (hostId: string) => ["computer-use", "remote-offer", hostId] as const;
const THIS_COMPUTER = "local";
const OFFER_STALE_MS = 15_000;

/**
 * Computer use across the user's machines, over the remote-host connections
 * they already have:
 *
 * - "Run computer use on" lists this computer and every connected machine that
 *   currently offers computer use. Choosing one hands the local daemon that
 *   machine's address and the credential this desktop holds for it (main
 *   process to daemon; the renderer never sees it), and from then on this
 *   computer's agents see and operate that machine's screen.
 * - "Let agents on your other computers use this computer" is the other side:
 *   it lets machines that can reach this one as a remote host run computer use
 *   here. It needs Cua Driver, and calls also need "Let agents use this
 *   computer" on.
 */
export function RemoteComputerUseSection({ installed, titleHidden }: { installed: boolean; titleHidden?: boolean }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const { settings: appSettings } = useSettings();
	const remote = useQuery({
		queryKey: computerUseRemoteQueryKey,
		queryFn: fetchComputerUseRemote,
		// While other computers may use this one, follow who is using it.
		refetchInterval: (query) => (query.state.data?.allowRemote ? REMOTE_ACTIVITY_POLL_MS : false),
	});
	const saved = useSyncExternalStore(subscribeSavedRemoteHosts, savedRemoteHosts, savedRemoteHosts);
	const connected = saved.filter((host) => host.status === "connected");
	const offers = useQueries({
		queries: connected.map((host) => ({
			queryKey: remoteOfferQueryKey(host.hostId),
			queryFn: async () => {
				const { data, error } = await clientForHost(host.hostId).GET("/api/v1/remote-computer-use/status");
				// An older Orchestrator has no such route: it offers nothing.
				if (error || !data) return false;
				return data.offered;
			},
			staleTime: OFFER_STALE_MS,
			retry: false,
		})),
	});
	const [busy, setBusy] = useState<"target" | "allow" | null>(null);
	const [error, setError] = useState<{ row: "target" | "allow"; message: string } | null>(null);

	const target = remote.data?.target;
	const offered = new Set(connected.filter((_, index) => offers[index]?.data === true).map((host) => host.hostId));
	const options: SettingsOption<string>[] = [{ value: THIS_COMPUTER, label: t("settings.computerUse.thisComputer") }];
	for (const host of connected) {
		if (offered.has(host.hostId)) options.push({ value: host.hostId, label: host.label });
	}
	// The chosen machine stays listed while it is offline or stops offering,
	// so the menu still shows where computer use runs.
	if (target && !options.some((option) => option.value === target.hostId)) {
		options.push({ value: target.hostId, label: saved.find((host) => host.hostId === target.hostId)?.label ?? target.label });
	}
	const targetAvailable = !target || offered.has(target.hostId);
	const targetChecked = !target || !connected.some((host) => host.hostId === target.hostId)
		|| offers[connected.findIndex((host) => host.hostId === target.hostId)]?.isFetched === true;

	const run = async (row: "target" | "allow", work: () => Promise<RemoteSettings>) => {
		setBusy(row);
		setError(null);
		try {
			queryClient.setQueryData(computerUseRemoteQueryKey, await work());
		} catch (cause) {
			setError({ row, message: cause instanceof Error ? cause.message : String(cause) });
		} finally {
			setBusy(null);
			void queryClient.invalidateQueries({ queryKey: computerUseRemoteQueryKey });
		}
	};
	const choose = (hostId: string) => {
		if (hostId === (target?.hostId ?? THIS_COMPUTER)) return;
		void run("target", async () => {
			if (hostId === THIS_COMPUTER) {
				const { data, error: apiError } = await apiClient.DELETE("/api/v1/computer-use/remote/target");
				if (apiError || !data) throw apiErrorException(apiError);
				return data;
			}
			return await aoBridge.remotes.setComputerUseTarget(hostId) as RemoteSettings;
		});
	};
	const setAllow = (enabled: boolean) => void run("allow", async () => {
		const { data, error: apiError } = await apiClient.PUT("/api/v1/computer-use/remote/allow", { body: { enabled } });
		if (apiError || !data) throw apiErrorException(apiError);
		return data;
	});

	const allowed = remote.data?.allowRemote ?? false;
	const activity = allowed ? remote.data?.activity ?? [] : [];
	const targetLabel = target ? options.find((option) => option.value === target.hostId)?.label ?? target.label : "";
	const runOnDescription = target && targetChecked && !targetAvailable
		? <span className="text-error">{t("settings.computerUse.runOnUnavailable", { name: targetLabel })}</span>
		: saved.length === 0 && !target ? t("settings.computerUse.runOnNoHosts")
		: t("settings.computerUse.runOnDescription");
	// Without Cua Driver the switch stays off; the driver row above says to install it.
	const allowDescription = allowed && appSettings && !appSettings.computerUse
		? <span className="text-error">{t("settings.computerUse.allowRemoteNeedsAccess")}</span>
		: t("settings.computerUse.allowRemoteDescription");

	return (
		<SettingsSection title={t("settings.computerUse.remote")} description={t("settings.computerUse.remoteDescription")} sectionId="remoteComputerUse" titleHidden={titleHidden} grouped>
			<div className="flex w-full flex-col">
				<SettingsRow label={t("settings.computerUse.runOn")} description={runOnDescription}>
					<SettingsOptionMenu
						aria-label={t("settings.computerUse.runOn")}
						value={target?.hostId ?? THIS_COMPUTER}
						options={options}
						onChange={choose}
						disabled={!remote.data || busy !== null}
					/>
				</SettingsRow>
				{error?.row === "target" ? <p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">{error.message}</p> : null}
			</div>
			<div className="flex w-full flex-col">
				<SettingsRow label={t("settings.computerUse.allowRemote")} description={allowDescription}>
					<Switch
						aria-label={t("settings.computerUse.allowRemote")}
						checked={allowed}
						disabled={!remote.data || busy !== null || (!installed && !allowed)}
						onCheckedChange={setAllow}
					/>
				</SettingsRow>
				{error?.row === "allow" ? <p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">{error.message}</p> : null}
			</div>
			{activity.map((run) => (
				<SettingsRow
					key={run.caller}
					label={t("settings.computerUse.remoteActive", { name: run.name || t("settings.computerUse.anotherComputer") })}
					description={t("settings.computerUse.remoteActiveStop")}
				>
					<Button type="button" size="sm" variant="ghost" disabled={busy !== null} onClick={() => setAllow(false)}>
						{t("settings.computerUse.stop")}
					</Button>
				</SettingsRow>
			))}
			{remote.isError ? (
				<div className="settings-content-row">
					<p role="alert" className="text-xs text-error">{remote.error instanceof Error ? remote.error.message : t("settings.computerUse.loadFailed")}</p>
				</div>
			) : null}
		</SettingsSection>
	);
}
