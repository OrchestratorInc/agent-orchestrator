import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { useUiStore } from "../stores/ui-store";

export type AutoSpawnPause = components["schemas"]["AutoSpawnPause"];

const SEEN_KEY = "ao.autoSpawnPauseNotices";

function seenPauses(): Set<string> {
	try {
		const raw = window.localStorage.getItem(SEEN_KEY);
		return new Set(raw ? (JSON.parse(raw) as string[]) : []);
	} catch {
		return new Set();
	}
}

/**
 * Shows each automatic-start pause once. The daemon stops tracker intake and
 * schedule starts for a project and harness after three identical failures;
 * the pause stays listed on the project until a start succeeds or the
 * project's agent settings change, so "once" is remembered per pause.
 */
export function useAutoSpawnPauseNotice(projects: { id: string; name: string; autoSpawnPauses?: AutoSpawnPause[] }[]) {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	useEffect(() => {
		const seen = seenPauses();
		let changed = false;
		for (const project of projects) {
			for (const pause of project.autoSpawnPauses ?? []) {
				const key = `${project.id}|${pause.harness}|${pause.pausedAt}`;
				if (seen.has(key)) continue;
				seen.add(key);
				changed = true;
				showGlobalToast(
					t("autoSpawnPause.title", { defaultValue: "Automatic starts paused for {{project}}", project: project.name }),
					t("autoSpawnPause.body", {
						defaultValue: "{{failures}} automatic starts with {{harness}} failed in a row: {{message}} Start a session yourself or change the project's agent or model settings to resume.",
						failures: pause.failures,
						harness: pause.harness,
						message: pause.message,
					}),
					{ tone: "error", durationMs: 20_000, dedupeKey: `auto-spawn-pause:${key}` },
				);
			}
		}
		if (changed) {
			// ponytail: grows by one key per pause; prune if it ever matters.
			window.localStorage.setItem(SEEN_KEY, JSON.stringify([...seen]));
		}
	}, [projects, showGlobalToast, t]);
}
