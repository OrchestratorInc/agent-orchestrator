import { useEffect } from "react";
import { useUiStore } from "../stores/ui-store";
import { useSettings, useUpdateCardSummaries } from "./useSettings";

const developerDefaultKey = "ao.cardSummaries.developerDefault";

/** Records that Developer mode already decided card summaries on this machine. */
export function markCardSummariesDeveloperDefault(): void {
	if (typeof window === "undefined" || !window.localStorage) return;
	window.localStorage.setItem(developerDefaultKey, "true");
}

/**
 * Card summaries come on with Developer mode. Someone who was already in
 * Developer mode before the setting existed gets it turned on once; after that
 * their own choice stands.
 */
export function useCardSummariesDeveloperDefault(): void {
	const developerMode = useUiStore((state) => state.developerMode);
	const { settings } = useSettings();
	const { update } = useUpdateCardSummaries();
	useEffect(() => {
		if (!developerMode || !settings) return;
		if (window.localStorage?.getItem(developerDefaultKey) === "true") return;
		markCardSummariesDeveloperDefault();
		if (!settings.cardSummaries) update(true);
	}, [developerMode, settings, update]);
}
