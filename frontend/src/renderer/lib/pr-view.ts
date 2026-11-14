import { useUiStore, type UiState } from "../stores/ui-store";

/**
 * The PR view is a Developer mode preview: a PR's own tab in the main area with
 * its description, comments, checks and stack, the one-card PR summary, and the
 * PR's actions. Off, the inspector shows its PR cards and git rows as before and
 * no PR tab opens. Remove this flag, and the classic components it keeps, once
 * the PR view ships to everyone.
 */
export function prViewEnabled(state: Pick<UiState, "developerMode" | "prView">): boolean {
	return state.developerMode && state.prView;
}

export function usePRViewEnabled(): boolean {
	return useUiStore(prViewEnabled);
}
