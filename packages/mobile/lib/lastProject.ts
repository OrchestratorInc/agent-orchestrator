import AsyncStorage from "@react-native-async-storage/async-storage";
import { useEffect, useState } from "react";

// The project the user last worked in on each machine: started a task in,
// opened, or filtered the board to. New task starts there. Kept per machine
// because a project id means nothing on another one.
const KEY_PREFIX = "ao.lastProject.";
const cache = new Map<string, string | null>();
const listeners = new Set<() => void>();

const storageKey = (hostId: string) => `${KEY_PREFIX}${hostId}`;

export function rememberLastProject(hostId: string | null | undefined, projectId: string | null | undefined): void {
	if (!hostId || !projectId) return;
	if (cache.get(hostId) === projectId) return;
	cache.set(hostId, projectId);
	listeners.forEach((notify) => notify());
	AsyncStorage.setItem(storageKey(hostId), projectId).catch(() => {});
}

async function loadLastProject(hostId: string): Promise<string | null> {
	if (cache.has(hostId)) return cache.get(hostId) ?? null;
	const stored = await AsyncStorage.getItem(storageKey(hostId)).catch(() => null);
	// A remember that landed while the read was in flight is newer.
	if (!cache.has(hostId)) cache.set(hostId, stored);
	return cache.get(hostId) ?? null;
}

/** `loaded` stays false until the machine's stored project has been read. */
export function useLastProject(hostId: string | null | undefined): { loaded: boolean; projectId: string | null } {
	const [state, setState] = useState<{ hostId: string | null; projectId: string | null }>(() => ({
		hostId: hostId && cache.has(hostId) ? hostId : null,
		projectId: hostId ? cache.get(hostId) ?? null : null,
	}));
	useEffect(() => {
		if (!hostId) return;
		let cancelled = false;
		const read = () => { void loadLastProject(hostId).then((projectId) => { if (!cancelled) setState({ hostId, projectId }); }); };
		read();
		listeners.add(read);
		return () => { cancelled = true; listeners.delete(read); };
	}, [hostId]);
	if (!hostId || state.hostId !== hostId) return { loaded: false, projectId: null };
	return { loaded: true, projectId: state.projectId };
}
