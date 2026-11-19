import { useUiStore, type UiState } from "../stores/ui-store";

/**
 * Card summaries are a Developer mode preview: new workers keep a one-line
 * summary on their card with `ao summary`, and cards show it. Off, workers get
 * no Card Summary prompt section and no card shows a summary line, whatever
 * the toggle was set to before.
 */
export function cardSummariesEnabled(state: Pick<UiState, "developerMode" | "cardSummaries">): boolean {
	return state.developerMode && state.cardSummaries;
}

export function useCardSummariesEnabled(): boolean {
	return useUiStore(cardSummariesEnabled);
}
