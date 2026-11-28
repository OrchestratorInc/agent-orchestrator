/** What the Device tab last saw for a session: the lease it treats as already known. */
export type DeviceBaseline = { sessionId: string; key: string };

/**
 * Decides whether a session's device lease is news worth opening the Device tab
 * for: one an agent opened while the user watches. The first answer seen for a
 * session is its baseline, never news, and it must be fresh: React Query keeps
 * a session's last answer for minutes, so a cached "no device" from an earlier
 * visit would make a lease opened meanwhile look new and pop the tab open.
 */
export function deviceRevealStep(
	baseline: DeviceBaseline | null,
	current: { sessionId: string; key: string; dataUpdatedAt: number; enteredAt: number },
): { baseline: DeviceBaseline | null; reveal: boolean } {
	if (!baseline || baseline.sessionId !== current.sessionId) {
		if (current.dataUpdatedAt < current.enteredAt) return { baseline, reveal: false };
		return { baseline: { sessionId: current.sessionId, key: current.key }, reveal: false };
	}
	if (baseline.key === current.key) return { baseline, reveal: false };
	return { baseline: { sessionId: current.sessionId, key: current.key }, reveal: current.key !== "" };
}
