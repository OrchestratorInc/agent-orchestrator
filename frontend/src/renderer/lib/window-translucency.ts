import { aoBridge } from "./bridge";
import { isMacPlatform } from "./platform";

export const developerModeStorageKey = "ao.developerMode";
export const windowTranslucencyStorageKey = "ao.windowTranslucency";

export function readStoredWindowTranslucency(): boolean {
	if (typeof window === "undefined" || !window.localStorage || !isMacPlatform()) return false;
	return window.localStorage.getItem(windowTranslucencyStorageKey) !== "false";
}

export function applyWindowTranslucency(enabled: boolean): void {
	if (typeof document === "undefined") return;
	if (enabled) document.documentElement.dataset.windowTranslucent = "true";
	else delete document.documentElement.dataset.windowTranslucent;
	void aoBridge.theme?.setWindowTranslucent(enabled)?.catch(() => undefined);
}
