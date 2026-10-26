import { useSyncExternalStore } from "react";
import {
	getEventsConnectionState,
	getHealthyRemoteHosts,
	isEventStreamHealthy,
	subscribeEventsConnection,
	type EventsConnectionState,
} from "../lib/events-connection";

/** Live connection state of the daemon SSE stream (see events-connection.ts). */
export function useEventsConnection(): EventsConnectionState {
	return useSyncExternalStore(subscribeEventsConnection, getEventsConnectionState);
}

/** See isEventStreamHealthy. */
export function useEventStreamHealthy(hostId?: string): boolean {
	return useSyncExternalStore(subscribeEventsConnection, () => isEventStreamHealthy(hostId));
}

/** Remote hosts whose event stream currently delivers live updates. */
export function useHealthyRemoteHosts(): ReadonlySet<string> {
	return useSyncExternalStore(subscribeEventsConnection, getHealthyRemoteHosts);
}
