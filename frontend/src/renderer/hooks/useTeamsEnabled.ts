import { useSyncExternalStore } from "react";
import { aoBridge } from "../lib/bridge";
import { isTeamsEnabled } from "../lib/build-channel";

// The app's version decides Teams in packaged builds and never changes while
// the app runs, so it is read once and kept here rather than in React Query:
// the gate then works in any part of the tree, provider or not.
let version: string | undefined;
let reading = false;
const listeners = new Set<() => void>();

function readVersion(): void {
	if (reading || version !== undefined) return;
	reading = true;
	Promise.resolve()
		.then(() => aoBridge.app.getVersion())
		.then((value) => {
			if (typeof value !== "string") return;
			version = value;
			for (const listener of listeners) listener();
		})
		.catch(() => undefined)
		.finally(() => {
			// A failed read is tried again by the next component that asks.
			reading = false;
		});
}

function subscribe(listener: () => void): () => void {
	listeners.add(listener);
	readVersion();
	return () => {
		listeners.delete(listener);
	};
}

const currentVersion = () => version;

/** Whether this build has Teams; see isTeamsEnabled. */
export function useTeamsEnabled(): boolean {
	return isTeamsEnabled(useSyncExternalStore(subscribe, currentVersion, currentVersion));
}
