import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { apiClient } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { aoBridge } from "../lib/bridge";
import { computerUseRemoteQueryKey, fetchComputerUseRemote, REMOTE_ACTIVITY_POLL_MS, type ComputerUseRemoteSettings } from "../lib/computer-use-remote";
import { clientForHost } from "../lib/host-clients";
import { savedRemoteHosts, subscribeSavedRemoteHosts } from "../lib/saved-remote-hosts";
import type { SettingsOption } from "../components/settings/SettingsOptionMenu";

export const THIS_COMPUTER = "local";
const remoteOfferQueryKey = (hostId: string) => ["computer-use", "remote-offer", hostId] as const;
const OFFER_STALE_MS = 15_000;
// A computer can start or stop offering computer use while a picker is open.
const OFFER_POLL_MS = 15_000;

/**
 * The computers computer use can run on: this one and every connected remote
 * host that offers it now, plus the chosen one while it is offline. `choose`
 * moves computer use there; with a session ID, that chat moves with it even
 * though it started elsewhere (the daemon would otherwise refuse its calls).
 */
export function useComputerUseTargets() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
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
			refetchInterval: OFFER_POLL_MS,
			retry: false,
		})),
	});
	const [choosing, setChoosing] = useState(false);
	const [error, setError] = useState<string | null>(null);

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
	const targetLabel = target ? options.find((option) => option.value === target.hostId)?.label ?? target.label : "";

	const choose = async (hostId: string, sessionId?: string) => {
		// From Settings, picking the current computer changes nothing. From a
		// chat it still moves that chat there.
		if (!sessionId && hostId === (target?.hostId ?? THIS_COMPUTER)) return;
		setChoosing(true);
		setError(null);
		try {
			let next: ComputerUseRemoteSettings;
			if (hostId === THIS_COMPUTER) {
				const { data, error: apiError } = await apiClient.DELETE("/api/v1/computer-use/remote/target", {
					params: { query: sessionId ? { sessionId } : {} },
				});
				if (apiError || !data) throw apiErrorException(apiError);
				next = data;
			} else {
				next = await aoBridge.remotes.setComputerUseTarget(hostId, sessionId) as ComputerUseRemoteSettings;
			}
			queryClient.setQueryData(computerUseRemoteQueryKey, next);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : String(cause));
		} finally {
			setChoosing(false);
			void queryClient.invalidateQueries({ queryKey: computerUseRemoteQueryKey });
		}
	};

	return { remote, saved, options, target, targetLabel, targetAvailable, targetChecked, choose, choosing, error };
}
