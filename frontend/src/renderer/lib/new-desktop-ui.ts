import { developerModeStorageKey } from "./window-translucency";

export const newDesktopUiStorageKey = "ao.newDesktopUi";

/**
 * The new desktop UI is a Developer mode preview, off by default. It is
 * styling only: `styles.css` scopes every rule for it under
 * `html[data-new-desktop-ui]`, so turning it off is exactly the current UI.
 */
export function newDesktopUiEnabled(state: { developerMode: boolean; newDesktopUi: boolean }): boolean {
	return state.developerMode && state.newDesktopUi;
}

/** The saved preference, read before React so the first paint is already in the right UI. */
export function readStoredNewDesktopUi(): boolean {
	if (typeof window === "undefined" || !window.localStorage) return false;
	const storage = window.localStorage;
	return storage.getItem(developerModeStorageKey) === "true" && storage.getItem(newDesktopUiStorageKey) === "true";
}

export function applyNewDesktopUi(enabled: boolean): void {
	if (typeof document === "undefined") return;
	if (enabled) document.documentElement.dataset.newDesktopUi = "true";
	else delete document.documentElement.dataset.newDesktopUi;
}
