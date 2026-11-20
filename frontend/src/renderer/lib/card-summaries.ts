import { useUiStore, type UiState } from "../stores/ui-store";

/**
 * Card summaries are a Developer mode preview: each worker report becomes a
 * one-line summary on its card, rewritten by a light model. Off, the daemon
 * writes none and no card shows a summary line, whatever the toggle was set
 * to before.
 */
export function cardSummariesEnabled(state: Pick<UiState, "developerMode" | "cardSummaries">): boolean {
	return state.developerMode && state.cardSummaries;
}

export function useCardSummariesEnabled(): boolean {
	return useUiStore(cardSummariesEnabled);
}
