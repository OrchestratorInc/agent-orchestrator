/**
 * Whether the simplified top bar is in effect. It is a Developer Mode preview
 * that stays on there until switched off; everyone else keeps the previous top
 * bar. A missing value (an older store, a test double) counts as on.
 */
export function isSimplifiedTopbar(state: { developerMode?: boolean; simplifiedTopbar?: boolean }): boolean {
	return state.developerMode === true && state.simplifiedTopbar !== false;
}
