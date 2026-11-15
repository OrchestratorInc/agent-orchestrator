import { useSyncExternalStore } from "react";
import {
	getEventsConnectionState,
	subscribeEventsConnection,
	type EventsConnectionState,
} from "../lib/events-connection";

export function useEventsConnection(): EventsConnectionState {
	return useSyncExternalStore(subscribeEventsConnection, getEventsConnectionState);
}
