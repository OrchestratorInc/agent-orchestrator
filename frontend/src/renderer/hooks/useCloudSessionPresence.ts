import { useEffect } from "react";
import { useCloudCp } from "./useCloudCp";

/** How often a visible session reports that someone is looking at it. The
 * control plane holds a 2-minute interaction lease, so one missed beat is safe. */
export const PRESENCE_INTERVAL_MS = 60_000;

/**
 * While a cloud session is on screen and the window is focused, tell the
 * control plane someone is using it, so reading an agent's output (without
 * typing) does not idle-pause the session underneath the reader. It never
 * wakes a paused session.
 */
export function useCloudSessionPresence(orgId: string | undefined, sessionId: string | undefined, enabled: boolean): void {
	const { client, ready } = useCloudCp();
	useEffect(() => {
		if (!enabled || !ready || !orgId || !sessionId) return;
		let controller: AbortController | null = null;
		const beat = () => {
			if (document.visibilityState !== "visible" || !document.hasFocus()) return;
			controller?.abort();
			controller = new AbortController();
			client.touchSessionPresence(orgId, sessionId, { signal: controller.signal }).catch(() => {
				// Best effort: a missed beat only shortens the idle grace.
			});
		};
		beat();
		const timer = window.setInterval(beat, PRESENCE_INTERVAL_MS);
		const onFocus = () => beat();
		window.addEventListener("focus", onFocus);
		document.addEventListener("visibilitychange", onFocus);
		return () => {
			window.clearInterval(timer);
			window.removeEventListener("focus", onFocus);
			document.removeEventListener("visibilitychange", onFocus);
			controller?.abort();
		};
	}, [client, enabled, orgId, ready, sessionId]);
}
