import { useUiStore, type UiState } from "../stores/ui-store";

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
