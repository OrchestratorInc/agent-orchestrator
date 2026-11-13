import { useUiStore, type UiState } from "../stores/ui-store";

/**
 * Private links (publishing an artifact privately and sharing it with people
 * by email) are a Developer mode preview. Off, the share panel offers public
 * links only, as before, even where the control plane offers private ones.
 */
export function privateLinksEnabled(state: Pick<UiState, "developerMode" | "privateLinks">): boolean {
	return state.developerMode && state.privateLinks;
}

export function usePrivateLinksEnabled(): boolean {
	return useUiStore(privateLinksEnabled);
}
