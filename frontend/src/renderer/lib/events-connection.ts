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

export function subscribeEventsConnection(listener: () => void): () => void {
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
	};
}

/** Let cold-start REST take its snapshot after SSE is listening. Offline startup
 * still proceeds; a later onopen performs the normal gap refresh.
 */
export function waitForEventsConnection(timeoutMs = 1_000): Promise<void> {
	if (state === "connected" || typeof EventSource === "undefined") return Promise.resolve();
	return new Promise((resolve) => {
		const finish = () => { clearTimeout(timer); unsubscribe(); resolve(); };
		const unsubscribe = subscribeEventsConnection(() => { if (state === "connected") finish(); });
		const timer = setTimeout(finish, timeoutMs);
	});
}
