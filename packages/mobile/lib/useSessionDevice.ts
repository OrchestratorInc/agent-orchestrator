import { useEffect, useState } from "react";
import { getDeviceStatus, getSessionDevice, type SessionDeviceLease } from "./api";
import type { ServerConfig } from "./config";
import { deviceSupportOn, startSessionDevicePoll } from "./sessionDevicePoll";
import { useForeground } from "./useForeground";

/**
 * Whether a session holds a device, polled so an agent's `ao device open`
 * shows up: only while device support is on and the app is in front (see
 * startSessionDevicePoll).
 */
export function useSessionDevice(config: ServerConfig | null | undefined, sessionId: string): SessionDeviceLease | null {
	const [lease, setLease] = useState<SessionDeviceLease | null>(null);
	const foreground = useForeground();
	useEffect(() => {
		setLease(null);
		if (!config || !sessionId || !foreground) return;
		const controller = new AbortController();
		const stop = startSessionDevicePoll(
			{
				supportOn: () => getDeviceStatus(config, { signal: controller.signal }).then(deviceSupportOn),
				lease: () => getSessionDevice(config, sessionId, { signal: controller.signal }),
			},
			setLease,
		);
		return () => { controller.abort(); stop(); };
	}, [config, foreground, sessionId]);
	return lease;
}
