import { useUiStore, type UiState } from "../stores/ui-store";
import { parseStoredGroups, trimStoredGroups, type TabGroup } from "./pane-split";

/**
 * Split panes are a Developer mode preview. Off, tabs never lift out of the
 * strip, the split shortcuts do nothing and no split is shown.
 */
export function splitPanesEnabled(state: Pick<UiState, "developerMode" | "splitPanes">): boolean {
	return state.developerMode && state.splitPanes;
}

export function useSplitPanesEnabled(): boolean {
	return useUiStore(splitPanesEnabled);
}

const splitGroupsStorageKey = "ao.splitPaneGroups";

/** Remembered split layouts by session; a shell still being opened never survives a reload. */
export function readStoredSplitGroups(): Record<string, TabGroup[]> {
	try {
		return parseStoredGroups(window.localStorage?.getItem(splitGroupsStorageKey), (key) => key.startsWith("pending-shell:"));
	} catch {
		return {};
	}
}

export function writeStoredSplitGroups(groupsBySession: Record<string, TabGroup[]>): void {
	try {
		const trimmed = trimStoredGroups(groupsBySession);
		if (Object.keys(trimmed).length) window.localStorage?.setItem(splitGroupsStorageKey, JSON.stringify(trimmed));
		else window.localStorage?.removeItem(splitGroupsStorageKey);
	} catch {
		// Storage full or unavailable: the layout still works for this run.
	}
}
