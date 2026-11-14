import { useUiStore, type UiState } from "../stores/ui-store";

export function splitPanesEnabled(state: Pick<UiState, "developerMode" | "splitPanes">): boolean {
	return state.developerMode && state.splitPanes;
}

export function useSplitPanesEnabled(): boolean {
	return useUiStore(splitPanesEnabled);
}
