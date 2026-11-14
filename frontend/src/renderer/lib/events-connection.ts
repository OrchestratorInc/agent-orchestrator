export type EventsConnectionState =
	| "idle"
	| "connected"
	| "disconnected";

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
