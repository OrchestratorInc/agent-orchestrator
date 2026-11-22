import { useEffect, useState } from "react";
import { getSessionDevice, type SessionDeviceLease } from "./api";
import type { ServerConfig } from "./config";

/** Whether a session holds a device, polled so an agent's `ao device open` shows up. */
export function useSessionDevice(config: ServerConfig | null | undefined, sessionId: string, intervalMs = 3_000): SessionDeviceLease | null {
	const [lease, setLease] = useState<SessionDeviceLease | null>(null);
	useEffect(() => {
		setLease(null);
		if (!config || !sessionId) return;
		const controller = new AbortController();
		const load = () => getSessionDevice(config, sessionId, { signal: controller.signal }).then(setLease).catch(() => {});
		void load();
		const poll = setInterval(() => void load(), intervalMs);
		return () => { controller.abort(); clearInterval(poll); };
	}, [config, intervalMs, sessionId]);
	return lease;
}
