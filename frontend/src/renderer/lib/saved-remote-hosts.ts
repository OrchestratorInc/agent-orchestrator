import type { HostId } from "./hosts";

/**
 * The saved remote AO machines and whether each is connected, as the shell's
 * useRemoteHosts last saw them.
 *
 * useRemoteHosts owns connecting (and reconnecting) to every saved machine and
 * runs once, in the shell. Anything else that only needs to know which saved
 * machines exist and which are down reads this snapshot instead of calling the
 * hook again, which would start a second set of connections.
 */
export type SavedRemoteHost = {
	hostId: HostId;
	label: string;
	status: "connecting" | "connected" | "offline";
};

const listeners = new Set<() => void>();
let snapshot: readonly SavedRemoteHost[] = [];

export function savedRemoteHosts(): readonly SavedRemoteHost[] {
	return snapshot;
}

export function subscribeSavedRemoteHosts(listener: () => void): () => void {
	listeners.add(listener);
	return () => listeners.delete(listener);
}

/** Replaces the snapshot; listeners hear only about a real change. */
export function publishSavedRemoteHosts(hosts: readonly SavedRemoteHost[]): void {
	const same = hosts.length === snapshot.length && hosts.every((host, index) => {
		const previous = snapshot[index];
		return previous?.hostId === host.hostId && previous.label === host.label && previous.status === host.status;
	});
	if (same) return;
	snapshot = hosts.map(({ hostId, label, status }) => ({ hostId, label, status }));
	for (const listener of listeners) listener();
}
