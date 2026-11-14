import { useSyncExternalStore } from "react";
import { aoBridge } from "../lib/bridge";
import { isTeamsEnabled } from "../lib/build-channel";

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

export function useTeamsEnabled(): boolean {
	return isTeamsEnabled(useSyncExternalStore(subscribe, currentVersion, currentVersion));
}
