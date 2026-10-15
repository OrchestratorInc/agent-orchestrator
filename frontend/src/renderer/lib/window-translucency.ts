import { aoBridge } from "./bridge";
import { isMacPlatform } from "./platform";

export const developerModeStorageKey = "ao.developerMode";
export const windowTranslucencyStorageKey = "ao.windowTranslucency";

/** The Developer Mode window transparency flag as persisted, macOS only. */
export function readStoredWindowTranslucency(): boolean {
	if (typeof window === "undefined" || !window.localStorage || !isMacPlatform()) return false;
	return (
		window.localStorage.getItem(developerModeStorageKey) === "true" &&
		window.localStorage.getItem(windowTranslucencyStorageKey) === "true"
	);
}

/**
 * Marks the document for the translucent styles (styles.css, "macOS window
 * transparency") and asks main for the native vibrancy behind them. Main keeps
 * no memory across windows, so every new document must ask again.
 */
export function applyWindowTranslucency(enabled: boolean): void {
	if (typeof document === "undefined") return;
	if (enabled) document.documentElement.dataset.windowTranslucent = "true";
	else delete document.documentElement.dataset.windowTranslucent;
	void aoBridge.theme?.setWindowTranslucent(enabled)?.catch(() => undefined);
}
