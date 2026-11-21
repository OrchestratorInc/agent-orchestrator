import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../lib/bridge";
import { computerUseRemoteQueryKey, fetchComputerUseRemote, REMOTE_ACTIVITY_POLL_MS } from "../lib/computer-use-remote";
import { useUiStore } from "../stores/ui-store";

const NOTIFICATION_PREFIX = "computer-use-remote:";
const IDLE_POLL_MS = 60_000;

/**
 * Tells the person at this computer when another computer's agent starts
 * using it: one OS notification per run of calls (the daemon starts a new run
 * after two quiet minutes). Clicking it opens Settings > Computer use agents,
 * where the same activity shows with a Stop button. Polls only while other
 * computers may use this one.
 */
export function useRemoteComputerUseAlerts(): void {
	const { t } = useTranslation();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const remote = useQuery({
		queryKey: computerUseRemoteQueryKey,
		queryFn: fetchComputerUseRemote,
		refetchInterval: (query) => (query.state.data?.allowRemote ? REMOTE_ACTIVITY_POLL_MS : IDLE_POLL_MS),
		retry: false,
	});
	const announced = useRef(new Set<string>());
	const activity = remote.data?.allowRemote ? remote.data.activity : undefined;

	useEffect(() => {
		for (const run of activity ?? []) {
			const id = `${NOTIFICATION_PREFIX}${run.caller}:${run.started}`;
			if (announced.current.has(id)) continue;
			announced.current.add(id);
			if (announced.current.size > 64) announced.current.delete(announced.current.values().next().value!);
			const name = run.name || t("settings.computerUse.anotherComputer");
			void aoBridge.notifications.show({
				id,
				title: t("settings.computerUse.remoteActive", { name }),
				body: t("settings.computerUse.remoteActiveBody"),
				type: "computer_use_remote",
			});
		}
	}, [activity, t]);

	useEffect(() => aoBridge.notifications.onClick((id) => {
		if (id.startsWith(NOTIFICATION_PREFIX)) openGlobalSettings("computerUse");
	}), [openGlobalSettings]);
}
