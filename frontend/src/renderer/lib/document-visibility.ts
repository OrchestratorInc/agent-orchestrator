import { useSyncExternalStore } from "react";

function subscribe(onChange: () => void) {
	document.addEventListener("visibilitychange", onChange);
	return () => document.removeEventListener("visibilitychange", onChange);
}

/** Whether the app's window can be seen: false while it is minimized, fully covered, or the screen is locked. */
export function useDocumentVisible(): boolean {
	return useSyncExternalStore(subscribe, () => document.visibilityState !== "hidden", () => true);
}
