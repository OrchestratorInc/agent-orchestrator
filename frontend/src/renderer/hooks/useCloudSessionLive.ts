import { useEffect, useState } from "react";
import type { CloudCpSessionLive } from "../lib/cloud-cp";
import { subscribeSessionLiveBridged } from "../lib/cloud-cp/stream-bridge";
import { useCloudCp } from "./useCloudCp";

const RECONNECT_MIN_MS = 1_000;
const RECONNECT_MAX_MS = 15_000;

/**
 * A cloud session's live state while it is on screen: who else has it open
 * and who holds its composer. Having it open lists this person as a viewer
 * for everyone else. Reconnects with backoff while enabled; undefined until
 * the first frame, and again after a reconnect fails, so a stale holder never
 * keeps the composer locked.
 */
export function useCloudSessionLive(orgId: string | undefined, sessionId: string | undefined, enabled: boolean): CloudCpSessionLive | undefined {
	const { baseUrl, ready } = useCloudCp(enabled);
	const [live, setLive] = useState<{ key: string; state: CloudCpSessionLive }>();
	const key = `${baseUrl}|${orgId ?? ""}|${sessionId ?? ""}`;
	useEffect(() => {
		if (!enabled || !ready || !baseUrl || !orgId || !sessionId) return;
		const controller = new AbortController();
		let timer: number | undefined;
		let delay = RECONNECT_MIN_MS;
		const connect = async () => {
			let received = false;
			await subscribeSessionLiveBridged({
				baseUrl, orgId, sessionId, signal: controller.signal,
				onLive: (state) => {
					received = true;
					setLive({ key, state });
				},
			});
			if (controller.signal.aborted) return;
			setLive(undefined);
			delay = received ? RECONNECT_MIN_MS : Math.min(delay * 2, RECONNECT_MAX_MS);
			timer = window.setTimeout(() => void connect(), delay);
		};
		void connect();
		return () => {
			controller.abort();
			if (timer !== undefined) window.clearTimeout(timer);
		};
	}, [baseUrl, enabled, key, orgId, ready, sessionId]);
	return live?.key === key ? live.state : undefined;
}
