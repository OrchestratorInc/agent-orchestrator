import { useEffect } from "react";
import { useCloudCp } from "./useCloudCp";

export const PRESENCE_INTERVAL_MS = 60_000;

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
