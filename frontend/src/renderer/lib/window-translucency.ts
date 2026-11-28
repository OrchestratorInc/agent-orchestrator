import { useSyncExternalStore } from "react";
import type { WindowTransparencyState } from "../../shared/window-transparency";
import { aoBridge } from "./bridge";
import { isMacPlatform, isWindowsPlatform } from "./platform";
import type { WindowTransparencyView } from "./window-transparency-presentation";

export const developerModeStorageKey = "ao.developerMode";
export const windowTranslucencyStorageKey = "ao.windowTranslucency";

/** Windows is opt-in; macOS keeps its existing default. Capability lives in main. */
export function readStoredWindowTranslucency(): boolean {
	if (typeof window === "undefined" || (!isMacPlatform() && !isWindowsPlatform())) return false;
	try {
		const saved = window.localStorage?.getItem(windowTranslucencyStorageKey);
		return saved === "true" || (isMacPlatform() && saved !== "false");
	} catch {
		return isMacPlatform();
	}
}

let state: WindowTransparencyView = { supported: null, enabled: false, reason: null };
const listeners = new Set<() => void>();
let requestVersion = 0;
let pendingRequest: number | null = null;
let pendingPush: WindowTransparencyState | null = null;
let stopListening: (() => void) | undefined;
let disposed = false;

function publish(next: WindowTransparencyView): void {
	const root = document.documentElement;
	if (next.enabled) {
		root.dataset.windowTranslucent = "true";
		root.dataset.windowMaterial = isWindowsPlatform() ? "acrylic" : "vibrancy";
	} else {
		delete root.dataset.windowTranslucent;
		delete root.dataset.windowMaterial;
	}
	if (state.enabled === next.enabled && state.supported === next.supported && state.reason === next.reason) return;
	state = next;
	for (const listener of listeners) listener();
}

export function useWindowTransparency(): WindowTransparencyView {
	return useSyncExternalStore((listener) => {
		listeners.add(listener);
		return () => { listeners.delete(listener); };
	}, () => state);
}

/** Preserve the shipped macOS loader's first paint; main then reconciles it. */
export function initializeWindowTranslucency(enabled: boolean): void {
	if (typeof document !== "undefined" && state.supported === null && isMacPlatform()) {
		publish({ supported: true, enabled, reason: null });
	}
}

/** A hot-replaced module must release its listener and ignore outstanding IPC. */
export function disposeWindowTranslucency(): void {
	disposed = true;
	++requestVersion;
	stopListening?.();
	stopListening = undefined;
	pendingRequest = null;
	pendingPush = null;
	listeners.clear();
}

if (import.meta.hot) import.meta.hot.dispose(disposeWindowTranslucency);

/** Only clear the shell after main has issued an unsuppressed material request.
 * The acknowledgement cannot confirm DWM composition (see WindowTransparencyState).
 */
export async function applyWindowTranslucency(enabled: boolean): Promise<void> {
	if (typeof document === "undefined" || disposed) return;
	const version = ++requestVersion;
	pendingRequest = version;
	pendingPush = null;
	if (!enabled) publish({ ...state, enabled: false });
	try {
		if (!stopListening) {
			stopListening = aoBridge.theme.onWindowTransparency((next) => {
				if (disposed) return;
				if (pendingRequest !== null) {
					// A setter sends its push before replying. Stage it so an earlier
					// enable cannot undo a newer optimistic disable in the meantime.
					pendingPush = next;
					if (!next.enabled && next.reason !== null) publish(next);
				} else {
					publish(next);
				}
			});
		}
		const next = await aoBridge.theme.setWindowTranslucent(enabled);
		if (version === requestVersion) {
			const current = pendingPush ?? next;
			pendingRequest = null;
			pendingPush = null;
			publish(!enabled && current.enabled ? { ...current, enabled: false } : current);
		}
	} catch {
		if (version !== requestVersion) return;
		let supported = state.supported;
		try {
			// A lost startup response must not permanently hide a supported feature.
			supported = (await aoBridge.theme.getWindowTransparency()).supported;
		} catch {
			// Keep capability unknown and let the Appearance switch retry.
		}
		if (version === requestVersion) publish({ supported, enabled: false, reason: "error" });
	} finally {
		if (version === requestVersion) {
			pendingRequest = null;
			pendingPush = null;
		}
	}
}
