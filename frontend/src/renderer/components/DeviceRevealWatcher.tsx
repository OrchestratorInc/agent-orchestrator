import { useEffect, useRef } from "react";
import { deviceRevealStep, type DeviceBaseline } from "../lib/device-reveal";
import { useSessionDevice } from "./DevicePanel";

/**
 * Opens the Device tab when an agent's `ao device open` gives the session a
 * device while it is on screen, as `ao preview` reveals Browser; entering a
 * session that already holds one does not. A component of its own so the lease
 * poll re-renders this, not the whole session view.
 */
export function DeviceRevealWatcher({
	enabled,
	onReveal,
	sessionId,
	terminated,
	uiSessionId,
}: {
	enabled: boolean;
	onReveal: () => void;
	sessionId: string;
	terminated: boolean;
	uiSessionId: string;
}) {
	const lease = useSessionDevice(sessionId, enabled && !terminated);
	const key = lease.data ? `${lease.data.device.id}:${lease.data.openedAt}` : "";
	const baselineRef = useRef<DeviceBaseline | null>(null);
	// When this session was entered: a lease answer older than that is the
	// cache from an earlier visit, not what the session holds now.
	const enteredRef = useRef({ sessionId: "", at: 0 });
	if (enteredRef.current.sessionId !== uiSessionId) enteredRef.current = { sessionId: uiSessionId, at: Date.now() };
	const onRevealRef = useRef(onReveal);
	onRevealRef.current = onReveal;
	const updatedAt = lease.dataUpdatedAt;
	const pending = lease.isPending;
	useEffect(() => {
		if (!enabled || pending) return;
		const step = deviceRevealStep(baselineRef.current, { sessionId: uiSessionId, key, dataUpdatedAt: updatedAt, enteredAt: enteredRef.current.at });
		baselineRef.current = step.baseline;
		if (step.reveal && !terminated) onRevealRef.current();
	}, [enabled, key, pending, terminated, uiSessionId, updatedAt]);
	return null;
}
