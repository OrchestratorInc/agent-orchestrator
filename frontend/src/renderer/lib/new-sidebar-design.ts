/**
 * Whether the new sidebar design is in effect. It is on unless switched off in
 * Developer mode; a missing value (an older store, a test double) counts as on.
 */
export function isNewSidebarDesign(state: { developerMode?: boolean; newSidebarDesign?: boolean }): boolean {
	return state.newSidebarDesign !== false || state.developerMode !== true;
}

/**
 * The same answer outside React, from the attribute the UI store keeps on the
 * root element (it also switches the status colour tokens).
 */
export function isNewSidebarDesignActive(): boolean {
	return typeof document === "undefined" || !document.documentElement.hasAttribute("data-legacy-status-colors");
}
