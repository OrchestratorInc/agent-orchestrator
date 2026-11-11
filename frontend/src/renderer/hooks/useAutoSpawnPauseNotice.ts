import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { useUiStore } from "../stores/ui-store";

export type AutoSpawnPause = components["schemas"]["AutoSpawnPause"];
const SEEN_KEY = "ao.autoSpawnPauseAcknowledgements.v2";
const acknowledged = new Set<string>();

function seenPauses(): Set<string> {
	try {
		const saved: unknown = JSON.parse(window.localStorage.getItem(SEEN_KEY) ?? "[]");
		if (Array.isArray(saved)) for (const key of saved) if (typeof key === "string") acknowledged.add(key);
	} catch { /* Memory acknowledgements remain available when storage is denied. */ }
	return acknowledged;
}

/** Keeps each active launch-configuration pause visible until acknowledged. */
export function useAutoSpawnPauseNotice(projects: { id: string; name: string; hostId?: string; autoSpawnPauses?: AutoSpawnPause[] }[]) {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	useEffect(() => {
		const seen = seenPauses();
		const active = new Set<string>();
		for (const project of projects) {
			for (const pause of project.autoSpawnPauses ?? []) {
				const key = JSON.stringify([project.hostId ?? "", project.id, pause.harness, pause.kind ?? "", pause.model ?? "", pause.effort ?? "", pause.pausedAt]);
				const dedupeKey = `auto-spawn-pause:${key}`;
				active.add(dedupeKey);
				if (seen.has(key) || useUiStore.getState().globalToasts.some((toast) => toast.dedupeKey === dedupeKey)) continue;
				showGlobalToast(
					t("autoSpawnPause.title", { defaultValue: "Automatic starts paused for {{project}}", project: project.hostId && project.hostId !== "local" ? `${project.name} (${project.hostId})` : project.name }),
					t("autoSpawnPause.configurationBody", {
						defaultValue: "{{failures}} starts with {{harness}} ({{kind}}, model {{model}}, effort {{effort}}) failed: {{message}} An automatic probe is allowed after a 30-minute cooldown.",
						failures: pause.failures, harness: pause.harness, kind: pause.kind || "worker",
						model: pause.model || "default", effort: pause.effort || "default", message: pause.message,
					}),
					{
						tone: "error", durationMs: 0, dedupeKey,
						action: { label: t("autoSpawnPause.acknowledge", { defaultValue: "Acknowledge" }), run: () => {
							seen.add(key);
							try { window.localStorage.setItem(SEEN_KEY, JSON.stringify([...seen])); } catch { /* Retained in memory for this app run. */ }
						} },
					},
				);
			}
		}
		for (const toast of useUiStore.getState().globalToasts) {
			if (toast.dedupeKey?.startsWith("auto-spawn-pause:") && !active.has(toast.dedupeKey)) useUiStore.getState().dismissGlobalToast(toast.nonce);
		}
	}, [projects, showGlobalToast, t]);
}
