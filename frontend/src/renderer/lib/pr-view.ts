import { useUiStore, type UiState } from "../stores/ui-store";

export function prViewEnabled(state: Pick<UiState, "developerMode" | "prView">): boolean {
	return state.developerMode && state.prView;
}

export function usePRViewEnabled(): boolean {
	return useUiStore(prViewEnabled);
}
