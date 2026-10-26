// Connection state of the daemon's SSE event stream, kept as a tiny external
// store (useSyncExternalStore-compatible) so the transport — which lives
// outside React — can drive UI signals like the sidebar's "events offline".
export type EventsConnectionState =
	| "idle" // no stream (transport not connected, or torn down)
	| "connected" // stream open; live updates flowing
	| "disconnected"; // stream lost; UI state may be stale until it reconnects

let state: EventsConnectionState = "idle";
const listeners = new Set<() => void>();

export function getEventsConnectionState(): EventsConnectionState {
	return state;
}

export function setEventsConnectionState(next: EventsConnectionState): void {
	if (next === state) return;
	state = next;
	listeners.forEach((listener) => listener());
}

// Remote hosts whose event stream has delivered a frame (see remote-sse-probe.ts).
// Replaced, never mutated, so it can serve as a useSyncExternalStore snapshot.
let healthyRemoteHosts: ReadonlySet<string> = new Set();

export function getHealthyRemoteHosts(): ReadonlySet<string> {
	return healthyRemoteHosts;
}

export function setRemoteEventsHealthy(hostId: string, healthy: boolean): void {
	if (healthyRemoteHosts.has(hostId) === healthy) return;
	const next = new Set(healthyRemoteHosts);
	if (healthy) next.add(hostId);
	else next.delete(hostId);
	healthyRemoteHosts = next;
	listeners.forEach((listener) => listener());
}

/**
 * Whether live updates for a host (the local daemon when omitted) arrive over
 * its event stream. Polls that only stand in for those updates run while this
 * is false: the stream is down or has not proven delivery yet.
 */
export function isEventStreamHealthy(hostId?: string): boolean {
	return hostId ? healthyRemoteHosts.has(hostId) : state === "connected";
}

export function subscribeEventsConnection(listener: () => void): () => void {
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
	};
}
